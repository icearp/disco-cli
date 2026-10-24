package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// SageMaker types are declared per family file via registerType;
	// scanSageMaker itself upserts nothing, only fans out to family scanners
	// (studio, training, inference, monitoring, …).
	// The name stays a string literal: regionsgen reads registerService names
	// from the AST and skips any other expression, which drops the service
	// from the generated region table.
	registerService(serviceEntry{name: "aws:sagemaker", fn: scanSageMaker})
}

const sagemakerService = "aws:sagemaker"

// sagemakerListTagsOp reads one SageMaker resource's tags.
const sagemakerListTagsOp = "sagemaker:ListTags"

// sagemakerTagsPageLimit is ListTags' MaxResults. The model sets a minimum of
// 50 and no maximum, and a resource holds at most 50 tags (TagList), so one
// page normally reads them all.
const sagemakerTagsPageLimit = 50

// sagemakerListPageLimit is the MaxResults of every SageMaker List op the
// scanners page: each one's model maximum.
const sagemakerListPageLimit = 100

// sagemakerTagsAPI reads tags; every family's narrow interface embeds it.
// tagReadSlots bounds the ListTags reads of every family of one scan
// together (sagemakerTagReader).
type sagemakerTagsAPI interface {
	ListTags(context.Context, *sagemaker.ListTagsInput, ...func(*sagemaker.Options)) (*sagemaker.ListTagsOutput, error)
	tagReadSlots() chan struct{}
}

// sagemakerClient is the client every family of one scan shares.
type sagemakerClient struct {
	*sagemaker.Client
	tagSlots chan struct{}
}

// newSageMakerClient returns the client of one region's scan, with fanoutMed
// tag read slots.
func newSageMakerClient(cfg sdkaws.Config, region string) sagemakerClient {
	return sagemakerClient{
		Client:   sagemaker.NewFromConfig(cfg, func(o *sagemaker.Options) { o.Region = region }),
		tagSlots: make(chan struct{}, fanoutMed),
	}
}

func (c sagemakerClient) tagReadSlots() chan struct{} { return c.tagSlots }

