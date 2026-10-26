package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/applicationinsights"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeApplicationInsightsApplication, Service: "applicationinsights"})
	registerType(restype.Descriptor{Type: TypeApplicationInsightsComponent, Service: "applicationinsights"})
	registerType(restype.Descriptor{Type: TypeApplicationInsightsWorkload, Service: "applicationinsights"})
	registerType(restype.Descriptor{Type: TypeApplicationInsightsLogPatternSet, Service: "applicationinsights"})
	registerType(restype.Descriptor{Type: TypeApplicationInsightsLogPattern, Service: "applicationinsights"})
	registerService(serviceEntry{
		name: "aws:applicationinsights",
		fn:   scanApplicationInsights,
	})
}

type applicationInsightsAPI interface {
	ListApplications(context.Context, *applicationinsights.ListApplicationsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListApplicationsOutput, error)
	ListComponents(context.Context, *applicationinsights.ListComponentsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListComponentsOutput, error)
	DescribeComponent(context.Context, *applicationinsights.DescribeComponentInput, ...func(*applicationinsights.Options)) (*applicationinsights.DescribeComponentOutput, error)
	ListWorkloads(context.Context, *applicationinsights.ListWorkloadsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListWorkloadsOutput, error)
	DescribeWorkload(context.Context, *applicationinsights.DescribeWorkloadInput, ...func(*applicationinsights.Options)) (*applicationinsights.DescribeWorkloadOutput, error)
	ListLogPatternSets(context.Context, *applicationinsights.ListLogPatternSetsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListLogPatternSetsOutput, error)
	ListLogPatterns(context.Context, *applicationinsights.ListLogPatternsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListLogPatternsOutput, error)
}

const (
	appInsightsListComponentsOp     = "applicationinsights:ListComponents"
	appInsightsDescribeComponentOp  = "applicationinsights:DescribeComponent"
	appInsightsListWorkloadsOp      = "applicationinsights:ListWorkloads"
	appInsightsDescribeWorkloadOp   = "applicationinsights:DescribeWorkload"
	appInsightsListLogPatternSetsOp = "applicationinsights:ListLogPatternSets"
	appInsightsListLogPatternsOp    = "applicationinsights:ListLogPatterns"
)

// appInsightsPageLimit is the model's cap (MaxEntities) on MaxResults for
// every child List op; a larger value is rejected with a ValidationException.
const appInsightsPageLimit = 40

// applicationInsightsAppNativeID synthesizes a NativeID for ApplicationInsights
// applications. The SDK exposes no application ARN — identity is the
// resource-group name. Shape mirrors the standard AWS service-resource form.
func applicationInsightsAppNativeID(partition, region, acct, rgName string) string {
	return arnFor(partition, "applicationinsights", region, acct, "application/resource-group/"+rgName)
}

// appInsightsComponentNativeID and its siblings below build child NativeIDs:
// Application Insights returns no ARN for an application's children, so each
// extends its application's NativeID with a kind segment. A resource-group
// name cannot contain "/" (model pattern [a-zA-Z0-9.-_]), so the segment after
// it always names the kind, and a component name, which may be an ARN
// containing "/" and ":", can follow verbatim without ambiguity.
func appInsightsComponentNativeID(appARN, component string) string {
	return appARN + "/component/" + component
}

// appInsightsWorkloadNativeID keys a workload under its application, not its
// component: workload ids are generated (w-<uuid>), so they are unique within
// the application, and keeping the component name out of the id means no
// component name (which may contain "/workload/") can collide with it.
func appInsightsWorkloadNativeID(appARN, workloadID string) string {
	return appARN + "/workload/" + workloadID
}

func appInsightsLogPatternSetNativeID(appARN, set string) string {
	return appARN + "/log-pattern-set/" + set
}

func appInsightsLogPatternNativeID(setNativeID, pattern string) string {
	return setNativeID + "/log-pattern/" + pattern
}

