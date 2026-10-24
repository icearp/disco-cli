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

// errTruncatedWithoutToken fails a hand-rolled pagination loop whose page says
// more results follow but carries no token to fetch them with.
var errTruncatedWithoutToken = errors.New("AWS marked the listing truncated but returned no continuation token, so it stopped incomplete; rescan, and report it if it recurs")

// errStoreRead marks a failed read of rows a scanner stored earlier in the
// scan.
var errStoreRead = errors.New("store read failed")

// storeReadError wraps a failed store read in errStoreRead. The cause is
// rendered with %v, not %w, for the reason store.storeWriteError gives: a
// dropped Postgres connection surfaces as io.EOF or a net.Error, which the
// dispatcher would otherwise report as a transient cloud glitch.
func storeReadError(what string, cause error) error {
	return fmt.Errorf("%w: %s: %v", errStoreRead, what, cause)
}

// incompleteLists collects, across one fan-out's concurrent keys, the errors
// of listings that ended early on errRepeatedNextToken or
// errTruncatedWithoutToken. Such a key keeps the rows it read and does not
// fail the errgroup, so its siblings are not cancelled; the fan-out stores
// every row, then returns the collected errors.
type incompleteLists struct {
	mu   sync.Mutex
	errs []error
}

// isIncompleteListing reports whether err ended a listing early: on a
// repeated token, or on a truncated page without one.
func isIncompleteListing(err error) bool {
	return errors.Is(err, errRepeatedNextToken) || errors.Is(err, errTruncatedWithoutToken)
}

// continuesPastIncomplete reports whether work may go on past err: it ended a
// listing early and carries no store write failure. A broken store must still
// stop the work, since every later write would fail too.
func continuesPastIncomplete(err error) bool {
	return isIncompleteListing(err) && !errors.Is(err, store.ErrStoreWrite)
}

// take reports whether work may go on past err (continuesPastIncomplete),
// recording it under op and key when it may.
func (l *incompleteLists) take(op, key string, err error) bool {
	if !continuesPastIncomplete(err) {
		return false
	}
	l.record(fmt.Errorf("%s %s: %w", op, key, err))
	return true
}

func (l *incompleteLists) record(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

// err joins the recorded errors; nil when none was recorded. Read it only
// after the fan-out returns.
func (l *incompleteLists) err() error { return errors.Join(l.errs...) }

// lastPage reports whether a hand-rolled NextToken loop has read its last
// page, given the token it sent and the one the page returned: true when got
// is empty, and true with errRepeatedNextToken when got repeats sent. The
// caller returns that error with the rows it read, so the listing ends marked
// partial instead of reading as complete.
func lastPage(sent, got *string) (bool, error) {
	next := sv(got)
	if next == "" {
		return true, nil
	}
	if next == sv(sent) {
		return true, fmt.Errorf("NextToken %q: %w", next, errRepeatedNextToken)
	}
	return false, nil
}

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

// upsertListed stores the rows a listing read and returns err, wrapped with
// op, alongside any write error, so a listing that stopped early keeps what
// it read and marks the scan partial.
func upsertListed(st *store.Store, batch []*store.Resource, op string, err error) (int, int, error) {
	total, inserted, uerr := upsertBatch(st, batch, op)
	if err != nil {
		err = fmt.Errorf("%s: %w", op, err)
	}
	return total, inserted, errors.Join(err, uerr)
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
// parent is dropped or returned per fanOutErrs.keyErr, except a listing that
// ended early (incompleteLists): its rows are stored with its siblings' and
// its error is returned after them.
func childFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var (
		mu         sync.Mutex
		children   []containedChild
		incomplete incompleteLists
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isSkip}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			rows, err := list(gctx, p)
			if err != nil && !incomplete.take(op, p.id, err) {
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
		return 0, 0, errors.Join(err, incomplete.err())
	}
	total, inserted, uerr := upsertContained(st, op, children)
	return total, inserted, errors.Join(incomplete.err(), uerr)
}

// childFanOutStored is childFanOut for parents whose children are too many or
// too large to hold until one final upsert: storeChildren runs for each parent
// concurrently (fanoutMed), writes the children itself (a page at a time,
// through upsertContained) and returns the counts. A failed parent is dropped
// or returned per fanOutErrs.keyErr; the rows it stored before failing stay
// and are counted. A parent whose listing ended early (incompleteLists) does
// not cancel its siblings; its error is returned after they finish.
func childFanOutStored(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	var (
		total, inserted atomic.Int64
		incomplete      incompleteLists
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isSkip}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			t, i, err := storeChildren(gctx, p)
			total.Add(int64(t))
			inserted.Add(int64(i))
			if err != nil && !incomplete.take(op, p.id, err) {
				return errs.keyErr(p.id, err)
			}
			return nil
		})
	}
	err := g.Wait()
	return int(total.Load()), int(inserted.Load()), errors.Join(err, incomplete.err())
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

