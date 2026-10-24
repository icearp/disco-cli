package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/semaphore"
)

func init() {
	// IoT types are declared per family file via registerType — scanIoT
	// upserts nothing itself, only fans out to family scanners (things,
	// certs/auth, defender, jobs, software, logging).
	registerService(serviceEntry{name: "aws:iot", fn: scanIoT})
}

const iotListTagsOp = "iot:ListTagsForResource"

// iotTagsAPI is the tag read shared by the families whose types carry tags
// no list or describe response returns.
type iotTagsAPI interface {
	ListTagsForResource(context.Context, *iot.ListTagsForResourceInput, ...func(*iot.Options)) (*iot.ListTagsForResourceOutput, error)
}

// iotTags is the tag read every IoT family shares: slots bounds the
// ListTagsForResource calls in flight across all families to fanoutMed, as
// readRowTags bounds them within one phase only and the families run
// concurrently.
type iotTags struct {
	client iotTagsAPI
	slots  *semaphore.Weighted
}

func newIoTTags(client iotTagsAPI) iotTags {
	return iotTags{client: client, slots: semaphore.NewWeighted(fanoutMed)}
}

// reader reads a row's tags with ListTagsForResource, sent the row's
// NativeID (the resource ARN for every type it is used for), every page of
// them, holding one of t's slots for the read. ResourceNotFoundException is
// the only not-found code the op's model lists. The op has no MaxResults.
func (t iotTags) reader() rowTagReader {
	return rowTagReader{
		op:       iotListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if err := t.slots.Acquire(ctx, 1); err != nil {
				return nil, err
			}
			defer t.slots.Release(1)
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]iottypes.Tag, *string, error) {
				out, err := t.client.ListTagsForResource(ctx, &iot.ListTagsForResourceInput{ResourceArn: &r.NativeID, NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t iottypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// isIoTNotFound reports the ResourceNotFoundException the describe ops of a
// listed resource model for one deleted since it was listed.
func isIoTNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// iotListPageCap is the page-size cap the model documents for most IoT list
// ops (maxResults or pageSize 1-250); an op with another cap names its own.
const iotListPageCap = 250

// iotListing is a listing read for a describe fan-out: the keys of its
// items, those read before a failed page included, the row each item that
// carries its ARN gives on its own (see iotList), the error that ended the
// listing, and the items that had no key.
type iotListing struct {
	op, what, keyField string
	keys               []string
	listed             map[string]*store.Resource
	err                error
	noKey              skipTally
}

// iotList reads every page of op's listing (readTokenPages) and keeps each
// item's key; what names the items and keyField the member key reads, for
// the warning about items without one. listed builds the row an item gives
// on its own, its list element as the attributes, or nil when it has no ARN;
// it is nil for a listing whose items never carry one. The listing proves
// such an item exists, so describe stores that row, rather than dropping
// the resource, when the item's describe fails other than with a not-found,
// answers without an ARN, or is never made because ctx ended.
func iotList[Item any](
	ctx context.Context, op, what, keyField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	key func(Item) *string,
	listed func(Item) *store.Resource,
) *iotListing {
	items, err := readTokenPages(ctx, page)
	l := &iotListing{op: op, what: what, keyField: keyField, err: err, listed: map[string]*store.Resource{}}
	for i, it := range items {
		k := sv(key(it))
		if k == "" {
			l.noKey.skipped(fmt.Sprintf("item %d", i+1), nil)
			continue
		}
		l.keys = append(l.keys, k)
		if listed != nil {
			if r := listed(it); r != nil {
				l.listed[k] = r
			}
		}
	}
	return l
}

// iotListedRow is the row of a listed item stored as its list element, item
// verbatim as the attributes; nil when the item has no ARN.
func iotListedRow(acct *account, region, scanID, typ string, arn, name *string, item any) *store.Resource {
	if sv(arn) == "" {
		return nil
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           typ,
		NativeID:       *arn,
		Name:           name,
		Region:         &region,
		AttributesJSON: mustJSON(item),
		DiscoveredBy:   scanID,
	}
}

// iotName is the key func of a listing whose items are the keys.
func iotName(s string) *string { return &s }

// warnNoKey warns once about the listed items that had no key.
func (l *iotListing) warnNoKey(st *store.Store, acct *account, region string) {
	warnNotStored(st, l.op, acct, region, &l.noKey, l.what+" have no "+l.keyField)
}

// iotDescribed is the result of reading a listing's keys with op: the rows
// built, a warning to send once they are stored, and the errors, the
// transient one apart from the hard one so that phaseErrs can demote it.
type iotDescribed struct {
	op              string
	rows            []*store.Resource
	warn            func()
	hard, transient error
}

// iotRead is what reading one key gave: its rows, those read before a
// failure included, the error that ended the read, and, for a describe,
// whether its response had no ARN.
type iotRead struct {
	rows  []*store.Resource
	err   error
	noARN bool
}

// iotReadEach reads each key with op, fanoutMed at a time (readEachKey): no
// key's failure cancels another's read. Per key, in key order:
//   - an error isGone matches: deleted since it was listed, dropped
//     silently with every row read for it;
//   - otherwise the rows read are kept, whatever the error; a key that gave
//     no row, because its read failed, its response had no ARN, or it was
//     never read because ctx ended, is stored as fallback(key) when fallback
//     (nil for none) gives a row for it;
//   - no ARN: warn reports every such key in one warning, saying whether it
//     was stored from fallback;
//   - a denial: warn reports every such key in one warning, one for the keys
//     with no row and one, saying kept, for the keys whose rows were kept;
//   - still transient after the SDK's retries: every such key is reported
//     in transient, one error;
//   - not read, or cut short, because ctx ended: one member of hard;
//   - any other error, a late NXDOMAIN (lateNXDOMAIN: the listing reached
//     the endpoint) included: a member of hard.
//
// what names the items; name renders a key for the messages.
func iotReadEach[Key any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string,
	keys []Key, name func(Key) string, isGone func(error) bool, kept string, fallback func(Key) *store.Resource,
	read func(context.Context, Key) iotRead,
) iotDescribed {
	reads, stopped := readEachKey(ctx, keys, read)
	orFallback := func(k Key, rows []*store.Resource) []*store.Resource {
		if len(rows) > 0 || fallback == nil {
			return rows
		}
		if r := fallback(k); r != nil {
			return []*store.Resource{r}
		}
		return nil
	}
	var (
		noARN, noARNKept, denied, deniedKept skipTally
		transients                           transientTally
		errs                                 []error
		interrupted                          bool
	)
	d := iotDescribed{op: op}
	for i, read := range reads {
		key := name(keys[i])
		err := lateNXDOMAIN(op, read.err)
		if err != nil && isGone(err) {
			continue
		}
		rows := read.rows
		if err != nil || read.noARN {
			rows = orFallback(keys[i], rows)
		}
		d.rows = append(d.rows, rows...)
		switch {
		case read.noARN && len(rows) > 0:
			noARNKept.skipped(key, nil)
		case read.noARN:
			noARN.skipped(key, nil)
		case err == nil:
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			interrupted = true
		case isAccessDenied(err) && len(rows) > 0:
			deniedKept.skipped(key, err)
		case isAccessDenied(err):
			denied.skipped(key, err)
		case transients.drop(key, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", op, key, err))
		}
	}
	// The keys readEachKey never read once ctx ended are listed all the same.
	for _, k := range keys[len(reads):] {
		d.rows = append(d.rows, orFallback(k, nil)...)
	}
	switch {
	case stopped != nil:
		errs = append(errs, fmt.Errorf("%s: %w", op, stopped))
	case interrupted:
		errs = append(errs, fmt.Errorf("%s: reads cut short: %w", op, ctx.Err()))
	}
	d.hard = errors.Join(errs...)
	if t := &transients.skipTally; t.count > 0 {
		d.transient = fmt.Errorf("%s: %d listed %s not read, first %s: %w", op, t.count, what, t.first, t.err)
	}
	d.warn = func() {
		warnNotStored(st, op, acct, region, &noARN, what+" have no ARN in their "+op+" response")
		if noARNKept.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d listed %s have no ARN in their %s response, so they were %s; first %s", noARNKept.count, what, op, kept, noARNKept.first),
			})
		}
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d listed %s not stored, first %s: %w", denied.count, what, denied.first, denied.err))
		}
		if deniedKept.count > 0 {
			_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d listed %s %s, first %s: %w", deniedKept.count, what, kept, deniedKept.first, deniedKept.err))
		}
	}
	return d
}