// scanApplicationInsights stores the applications, then their components (with
// workloads) and log pattern sets (with patterns). The child phases run
// whatever the applications phase returned, over the applications it stored.
// The two child families are independent, so they run concurrently, and neither
// cancels the other; all errors are joined. The applications phase's transient
// error is demoted to a warning beside a child phase's hard error: joined, it
// would be buried in the hard error instead of reported under its op; alone it
// is returned, so the scan is marked partial.
func scanApplicationInsights(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := applicationinsights.NewFromConfig(acct.cfg, func(o *applicationinsights.Options) { o.Region = region })
	total, inserted, apps, err := scanApplicationInsightsApplications(ctx, client, acct, region, st, scanID)
	var (
		wg             sync.WaitGroup
		ct, ci, pt, pi int
		cerr, perr     error
	)
	wg.Go(func() { ct, ci, cerr = scanAppInsightsComponentFamily(ctx, client, acct, region, st, scanID, apps) })
	wg.Go(func() { pt, pi, perr = scanAppInsightsLogPatternFamily(ctx, client, acct, region, st, scanID, apps) })
	wg.Wait()
	return total + ct + pt, inserted + ci + pi, demoteBeside(
		st, "applicationinsights:ListApplications", acct, region, err, cerr, perr)
}

// scanAppInsightsComponentFamily stores the components, then the workloads of
// the components it stored.
func scanAppInsightsComponentFamily(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	ct, ci, components, cerr := scanAppInsightsComponents(ctx, client, acct, region, st, scanID, apps)
	wt, wi, werr := scanAppInsightsWorkloads(ctx, client, acct, region, st, scanID, components)
	return ct + wt, ci + wi, errors.Join(cerr, werr)
}

// scanAppInsightsLogPatternFamily stores the log pattern sets, then the
// patterns of the sets it stored.
func scanAppInsightsLogPatternFamily(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	st1, si, sets, serr := scanAppInsightsLogPatternSets(ctx, client, acct, region, st, scanID, apps)
	pt, pi, perr := scanAppInsightsLogPatterns(ctx, client, acct, region, st, scanID, sets)
	return st1 + pt, si + pi, errors.Join(serr, perr)
}

// scanApplicationInsightsApplications stores the applications and returns
// those it stored (id: resource-group name, arn: NativeID), including the
// pages stored before an error.
func scanApplicationInsightsApplications(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, apps []childParent, err error) {
	p := applicationinsights.NewListApplicationsPaginator(client, &applicationinsights.ListApplicationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, apps, skipIfAccessDenied(st, "applicationinsights:ListApplications", acct.ID, region, err)
			}
			return total, inserted, apps, fmt.Errorf("applicationinsights:ListApplications: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.ApplicationInfoList))
		listed := make([]childParent, 0, len(page.ApplicationInfoList))
		for _, a := range page.ApplicationInfoList {
			rg := sv(a.ResourceGroupName)
			if rg == "" {
				continue
			}
			arn := applicationInsightsAppNativeID(acct.partition(), region, acct.ID, rg)
			listed = append(listed, childParent{id: rg, arn: arn})
			name := rg
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationInsightsApplication,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, apps, fmt.Errorf("upsert applicationinsights apps: %w", err)
			}
			total += len(batch)
			inserted += n
			apps = append(apps, listed...)
		}
	}
	return total, inserted, apps, nil
}

// isAppInsightsGone reports a child call whose application, component or
// pattern set was deleted after it was listed.
func isAppInsightsGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// appInsightsFanOut runs call for each parent concurrently (fanoutMed) and
// returns everything the calls returned. Per parent: a deleted parent
// (isAppInsightsGone) is dropped silently, a denial is dropped with one
// warning for op, a transient error after the SDK's retries is dropped with
// one warning for op covering every such parent, and any other error fails
// the fan-out, returning nothing.
func appInsightsFanOut[P, C any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []P,
	key func(P) string, call func(context.Context, P) ([]C, error),
) ([]C, error) {
	var (
		mu        sync.Mutex
		out       []C
		transient transientTally
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: op, isSkip: isAppInsightsGone}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			got, err := call(gctx, p)
			if isTransientNetworkError(err) {
				transient.skipped(key(p), err)
				return nil
			}
			if err != nil {
				return errs.keyErr(key(p), err)
			}
			mu.Lock()
			defer mu.Unlock()
			out = append(out, got...)
			return nil
		})
	}
	err := g.Wait()
	transient.warnSkipped(st, op, acct, region)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func appParentKey(p childParent) string { return p.id }

// appInsightsComponent is a component listed under app (id: resource-group
// name, arn: application NativeID).
type appInsightsComponent struct {
	app  childParent
	name string
}

func (c appInsightsComponent) nativeID() string {
	return appInsightsComponentNativeID(c.app.arn, c.name)
}