// readEachKey runs read for each key concurrently (fanoutMed) and returns the
// results in key order, so what the caller builds from them, its warnings
// included, does not depend on which read finished first. No key's failure
// cancels another. Once ctx is done no further key is read: the results then
// cover only the keys before it, and the error says how many were not read.
// It returns only after every read it started has finished.
func readEachKey[Key, Got any](ctx context.Context, keys []Key, read func(context.Context, Key) Got) ([]Got, error) {
	results := make([]Got, len(keys))
	slots := make(chan struct{}, fanoutMed)
	var wg sync.WaitGroup
	for i, key := range keys {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		// Checked after the slot is taken too: ctx may end while waiting for
		// one, and select picks at random when both cases are ready.
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return results[:i], fmt.Errorf("%d of %d not read: %w", len(keys)-i, len(keys), err)
		}
		wg.Go(func() {
			defer func() { <-slots }()
			results[i] = read(ctx, key)
		})
	}
	wg.Wait()
	return results, nil
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

// dropUnlessNXDOMAIN is drop for a phase that runs only after an earlier
// phase on the same host stored rows: an NXDOMAIN, which
// isTransientNetworkError also accepts, is not dropped but returned, so the
// scan's resultLateNXDOMAIN reports it as a failure of this scan instead of
// a warning.
func (t *transientTally) dropUnlessNXDOMAIN(parent string, err error) bool {
	return !isDNSNotFound(err) && t.drop(parent, err)
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

// warnDeniedIn is warnDenied for a regional fan-out: the warning is scoped to
// region. what says what the keys are and what became of them.
func (t *skipTally) warnDeniedIn(st *store.Store, op string, acct *account, region, what string) {
	if t.count == 0 {
		return
	}
	_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d %s denied, first %s: %w", t.count, what, t.first, t.err))
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
// childFanOutStoredSkipCodes; the rows of parents listed before a failure,
// and those a parent returns with its error, are still stored, and the failure
// is returned alongside any write error.
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
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				children = append(children, containedChild{row: r, parentID: parentID})
			}
			return 0, 0, err
		})
	total, inserted, uerr := upsertContained(st, op, children)
	return total, inserted, errors.Join(err, uerr)
}

// demoteTransient reports a phase's transient error as a warning and drops it,
// for scanners that join the errors of independent phases, so each transient
// one is warned about under the op that failed and only the hard ones reach
// the dispatcher. A join is transient only when every member is
// (isTransientNetworkError), so a transient list error joined with a failed
// upsert or an incomplete listing stays an error.
// NXDOMAIN is returned unchanged: the dispatcher reads it as a silent
// (region: unavailable) before its transient rung, so demoting it would turn
// a region gap into a warning.
func demoteTransient(st *store.Store, op string, acct *account, region string, err error) error {
	if isDemotableTransient(err) {
		return skipIfTransient(st, op, acct.ID, region, err)
	}
	return err
}

// isDemotableTransient reports whether err is one demoteTransient turns into a
// warning: every member transient, and no NXDOMAIN among them.
func isDemotableTransient(err error) bool {
	return err != nil && isTransientNetworkError(err) && !isDNSNotFound(err)
}

// isHardPhaseErr reports whether err, beside which a transient phase error is
// demoted, holds a hard member: one neither transient nor a silent dispatch
// sentinel (disabled, not entitled, unavailable or NXDOMAIN, blocked). Beside
// a silent sentinel the transient error is kept, so the join is not read as
// the sentinel, which would discard the stored rows' counts and the transient
// error with them; it is classified as an error or transient instead.
func isHardPhaseErr(err error) bool {
	return !everyMember(err, func(m error) bool {
		return isTransientNetworkError(m) || isServiceDisabledErr(m) || isServiceNotEntitledErr(m) ||
			isServiceUnavailableErr(m) || isServiceBlocked(m)
	})
}

// demoteBeside joins err with the errors of the phases that ran beside it,
// demoting err to a warning under op only when those hold a hard error. A
// transient err alone, or beside transient ones only, is returned, so the
// dispatcher marks the scan partial instead of complete. rest is never
// demoted.
func demoteBeside(st *store.Store, op string, acct *account, region string, err error, rest ...error) error {
	if others := errors.Join(rest...); others != nil && isHardPhaseErr(others) {
		err = demoteTransient(st, op, acct, region, err)
	}
	return errors.Join(append([]error{err}, rest...)...)
}

