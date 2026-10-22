package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

// errRepeatedNextToken fails a hand-rolled pagination loop whose service
// returned the token it was just sent: following it would loop forever, and
// stopping silently would store a partial listing as if it were complete.
var errRepeatedNextToken = errors.New("AWS returned the same NextToken twice in a row, so the listing stopped incomplete; rescan, and report it if it recurs")

// pageScan drives an AWS SDK v2 paginator, converts each page's items to
// *store.Resource via toResource, and upserts in per-page batches. iamAction
// labels the call for AccessDenied warnings (e.g. "secretsmanager:ListSecrets").
//
// hasMore/next are callbacks, not an interface, because every SDK v2
// paginator's NextPage carries a service-typed variadic
// (`...func(*<svc>.Options)`) no single generic interface can express.
// Caller passes `p.HasMorePages` and a one-line closure around `p.NextPage`.
// Transient network errors are wrapped at the dispatch layer (aws.go
// scanRegion); pageScan only handles AccessDenied and propagates.
func pageScan[Page any, Item any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	hasMore func() bool,
	next func(context.Context) (Page, error),
	items func(Page) []Item,
	toResource func(Item) *store.Resource,
) (total, inserted int, err error) {
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", iamAction, err)
		}
		raw := items(page)
		if len(raw) == 0 {
			continue
		}
		batch := make([]*store.Resource, 0, len(raw))
		for _, it := range raw {
			if r := toResource(it); r != nil {
				batch = append(batch, r)
			}
		}
		if len(batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", iamAction, err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}

// pageScanConcurrent is pageScan's sibling for the List-then-Describe N+1
// pattern: the List API yields skeletons (names or ARNs), each requiring a
// concurrent Describe/Get to assemble the persisted Resource. enrich runs
// per item with a derived context; returning (nil, nil) silently drops the
// item (e.g. on per-item AccessDenied — mirrors existing scanner behavior).
// A non-nil error from enrich aborts the page via errgroup.
//
// concurrency caps in-flight enrich goroutines per page; 0 means unbounded
// (matches sns/acm/eks). Use a positive bound where unbounded fan-out has
// tripped throttling in practice.
func pageScanConcurrent[Page any, Item any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	hasMore func() bool,
	next func(context.Context) (Page, error),
	items func(Page) []Item,
	enrich func(ctx context.Context, item Item) (*store.Resource, error),
	concurrency int,
) (total, inserted int, err error) {
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", iamAction, err)
		}
		raw := items(page)
		if len(raw) == 0 {
			continue
		}
		var (
			mu    sync.Mutex
			batch = make([]*store.Resource, 0, len(raw))
		)
		g, gctx := errgroup.WithContext(ctx)
		if concurrency > 0 {
			g.SetLimit(concurrency)
		}
		for _, it := range raw {
			g.Go(func() error {
				r, err := enrich(gctx, it)
				if err != nil {
					return err
				}
				if r == nil {
					return nil
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return total, inserted, err
		}
		if len(batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", iamAction, err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}

// nonEmptyPtr returns nil for "", so an absent SDK enum leaves a column unset
// rather than storing an empty string.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// childParent is a parent listed earlier in the same scan: id is what the
// child list op keys on, arn is the parent row's NativeID.
type childParent struct{ id, arn string }

// fanOutErrs is the per-key error policy shared by childFanOut and
// describeFanOut, so a change to the skip or deny handling lands in both.
type fanOutErrs struct {
	st       *store.Store
	acct     *account
	region   string
	op       string
	isSkip   func(error) bool
	denyOnce sync.Once
}

// keyErr classifies a non-nil error from one key's call. It returns nil when
// the key is dropped: an error isSkip accepts drops it silently (checked first
// so a service can claim access-denied-coded feature gaps), and an AccessDenied
// drops it after warning once for op while the other keys continue. Anything
// else comes back wrapped with op and key.
func (f *fanOutErrs) keyErr(key string, err error) error {
	switch {
	case f.isSkip(err):
		return nil
	case isAccessDenied(err):
		f.denyOnce.Do(func() { _ = skipIfAccessDenied(f.st, f.op, f.acct.ID, f.region, err) })
		return nil
	default:
		return fmt.Errorf("%s %s: %w", f.op, key, err)
	}
}

// childFanOut runs list for each parent concurrently (fanoutMed), upserts every
// child and links it under its parent via the contains closure. A failed
// parent is dropped or returned per fanOutErrs.keyErr.
func childFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var (
		mu       sync.Mutex
		children []containedChild
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isSkip}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			rows, err := list(gctx, p)
			if err != nil {
				return errs.keyErr(p.id, err)
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				children = append(children, containedChild{row: r, parentID: parentID})
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return upsertContained(st, op, children)
}

// childFanOutStored is childFanOut for parents whose children are too many or
// too large to hold until one final upsert: storeChildren runs for each parent
// concurrently (fanoutMed), writes the children itself (a page at a time,
// through upsertContained) and returns the counts. A failed parent is dropped
// or returned per fanOutErrs.keyErr; the rows it stored before failing stay
// and are counted.
func childFanOutStored(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	var total, inserted atomic.Int64
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isSkip}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			t, i, err := storeChildren(gctx, p)
			total.Add(int64(t))
			inserted.Add(int64(i))
			if err != nil {
				return errs.keyErr(p.id, err)
			}
			return nil
		})
	}
	err := g.Wait()
	return int(total.Load()), int(inserted.Load()), err
}

// describeFanOut runs describe for each key concurrently (fanoutMed) and
// returns the rows it builds; a nil row is dropped. A failed key is dropped or
// returned per fanOutErrs.keyErr.
func describeFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, keys []string,
	isSkip func(error) bool, describe func(context.Context, string) (*store.Resource, error),
) ([]*store.Resource, error) {
	var (
		mu   sync.Mutex
		rows []*store.Resource
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isSkip}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, key := range keys {
		g.Go(func() error {
			r, err := describe(gctx, key)
			if err != nil {
				return errs.keyErr(key, err)
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			rows = append(rows, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

// skipTally counts the keys of a fan-out skipped for one reason and keeps the
// first with its error, so that one warning covers them all. Safe for
// concurrent skipped calls; read it only after the fan-out returns.
type skipTally struct {
	mu    sync.Mutex
	count int
	first string
	err   error
}

func (t *skipTally) skipped(key string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		t.first, t.err = key, err
	}
	t.count++
}

// transientTally collects the parents of a fan-out whose call still failed
// transiently after the SDK's retries, so that a throttled account reports one
// warning per fan-out rather than one per parent.
type transientTally struct{ skipTally }

// drop tallies parent and reports true when err is transient.
func (t *transientTally) drop(parent string, err error) bool {
	if !isTransientNetworkError(err) {
		return false
	}
	t.skipped(parent, err)
	return true
}

// warnSkipped warns once for every parent skipped, naming the first and its error.
func (t *transientTally) warnSkipped(st *store.Store, op string, acct *account, region string) {
	if t.count == 0 {
		return
	}
	_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf("%d parent(s) skipped, first %s: %w", t.count, t.first, t.err))
}

// notReadyTally collects the parents whose child listing the service refused
// with ValidationException, for fan-outs over parents that may not be ready,
// so that one warning covers them all. Each caller says why it skips rather
// than fails on that code.
type notReadyTally struct{ skipTally }

// drop tallies parent and reports true when err is ValidationException.
func (t *notReadyTally) drop(parent string, err error) bool {
	return t.dropCode("ValidationException", parent, err)
}

// dropCode tallies parent and reports true when err has the error code
// code. Every per-code skip matches through it and warns through warnCode.
func (t *skipTally) dropCode(code, parent string, err error) bool {
	if !isAPIErrorCode(err, code) {
		return false
	}
	t.skipped(parent, err)
	return true
}

// warnSkipped warns once for every parent skipped, naming what the parents
// are (e.g. "parent(s)"), why the service may refuse one, the first and its error.
func (t *notReadyTally) warnSkipped(st *store.Store, op string, acct *account, region, what, why string) {
	t.warnCode(st, op, acct, region, "ValidationException", what, why)
}

// warnDenied warns once, through skipIfAccessDenied, for every key whose call
// was denied, naming what the keys are, the first and its error.
func (t *skipTally) warnDenied(st *store.Store, op, accountID, what string) {
	if t.count == 0 {
		return
	}
	_ = skipIfAccessDenied(st, op, accountID, "", fmt.Errorf("%d %s denied, first %s: %w", t.count, what, t.first, t.err))
}

// warnCode warns once for every parent skipped with the error code code,
// naming what the parents are, why the service may refuse one, the first and
// its error.
func (t *skipTally) warnCode(st *store.Store, op string, acct *account, region, code, what, why string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s skipped with %s; %s, first %s: %v", t.count, what, code, why, t.first, t.err),
	})
}

// byParentID and byParentARN pick which of a parent's keys a skip warning
// names.
func byParentID(p childParent) string  { return p.id }
func byParentARN(p childParent) string { return p.arn }

// childFanOutSkipTransient is childFanOut, except that a parent whose call
// still fails transiently after the SDK's retries is skipped, keeping its
// siblings' rows, and the skips are reported in one warning naming label of
// the first.
func childFanOutSkipTransient(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var transient transientTally
	total, inserted, err := childFanOut(ctx, st, acct, region, op, parents, isSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := list(ctx, p)
			if transient.drop(label(p), err) {
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// childFanOutSkipNotReady is childFanOutSkipTransient, except that a parent
// whose call fails with ValidationException is also skipped, keeping its
// siblings' rows, and those skips are reported in one warning naming what the
// parents are, why the service may refuse one, and label of the first. For
// parents listed in states (creating, failed, deleting) whose effect on the
// child op has not been observed: failing the phase would fail it on every
// scan, and skipping silently would hide a real input error.
func childFanOutSkipNotReady(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string, what, why string,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var notReady notReadyTally
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, op, parents, isSkip, label,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := list(ctx, p)
			if notReady.drop(label(p), err) {
				return nil, nil
			}
			return rows, err
		})
	notReady.warnSkipped(st, op, acct, region, what, why)
	return total, inserted, err
}

// childFanOutStoredSkipTransient is childFanOutStored with the transient skip
// of childFanOutSkipTransient; rows a skipped parent stored before its error
// stay and are counted.
func childFanOutStoredSkipTransient(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string,
	storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	var transient transientTally
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isSkip,
		func(ctx context.Context, p childParent) (int, int, error) {
			t, i, err := storeChildren(ctx, p)
			if transient.drop(label(p), err) {
				return t, i, nil
			}
			return t, i, err
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// childFanOutStoredSkipNotReady is childFanOutStoredSkipTransient, except that
// a parent whose call fails with one of notReadyCodes is also skipped, keeping
// its siblings' rows and the rows it stored before the error, and the skips
// are reported in one warning per code naming what the parents are, why the
// service may refuse one, and label of the first. For the parent states
// childFanOutSkipNotReady describes, when the children are stored a page at a
// time.
func childFanOutStoredSkipNotReady(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string, what, why string, notReadyCodes []string,
	storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	codes := make([]skipCode, len(notReadyCodes))
	for k, code := range notReadyCodes {
		codes[k] = skipCode{code: code, why: why}
	}
	return childFanOutStoredSkipCodes(ctx, st, acct, region, op, parents, isSkip, label, what, codes, storeChildren)
}

// skipCode is an error code a fan-out skips per parent, with why the service
// may refuse a parent with it.
type skipCode struct{ code, why string }

// childFanOutStoredSkipCodes is childFanOutStoredSkipNotReady with a reason
// per code, for fan-outs whose skipped codes have different causes.
func childFanOutStoredSkipCodes(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string, what string, codes []skipCode,
	storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	tallies := make([]skipTally, len(codes))
	total, inserted, err := childFanOutStoredSkipTransient(ctx, st, acct, region, op, parents, isSkip, label,
		func(ctx context.Context, p childParent) (int, int, error) {
			t, i, err := storeChildren(ctx, p)
			for k, c := range codes {
				if tallies[k].dropCode(c.code, label(p), err) {
					return t, i, nil
				}
			}
			return t, i, err
		})
	for k, c := range codes {
		tallies[k].warnCode(st, op, acct, region, c.code, what, c.why)
	}
	return total, inserted, err
}

// childFanOutCollectSkipCodes is childFanOutStoredSkipCodes for parents with
// a row or two each, where a write per parent would cost more than the
// listing: the rows list returns are collected and stored under their parents
// in one upsert after the fan-out. The skip and warning rules are those of
// childFanOutStoredSkipCodes; the rows of parents listed before a failure are
// still stored, and the failure is returned alongside any write error.
func childFanOutCollectSkipCodes(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, label func(childParent) string, what string, codes []skipCode,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var (
		mu       sync.Mutex
		children []containedChild
	)
	_, _, err := childFanOutStoredSkipCodes(ctx, st, acct, region, op, parents, isSkip, label, what, codes,
		func(ctx context.Context, p childParent) (int, int, error) {
			rows, err := list(ctx, p)
			if err != nil {
				return 0, 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				children = append(children, containedChild{row: r, parentID: parentID})
			}
			return 0, 0, nil
		})
	total, inserted, uerr := upsertContained(st, op, children)
	return total, inserted, errors.Join(err, uerr)
}

// isDemotableTransient reports whether a phase's error may be reported as a
// transient warning and dropped: it is transient and carries no store write
// failure. A phase can join a transient list error with a failed upsert of the
// rows listed before it, and that write failure must stay a hard error.
func isDemotableTransient(err error) bool {
	return isTransientNetworkError(err) && !errors.Is(err, store.ErrStoreWrite)
}

// demoteTransient reports a phase's transient error as a warning and drops it,
// for scanners that join the errors of independent phases. The dispatcher
// classifies a join as transient when any member is a network error or its
// first API error is a throttle or 5xx, so a joined transient error would
// report another phase's hard error as a transient one that a rescan fixes.
// NXDOMAIN is returned unchanged: the dispatcher reads it as a silent
// (region: unavailable) before its transient rung, so demoting it would turn
// a region gap into a warning.
func demoteTransient(st *store.Store, op string, acct *account, region string, err error) error {
	if isDemotableTransient(err) && !isDNSNotFound(err) {
		return skipIfTransient(st, op, acct.ID, region, err)
	}
	return err
}

// containedChild is a child row and the ResourceID of the parent that
// contains it, or "" when it has no stored parent.
type containedChild struct {
	row      *store.Resource
	parentID string
}

// upsertRegionChildren stores the children a region-wide listing collected,
// including the pages read before err. A denial warns and is dropped; any
// other err is returned wrapped with op, alongside the stored pages' counts.
func upsertRegionChildren(st *store.Store, acct *account, region, op string, children []containedChild, err error) (int, int, error) {
	if err != nil && isAccessDenied(err) {
		_ = skipIfAccessDenied(st, op, acct.ID, region, err)
		err = nil
	}
	total, inserted, uerr := upsertContained(st, op, children)
	if err != nil {
		return total, inserted, errors.Join(fmt.Errorf("%s: %w", op, err), uerr)
	}
	return total, inserted, uerr
}

// upsertContained stores the children, then links each under its parent via
// the contains closure; the pairs need the row IDs the upsert assigns.
func upsertContained(st *store.Store, op string, children []containedChild) (int, int, error) {
	batch := make([]*store.Resource, len(children))
	for i, c := range children {
		batch[i] = c.row
	}
	total, inserted, err := upsertBatch(st, batch, op)
	if err != nil {
		return 0, 0, err
	}
	pairs := make([][2]string, 0, len(children))
	for _, c := range children {
		// The parent is optional: a child listed region-wide may name a parent
		// that was not stored this scan, and is stored unlinked.
		if c.parentID != "" {
			pairs = append(pairs, [2]string{c.row.ID, c.parentID})
		}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure %s: %w", op, err)
	}
	return total, inserted, nil
}

// warnNotStored warns once for the listed items of a phase that carry no
// identifier to store them under; why says which one is missing.
func warnNotStored(st *store.Store, op string, acct *account, region string, t *skipTally, why string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d listed %s, so they were not stored; first %s", t.count, why, t.first),
	})
}
