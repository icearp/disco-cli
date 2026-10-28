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

// addAll adds every key o tallied to t, keeping t's first unless t has none.
// Read o only once its own fan-out has returned.
func (t *skipTally) addAll(o *skipTally) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		t.first, t.err = o.first, o.err
	}
	t.count += o.count
}

// skippedLeast is skipped with first kept as the smallest key tallied, not
// the first to arrive, for a tally fed by concurrent reads whose warning must
// not depend on which read finished first.
func (t *skipTally) skippedLeast(key string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 || key < t.first {
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

// firstPageGapPages is page with a first page whose error isGap reports read
// as an empty listing: the op has nothing to serve this caller here. The same
// answer on a later page follows a page the op did serve, so it is returned
// as the error it is (a denial is warned about) and the later pages are not
// read.
func firstPageGapPages[Item any](isGap func(error) bool, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if token == nil && isGap(err) {
			return nil, nil, nil
		}
		return items, next, err
	}
}

// readTokenPages reads every page of one listing. On a failed page it returns the
// items already read with the error, so the caller can keep them. A repeated
// NextToken ends the listing with errRepeatedNextToken.
func readTokenPages[Item any](ctx context.Context, page func(ctx context.Context, token *string) ([]Item, *string, error)) ([]Item, error) {
	var items []Item
	err := eachTokenPage(ctx, page, func(got []Item) error {
		items = append(items, got...)
		return nil
	})
	return items, err
}

// eachTokenPage reads one listing page by page and hands each page's items to
// use before it reads the next, so a caller can write a page instead of
// holding the whole listing. It returns the first error of a page or of use,
// and errRepeatedNextToken when a NextToken repeats (after use saw that page).
func eachTokenPage[Item any](ctx context.Context, page func(ctx context.Context, token *string) ([]Item, *string, error), use func([]Item) error) error {
	var token *string
	for {
		got, next, err := page(ctx, token)
		if err != nil {
			return err
		}
		if err := use(got); err != nil {
			return err
		}
		done, err := lastPage(token, next)
		if done {
			return err
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

// addAll adds every item from tallied to s, each under its reason, after
// s's own.
func (s *notStoredSkips) addAll(from *notStoredSkips) {
	for _, why := range from.whys {
		if s.tallies == nil {
			s.tallies = map[string]*skipTally{}
		}
		t, ok := s.tallies[why]
		if !ok {
			t = &skipTally{}
			s.tallies[why] = t
			s.whys = append(s.whys, why)
		}
		t.addAll(from.tallies[why])
	}
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
// does not exist; nil when it lists none. denied holds the denial codes op
// models that isAccessDenied does not match (e.g. UnauthorizedException), so
// that they are warned about as denials rather than returned; nil for none.
// denials, when set, collects the denied rows of every readRowTags call
// through this reader instead of each call warning on its own, so that a
// service whose listings run as concurrent families warns once for op
// (warnDenials); nil makes each call warn.
type rowTagReader struct {
	op       string
	notFound []string
	denied   []string
	read     func(context.Context, *store.Resource) (*string, error)
	denials  *skipTally
}

// sharingDenials is tags with one denial tally for every call through it;
// warnDenials on the result sends the one warning.
func (tags rowTagReader) sharingDenials() rowTagReader {
	tags.denials = &skipTally{}
	return tags
}

// warnDenials warns once under op about every denied row tallied through
// tags.denials, if any. The rows were tallied only once their write
// succeeded. With several families the first row named is whichever family
// stored first.
func (tags rowTagReader) warnDenials(st *store.Store, acct *account, region string) {
	if t := tags.denials; t != nil && t.count > 0 {
		_ = skipIfAccessDenied(st, tags.op, acct.ID, region, fmt.Errorf("%d row(s) stored without tags, first %s: %w", t.count, t.first, t.err))
	}
}

// boundedBy is tags with each read holding one of slots while it runs, so
// that the reads of several concurrent listings together stay within
// cap(slots). A read that ctx ends while it waits returns ctx's error.
func (tags rowTagReader) boundedBy(slots chan struct{}) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-slots }()
		return read(ctx, r)
	}
	return tags
}

// otherAccountGap reports whether err, from a read about arn, is a denial
// (isAccessDenied) or one of notFound while arn names an account other than
// acctID: a resource shared in through RAM, or listed from another account
// (an invitation, a management account's view). Such a resource is its
// owner's, so this account may be refused it or told it does not exist while
// it does; dropping the row or warning would lose or misreport a resource the
// listing just returned, and no IAM change here can grant the read. A tag
// reader returns no tags for it (the row is kept untagged and silent) and
// passes its op's notFound; a listing that tolerates only a denial passes nil.
// An ARN with an empty account field counts as another account's: a scanned
// account always has an id, so such a row is not one of its own; a type whose
// own ARNs carry no account (path-style or global ARNs) must not use this. An
// ARN that does not parse counts as acctID's own. Only isAccessDenied counts as
// a denial: a reader with rowTagReader.denied codes would still warn on them.
func otherAccountGap(arn, acctID string, err error, notFound []string) bool {
	if !isAccessDenied(err) && !isAPIErrorCode(err, notFound...) {
		return false
	}
	a, ok := parseARN(arn)
	return ok && a.AccountID != acctID
}

// untaggedIfOtherAccount is tags for a listing that can return resources
// another account owns: a read whose error otherAccountGap reports for the
// row's NativeID (its ARN) and acctID gives no tags and no error, so the row
// is kept untagged without a warning.
func (tags rowTagReader) untaggedIfOtherAccount(acctID string) rowTagReader {
	own := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := own(ctx, r)
		if otherAccountGap(r.NativeID, acctID, err, tags.notFound) {
			return nil, nil
		}
		return got, err
	}
	return tags
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
//   - a denial (isAccessDenied, or a code in denied): the row is kept; warn
//     reports every such row in one warning;
//   - still transient after the SDK's retries: the row is kept, and every
//     such row is reported in transient, one error;
//   - not read, or its read cut short, because ctx ended: the row is kept,
//     and the stop is one member of hard;
//   - any other error, an NXDOMAIN included: the row is kept and the error
//     is a member of hard.
//
// A row kept unread keeps the TagsJSON its builder set.
func readRowTags(ctx context.Context, st *store.Store, acct *account, region string, tags rowTagReader, rows []*store.Resource) (kept []*store.Resource, warn func(), hard, transient error) {
	var tally rowTagTally
	kept, hard = tally.read(ctx, tags, rows)
	return kept, func() { tally.warn(st, tags, acct, region) }, hard, tally.transient(tags.op)
}

// rowTagTally carries readRowTags' denied and transient rows across reads, so
// that a listing whose rows are read in batches (one per page) warns once
// about its denials and returns one transient error for the whole listing.
type rowTagTally struct {
	denied     skipTally
	transients transientTally
}

// read is readRowTags for one batch of rows, its denied and transient rows
// added to the tally. It returns the rows to store and the hard errors.
func (t *rowTagTally) read(ctx context.Context, tags rowTagReader, rows []*store.Resource) (kept []*store.Resource, hard error) {
	reads, stopped := readEachKey(ctx, rows, func(ctx context.Context, r *store.Resource) rowTagRead {
		tg, err := tags.read(ctx, r)
		return rowTagRead{tg, err}
	})
	var (
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
		case isAccessDenied(err) || isAPIErrorCode(err, tags.denied...):
			t.denied.skipped(r.NativeID, err)
		case t.transients.drop(r.NativeID, err):
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
	return kept, errors.Join(errs...)
}

// addAll adds from's denied and transient rows to t's, after t's own.
func (t *rowTagTally) addAll(from *rowTagTally) {
	t.denied.addAll(&from.denied)
	t.transients.addAll(&from.transients.skipTally)
}

// transient is one error for every row whose tag read stayed transient; nil
// for none.
func (t *rowTagTally) transient(op string) error {
	if tt := &t.transients.skipTally; tt.count > 0 {
		return fmt.Errorf("%s: tags of %d row(s) not read, first %s: %w", op, tt.count, tt.first, tt.err)
	}
	return nil
}

// warn reports every denied row in one warning, or, when tags shares its
// denials (sharingDenials), adds them to that tally for its caller to warn
// once. Call it only once the rows are stored.
func (t *rowTagTally) warn(st *store.Store, tags rowTagReader, acct *account, region string) {
	switch {
	case t.denied.count == 0:
	case tags.denials != nil:
		tags.denials.addAll(&t.denied)
	default:
		_ = skipIfAccessDenied(st, tags.op, acct.ID, region, fmt.Errorf("%d row(s) stored without tags, first %s: %w", t.denied.count, t.denied.first, t.denied.err))
	}
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

// storeTokenPagesTags is storeTokenListTags for a listing too large to hold:
// each page's rows are written as soon as their tags are read, before the
// next page is read, so one page is held at a time and the pages written
// survive a later failure. A failed write stops the listing. Each kind of
// warning (items not stored, a denied listing, denied tag reads) is sent
// once for the whole listing, and only after every write succeeded. Counts
// and errors go to p as rowTagErrs.addPhase says; it reports whether a write
// failed, after which the scanner must stop. Once ctx ends, the page in
// flight is written with the rows its tag reads did not reach kept untagged,
// and no later page is read; the stop is reported both by the tag reads and
// by the listing, for the runner to fold into one (phaseTotals.foldStop).
func storeTokenPagesTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	return storeTokenPagesTagsWith(ctx, st, acct, region, op, page, row, tokenPageSteps{}, tags, p)
}

// tokenPageSteps are what storeTokenPagesTagsWith runs on each page besides
// its tag reads; the zero value runs none.
type tokenPageSteps struct {
	// details, when its get is set, replaces each row with its detail body
	// before the row's tags are read, as readRowDetailsDeniedBy does (a nil
	// isDenied means isAccessDenied); a row it drops as not found has no tags
	// read and is not stored.
	details rowDetailReader
	// stored, when set, is handed each page's rows once they are written.
	stored func(rows []*store.Resource)
}

// storeTokenPagesTagsWith is storeTokenPagesTags with steps run on each
// page. The detail reads' denials are warned about once for the listing,
// after every write succeeded, and their errors go to p under
// steps.details.op, hard and transient apart, or are joined into the
// write's error after a failed write (detailedRows.addPhase).
func storeTokenPagesTagsWith[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
	steps tokenPageSteps, tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var (
		skips            notStoredSkips
		tally            rowTagTally
		detailTally      rowDetailTally
		hard, detailHard []error
		total, inserted  int
		werr             error
	)
	details := steps.details
	details.isDenied = orAccessDenied(details.isDenied)
	// No page is asked for once ctx has ended, so a page whose tag reads the
	// stop cut short is the last one written, whatever page does with ctx.
	listErr := eachTokenPage(ctx, func(ctx context.Context, token *string) ([]Item, *string, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return page(ctx, token)
	}, func(items []Item) error {
		rows := buildListedRows(items, row, nil, &skips)
		if details.get != nil {
			var h error
			rows, h = detailTally.read(ctx, details, rows)
			detailHard = append(detailHard, h)
		}
		rows, h := tally.read(ctx, tags, rows)
		hard = append(hard, h)
		t, i, err := upsertBatch(st, rows, op)
		total += t
		inserted += i
		werr = err
		if err == nil && steps.stored != nil {
			steps.stored(rows)
		}
		return err
	})
	detailErrs := rowTagErrs{hard: errors.Join(detailHard...), transient: detailTally.transient(details)}
	errs := rowTagErrs{hard: errors.Join(hard...), transient: tally.transient(tags.op)}
	if werr != nil {
		return errs.addPhase(p, tags.op, op, total, inserted, errors.Join(werr, detailErrs.hard, detailErrs.transient))
	}
	p.errs.add(details.op, detailErrs.hard)
	p.errs.add(details.op, detailErrs.transient)
	_, _, err := storeReadRows(st, acct, region, op, nil, listErr, func() {
		skips.warn(st, op, acct, region)
		detailTally.warn(st, details, acct, region)
		tally.warn(st, tags, acct, region)
	})
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
	return storeTokenEachParentWith(ctx, st, acct, region, op, notFound, parents, page, row, skip, isAccessDenied)
}

// storeTokenEachParentDeniedBy is storeTokenEachParent for an op whose denial
// codes isAccessDenied does not all match: a parent whose error isDenied
// reports is tallied as denied, under the same one warning as the codes
// isAccessDenied matches would be.
func storeTokenEachParentDeniedBy[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound, parents []string,
	isDenied func(error) bool,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
) (int, int, error) {
	return storeTokenEachParentWith(ctx, st, acct, region, op, notFound, parents, page, row, nil, isDenied)
}

func storeTokenEachParentWith[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound, parents []string,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
	skip func(parent string, err error) bool,
	isDenied func(error) bool,
) (int, int, error) {
	reads, stopped := readEachKey(ctx, parents, func(ctx context.Context, p string) parentItems[Item] {
		return readParentPages(ctx, p, page)
	})
	out := parentOutcomes{op: op, notFound: notFound, skip: skip, isDenied: isDenied}
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
	op       string
	notFound []string
	skip     func(parent string, err error) bool
	// isDenied reports a denial; nil means isAccessDenied.
	isDenied         func(error) bool
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
	o.parentErr(parent, len(read.items) > 0, read.err)
}

// parentErr tallies the error that ended parent's listing, nil when it read
// every page; readAny says whether any item was read before it.
func (o *parentOutcomes) parentErr(parent string, readAny bool, err error) {
	switch {
	case err == nil:
	case isAPIErrorCode(err, o.notFound...) && !readAny:
	case isAPIErrorCode(err, o.notFound...):
		o.vanished.skipped(parent, err)
	case o.skip != nil && o.skip(parent, err):
	case o.deniedBy(err):
		o.denied.skipped(parent, err)
	default:
		o.errs = append(o.errs, fmt.Errorf("%s %s: %w", o.op, parent, err))
	}
}

// addAll adds from's items not stored, parent outcomes and errors to o,
// after o's own, so that merging per-parent outcomes in parent order keeps
// that order. Rows and parentOf are not merged: their callers write rows as
// they are built.
func (o *parentOutcomes) addAll(from *parentOutcomes) {
	o.skips.addAll(&from.skips)
	o.denied.addAll(&from.denied)
	o.vanished.addAll(&from.vanished)
	o.errs = append(o.errs, from.errs...)
}

// deniedBy reports whether err denies o's op.
func (o *parentOutcomes) deniedBy(err error) bool { return orAccessDenied(o.isDenied)(err) }

// orAccessDenied is isDenied, or isAccessDenied when isDenied is nil: the
// default of the optional denial predicates.
func orAccessDenied(isDenied func(error) bool) func(error) bool {
	if isDenied == nil {
		return isAccessDenied
	}
	return isDenied
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
	total, inserted, werr := o.write(st, o.rows)
	if werr != nil {
		return total, inserted, errors.Join(append(o.errs, werr)...)
	}
	o.warn(st, acct, region)
	return total, inserted, errors.Join(o.errs...)
}

// write stores rows and, with parentOf set, their closure, and returns the
// write's error alone. It changes nothing in o, so concurrent calls are safe.
func (o *parentOutcomes) write(st *store.Store, rows []*store.Resource) (int, int, error) {
	total, inserted, uerr := upsertBatch(st, rows, o.op)
	if uerr != nil {
		return total, inserted, uerr
	}
	if o.parentOf != nil {
		pairs := make([][2]string, 0, len(rows))
		for _, r := range rows {
			pairs = append(pairs, [2]string{r.ID, o.parentOf[r]})
		}
		if herr := st.RecordHierarchyBatch(pairs); herr != nil {
			return total, inserted, fmt.Errorf("closure %s: %w", o.op, herr)
		}
	}
	return total, inserted, nil
}

// warn warns about the denied and vanished parents and the items not
// stored. Call it only once every row is written.
func (o *parentOutcomes) warn(st *store.Store, acct *account, region string) {
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

// opErrs is the errors of a phase or family, each under the op it failed
// under. Phase and family runners carry it up unjoined, so that the service
// joins every error once (phaseErrs.join), demoting each transient one under
// its own op (e.g. a tag op) rather than its phase's List op or the service.
type opErrs struct{ errs phaseErrs }

func (e opErrs) Unwrap() []error {
	errs := make([]error, len(e.errs))
	for i, f := range e.errs {
		errs[i] = f.err
	}
	return errs
}

func (e opErrs) Error() string { return errors.Join(e.Unwrap()...).Error() }

// opErrsResult returns p's counts and its errors as opErrs, nil when there
// are none.
func opErrsResult(p *phaseTotals) (int, int, error) {
	if len(p.errs) == 0 {
		return p.total, p.inserted, nil
	}
	return p.total, p.inserted, opErrs{p.errs}
}

// addOpErrsPhase adds a phase's counts and error to p: the members of an
// opErrs under their own ops, any other error under op. It reports whether a
// write failed, after which the runner must stop.
func addOpErrsPhase(p *phaseTotals, op string, total, inserted int, err error) (storeWriteFailed bool) {
	// Only err itself is unpacked: an opErrs wrapped inside another error is
	// filed whole under op, which keeps every member.
	labelled, ok := err.(opErrs)
	if !ok {
		return p.addPhase(op, total, inserted, err)
	}
	p.total += total
	p.inserted += inserted
	p.errs = append(p.errs, labelled.errs...)
	return errors.Is(err, store.ErrStoreWrite)
}

// runFamilies runs the families of one service concurrently on ctx itself,
// so one family's error cannot cancel the others and lose the rows they have
// not stored yet, and returns their summed counts and errors (each family's
// opErrs members under their own ops, any other error under service). The
// caller joins the errors once (phaseTotals.result).
func runFamilies(ctx context.Context, service string, families ...func(context.Context) (int, int, error)) *phaseTotals {
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
		addOpErrsPhase(&p, service, r.total, r.inserted, r.err)
	}
	return &p
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
	d := listDenial{isDenied: isDenied, unread: what + " on later pages and their children"}
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, op, what, idField, deniedPages(&d, page), row, key)
	if !errors.Is(err, store.ErrStoreWrite) {
		d.warn(st, op, acct, region)
	}
	return total, inserted, parents, err
}

// listDenial ends a listing on the first error isDenied reports, for an op
// whose denial codes isAccessDenied does not all match, and holds that error
// until the rows read before it are written. unread names what a denial
// after the first page left unread.
type listDenial struct {
	isDenied func(error) bool
	unread   string
	err      error
}

// deniedPages is page with an error d.isDenied reports ending the listing
// without an error, keeping the pages read before it; d records it.
func deniedPages[Item any](d *listDenial, page func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if !d.isDenied(err) {
			return items, next, err
		}
		d.err = err
		if token != nil {
			d.err = fmt.Errorf("the listing stopped at a denied page after the first; %s were not read: %w", d.unread, err)
		}
		return nil, nil, nil
	}
}

// warn warns once under op about the denial the listing ended on, if any.
// Call it only after the rows read were written.
func (d *listDenial) warn(st *store.Store, op string, acct *account, region string) {
	if d.err != nil {
		_ = skipIfAccessDenied(st, op, acct.ID, region, d.err)
	}
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
	return storeEachParentTagsDeniedBy(ctx, st, acct, region, op, notFound, parents, isAccessDenied, page, row, tags, p)
}

// storeEachParentTagsDeniedBy is storeEachParentTags for an op whose denial
// codes isAccessDenied does not all match: a parent whose error isDenied
// reports is tallied as denied, under the same one warning.
func storeEachParentTagsDeniedBy[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	isDenied func(error) bool,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (stored []*store.Resource, storeWriteFailed bool) {
	ids := childParentIDs(parents)
	reads, stopped := readEachKey(ctx, ids, func(ctx context.Context, id string) parentItems[Item] {
		return readParentPages(ctx, id, page)
	})
	out := parentOutcomes{op: op, notFound: notFound, isDenied: isDenied}
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

// storeEachParentPagesTags is storeEachParentTags for child listings too
// large to hold (a vault's recovery points): each page's rows are written as
// soon as their tags are read, so at most one page per parent read is held
// and the pages written survive a later failure. The parents are read
// concurrently (readEachKey) and their tag reads share one bound of
// fanoutMed (rowTagReader.boundedBy): up to fanoutMed listing calls and
// fanoutMed tag reads are in flight at once. Each parent's outcomes are kept
// apart and merged in parent order, so every warning's "first" and the
// order of the joined errors do not depend on which parent finished first.
// Each kind of warning, the tag reads' included, is sent once for the whole
// phase and only after every write succeeded.
//
// A failed write stops the phase: it cancels the calls in flight, so a page
// already fetched starts no tag read; no further page or parent is read;
// and no page is written after it, those whose tag reads it cut short
// included. The listings and tag reads cut short by that stop report
// nothing, not even as rows whose tags were not read; the phase returns the
// write's error with the errors met before it. Counts and errors go to p as rowTagErrs.addPhase says; it
// reports whether a write failed, after which the scanner must stop.
func storeEachParentPagesTags[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var (
		mu              sync.Mutex // guards the counts and werr
		total, inserted int
		werr            error
	)
	phaseCtx, stopPhase := context.WithCancel(ctx)
	defer stopPhase()
	writeFailed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return werr != nil
	}
	tags = tags.boundedBy(make(chan struct{}, fanoutMed))
	results, stopped := readEachKey(phaseCtx, parents, func(ctx context.Context, parent childParent) *parentPages {
		r := &parentPages{out: parentOutcomes{op: op, notFound: notFound}}
		readAny := false
		err := eachTokenPage(ctx, func(ctx context.Context, token *string) ([]Item, *string, error) {
			// Checked before every page, a parent's first included.
			if writeFailed() {
				return nil, nil, errPhaseWriteFailed
			}
			return page(ctx, parent.id, token)
		}, func(items []Item) error {
			readAny = readAny || len(items) > 0
			rows := buildListedRows(items, func(it Item) (*store.Resource, string, string) { return row(parent.id, it) }, nil, &r.out.skips)
			var pageTally rowTagTally
			rows, h := pageTally.read(ctx, tags, rows)
			// The check and the write hold mu together, so no page is written
			// once another's write failed (SQLite serialises the writes
			// anyway). No test pins the lock: nothing runs between the check
			// and the write that a test could use to order another parent's
			// failure there. A page dropped here leaves nothing in r: its
			// tag reads, cut short by the cancelled ctx, name no row as
			// unread.
			mu.Lock()
			defer mu.Unlock()
			if werr != nil {
				return errPhaseWriteFailed
			}
			r.tally.addAll(&pageTally)
			r.hard = append(r.hard, h)
			t, i, err := r.out.write(st, rows)
			total += t
			inserted += i
			if err != nil && werr == nil {
				werr = err
				stopPhase()
			}
			return err
		})
		if !writeFailed() {
			r.out.parentErr(parent.id, readAny, err)
		}
		return r
	})
	var (
		out   = parentOutcomes{op: op, notFound: notFound}
		tally rowTagTally
		hard  []error
	)
	for _, r := range results {
		out.addAll(&r.out)
		tally.addAll(&r.tally)
		hard = append(hard, r.hard...)
	}
	errs := rowTagErrs{hard: errors.Join(hard...), transient: tally.transient(tags.op)}
	if werr != nil {
		return errs.addPhase(p, tags.op, op, total, inserted, errors.Join(append(out.errs, werr)...))
	}
	out.stopped(stopped)
	out.warn(st, acct, region)
	tally.warn(st, tags, acct, region)
	return errs.addPhase(p, tags.op, op, total, inserted, errors.Join(out.errs...))
}

// parentPages is what storeEachParentPagesTags gathered from one parent.
type parentPages struct {
	out   parentOutcomes
	tally rowTagTally
	hard  []error
}

// errPhaseWriteFailed ends a listing whose phase already failed a write; the
// write's own error is what the phase returns.
var errPhaseWriteFailed = errors.New("not read: an earlier write of this phase failed")

// foldStop leaves ctx's end as one member of p's errors, for a scanner of
// service whose phases run one at a time and stop starting once ctx ends
// (notRun of them never started), or run as concurrent families that all
// start (runFamilies; notRun 0). Each phase in flight when ctx ended reports
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

// hostPhase is one listing of a scan whose SDK packages call different
// hosts: the op it is reported under, the host it calls (an index the scan
// defines), and run, which adds its counts and errors to p and reports
// whether a store write failed.
type hostPhase struct {
	op   string
	host int
	run  func(p *phaseTotals) (storeWriteFailed bool)
}

// countedPhase is the hostPhase of a scanner that returns its counts and
// error rather than adding them to p.
func countedPhase(host int, op string, run func() (int, int, error)) hostPhase {
	return hostPhase{op, host, func(p *phaseTotals) bool {
		t, i, err := run()
		return p.addPhase(op, t, i, err)
	}}
}

// runHostPhases runs phases in order, one at a time: a child phase gets the
// parents its parent phase stored. A phase's error does not stop the later
// ones; a failed write does, since every later write would fail too, and so
// does ctx's end, reported as one error of service.
//
// A host whose first phase stores nothing and fails only with NXDOMAIN (a
// phase making one listing per owner fails alike for each) is not served in
// this region, and its later phases are not run. For any host but primary
// that NXDOMAIN is dropped: the region serves the service without that part
// of it. The primary host's NXDOMAIN is kept, so a region without the
// service is reported unavailable; once any host stored rows, every NXDOMAIN
// left is a failure of this scan instead (resultLateNXDOMAIN), so those rows
// keep their counts.
func runHostPhases(ctx context.Context, st *store.Store, acct *account, region, service string, primary int, phases []hostPhase) (int, int, error) {
	var p phaseTotals
	ran, gone := map[int]bool{}, map[int]bool{}
	notRun := 0
	for i, ph := range phases {
		if gone[ph.host] {
			continue
		}
		if ctx.Err() != nil {
			for _, rest := range phases[i:] {
				if !gone[rest.host] {
					notRun++
				}
			}
			break
		}
		total, errs := p.total, len(p.errs)
		if ph.run(&p) {
			break
		}
		if !ran[ph.host] && p.total == total && onlyDNSNotFound(p.errs[errs:]) {
			gone[ph.host] = true
			if ph.host != primary {
				p.errs = p.errs[:errs]
			}
		}
		ran[ph.host] = true
	}
	p.foldStop(ctx, service, notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// onlyDNSNotFound reports whether errs is not empty and every one is an
// NXDOMAIN.
func onlyDNSNotFound(errs phaseErrs) bool {
	for _, f := range errs {
		if !isDNSNotFound(f.err) {
			return false
		}
	}
	return len(errs) > 0
}

// readRowDetails reads each row's detail body with get, concurrently
// (readEachKey), and stores a body get returns as the row's attributes. A
// resource the Get reports not found (an error code in notFound, the codes
// op's model lists) was deleted since the listing, and its row is dropped.
// Any other failure keeps the list element as the row, which carries the
// same ids and NativeID: a denial is warned about through warn, once the
// rows are stored; a transient failure after the SDK's retries is returned
// in transient; ctx's end is returned in hard as one error, counting the
// reads it cut short and those it kept from starting; any other error is
// returned in hard. what names the rows in those messages.
func readRowDetails(ctx context.Context, st *store.Store, acct *account, region, op, what string, notFound []string, rows []*store.Resource, get func(context.Context, *store.Resource) (any, error)) (kept []*store.Resource, warn func(), hard, transient error) {
	return readRowDetailsDeniedBy(ctx, st, acct, region, op, what, notFound, isAccessDenied, rows, get)
}

// readRowDetailsDeniedBy is readRowDetails for an op whose denial codes
// isAccessDenied does not all match: a read whose error isDenied reports is a
// denial.
func readRowDetailsDeniedBy(ctx context.Context, st *store.Store, acct *account, region, op, what string, notFound []string, isDenied func(error) bool, rows []*store.Resource, get func(context.Context, *store.Resource) (any, error)) (kept []*store.Resource, warn func(), hard, transient error) {
	d := rowDetailReader{op: op, what: what, notFound: notFound, isDenied: isDenied, get: get}
	var tally rowDetailTally
	kept, hard = tally.read(ctx, d, rows)
	return kept, func() { tally.warn(st, d, acct, region) }, hard, tally.transient(d)
}

// rowDetailReader reads, with op, each row's detail body through get, as
// readRowDetailsDeniedBy says; notFound, isDenied and what are its arguments
// of those names.
type rowDetailReader struct {
	op, what string
	notFound []string
	isDenied func(error) bool
	get      func(context.Context, *store.Resource) (any, error)
}

// rowDetailTally carries readRowDetailsDeniedBy's denied and transient rows
// across reads, so that a listing whose rows are read in batches (one per
// page) warns once about its denials and returns one transient error for the
// whole listing.
type rowDetailTally struct {
	denied     skipTally
	transients transientTally
}

// read is readRowDetailsDeniedBy for one batch of rows, its denied and
// transient rows added to the tally. It returns the rows to store and the
// hard errors, ctx's end among them as one error for the batch.
func (t *rowDetailTally) read(ctx context.Context, d rowDetailReader, rows []*store.Resource) (kept []*store.Resource, hard error) {
	type detail struct {
		body any
		err  error
	}
	reads, _ := readEachKey(ctx, rows, func(ctx context.Context, r *store.Resource) detail {
		body, err := d.get(ctx, r)
		return detail{body, err}
	})
	var errs []error
	cut := len(rows) - len(reads)
	kept = make([]*store.Resource, 0, len(rows))
	for i, got := range reads {
		r := rows[i]
		// The listing reached this host, so an NXDOMAIN now is a failure of
		// this scan, not a region gap (lateNXDOMAIN).
		switch err := lateNXDOMAIN(d.op, got.err); {
		case err == nil:
			if got.body != nil {
				r.AttributesJSON = mustJSON(got.body)
			}
		case isAPIErrorCode(err, d.notFound...):
			continue
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			cut++
		case d.isDenied(err):
			t.denied.skipped(r.NativeID, err)
		case t.transients.drop(r.NativeID, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", d.op, r.NativeID, err))
		}
		kept = append(kept, r)
	}
	kept = append(kept, rows[len(reads):]...)
	if cut > 0 {
		errs = append(errs, fmt.Errorf("%s: %d %s stored as their list element: %w", d.op, cut, d.what, ctx.Err()))
	}
	return kept, errors.Join(errs...)
}

// transient is one error for every row whose read stayed transient; nil for
// none.
func (t *rowDetailTally) transient(d rowDetailReader) error {
	if tt := &t.transients.skipTally; tt.count > 0 {
		return fmt.Errorf("%s: %d %s stored as their list element, first %s: %w", d.op, tt.count, d.what, tt.first, tt.err)
	}
	return nil
}

// warn reports every denied row in one warning. Call it only once the rows
// are stored.
func (t *rowDetailTally) warn(st *store.Store, d rowDetailReader, acct *account, region string) {
	if t.denied.count > 0 {
		_ = skipIfAccessDenied(st, d.op, acct.ID, region, fmt.Errorf("%d %s stored as their list element, first %s: %w", t.denied.count, d.what, t.denied.first, t.denied.err))
	}
}

// detailedRows replaces each row a listing built with its Get body
// (readRowDetails), then reads its tags (readRowTags), and holds both reads'
// errors until the write is done.
type detailedRows struct {
	getOp, what string
	notFound    []string
	// isDenied reports the Get's denial; nil means isAccessDenied.
	isDenied         func(error) bool
	tags             rowTagReader
	getErrs, tagErrs rowTagErrs
}

// before is the listing's beforeWrite: get reads one row's body.
func (d *detailedRows) before(ctx context.Context, st *store.Store, acct *account, region string, get func(context.Context, *store.Resource) (any, error)) beforeWrite {
	return func(rows []*store.Resource) ([]*store.Resource, func()) {
		rows, getWarn, hard, transient := readRowDetailsDeniedBy(ctx, st, acct, region, d.getOp, d.what, d.notFound, orAccessDenied(d.isDenied), rows, get)
		d.getErrs = rowTagErrs{hard, transient}
		kept, tagWarn := d.tags.hook(ctx, st, acct, region, &d.tagErrs)(rows)
		return kept, func() {
			getWarn()
			tagWarn()
		}
	}
}

// addPhase adds the listing's counts and err to p under op, and both reads'
// errors under their ops, as rowTagErrs.addPhase does. It reports whether
// the write failed, after which the scanner must stop.
func (d *detailedRows) addPhase(p *phaseTotals, op string, total, inserted int, err error) (storeWriteFailed bool) {
	if errors.Is(err, store.ErrStoreWrite) {
		err = errors.Join(err, d.getErrs.hard, d.getErrs.transient)
	} else {
		p.errs.add(d.getOp, d.getErrs.hard)
		p.errs.add(d.getOp, d.getErrs.transient)
	}
	return d.tagErrs.addPhase(p, d.tags.op, op, total, inserted, err)
}