// lateNXDOMAIN returns err, except that an NXDOMAIN from a phase that ran
// after an earlier phase reached the same endpoint is flattened to a plain
// error. Kept as NXDOMAIN, the dispatcher would report the region unavailable
// and discard the counts of the rows the earlier phases stored; the host
// resolved a moment ago, so it is a failure of this scan, not a region gap.
func lateNXDOMAIN(op string, err error) error {
	if !isDNSNotFound(err) {
		return err
	}
	return fmt.Errorf("%s: the endpoint stopped resolving after earlier calls to it succeeded; rescan: %v", op, err)
}

// resultLateNXDOMAIN is p's result once each NXDOMAIN among its errors is
// flattened (lateNXDOMAIN) when rows were stored: those rows prove the
// endpoint resolved, and the dispatcher would otherwise report the region
// unavailable and drop their counts. A failed write is left as is, so that
// the dispatcher still sees it. Every NXDOMAIN in p must be from the host the
// stored rows came from: a phase on another host must not add its own.
func (p *phaseTotals) resultLateNXDOMAIN(st *store.Store, acct *account, region string) (int, int, error) {
	if p.total > 0 {
		for i, f := range p.errs {
			if !errors.Is(f.err, store.ErrStoreWrite) {
				p.errs[i].err = lateNXDOMAIN(f.op, f.err)
			}
		}
	}
	return p.result(st, acct, region)
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
	// The rows are stored even when the closure write fails, so their counts
	// are reported with its error.
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return total, inserted, fmt.Errorf("closure %s: %w", op, err)
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

// phaseErrs collects the errors of a scanner's independent phases, each with
// the op it is warned under if demoted.
type phaseErrs []struct {
	op  string
	err error
}

func (p *phaseErrs) add(op string, err error) {
	if err != nil {
		*p = append(*p, struct {
			op  string
			err error
		}{op, err})
	}
}

// join returns the phases' errors for the dispatcher. When every one is
// transient they are returned as is, so the dispatcher reports them as
// transient and the scan as partial. When any is a hard error, each transient
// one is demoted to a warning first, so it is reported as one a rescan fixes
// rather than inside the hard error's message.
func (p phaseErrs) join(st *store.Store, acct *account, region string) error {
	errs := make([]error, len(p))
	hard := false
	for i, f := range p {
		errs[i] = f.err
		if isHardPhaseErr(f.err) {
			hard = true
		}
	}
	if len(p) == 1 {
		return p[0].err
	}
	if hard {
		for i, f := range p {
			errs[i] = demoteTransient(st, f.op, acct, region, f.err)
		}
	}
	return errors.Join(errs...)
}

// readTokenPages reads every page of one listing. On a failed page it returns the
// items already read with the error, so the caller can keep them. A repeated
// NextToken ends the listing with errRepeatedNextToken.
func readTokenPages[Item any](ctx context.Context, page func(ctx context.Context, token *string) ([]Item, *string, error)) ([]Item, error) {
	var (
		items []Item
		token *string
	)
	for {
		got, next, err := page(ctx, token)
		if err != nil {
			return items, err
		}
		items = append(items, got...)
		done, err := lastPage(token, next)
		if done {
			return items, err
		}
		token = next
	}
}

// notStoredSkips tallies the listed items not stored, one tally per reason, so each
// reason is warned about once.
type notStoredSkips struct {
	whys    []string
	tallies map[string]*skipTally
}

func (s *notStoredSkips) skipped(why, key string) {
	if s.tallies == nil {
		s.tallies = map[string]*skipTally{}
	}
	t, ok := s.tallies[why]
	if !ok {
		t = &skipTally{}
		s.tallies[why] = t
		s.whys = append(s.whys, why)
	}
	t.skipped(key, nil)
}

func (s *notStoredSkips) warn(st *store.Store, op string, acct *account, region string) {
	for _, why := range s.whys {
		warnNotStored(st, op, acct, region, s.tallies[why], why)
	}
}

// buildListedRows turns listed items into rows. row returns nil with the reason and a
// key naming the item when the item cannot be stored.
func buildListedRows[Item any](items []Item, row func(Item) (r *store.Resource, why, key string), rows []*store.Resource, skips *notStoredSkips) []*store.Resource {
	for _, it := range items {
		r, why, key := row(it)
		if r == nil {
			skips.skipped(why, key)
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

// storeTokenList stores the rows of an account-wide listing, those read before a
// failed page included. A denial is warned about rather than returned; any
// other error is returned after the rows are stored. Warnings are reported only
// once the write succeeded, so none claims rows a failed write lost.
func storeTokenList[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
) (int, int, error) {
	return storeTokenListWith(ctx, st, acct, region, op, page, row, keepRows)
}

// beforeWrite runs on the rows a listing built, before they are stored. It
// returns the rows to store and a warning to send only once the write
// succeeded.
type beforeWrite func(rows []*store.Resource) (kept []*store.Resource, warn func())

// keepRows is the beforeWrite that stores every row and has nothing to warn
// about.
func keepRows(rows []*store.Resource) ([]*store.Resource, func()) { return rows, func() {} }

// storeTokenListWith is storeTokenList with before run on the built rows
// before the write.
func storeTokenListWith[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
	before beforeWrite,
) (int, int, error) {
	var skips notStoredSkips
	items, err := readTokenPages(ctx, page)
	rows, warn := before(buildListedRows(items, row, nil, &skips))
	return storeReadRows(st, acct, region, op, rows, err, func() {
		skips.warn(st, op, acct, region)
		warn()
	})
}

// rowTagReader reads, with op (e.g. "ecr:ListTagsForResource"), the tags of
// rows built from a response that carries none. read returns one row's
// TagsJSON (awsTagsJSON, kvTagsJSON or mapTagsJSON: nil for no tags) and picks
// the identifier op is sent; it runs concurrently, so it must not modify the
// row. notFound holds the codes op's Smithy model lists for a resource that
// does not exist; nil when it lists none.
type rowTagReader struct {
	op       string
	notFound []string
	read     func(context.Context, *store.Resource) (*string, error)
}

// rowTagRead is one row's read result; both nil for a resource without tags.
type rowTagRead struct {
	tags *string
	err  error
}

// readRowTags reads the tags of rows through tags, fanoutMed at a time
// (readEachKey), and sets TagsJSON on each row read. It returns the rows to
// store, in their order, a warning to send only once they are stored, and the
// read errors: transient apart from hard, so that phaseErrs can demote the
// transient one beside a hard error. Per row:
//   - tags read: TagsJSON is set, nil when the resource has no tags;
//   - a notFound code: the resource was deleted after it was listed, and the
//     row is dropped without a warning;
//   - a denial: the row is kept; warn reports every such row in one warning;
//   - still transient after the SDK's retries: the row is kept, and every
//     such row is reported in transient, one error;
//   - not read, or its read cut short, because ctx ended: the row is kept,
//     and the stop is one member of hard;
//   - any other error, an NXDOMAIN included: the row is kept and the error
//     is a member of hard.
//
// A row kept unread keeps the TagsJSON its builder set.
func readRowTags(ctx context.Context, st *store.Store, acct *account, region string, tags rowTagReader, rows []*store.Resource) (kept []*store.Resource, warn func(), hard, transient error) {
	reads, stopped := readEachKey(ctx, rows, func(ctx context.Context, r *store.Resource) rowTagRead {
		t, err := tags.read(ctx, r)
		return rowTagRead{t, err}
	})
	var (
		denied      skipTally
		transients  transientTally
		errs        []error
		interrupted bool
	)
	kept = make([]*store.Resource, 0, len(rows))
	for i, read := range reads {
		r := rows[i]
		// The listing reached op's endpoint, so an NXDOMAIN now is a failure
		// of this scan, not a region gap (lateNXDOMAIN).
		switch err := lateNXDOMAIN(tags.op, read.err); {
		case err == nil:
			r.TagsJSON = read.tags
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			interrupted = true
		case isAPIErrorCode(err, tags.notFound...):
			continue
		case isAccessDenied(err):
			denied.skipped(r.NativeID, err)
		case transients.drop(r.NativeID, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", tags.op, r.NativeID, err))
		}
		kept = append(kept, r)
	}
	kept = append(kept, rows[len(reads):]...)
	// One stop member, whether ctx ended before some reads started or only
	// during the reads already running.
	switch {
	case stopped != nil:
		errs = append(errs, fmt.Errorf("%s: %w", tags.op, stopped))
	case interrupted:
		errs = append(errs, fmt.Errorf("%s: reads cut short: %w", tags.op, ctx.Err()))
	}
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: tags of %d row(s) not read, first %s: %w", tags.op, t.count, t.first, t.err)
	}
	warn = func() {
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, tags.op, acct.ID, region, fmt.Errorf("%d row(s) stored without tags, first %s: %w", denied.count, denied.first, denied.err))
		}
	}
	return kept, warn, errors.Join(errs...), transient
}

// rowTagErrs is what readRowTags returned besides the rows, kept by
// rowTagReader.hook until the write is done.
type rowTagErrs struct{ hard, transient error }

// hook returns readRowTags through tags as a beforeWrite, leaving the read
// errors in errs.
func (tags rowTagReader) hook(ctx context.Context, st *store.Store, acct *account, region string, errs *rowTagErrs) beforeWrite {
	return func(rows []*store.Resource) (kept []*store.Resource, warn func()) {
		kept, warn, errs.hard, errs.transient = readRowTags(ctx, st, acct, region, tags, rows)
		return kept, warn
	}
}

// addPhase adds a listing's counts and err to p under op, and the tag reads'
// errors under tagOp, hard and transient apart so that the transient one is
// demoted beside a hard error. After a failed write both are joined into err
// instead: demoted, the transient one would be a warning about rows the write
// lost. It reports whether the write failed, after which the scanner must
// stop.
func (e rowTagErrs) addPhase(p *phaseTotals, tagOp, op string, total, inserted int, err error) (storeWriteFailed bool) {
	if errors.Is(err, store.ErrStoreWrite) {
		return p.addPhase(op, total, inserted, errors.Join(err, e.hard, e.transient))
	}
	p.errs.add(tagOp, e.hard)
	p.errs.add(tagOp, e.transient)
	return p.addPhase(op, total, inserted, err)
}

// storeRowsTags is storeReadRows with each row's tags read through tags
// (readRowTags) before the write, for scanners that build their rows
// themselves. warn (nil for none) and the tag denial warning run only after a
// successful write. Counts and errors go to p as rowTagErrs.addPhase says; it
// reports whether the write failed, after which the scanner must stop.
func storeRowsTags(ctx context.Context, st *store.Store, acct *account, region, op string, rows []*store.Resource, listErr error, warn func(), tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	var errs rowTagErrs
	rows, tagWarn := tags.hook(ctx, st, acct, region, &errs)(rows)
	total, inserted, err := storeReadRows(st, acct, region, op, rows, listErr, func() {
		if warn != nil {
			warn()
		}
		tagWarn()
	})
	return errs.addPhase(p, tags.op, op, total, inserted, err)
}

// storeTokenListTags is storeTokenList with each row's tags read through
// tags (readRowTags) before the write. Counts and errors go to p as
// rowTagErrs.addPhase says; it reports whether the write failed, after which
// the scanner must stop.
func storeTokenListTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var errs rowTagErrs
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op, page, row, tags.hook(ctx, st, acct, region, &errs))
	return errs.addPhase(p, tags.op, op, total, inserted, err)
}