// scanAppInsightsComponents lists each application's components, then
// describes each one: only DescribeComponent returns ResourceList, the ARNs of
// the resources the component is made of. It returns the components it
// stored, the parents of the workload phase.
func scanAppInsightsComponents(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, []appInsightsComponent, error) {
	listed, err := appInsightsFanOut(ctx, st, acct, region, appInsightsListComponentsOp, apps, appParentKey,
		func(ctx context.Context, app childParent) ([]appInsightsComponent, error) {
			var comps []appInsightsComponent
			p := applicationinsights.NewListComponentsPaginator(client,
				&applicationinsights.ListComponentsInput{ResourceGroupName: &app.id},
				func(o *applicationinsights.ListComponentsPaginatorOptions) { o.Limit = appInsightsPageLimit })
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range page.ApplicationComponentList {
					if name := sv(c.ComponentName); name != "" {
						comps = append(comps, appInsightsComponent{app: app, name: name})
					}
				}
			}
			return comps, nil
		})
	if err != nil {
		return 0, 0, nil, err
	}

	type described struct {
		comp appInsightsComponent
		out  *applicationinsights.DescribeComponentOutput
	}
	descs, err := appInsightsFanOut(ctx, st, acct, region, appInsightsDescribeComponentOp, listed, appInsightsComponent.nativeID,
		func(ctx context.Context, c appInsightsComponent) ([]described, error) {
			out, err := client.DescribeComponent(ctx, &applicationinsights.DescribeComponentInput{ResourceGroupName: &c.app.id, ComponentName: &c.name})
			if err != nil {
				return nil, err
			}
			return []described{{comp: c, out: out}}, nil
		})
	if err != nil {
		return 0, 0, nil, err
	}
	children := make([]containedChild, len(descs))
	stored := make([]appInsightsComponent, len(descs))
	for i, d := range descs {
		name := d.comp.name
		// The API does not document ResourceList's order; sorting keeps a
		// reorder from version-splitting an unchanged component.
		d.out.ResourceList = slices.Sorted(slices.Values(d.out.ResourceList))
		children[i] = containedChild{
			row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeApplicationInsightsComponent, NativeID: d.comp.nativeID(),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d.out), DiscoveredBy: scanID,
			},
			parentID: store.ResourceID("aws", acct.ID, d.comp.app.arn),
		}
		stored[i] = d.comp
	}
	total, inserted, err := upsertContained(st, "applicationinsights components", children)
	// upsertContained reports no rows when the upsert failed, and real ones
	// when only the closure write after it failed: workloads are then still
	// listed under the components stored.
	if total == 0 {
		stored = nil
	}
	return total, inserted, stored, err
}

type appInsightsWorkload struct {
	comp    appInsightsComponent
	id      string
	name    string
	missing *bool // the ListWorkloads entry's MissingWorkloadConfig
}

// appInsightsWorkloadAttrs is the DescribeWorkload body plus
// MissingWorkloadConfig, which only the ListWorkloads entry carries
// (embedding keeps the Describe fields top-level, as lambdaFunctionAttrs does).
type appInsightsWorkloadAttrs struct {
	*applicationinsights.DescribeWorkloadOutput
	MissingWorkloadConfig *bool
}

func (w appInsightsWorkload) nativeID() string {
	return appInsightsWorkloadNativeID(w.comp.app.arn, w.id)
}