// iotDescribe describes each key with op (iotReadEach), one row per key;
// describe returns a nil row for a response without an ARN. listed (nil for
// none) is iotReadEach's fallback: a key's list element, stored when its
// describe fails other than with a not-found, has no ARN, or is never made.
// A transient or other failure is still returned, as iotReadEach says: the
// describe body was lost.
func iotDescribe[Key any](
	ctx context.Context, st *store.Store, acct *account, region, op, what string,
	keys []Key, name func(Key) string, isGone func(error) bool, listed func(Key) *store.Resource,
	describe func(context.Context, Key) (*store.Resource, error),
) iotDescribed {
	return iotReadEach(ctx, st, acct, region, op, what, keys, name, isGone, "stored as their list element, without the "+op+" body", listed,
		func(ctx context.Context, k Key) iotRead {
			r, err := describe(ctx, k)
			switch {
			case err != nil:
				return iotRead{err: err}
			case r == nil:
				return iotRead{noARN: true}
			}
			return iotRead{rows: []*store.Resource{r}}
		})
}

// describe is iotDescribe over l's keys, a key that gives no describe row
// stored as its list element when it carries its ARN.
func (l *iotListing) describe(
	ctx context.Context, st *store.Store, acct *account, region, op string, isGone func(error) bool,
	describe func(context.Context, string) (*store.Resource, error),
) iotDescribed {
	return iotDescribe(ctx, st, acct, region, op, l.what, l.keys, func(k string) string { return k }, isGone,
		func(k string) *store.Resource { return l.listed[k] }, describe)
}