// storeReadRows stores the rows read from op's listing, which ended with
// listErr, and returns listErr (wrapped with op) and errs after the write. A
// denied listing is warned about instead of returned. A failed write is
// returned at once: the denial warning and warn (nil for none) run only after
// a successful write, so no warning claims rows a failed write lost.
func storeReadRows(st *store.Store, acct *account, region, op string, rows []*store.Resource, listErr error, warn func(), errs ...error) (int, int, error) {
	var denied error
	if isAccessDenied(listErr) {
		denied, listErr = listErr, nil
	}
	total, inserted, err := upsertListed(st, rows, op, listErr)
	err = errors.Join(append([]error{err}, errs...)...)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if denied != nil {
		_ = skipIfAccessDenied(st, op, acct.ID, region, denied)
	}
	if warn != nil {
		warn()
	}
	return total, inserted, err
}

// storeTokenPerParent lists op's items for each parent in turn and stores the
// rows of every parent read. One parent's failure does not stop the others:
// a parent the service no longer finds (an error code in notFound, the codes
// op models for a parent deleted since it was listed; nil when it models
// none) is skipped silently when nothing was read from it and with a warning
// otherwise; a denied parent is warned about once for the op;
// any other error is returned after the rows are stored. Rows read before a
// failed page are kept in every case.
func storeTokenPerParent[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound, parents []string,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
) (int, int, error) {
	out := parentOutcomes{op: op, notFound: notFound}
	for _, p := range parents {
		if err := ctx.Err(); err != nil {
			out.stopped(err)
			break
		}
		// Each parent's rows are built as soon as it is read, so its items
		// are not held while the later parents are read.
		addParentRows(&out, p, readParentPages(ctx, p, page), row)
	}
	return out.store(st, acct, region)
}

