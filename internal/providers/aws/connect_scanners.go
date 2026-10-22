package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// Connect types are declared per family file via registerType;
	// scanConnect fans out to family scanners (core, routing, flows,
	// users, integration, workspace, datatable, extra, children, analytics,
	// access).
	registerService(serviceEntry{name: "aws:connect", fn: scanConnect})
}

// scanConnect discovers all Connect resource types in one region. Connect
// resources are mostly per-Instance scoped, so we fetch instances first
// and pass them to each family. Families that own account-scoped
// resources (TrafficDistributionGroup) ignore the instance list.
func scanConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connect.NewFromConfig(acct.cfg, func(o *connect.Options) { o.Region = region })

	instances, ierr := listConnectInstances(ctx, client, acct, region, st)
	total, inserted, err = scanConnectAll(ctx, client, instances, acct, region, st, scanID)
	var errs phaseErrs
	errs.add("connect:ListInstances", ierr)
	errs.add("connect", err)
	return total, inserted, errs.join(st, acct, region)
}

// scanConnectAll runs the older families (scanConnectOlderFamilies) and the
// analytics family concurrently, on a WaitGroup rather than an errgroup so that
// a failure on either side cancels nothing and costs the other no rows.
// Analytics reads no stored parents, so it need not wait for the older side.
// The two sides' errors are joined by phaseErrs, so a transient one is demoted
// to a warning only beside a hard one: joined, it would be buried in the hard
// error instead of reported under its op.
func scanConnectAll(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		wg                    sync.WaitGroup
		total, inserted, t, i int
		err, aerr             error
	)
	wg.Go(func() {
		total, inserted, err = scanConnectOlderFamilies(ctx, client, instances, acct, region, st, scanID)
	})
	wg.Go(func() { t, i, aerr = scanConnectAnalytics(ctx, client, instances, acct, region, st, scanID) })
	wg.Wait()
	var errs phaseErrs
	errs.add("connect", err)
	errs.add("connect analytics", aerr)
	return total + t, inserted + i, errs.join(st, acct, region)
}

// scanConnectOlderFamilies runs the families that predate analytics, then the
// children and the access family concurrently. Both read parent rows those
// families stored this scan (the children read hours of operation, integration
// associations, evaluation forms and workspaces; the access family reads
// instances, security profiles and routing profiles), never each other's rows.
// Both run whatever the families returned, so after a family failure they list
// the children of only the parents stored before it. On a WaitGroup, a failure
// on one side cancels nothing on the other. The family, children and access
// errors are joined by phaseErrs, which demotes a transient one to a warning
// only beside a hard one.
func scanConnectOlderFamilies(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, err := scanConnectFamilies(ctx, client, instances, acct, region, st, scanID)
	var (
		wg                  sync.WaitGroup
		ct, ci, at, ai      int
		childErr, accessErr error
	)
	wg.Go(func() { ct, ci, childErr = scanConnectChildren(ctx, client, instances, acct, region, st, scanID) })
	wg.Go(func() { at, ai, accessErr = scanConnectAccess(ctx, client, acct, region, st, scanID) })
	wg.Wait()
	var errs phaseErrs
	errs.add("connect", err)
	errs.add("connect", childErr)
	errs.add("connect access", accessErr)
	return total + ct + at, inserted + ci + ai, errs.join(st, acct, region)
}