// iotStoreTagged stores d's rows, each with the tags read for it by the tags reader
// (storeRowsTags), as the rows of l's listing: l's error is handled as
// storeReadRows says, and l's and d's warnings run only after a successful
// write. d's errors are added to p under d.op. It reports whether the write
// failed.
func iotStoreTagged(ctx context.Context, st *store.Store, acct *account, region string, p *phaseTotals, l *iotListing, d iotDescribed, tags rowTagReader) (storeWriteFailed bool) {
	stop := storeRowsTags(ctx, st, acct, region, l.op, d.rows, l.err, func() {
		l.warnNoKey(st, acct, region)
		d.warn()
	}, tags, p)
	p.errs.add(d.op, d.hard)
	p.errs.add(d.op, d.transient)
	return stop
}

// iotStore is iotStoreTagged for a type that carries no tags.
func iotStore(st *store.Store, acct *account, region string, p *phaseTotals, l *iotListing, d iotDescribed) (storeWriteFailed bool) {
	total, inserted, err := storeReadRows(st, acct, region, l.op, d.rows, l.err, func() {
		l.warnNoKey(st, acct, region)
		d.warn()
	})
	stop := p.addPhase(l.op, total, inserted, err)
	p.errs.add(d.op, d.hard)
	p.errs.add(d.op, d.transient)
	return stop
}

// iotStoreReads stores the rows d read for the keys of an earlier phase's
// listing, whose own error and warnings that phase reports, under d.op; d's
// warnings run only after a successful write. It reports whether the write
// failed.
func iotStoreReads(st *store.Store, acct *account, region string, p *phaseTotals, d iotDescribed) (storeWriteFailed bool) {
	total, inserted, err := storeReadRows(st, acct, region, d.op, d.rows, nil, d.warn)
	stop := p.addPhase(d.op, total, inserted, err)
	p.errs.add(d.op, d.hard)
	p.errs.add(d.op, d.transient)
	return stop
}

// iotPrincipalPhase names a phase that lists the principals attached to each
// key of an earlier phase's listing: its op, the items for its messages, the
// key's kind in the synthesized NativeID and its member in the attributes,
// and the row type.
type iotPrincipalPhase struct {
	op, what, kind, keyAttr, typ string
}