// storeTokenEachParent is storeTokenPerParent with the parents read
// concurrently through readEachKey, so page must be safe for concurrent use.
// The rows are built and the outcomes tallied in parent order after the
// reads, so row need not be. Once ctx is done no further parent is read and
// that stop is returned as one error; reads already in flight report their
// own errors.
func storeTokenEachParent[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound, parents []string,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
) (int, int, error) {
	return storeTokenEachParentSkip(ctx, st, acct, region, op, notFound, parents, page, row, nil)
}

// storeTokenEachParentSkip is storeTokenEachParent, except that skip (nil for
// none) sees, in parent order, each parent's error that is not a not-found
// code, before the denial check: when it reports true the error is dropped,
// keeping the rows read before it, and skip's caller warns about it once the
// write succeeded. For a service whose denial code isAccessDenied does not
// match, or a code skipped with a warning of its own.
func storeTokenEachParentSkip[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound, parents []string,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
	skip func(parent string, err error) bool,
) (int, int, error) {
	reads, stopped := readEachKey(ctx, parents, func(ctx context.Context, p string) parentItems[Item] {
		return readParentPages(ctx, p, page)
	})
	out := parentOutcomes{op: op, notFound: notFound, skip: skip}
	for i, read := range reads {
		addParentRows(&out, parents[i], read, row)
	}
	out.stopped(stopped)
	return out.store(st, acct, region)
}