// scanConnectFamilies runs the families that predate analytics concurrently
// and joins their errors through phaseErrs. It uses a WaitGroup, not
// runScanners: runScanners' errgroup cancels every sibling on the first error,
// and these families are independent, so one family's failure must cost the
// others nothing. They all start together, so a failed store write has no
// later family to stop.
func scanConnectFamilies(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	families := []connectPhase{
		{"connect core", func() (int, int, error) {
			return scanConnectCore(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect routing", func() (int, int, error) {
			return scanConnectRouting(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect users", func() (int, int, error) {
			return scanConnectUsers(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect flows", func() (int, int, error) {
			return scanConnectFlows(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect integration", func() (int, int, error) {
			return scanConnectIntegration(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect workspace", func() (int, int, error) {
			return scanConnectWorkspace(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect data tables", func() (int, int, error) {
			return scanConnectDataTable(ctx, client, instances, acct, region, st, scanID)
		}},
		{"connect extra", func() (int, int, error) {
			return scanConnectExtra(ctx, client, instances, acct, region, st, scanID)
		}},
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs phaseErrs
	)
	for _, f := range families {
		wg.Go(func() {
			t, i, ferr := f.run()
			mu.Lock()
			defer mu.Unlock()
			total += t
			inserted += i
			errs.add(f.op, ferr)
		})
	}
	wg.Wait()
	return total, inserted, errs.join(st, acct, region)
}

// listConnectInstances pages ListInstances once and returns the instances it
// read. A denial warns; any other error is returned with the instances read
// before it, so the families still scan those.
func listConnectInstances(ctx context.Context, client connectInstanceLister, acct *account, region string, st *store.Store) ([]cttypes.InstanceSummary, error) {
	pager := connect.NewListInstancesPaginator(client, &connect.ListInstancesInput{},
		func(o *connect.ListInstancesPaginatorOptions) { o.Limit = 10 })
	var all []cttypes.InstanceSummary
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "connect:ListInstances", acct.ID, region, perr)
				return all, nil
			}
			// Per-region availability gap: regions without Connect instance
			// enumeration reject ListInstances with InvalidRequestException —
			// treat as no instances.
			if isAPIErrorCode(perr, "InvalidRequestException") {
				return all, nil
			}
			return all, fmt.Errorf("connect:ListInstances: %w", perr)
		}
		all = append(all, out.InstanceSummaryList...)
	}
	return all, nil
}

// connectInstanceLister narrows ListInstances for the dispatcher's pre-pass.
type connectInstanceLister interface {
	ListInstances(context.Context, *connect.ListInstancesInput, ...func(*connect.Options)) (*connect.ListInstancesOutput, error)
}

// connectPhase is one phase of a Connect family; op names it in the warning a
// demoted transient error becomes.
type connectPhase struct {
	op  string
	run func() (int, int, error)
}

// connectRunPhases runs every phase, so one phase's error costs the later
// phases nothing, and sums their counts. A failed store write returns at once;
// other errors are joined by phaseErrs after every phase has run.
func connectRunPhases(st *store.Store, acct *account, region string, phases ...connectPhase) (total, inserted int, err error) {
	var errs phaseErrs
	for _, p := range phases {
		t, i, perr := p.run()
		total += t
		inserted += i
		errs.add(p.op, perr)
		if errors.Is(perr, store.ErrStoreWrite) {
			break
		}
	}
	return total, inserted, errs.join(st, acct, region)
}

// errConnectNoBody is a Describe response that lacks the resource it describes.
var errConnectNoBody = errors.New("response has no resource body")

// connectPager is the method set every Connect list paginator shares.
type connectPager[O any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*connect.Options)) (O, error)
}

// connectListed is one resource a Connect list op returned: the instance it
// was listed under ("" for account-scoped resources), the id its Describe op
// takes, its ARN (the row's NativeID) and its list summary.
type connectListed[S any] struct {
	instanceID, id, arn string
	summary             S
}

// connectListing collects what one list op returned across instances. A
// failed listing keeps the resources read before it failed.
type connectListing[S any] struct {
	op     string
	items  []connectListed[S]
	noID   skipTally
	denied bool
	errs   []error
}

// add keeps a listed resource, or tallies it when it has no id or ARN.
func (l *connectListing[S]) add(instanceID, id, arn string, s S) {
	if id == "" || arn == "" {
		l.noID.skipped(fmt.Sprintf("id %q ARN %q", id, arn), nil)
		return
	}
	l.items = append(l.items, connectListed[S]{instanceID: instanceID, id: id, arn: arn, summary: s})
}

// failed records the error that ended the listing of key: the first denial is
// warned now (one IAM grant covers every key); anything else is returned after
// the listed rows are stored.
func (l *connectListing[S]) failed(st *store.Store, acct *account, region, key string, err error) {
	if isAccessDenied(err) {
		if !l.denied {
			l.denied = true
			_ = skipIfAccessDenied(st, l.op, acct.ID, region, err)
		}
		return
	}
	l.errs = append(l.errs, fmt.Errorf("%s %s: %w", l.op, key, err))
}