// sagemakerTagReader reads a row's tags with ListTags on its NativeID, which
// every type read through it holds as the resource's ARN. Each read takes one
// of the client's tagReadSlots: the families run concurrently, and each
// family's readRowTags alone would allow fanoutMed reads at once, so without
// the shared slots one region would send fanoutMed reads per family against
// SageMaker's low per-account rate.
//
// The ListTags model lists no error, so no code marks a resource deleted after
// it was listed: a row deleted between its listing and its tag read fails its
// phase with the error ListTags returns (decision #14, a known race), and is
// kept.
func sagemakerTagReader(client sagemakerTagsAPI) rowTagReader {
	return rowTagReader{
		op: sagemakerListTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			slots := client.tagReadSlots()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			defer func() { <-slots }()
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]smtypes.Tag, *string, error) {
				out, err := client.ListTags(ctx, &sagemaker.ListTagsInput{
					ResourceArn: sdkaws.String(r.NativeID), MaxResults: sdkaws.Int32(sagemakerTagsPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t smtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// sagemakerPut stores a phase's rows, read before its listing ended with
// listErr (nil when it ended normally). warn (nil for none) runs only after a
// successful write. scanSageMaker's probe has reached the endpoint before any
// listing runs, so an NXDOMAIN in listErr is a failure of this scan, not a
// region gap (lateNXDOMAIN).
type sagemakerPut func(rows []*store.Resource, listErr error, warn func()) (int, int, error)

// sagemakerStoreTagged returns the store step of a phase listed by op whose
// rows hold their ARN as NativeID: each row's tags are read
// (sagemakerTagReader) before the rows are written.
func sagemakerStoreTagged(ctx context.Context, client sagemakerTagsAPI, acct *account, region string, st *store.Store, op string) sagemakerPut {
	return func(rows []*store.Resource, listErr error, warn func()) (int, int, error) {
		var p phaseTotals
		// A failed write is also in p's errors, and this one phase has
		// nothing after it to stop.
		_ = storeRowsTags(ctx, st, acct, region, op, rows, lateNXDOMAIN(op, listErr), warn, sagemakerTagReader(client), &p)
		return sagemakerResult(&p)
	}
}

// sagemakerStoreUntagged returns the store step of a phase listed by op whose
// tags are not read; each caller says why.
func sagemakerStoreUntagged(st *store.Store, acct *account, region, op string) sagemakerPut {
	return func(rows []*store.Resource, listErr error, warn func()) (int, int, error) {
		return storeReadRows(st, acct, region, op, rows, lateNXDOMAIN(op, listErr), warn)
	}
}

// sagemakerOpErrs is the errors of a SageMaker phase or family, each under the
// op it failed under. The phase and family runners carry it up unjoined, so
// that scanSageMaker joins every error once (phaseErrs.join), demoting each
// transient one under its own op (e.g. sagemaker:ListTags) rather than its
// phase's List op or the service.
type sagemakerOpErrs struct{ errs phaseErrs }

func (e sagemakerOpErrs) Unwrap() []error {
	errs := make([]error, len(e.errs))
	for i, f := range e.errs {
		errs[i] = f.err
	}
	return errs
}

func (e sagemakerOpErrs) Error() string { return errors.Join(e.Unwrap()...).Error() }

// sagemakerResult returns p's counts and its errors as sagemakerOpErrs, nil
// when there are none.
func sagemakerResult(p *phaseTotals) (int, int, error) {
	if len(p.errs) == 0 {
		return p.total, p.inserted, nil
	}
	return p.total, p.inserted, sagemakerOpErrs{p.errs}
}

// addSageMakerPhase adds a phase's counts and error to p: the members of a
// sagemakerOpErrs under their own ops, any other error under op. It reports
// whether a write failed, after which the runner must stop.
func addSageMakerPhase(p *phaseTotals, op string, total, inserted int, err error) (storeWriteFailed bool) {
	// Only err itself is unpacked: a sagemakerOpErrs wrapped inside another
	// error is filed whole under op, which keeps every member.
	labelled, ok := err.(sagemakerOpErrs)
	if !ok {
		return p.addPhase(op, total, inserted, err)
	}
	p.total += total
	p.inserted += inserted
	p.errs = append(p.errs, labelled.errs...)
	return errors.Is(err, store.ErrStoreWrite)
}

// sagemakerPhase is one phase of a family; op labels its error.
type sagemakerPhase struct {
	op  string
	run func() (int, int, error)
}

// runSageMakerPhases runs every phase in order, so a failed phase does not
// skip the ones after it (a dependent phase then sees fewer parents). Only a
// failed write stops it: every later write would fail too.
func runSageMakerPhases(phases ...sagemakerPhase) (int, int, error) {
	var p phaseTotals
	for _, phase := range phases {
		t, i, err := phase.run()
		if addSageMakerPhase(&p, phase.op, t, i, err) {
			break
		}
	}
	return sagemakerResult(&p)
}

// runSageMakerFamilies runs the families concurrently on ctx itself, so one
// family's error cannot cancel the others and lose the rows they have not
// stored yet. It joins every family's errors once (phaseErrs.join).
func runSageMakerFamilies(ctx context.Context, st *store.Store, acct *account, region string, families ...func(context.Context) (int, int, error)) (int, int, error) {
	type result struct {
		total, inserted int
		err             error
	}
	results := make([]result, len(families))
	var wg sync.WaitGroup
	for i, family := range families {
		wg.Go(func() {
			r := &results[i]
			r.total, r.inserted, r.err = family(ctx)
		})
	}
	wg.Wait()
	var p phaseTotals
	for _, r := range results {
		addSageMakerPhase(&p, sagemakerService, r.total, r.inserted, r.err)
	}
	return p.result(st, acct, region)
}

// sagemakerDescribe lists keys with listOp and describes each with describeOp,
// for types whose listing omits fields the row needs.
type sagemakerDescribe[Key any] struct {
	listOp, describeOp string
	// notFound holds the codes describeOp's model lists for a resource that
	// does not exist; nil when it lists none.
	notFound []string
	// list reads one page of keys.
	list func(ctx context.Context, token *string) ([]Key, *string, error)
	// name names a key in warnings and errors; "" when the listing left out
	// a field describeOp needs.
	name func(Key) string
	// describe returns a key's row, nil when the response carries no ARN.
	describe func(context.Context, Key) (*store.Resource, error)
	// listed returns the row a key's listing gives on its own (the summary
	// verbatim as attributes, sagemakerListedRow), stored when its describe
	// failed other than with a notFound code, or was cut short or never run
	// because ctx ended, so that no listed resource is lost; nil for a type
	// whose summary carries no ARN of its own, whose key is then not stored.
	listed func(Key) *store.Resource
}

// sagemakerListedRow is the row of a summary listed with its own ARN, for a
// sagemakerDescribe's listed; nil when arn is empty. status is the summary's
// copy of the field the describe stores as Status ("" for a type whose
// summary has none, or whose describe stores none).
func sagemakerListedRow(acct *account, region, scanID, rtype string, arn, name *string, created *time.Time, status string, summary any) *store.Resource {
	if sv(arn) == "" {
		return nil
	}
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           rtype,
		NativeID:       sv(arn),
		Region:         &region,
		Status:         nonEmptyPtr(status),
		CreatedAt:      tp(created),
		AttributesJSON: mustJSON(summary),
		DiscoveredBy:   scanID,
	}
	if n := sv(name); n != "" {
		r.Name = &n
	}
	return r
}

// sagemakerWarnNameless warns once for every parent in t not listed for its
// children because it has an empty name, naming what the parents are.
func sagemakerWarnNameless(st *store.Store, op string, acct *account, region string, t *skipTally, what string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s have an empty name, so their children were not listed; first %s", t.count, what, t.first),
	})
}

// sagemakerKeyName is the name of a key that is the resource's name.
func sagemakerKeyName(name string) string { return name }

// sagemakerDescribed is one key's describe result.
type sagemakerDescribed struct {
	row *store.Resource
	err error
}

// scan lists every page, describes each key fanoutMed at a time (readEachKey),
// and stores the rows through put, those of keys listed before a failed page
// included. Per key:
//   - described: the row is stored;
//   - a notFound code: deleted after it was listed; not stored, no warning;
//   - denied: stored from its listing (d.listed) when the listing carries its
//     ARN, else not stored; one warning per case after the write;
//   - described without an ARN, or lacking a field describeOp needs (name
//     returns ""): not stored; one warning per reason after the write;
//   - still transient after the SDK's retries: stored from its listing as
//     when denied, and every such key is one error under describeOp, which
//     phaseErrs.join can demote: the describe data is lost either way;
//   - cut short or not described because ctx ended: stored from its listing
//     as when denied; one error under describeOp covers every such key;
//   - any other error, an NXDOMAIN included: stored from its listing as when
//     denied, and the error is returned under describeOp.
func (d sagemakerDescribe[Key]) scan(ctx context.Context, st *store.Store, acct *account, region string, put sagemakerPut) (int, int, error) {
	listed, listErr := readTokenPages(ctx, d.list)
	return d.describeEach(ctx, st, acct, region, listed, listErr, put)
}

// describeEach is scan on keys already listed, whose listing ended with
// listErr; d.list is not called.
func (d sagemakerDescribe[Key]) describeEach(ctx context.Context, st *store.Store, acct *account, region string, listed []Key, listErr error, put sagemakerPut) (int, int, error) {
	var (
		keys         []Key
		names        []string
		nameless     skipTally
		denied       skipTally
		deniedListed skipTally
		noARN        skipTally
		transients   transientTally
		errs         []error
		interrupted  bool
	)
	for i, k := range listed {
		name := d.name(k)
		if name == "" {
			nameless.skipped(fmt.Sprintf("#%d", i+1), nil)
			continue
		}
		keys, names = append(keys, k), append(names, name)
	}
	reads, stopped := readEachKey(ctx, keys, func(ctx context.Context, k Key) sagemakerDescribed {
		r, err := d.describe(ctx, k)
		return sagemakerDescribed{r, err}
	})
	var rows []*store.Resource
	fromListing := func(k Key) bool {
		if d.listed == nil {
			return false
		}
		r := d.listed(k)
		if r != nil {
			rows = append(rows, r)
		}
		return r != nil
	}
	for i, read := range reads {
		// The listing reached the endpoint, so an NXDOMAIN now is a failure
		// of this scan, not a region gap (lateNXDOMAIN).
		switch err := lateNXDOMAIN(d.describeOp, read.err); {
		case err == nil && read.row == nil:
			noARN.skipped(names[i], nil)
		case err == nil:
			rows = append(rows, read.row)
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			fromListing(keys[i])
			interrupted = true
		case isAPIErrorCode(err, d.notFound...):
		case isAccessDenied(err):
			if fromListing(keys[i]) {
				deniedListed.skipped(names[i], err)
			} else {
				denied.skipped(names[i], err)
			}
		case transients.drop(names[i], err):
			fromListing(keys[i])
		default:
			fromListing(keys[i])
			errs = append(errs, fmt.Errorf("%s %s: %w", d.describeOp, names[i], err))
		}
	}
	// readEachKey returns no result for the keys it did not start.
	for _, k := range keys[len(reads):] {
		fromListing(k)
	}
	// One stop member, whether ctx ended before some reads started or only
	// during the reads already running.
	switch {
	case stopped != nil:
		errs = append(errs, fmt.Errorf("%s: %w", d.describeOp, stopped))
	case interrupted:
		errs = append(errs, fmt.Errorf("%s: describes cut short: %w", d.describeOp, ctx.Err()))
	}
	var p phaseTotals
	p.errs.add(d.describeOp, errors.Join(errs...))
	if t := &transients.skipTally; t.count > 0 {
		p.errs.add(d.describeOp, fmt.Errorf("%s: %d listed item(s) not described, first %s: %w", d.describeOp, t.count, t.first, t.err))
	}
	total, inserted, err := put(rows, listErr, func() {
		warnNotStored(st, d.listOp, acct, region, &nameless, "items lack a field their describe needs")
		denied.warnDeniedIn(st, d.describeOp, acct, region, "listed item(s) not stored as their describe was")
		deniedListed.warnDeniedIn(st, d.describeOp, acct, region, "listed item(s) stored from their listing only as their describe was")
		warnNotStored(st, d.describeOp, acct, region, &noARN, "items were described without an ARN")
	})
	addSageMakerPhase(&p, d.listOp, total, inserted, err)
	return sagemakerResult(&p)
}

// sagemakerInUseProbe checks three highest-signal SageMaker surfaces for
// activity: Studio domains (modern entry), notebook instances (legacy entry),
// and inference endpoints (deployed models). A full scan makes ~42 ListXxx
// calls across 8 families; SageMaker's low per-account TPS quota makes the
// SDK's adaptive retry throttle for minutes. Probing first cuts dormant
// accounts to ~3 RPCs instead of dozens — false negatives (e.g.
// training-jobs-only accounts) are an accepted trade-off for the wall-time win.
func sagemakerInUseProbe(ctx context.Context, client *sagemaker.Client) (bool, error) {
	one := sdkaws.Int32(1)
	if d, err := client.ListDomains(ctx, &sagemaker.ListDomainsInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(d.Domains) > 0 {
		return true, nil
	}
	if n, err := client.ListNotebookInstances(ctx, &sagemaker.ListNotebookInstancesInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(n.NotebookInstances) > 0 {
		return true, nil
	}
	if e, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(e.Endpoints) > 0 {
		return true, nil
	}
	return false, nil
}

// scanSageMaker discovers all SageMaker resource types in one region by
// running family scanners concurrently (runSageMakerFamilies); each runs its
// own phases in order (runSageMakerPhases).
func scanSageMaker(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := newSageMakerClient(acct.cfg, region)

	// Phase 0: cheap probe skips the full 42-phase fan-out on dormant accounts.
	inUse, perr := sagemakerInUseProbe(ctx, client.Client)
	var apiErr smithy.APIError
	switch {
	case perr == nil:
		if !inUse {
			return 0, 0, nil
		}
	case isAccessDenied(perr):
		return 0, 0, skipIfAccessDenied(st, "sagemaker:probe", acct.ID, region, perr)
	case !errors.As(perr, &apiErr):
		// No answer from the endpoint (an NXDOMAIN, a DNS server timeout, a
		// reset): returned for the dispatcher to classify. The families run
		// only once the endpoint answered, so a later NXDOMAIN is a failure
		// of this scan, not a region gap (sagemakerPut).
		return 0, 0, perr
	}
	// The endpoint answered, possibly with an API error: every family runs,
	// with its own per-call tolerance.
	return runSageMakerFamilies(ctx, st, acct, region,
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerStudio(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerTraining(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerInference(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerRegistry(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerMonitoring(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerPipelines(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerEdge(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerMisc(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerGovernance(ctx, client, acct, region, st, scanID)
		},
	)
}