// parentItems is what one parent's listing read before err (nil when it
// read every page).
type parentItems[Item any] struct {
	items []Item
	err   error
}

func readParentPages[Item any](ctx context.Context, parent string, page func(ctx context.Context, parent string, token *string) ([]Item, *string, error)) parentItems[Item] {
	items, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]Item, *string, error) {
		return page(ctx, parent, token)
	})
	return parentItems[Item]{items, err}
}

// storeChildrenEachParent stores each parent's children, the parents read
// concurrently (readEachKey) and each read's pages kept up to its error
// (readParentPages, a repeated NextToken included). Each parent is then
// classified on its own, as parentOutcomes does, so one parent's error
// costs no sibling its rows: a notFound code says the parent was deleted
// since it was listed; skip (nil for none) drops the errors its caller warns
// about in warn; a denial warns once; any other error is returned after the
// rows are stored. Each row is recorded under its parent
// (RecordHierarchyBatch). The warnings, warn's included, go out only after
// the rows were written.
func storeChildrenEachParent[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	skip func(parent string, err error) bool, warn func(),
	page func(ctx context.Context, p childParent, token *string) ([]Item, *string, error),
	row func(p childParent, it Item) (*store.Resource, string, string),
) (int, int, error) {
	reads, stopped := readEachKey(ctx, parents, func(ctx context.Context, p childParent) parentItems[Item] {
		return readParentPages(ctx, p.arn, func(ctx context.Context, _ string, token *string) ([]Item, *string, error) {
			return page(ctx, p, token)
		})
	})
	parentOf := map[*store.Resource]string{}
	out := parentOutcomes{op: op, notFound: notFound, skip: skip, parentOf: parentOf}
	for i, read := range reads {
		p := parents[i]
		addParentRows(&out, p.arn, read, func(_ string, it Item) (*store.Resource, string, string) {
			r, why, key := row(p, it)
			if r != nil {
				parentOf[r] = store.ResourceID("aws", acct.ID, p.arn)
			}
			return r, why, key
		})
	}
	out.stopped(stopped)
	total, inserted, err := out.store(st, acct, region)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if warn != nil {
		warn()
	}
	return total, inserted, err
}

// parentOutcomes collects, for storeTokenPerParent and storeTokenEachParent,
// the rows built from each parent's listing and what became of the parent.
type parentOutcomes struct {
	op               string
	notFound         []string
	skip             func(parent string, err error) bool
	rows             []*store.Resource
	errs             []error
	denied, vanished skipTally
	skips            notStoredSkips
	// parentOf, set, maps each row to its parent's resource id: store then
	// records the rows under their parents (RecordHierarchyBatch) after
	// writing them and before any warning, so a failed closure write sends
	// none.
	parentOf map[*store.Resource]string
}

// addParentRows builds parent's rows from what its listing read and tallies
// the listing's error, as storeTokenPerParent says.
func addParentRows[Item any](o *parentOutcomes, parent string, read parentItems[Item], row func(parent string, it Item) (r *store.Resource, why, key string)) {
	o.rows = buildListedRows(read.items, func(it Item) (*store.Resource, string, string) { return row(parent, it) }, o.rows, &o.skips)
	switch err := read.err; {
	case err == nil:
	case isAPIErrorCode(err, o.notFound...) && len(read.items) == 0:
	case isAPIErrorCode(err, o.notFound...):
		o.vanished.skipped(parent, err)
	case o.skip != nil && o.skip(parent, err):
	case isAccessDenied(err):
		o.denied.skipped(parent, err)
	default:
		o.errs = append(o.errs, fmt.Errorf("%s %s: %w", o.op, parent, err))
	}
}