// connectReadPages adds every resource p lists under instanceID to l and
// returns the error of the first page that fails.
func connectReadPages[O, S any](ctx context.Context, instanceID string, l *connectListing[S], p connectPager[O], items func(O) []S, key func(S) (id, arn string)) error {
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, s := range items(out) {
			id, arn := key(s)
			l.add(instanceID, id, arn, s)
		}
	}
	return nil
}

// connectTokenPager pages a list op that has no SDK paginator. A NextToken
// that repeats the one sent ends the listing: that page's resources are still
// read, and the next call returns errRepeatedNextToken.
type connectTokenPager[O any] struct {
	call  func(ctx context.Context, token *string) (O, error)
	next  func(O) *string
	token *string
	done  bool
	err   error
}

func (p *connectTokenPager[O]) HasMorePages() bool { return !p.done }

func (p *connectTokenPager[O]) NextPage(ctx context.Context, _ ...func(*connect.Options)) (O, error) {
	var zero O
	if p.err != nil {
		p.done = true
		return zero, p.err
	}
	out, err := p.call(ctx, p.token)
	if err != nil {
		return zero, err
	}
	last, err := lastPage(p.token, p.next(out))
	p.token, p.done, p.err = p.next(out), last && err == nil, err
	return out, nil
}

// connectChild is a resource listed under a parent: the parent's id and the
// resource's list summary.
type connectChild[S any] struct {
	parentID string
	summary  S
}

// connectListUnder runs the listing for every parent; one parent's failure does
// not stop the others, and a parent deleted since it was listed
// (ResourceNotFoundException, which every list op using this models) is
// skipped.
func connectListUnder[P, O, S any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []connectListed[P],
	pager func(p connectListed[P]) connectPager[O], items func(O) []S, key func(parentID string, s S) (id, arn string),
) *connectListing[connectChild[S]] {
	l := &connectListing[connectChild[S]]{op: op}
	for _, p := range parents {
		err := connectReadPages(ctx, p.instanceID, l, pager(p),
			func(o O) []connectChild[S] {
				var children []connectChild[S]
				for _, s := range items(o) {
					children = append(children, connectChild[S]{parentID: p.id, summary: s})
				}
				return children
			},
			func(c connectChild[S]) (string, string) { return key(c.parentID, c.summary) })
		if err != nil && !isConnectNotFound(err) {
			l.failed(st, acct, region, p.arn, err)
		}
	}
	return l
}

// connectRow is the row of a listed Connect resource, before its name and
// attributes are filled in.
func connectRow(acct *account, region, scanID, rtype, arn string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name, Type: rtype,
		NativeID: arn, Region: &region, DiscoveredBy: scanID,
	}
}

// connectStoreListed stores each listed resource from its list summary
// (fill), then returns the listing's errors. Resources listed without an id or
// ARN are warned about and not stored.
func connectStoreListed[S any](st *store.Store, acct *account, region, scanID, rtype, label string, l *connectListing[S], fill func(r *store.Resource, it connectListed[S])) (int, int, error) {
	rows := make([]*store.Resource, 0, len(l.items))
	for _, it := range l.items {
		r := connectRow(acct, region, scanID, rtype, it.arn)
		fill(r, it)
		rows = append(rows, r)
	}
	listErr := errors.Join(l.errs...)
	total, inserted, err := upsertBatch(st, rows, label)
	if err != nil {
		return total, inserted, errors.Join(listErr, err)
	}
	warnNotStored(st, l.op, acct, region, &l.noID, label+" have no id or ARN")
	return total, inserted, listErr
}

// connectListPerInstance runs the listing for every instance; one instance's
// failure does not stop the others.
func connectListPerInstance[O, S any](
	ctx context.Context, st *store.Store, acct *account, region, op string, instances []cttypes.InstanceSummary,
	pager func(instanceID string) connectPager[O], items func(O) []S, key func(S) (id, arn string),
) *connectListing[S] {
	l := &connectListing[S]{op: op}
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		if err := connectReadPages(ctx, instID, l, pager(instID), items, key); err != nil {
			l.failed(st, acct, region, instID, err)
		}
	}
	return l
}