// scanAppInsightsWorkloads lists each stored component's workloads, then
// describes each one: only DescribeWorkload returns the workload's monitoring
// configuration.
func scanAppInsightsWorkloads(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, components []appInsightsComponent) (int, int, error) {
	listed, err := appInsightsFanOut(ctx, st, acct, region, appInsightsListWorkloadsOp, components, appInsightsComponent.nativeID,
		func(ctx context.Context, c appInsightsComponent) ([]appInsightsWorkload, error) {
			var workloads []appInsightsWorkload
			p := applicationinsights.NewListWorkloadsPaginator(client,
				&applicationinsights.ListWorkloadsInput{ResourceGroupName: &c.app.id, ComponentName: &c.name},
				func(o *applicationinsights.ListWorkloadsPaginatorOptions) { o.Limit = appInsightsPageLimit })
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, w := range page.WorkloadList {
					if id := sv(w.WorkloadId); id != "" {
						workloads = append(workloads, appInsightsWorkload{comp: c, id: id, name: sv(w.WorkloadName), missing: w.MissingWorkloadConfig})
					}
				}
			}
			return workloads, nil
		})
	if err != nil {
		return 0, 0, err
	}

	children, err := appInsightsFanOut(ctx, st, acct, region, appInsightsDescribeWorkloadOp, listed, appInsightsWorkload.nativeID,
		func(ctx context.Context, w appInsightsWorkload) ([]containedChild, error) {
			out, err := client.DescribeWorkload(ctx, &applicationinsights.DescribeWorkloadInput{
				ResourceGroupName: &w.comp.app.id, ComponentName: &w.comp.name, WorkloadId: &w.id,
			})
			if err != nil {
				return nil, err
			}
			return []containedChild{{
				row: &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeApplicationInsightsWorkload, NativeID: w.nativeID(),
					Name: nonEmptyPtr(w.name), Region: &region,
					AttributesJSON: mustJSON(appInsightsWorkloadAttrs{DescribeWorkloadOutput: out, MissingWorkloadConfig: w.missing}),
					DiscoveredBy:   scanID,
				},
				parentID: store.ResourceID("aws", acct.ID, w.comp.nativeID()),
			}}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertContained(st, "applicationinsights workloads", children)
}

// appInsightsPatternSet is a log pattern set listed under app.
type appInsightsPatternSet struct {
	app  childParent
	name string
}

func (s appInsightsPatternSet) nativeID() string {
	return appInsightsLogPatternSetNativeID(s.app.arn, s.name)
}

// scanAppInsightsLogPatternSets stores each application's log pattern sets,
// which the API returns as bare names, and returns them as the parents of the
// log pattern phase.
func scanAppInsightsLogPatternSets(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, []appInsightsPatternSet, error) {
	sets, err := appInsightsFanOut(ctx, st, acct, region, appInsightsListLogPatternSetsOp, apps, appParentKey,
		func(ctx context.Context, app childParent) ([]appInsightsPatternSet, error) {
			var sets []appInsightsPatternSet
			p := applicationinsights.NewListLogPatternSetsPaginator(client,
				&applicationinsights.ListLogPatternSetsInput{ResourceGroupName: &app.id},
				func(o *applicationinsights.ListLogPatternSetsPaginatorOptions) { o.Limit = appInsightsPageLimit })
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, name := range page.LogPatternSets {
					if name != "" {
						sets = append(sets, appInsightsPatternSet{app: app, name: name})
					}
				}
			}
			return sets, nil
		})
	if err != nil {
		return 0, 0, nil, err
	}
	children := make([]containedChild, len(sets))
	for i, s := range sets {
		name := s.name
		children[i] = containedChild{
			row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeApplicationInsightsLogPatternSet, NativeID: s.nativeID(),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"PatternSetName": name}), DiscoveredBy: scanID,
			},
			parentID: store.ResourceID("aws", acct.ID, s.app.arn),
		}
	}
	total, inserted, err := upsertContained(st, "applicationinsights log pattern sets", children)
	// As for components: patterns are listed under the sets stored.
	if total == 0 {
		sets = nil
	}
	return total, inserted, sets, err
}

// scanAppInsightsLogPatterns lists the patterns of each stored pattern set.
func scanAppInsightsLogPatterns(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string, sets []appInsightsPatternSet) (int, int, error) {
	children, err := appInsightsFanOut(ctx, st, acct, region, appInsightsListLogPatternsOp, sets, appInsightsPatternSet.nativeID,
		func(ctx context.Context, s appInsightsPatternSet) ([]containedChild, error) {
			var rows []containedChild
			setID := store.ResourceID("aws", acct.ID, s.nativeID())
			p := applicationinsights.NewListLogPatternsPaginator(client,
				&applicationinsights.ListLogPatternsInput{ResourceGroupName: &s.app.id, PatternSetName: &s.name},
				func(o *applicationinsights.ListLogPatternsPaginatorOptions) { o.Limit = appInsightsPageLimit })
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, lp := range page.LogPatterns {
					name := sv(lp.PatternName)
					if name == "" {
						continue
					}
					rows = append(rows, containedChild{
						row: &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeApplicationInsightsLogPattern, NativeID: appInsightsLogPatternNativeID(s.nativeID(), name),
							Name: &name, Region: &region,
							AttributesJSON: mustJSON(lp), DiscoveredBy: scanID,
						},
						parentID: setID,
					})
				}
			}
			return rows, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertContained(st, "applicationinsights log patterns", children)
}