// stopped records that the parents not yet read were not, because ctx ended.
func (o *parentOutcomes) stopped(err error) {
	if err != nil {
		o.errs = append(o.errs, fmt.Errorf("%s: %w", o.op, err))
	}
}

// store writes the rows and, with parentOf set, their closure, then warns
// about the denied and vanished parents and the items not stored, and
// returns the collected errors. After a failed write it warns nothing.
func (o *parentOutcomes) store(st *store.Store, acct *account, region string) (int, int, error) {
	total, inserted, uerr := upsertBatch(st, o.rows, o.op)
	if uerr != nil {
		return total, inserted, errors.Join(append(o.errs, uerr)...)
	}
	if o.parentOf != nil {
		pairs := make([][2]string, 0, len(o.rows))
		for _, r := range o.rows {
			pairs = append(pairs, [2]string{r.ID, o.parentOf[r]})
		}
		if herr := st.RecordHierarchyBatch(pairs); herr != nil {
			return total, inserted, errors.Join(append(o.errs, fmt.Errorf("closure %s: %w", o.op, herr))...)
		}
	}
	if o.denied.count > 0 {
		_ = skipIfAccessDenied(st, o.op, acct.ID, region, fmt.Errorf("%d parent(s) denied, first %s: %w", o.denied.count, o.denied.first, o.denied.err))
	}
	if o.vanished.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: o.op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d parent(s) were deleted while their listing was read, so only part of it was stored; first %s: %v", o.vanished.count, o.vanished.first, o.vanished.err),
		})
	}
	o.skips.warn(st, o.op, acct, region)
	return total, inserted, errors.Join(o.errs...)
}

// warnNoChildID warns once for the parents stored without the id their
// child listings key on, so those children were not read.
func warnNoChildID(st *store.Store, op string, acct *account, region string, t *skipTally, what, field string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s stored with an empty %s, so their children were not read; first %s", t.count, what, field, t.first),
	})
}

// phaseTotals sums the counts of a scanner's independent phases and collects
// their errors, so every phase runs and only a store write failure stops the
// scanner.
type phaseTotals struct {
	total, inserted int
	errs            phaseErrs
}

// addPhase adds one phase's counts and error under op, and reports whether
// that error is a store write failure, after which the scanner must stop:
// every later write would fail too.
func (p *phaseTotals) addPhase(op string, total, inserted int, err error) (storeWriteFailed bool) {
	p.total += total
	p.inserted += inserted
	p.errs.add(op, err)
	return errors.Is(err, store.ErrStoreWrite)
}

// result returns the summed counts and the phases' errors joined by
// phaseErrs.join.
func (p *phaseTotals) result(st *store.Store, acct *account, region string) (int, int, error) {
	return p.total, p.inserted, p.errs.join(st, acct, region)
}

// storeParentList stores an account-wide listing through storeTokenList whose
// rows are parents of a later phase, and returns the stored rows that carry
// the id the child op keys on (read by key); the others are warned about, as
// stored without their children. A failed write returns no parents.
func storeParentList[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, idField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	key func(Item) string,
) (int, int, []childParent, error) {
	return storeParentListWith(ctx, st, acct, region, op, what, idField, page, row, key, keepRows)
}

// storeParentListWith is storeParentList with before run on the built rows
// before the write; only the rows it keeps are returned as parents.
func storeParentListWith[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, idField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	key func(Item) string,
	before beforeWrite,
) (int, int, []childParent, error) {
	var (
		keys    = map[*store.Resource]string{}
		parents []childParent
		noID    skipTally
	)
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op, page, func(it Item) (*store.Resource, string, string) {
		r, why, k := row(it)
		if r != nil {
			keys[r] = key(it)
		}
		return r, why, k
	}, func(rows []*store.Resource) ([]*store.Resource, func()) {
		kept, warn := before(rows)
		for _, r := range kept {
			if id := keys[r]; id != "" {
				parents = append(parents, childParent{id: id, arn: r.NativeID})
			} else {
				noID.skipped(r.NativeID, nil)
			}
		}
		return kept, warn
	})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, nil, err
	}
	warnNoChildID(st, op, acct, region, &noID, what, idField)
	return total, inserted, parents, err
}