// connectDescribe stores listed Connect resources whose full body, tags
// included, only a Describe op returns.
type connectDescribe[S any] struct {
	rtype, op, label string
	// describe reads it and fills r from the body. It must leave r untouched
	// when it returns an error.
	describe func(ctx context.Context, it connectListed[S], r *store.Resource) error
	// summary fills r from the list summary.
	summary func(r *store.Resource, s S)
	// notReady, when set, is an error code the Describe op models for a
	// resource that exists but has no readable body yet (an unpublished
	// draft); why says so in the warning.
	notReady, why string
}

// store describes every listed resource (fanoutMed at a time) and stores each
// as its Describe body. ResourceNotFoundException, which every Describe op
// using this models, means the resource was deleted after it was listed, so it
// is skipped. Any other failure stores the list summary instead, without the
// tags: a denied, not-ready or still-throttled read is warned once per cause, and any
// other error is returned once (a count and the first resource's error) with
// the listing's errors after the rows are stored.
// Resources listed without an id or ARN are warned about and not stored.
// Once ctx is done no further Describe starts and ctx's error is returned in
// place of the per-resource failures the cancel caused.
func (d connectDescribe[S]) store(ctx context.Context, st *store.Store, acct *account, region, scanID string, l *connectListing[S]) (int, int, error) {
	var (
		denied    skipTally
		notReady  skipTally
		transient transientTally
		mu        sync.Mutex
		rows      []*store.Resource
		failed    skipTally
		wg        sync.WaitGroup
	)
	sem := make(chan struct{}, fanoutMed)
	// The slot is taken before the goroutine starts, so at most fanoutMed
	// goroutines exist however many resources are listed.
items:
	for _, it := range l.items {
		// Checked first because select picks at random when a slot is free
		// and ctx is done too.
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break items
		}
		wg.Go(func() {
			defer func() { <-sem }()
			r := connectRow(acct, region, scanID, d.rtype, it.arn)
			err := d.describe(ctx, it, r)
			switch {
			case err == nil:
			case isConnectNotFound(err):
				return
			case d.notReady != "" && notReady.dropCode(d.notReady, it.arn, err):
				d.summary(r, it.summary)
			case transient.drop(it.arn, err):
				d.summary(r, it.summary)
			case isAccessDenied(err):
				denied.skipped(it.arn, err)
				d.summary(r, it.summary)
			default:
				failed.skipped(it.arn, err)
				d.summary(r, it.summary)
			}
			mu.Lock()
			rows = append(rows, r)
			mu.Unlock()
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", d.op, err)
	}
	readErr := errors.Join(l.errs...)
	if failed.count > 0 {
		readErr = errors.Join(readErr, fmt.Errorf("%s: %d %s stored as their list summary, first %s: %w",
			d.op, failed.count, d.label, failed.first, failed.err))
	}
	total, inserted, err := upsertBatch(st, rows, d.label)
	if err != nil {
		return total, inserted, errors.Join(readErr, err)
	}
	d.warnKept(st, acct, region, &denied, &notReady, &transient)
	warnNotStored(st, l.op, acct, region, &l.noID, d.label+" have no id or ARN")
	return total, inserted, readErr
}

// warnKept warns once per cause for the resources stored as their list
// summary; call it only after they are stored.
func (d connectDescribe[S]) warnKept(st *store.Store, acct *account, region string, denied, notReady *skipTally, transient *transientTally) {
	const kept = "%d %s stored as their list summary, without the %s body (tags included), first %s: %w"
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, d.op, acct.ID, region, fmt.Errorf(kept, denied.count, d.label, d.op, denied.first, denied.err))
	}
	if notReady.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: d.op, Scope: acct.ID + "/" + region,
			Message: fmt.Errorf(kept+" (%s)", notReady.count, d.label, d.op, notReady.first, notReady.err, d.why).Error(),
		})
	}
	if transient.count > 0 {
		_ = skipIfTransient(st, d.op, acct.ID, region, fmt.Errorf(kept, transient.count, d.label, d.op, transient.first, transient.err))
	}
}