// scanIoTPrincipals lists the principals of each key (iotReadEach: a key
// ResourceNotFoundException says was deleted since it was listed is dropped
// silently, the principals read before any other failure are kept) and
// stores one row per (key, principal) under ph.op, adding its counts and
// errors to p. The keys' own listing error is the earlier phase's to report.
// It reports whether the write failed.
func scanIoTPrincipals(
	ctx context.Context, st *store.Store, acct *account, region, scanID string, p *phaseTotals,
	ph iotPrincipalPhase, keys []string, page func(key string) func(context.Context, *string) ([]string, *string, error),
) (storeWriteFailed bool) {
	d := iotReadEach(ctx, st, acct, region, ph.op, ph.what, keys, func(k string) string { return k }, isIoTNotFound,
		"stored only up to the denial", nil,
		func(ctx context.Context, key string) iotRead {
			principals, err := readTokenPages(ctx, page(key))
			rows := make([]*store.Resource, 0, len(principals))
			for _, principal := range principals {
				rows = append(rows, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           ph.typ,
					NativeID:       fmt.Sprintf("arn:aws:iot:%s:%s:%s/%s/principal/%s", region, acct.ID, ph.kind, key, principal),
					Name:           &principal,
					Region:         &region,
					AttributesJSON: mustJSON(map[string]string{ph.keyAttr: key, "Principal": principal}),
					DiscoveredBy:   scanID,
				})
			}
			return iotRead{rows: rows, err: err}
		})
	return iotStoreReads(st, acct, region, p, d)
}

// iotUntaggedManaged is tags without the read for AWS-default rows
// (ManagedByProvider): whether ListTagsForResource accepts their ARNs is
// unverified, and a rejection would fail every IoT scan. Such a row keeps
// the TagsJSON its builder set.
func iotUntaggedManaged(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if r.ManagedByProvider {
			return r.TagsJSON, nil
		}
		return read(ctx, r)
	}
	return tags
}

// iotPhase runs one phase of an IoT family, adding its counts and errors to
// p, and reports whether a write failed.
type iotPhase func(p *phaseTotals) (storeWriteFailed bool)

// iotCounted is the iotPhase of a phase that returns its counts and error,
// added to p under op.
func iotCounted(op string, run func() (int, int, error)) iotPhase {
	return func(p *phaseTotals) bool {
		t, i, err := run()
		return p.addPhase(op, t, i, err)
	}
}

// runIoTPhases runs phases in order. A phase's error does not stop the
// later ones; only a failed write does, since every later write would fail
// too.
func runIoTPhases(p *phaseTotals, phases ...iotPhase) (storeWriteFailed bool) {
	for _, run := range phases {
		if run(p) {
			return true
		}
	}
	return false
}

// scanIoT discovers all IoT resource types in one region by running family
// scanners concurrently (scanIoTFamilies) over one client and one tag read.
func scanIoT(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iot.NewFromConfig(acct.cfg, func(o *iot.Options) { o.Region = region })
	return scanIoTFamilies(ctx, client, acct, region, st, scanID, newIoTTags(client))
}

// scanIoTFamilies runs the families concurrently, every one reading tags
// through tags, so that one bound holds for them all. A family's error does
// not cancel the others: each family adds its phases to its own
// phaseTotals, joined once all are done. A failed write in any family skips
// the audit-suppression phase that runs after them.
func scanIoTFamilies(ctx context.Context, client *iot.Client, acct *account, region string, st *store.Store, scanID string, tags iotTags) (int, int, error) {
	families := []iotPhase{
		func(p *phaseTotals) bool { return scanIoTThings(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTCerts(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTDefender(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTJobs(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTSoftware(ctx, client, acct, region, st, scanID, p) },
		func(p *phaseTotals) bool { return scanIoTTopic(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTLogging(ctx, client, acct, region, st, scanID, p) },
		func(p *phaseTotals) bool { return scanIoTInventory(ctx, client, acct, region, st, scanID, p, tags) },
	}
	parts := make([]phaseTotals, len(families))
	stopped := make([]bool, len(families))
	var wg sync.WaitGroup
	for i, family := range families {
		wg.Go(func() { stopped[i] = family(&parts[i]) })
	}
	wg.Wait()
	var p phaseTotals
	for _, part := range parts {
		p.total += part.total
		p.inserted += part.inserted
		p.errs = append(p.errs, part.errs...)
	}
	// Phases added after the families run once every family is done, so the
	// established families' calls are made first.
	if !slices.Contains(stopped, true) {
		t, i, err := scanIoTAuditSuppressions(ctx, client, acct, region, st, scanID)
		p.addPhase("iot:ListAuditSuppressions", t, i, err)
	}
	return p.resultLateNXDOMAIN(st, acct, region)
}