// storeParentListTags is storeParentList with each row's tags read through
// tags (readRowTags) before the write; a row dropped as not found is not
// returned as a parent. Counts and errors go to p as rowTagErrs.addPhase says.
// It returns the parents, or storeWriteFailed (and none) when the write
// failed, after which the scanner must stop.
func storeParentListTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, idField string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	key func(Item) string,
	tags rowTagReader, p *phaseTotals,
) (parents []childParent, storeWriteFailed bool) {
	var errs rowTagErrs
	total, inserted, parents, err := storeParentListWith(ctx, st, acct, region, op, what, idField, page, row, key, tags.hook(ctx, st, acct, region, &errs))
	return parents, errs.addPhase(p, tags.op, op, total, inserted, err)
}

// storeParentListDeniedBy is storeParentList for an op whose denial code
// isAccessDenied does not match: an error isDenied reports ends the listing,
// keeping the pages read before it, and is warned about once the write
// succeeded, as storeParentList warns about the codes isAccessDenied matches.
// A denial after the first page says that the later pages, and their
// children, were not read.
func storeParentListDeniedBy[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, what, idField string,
	isDenied func(error) bool,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	key func(Item) string,
) (int, int, []childParent, error) {
	var denied error
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, op, what, idField,
		func(ctx context.Context, token *string) ([]Item, *string, error) {
			items, next, err := page(ctx, token)
			if !isDenied(err) {
				return items, next, err
			}
			denied = err
			if token != nil {
				denied = fmt.Errorf("the listing stopped at a denied page after the first; %s on later pages and their children were not read: %w", what, err)
			}
			return nil, nil, nil
		}, row, key)
	if denied != nil && !errors.Is(err, store.ErrStoreWrite) {
		_ = skipIfAccessDenied(st, op, acct.ID, region, denied)
	}
	return total, inserted, parents, err
}

// childParentIDs is the ids of parents, which their child listings key on.
func childParentIDs(parents []childParent) []string {
	ids := make([]string, len(parents))
	for i, p := range parents {
		ids[i] = p.id
	}
	return ids
}

// storeEachParentTags is storeTokenEachParent with each row's tags read
// through tags (readRowTags) before the write: one parent's failure does not
// stop the others, and its rows read before the failure are kept. Counts
// and errors go to p as rowTagErrs.addPhase says. It returns the rows
// stored, none after a failed write, and whether the write failed.
func storeEachParentTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (stored []*store.Resource, storeWriteFailed bool) {
	ids := childParentIDs(parents)
	reads, stopped := readEachKey(ctx, ids, func(ctx context.Context, id string) parentItems[Item] {
		return readParentPages(ctx, id, page)
	})
	out := parentOutcomes{op: op, notFound: notFound}
	for i, read := range reads {
		addParentRows(&out, ids[i], read, row)
	}
	out.stopped(stopped)
	var (
		errs rowTagErrs
		warn func()
	)
	out.rows, warn, errs.hard, errs.transient = readRowTags(ctx, st, acct, region, tags, out.rows)
	total, inserted, err := out.store(st, acct, region)
	if errors.Is(err, store.ErrStoreWrite) {
		return nil, errs.addPhase(p, tags.op, op, total, inserted, err)
	}
	warn()
	return out.rows, errs.addPhase(p, tags.op, op, total, inserted, err)
}

// foldStop leaves ctx's end as one member of p's errors, for a scanner of
// service whose phases run one at a time and stop starting once ctx ends,
// notRun of them never started. Each phase in flight when ctx ended reports
// the stop itself, from its listing and from its tag reads; those reports
// are dropped from the joins they are in, the rest of each join kept, and
// one stop is added: the phases not run when notRun is set, else the first
// report dropped. Only joins are looked into: a single error that wraps the
// stop is the stop.
func (p *phaseTotals) foldStop(ctx context.Context, service string, notRun int) {
	stop := ctx.Err()
	if stop == nil {
		return
	}
	var (
		errs  phaseErrs
		first error
	)
	for _, f := range p.errs {
		kept, dropped := withoutStop(stop, f.err)
		if first == nil {
			first = dropped
		}
		errs.add(f.op, kept)
	}
	if notRun > 0 {
		first = fmt.Errorf("%s: %d phases not run: %w", service, notRun, stop)
	}
	errs.add(service, first)
	p.errs = errs
}

// withoutStop splits err into the part that is not stop and the first
// member that is.
func withoutStop(stop, err error) (kept, dropped error) {
	members, ok := err.(interface{ Unwrap() []error })
	if !ok {
		if errors.Is(err, stop) {
			return nil, err
		}
		return err, nil
	}
	var rest []error
	for _, m := range members.Unwrap() {
		k, d := withoutStop(stop, m)
		if dropped == nil {
			dropped = d
		}
		if k != nil {
			rest = append(rest, k)
		}
	}
	return errors.Join(rest...), dropped
}
