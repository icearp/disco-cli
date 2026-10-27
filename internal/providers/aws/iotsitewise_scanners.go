package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isIoTSiteWiseFeatureUnsupported reports whether err is the per-region
// `InvalidRequestException: Feature not supported yet` shape IoT SiteWise
// returns when a sub-API isn't available in-region (e.g. ListComputationModels:
// works in us-east-1/eu-west-1, rejects in us-west-2) — distinct from a real
// validation error, which carries a different message body.
func isIoTSiteWiseFeatureUnsupported(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "Feature not supported")
}

func init() {
	registerType(restype.Descriptor{Type: TypeIoTSWAccessPolicy, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWApplication, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWAsset, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWAssetModel, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWComputationModel, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWDashboard, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWDataset, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWGateway, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWPipeline, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWPortal, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWProject, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWTask, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWWorkspace, Service: "iotsitewise"})
	registerService(serviceEntry{
		name: "aws:iotsitewise",
		fn:   scanIoTSiteWise,
	})
}

type iotSWAPI interface {
	ListAccessPolicies(context.Context, *iotsitewise.ListAccessPoliciesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAccessPoliciesOutput, error)
	ListAssets(context.Context, *iotsitewise.ListAssetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetsOutput, error)
	ListAssetModels(context.Context, *iotsitewise.ListAssetModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetModelsOutput, error)
	DescribeAssetModel(context.Context, *iotsitewise.DescribeAssetModelInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeAssetModelOutput, error)
	ListComputationModels(context.Context, *iotsitewise.ListComputationModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListComputationModelsOutput, error)
	DescribeComputationModel(context.Context, *iotsitewise.DescribeComputationModelInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeComputationModelOutput, error)
	ListDashboards(context.Context, *iotsitewise.ListDashboardsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDashboardsOutput, error)
	ListDatasets(context.Context, *iotsitewise.ListDatasetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDatasetsOutput, error)
	DescribeDataset(context.Context, *iotsitewise.DescribeDatasetInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeDatasetOutput, error)
	ListGateways(context.Context, *iotsitewise.ListGatewaysInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListGatewaysOutput, error)
	ListPortals(context.Context, *iotsitewise.ListPortalsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListPortalsOutput, error)
	ListProjects(context.Context, *iotsitewise.ListProjectsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListProjectsOutput, error)
	ListWorkspaces(context.Context, *iotsitewise.ListWorkspacesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListWorkspacesOutput, error)
	ListPipelines(context.Context, *iotsitewise.ListPipelinesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListPipelinesOutput, error)
	ListTasks(context.Context, *iotsitewise.ListTasksInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListTasksOutput, error)
	ListApplications(context.Context, *iotsitewise.ListApplicationsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListApplicationsOutput, error)
	ListTagsForResource(context.Context, *iotsitewise.ListTagsForResourceInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListTagsForResourceOutput, error)
}

const (
	// iotSWPageCap is the model's maxResults cap (1 to 250) of every
	// SiteWise listing scanned.
	iotSWPageCap = 250

	iotSWListTagsOp = "iotsitewise:ListTagsForResource"
)

// iotSWNotFound is the only not-found code modeled by ListTagsForResource,
// the Describes, and the ListAssets, ListPipelines and ListTasks child
// listings.
var iotSWNotFound = []string{"ResourceNotFoundException"}

func iotSWARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "iotsitewise", region, acct, kind+"/"+id)
}

// iotSWRow is the row of a listed SiteWise item stored under nativeID, item
// verbatim as the attributes.
func iotSWRow(acct *account, region, scanID, typ, nativeID, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// iotSWTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its list element carries, or, for gateways, portals,
// projects, dashboards and access policies, whose elements carry none, the
// ARN built in the format the Service Reference documents for the type. The
// Service Reference lists the action for every SiteWise type stored here.
// The op answers with every tag at once (no page token). It also models
// UnauthorizedException ("you are not authorized"), a denial isAccessDenied
// does not match, so it is listed as one.
func iotSWTags(client iotSWAPI) rowTagReader {
	return rowTagReader{
		op:       iotSWListTagsOp,
		notFound: iotSWNotFound,
		denied:   []string{"UnauthorizedException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &iotsitewise.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

type (
	iotSWPage[Item any]       func(ctx context.Context, token *string) ([]Item, *string, error)
	iotSWParentPage[Item any] func(ctx context.Context, parentID string, token *string) ([]Item, *string, error)
)

// iotSWPages is page with SiteWise's per-region rollout handled: a first page
// refused as a feature not supported yet (isIoTSiteWiseFeatureUnsupported)
// ends the listing as empty, since the op is not deployed in this region.
// After a page was read the same answer is returned as an error: the later
// pages were not read.
func iotSWPages[Item any](page iotSWPage[Item]) iotSWPage[Item] {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := page(ctx, token)
		if token == nil && isIoTSiteWiseFeatureUnsupported(err) {
			return nil, nil, nil
		}
		return items, next, err
	}
}

// iotSWParentPages is iotSWPages for each parent's child listing.
func iotSWParentPages[Item any](page iotSWParentPage[Item]) iotSWParentPage[Item] {
	return func(ctx context.Context, parentID string, token *string) ([]Item, *string, error) {
		return iotSWPages(func(ctx context.Context, token *string) ([]Item, *string, error) {
			return page(ctx, parentID, token)
		})(ctx, token)
	}
}

func scanIoTSiteWise(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iotsitewise.NewFromConfig(acct.cfg, func(o *iotsitewise.Options) { o.Region = region })
	return scanIoTSiteWiseAll(ctx, client, acct, region, st, scanID)
}

// scanIoTSiteWiseAll discovers IoT SiteWise resources in one region, running
// its phases one after another. A phase's error does not stop the later
// ones; a failed write does, since every later write would fail too, and so
// does ctx's end, reported as one error. A child phase gets the parents its
// parent phase stored: the assets the asset models, the pipelines and tasks
// the workspaces (the applications link to them), the projects and the
// portal access policies the portals, and the dashboards and the project
// access policies the projects. The phases run one at a time, so
// readRowTags's own bound is the bound on the tag reads in flight.
//
// The portal family (portals, projects, dashboards, access policies) is
// served by the SDK's "monitor." host, every other op by its "api." host.
// Whether any region serves one without the other is not verified (no live
// check was possible), so neither host's NXDOMAIN after rows were stored is
// read as a region gap: it is returned as an error and the stored rows keep
// their counts (resultLateNXDOMAIN; a portal-family one as
// iotSWMonitorUnresolvedError, since the monitor host may never have
// resolved). With nothing stored and every phase failing with an NXDOMAIN,
// the region reads as unavailable.
func scanIoTSiteWiseAll(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p                  phaseTotals
		tags               = iotSWTags(client)
		models, workspaces []childParent
		portals, projects  []childParent
		// monitorFrom is the index in p.errs of the portal family's first
		// error; -1 until that family starts.
		monitorFrom = -1
	)
	phases := []func() bool{
		func() (stop bool) {
			models, stop = scanIoTSWAssetModels(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool { return scanIoTSWAssets(ctx, client, acct, region, st, scanID, models, tags, &p) },
		func() bool { return scanIoTSWComputationModels(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanIoTSWGateways(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanIoTSWDatasets(ctx, client, acct, region, st, scanID, tags, &p) },
		func() (stop bool) {
			workspaces, stop = scanIoTSWWorkspaces(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool { return scanIoTSWApplications(ctx, client, acct, region, st, scanID, workspaces, tags, &p) },
		func() bool { return scanIoTSWPipelines(ctx, client, acct, region, st, scanID, workspaces, tags, &p) },
		func() bool { return scanIoTSWTasks(ctx, client, acct, region, st, scanID, workspaces, tags, &p) },
		func() (stop bool) {
			monitorFrom = len(p.errs)
			portals, stop = scanIoTSWPortals(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() (stop bool) {
			projects, stop = scanIoTSWProjects(ctx, client, acct, region, st, scanID, portals, tags, &p)
			return stop
		},
		func() bool {
			return scanIoTSWAccessPolicies(ctx, client, acct, region, st, scanID, types.ResourceTypePortal, portals, tags, &p)
		},
		func() bool { return scanIoTSWDashboards(ctx, client, acct, region, st, scanID, projects, tags, &p) },
		func() bool {
			return scanIoTSWAccessPolicies(ctx, client, acct, region, st, scanID, types.ResourceTypeProject, projects, tags, &p)
		},
	}
	notRun := 0
	for i, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - i
			break
		}
		if run() {
			break
		}
	}
	if monitorFrom >= 0 && p.total > 0 {
		for i := monitorFrom; i < len(p.errs); i++ {
			p.errs[i].err = iotSWMonitorUnresolved(p.errs[i].err)
		}
	}
	p.foldStop(ctx, "iotsitewise", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// iotSWMonitorUnresolvedError is an NXDOMAIN from a portal-family op, on the
// "monitor." host, after rows were stored. It keeps only the cause's text,
// not the cause: unwrapped to the DNS error, resultLateNXDOMAIN would
// describe it as an endpoint that stopped resolving after earlier calls to it
// succeeded, which the monitor host may never have done.
type iotSWMonitorUnresolvedError struct{ cause string }

func (e *iotSWMonitorUnresolvedError) Error() string {
	return "the monitor.iotsitewise host did not resolve: " + e.cause
}

// iotSWMonitorUnresolved replaces each NXDOMAIN member of err, a join
// looked into member by member, with an iotSWMonitorUnresolvedError; the
// other members are kept as they are.
func iotSWMonitorUnresolved(err error) error {
	if members, ok := err.(interface{ Unwrap() []error }); ok {
		out := make([]error, 0, len(members.Unwrap()))
		for _, m := range members.Unwrap() {
			out = append(out, iotSWMonitorUnresolved(m))
		}
		return errors.Join(out...)
	}
	if isDNSNotFound(err) {
		return &iotSWMonitorUnresolvedError{cause: err.Error()}
	}
	return err
}

// iotSWDetails is the read of a listing whose rows are replaced by their
// Describe body (readRowDetails) before their tags are read, and the errors
// of both reads, kept until the write is done. describe is sent the id the
// row was built with (ids, filled by the listing's row builder).
type iotSWDetails struct {
	op, what        string
	ids             map[*store.Resource]string
	describe        func(ctx context.Context, id string) (any, error)
	tags            rowTagReader
	hard, transient error
	tagErrs         rowTagErrs
}

// hook returns the reads as a beforeWrite. A row built from an element
// without an Id cannot be described: it is stored as its list element, tags
// still read, and one warning, sent once the rows are stored, names the
// first by its NativeID.
func (d *iotSWDetails) hook(ctx context.Context, st *store.Store, acct *account, region string) beforeWrite {
	return func(rows []*store.Resource) ([]*store.Resource, func()) {
		rows, getWarn, hard, transient := readRowDetails(ctx, st, acct, region, d.op, d.what, iotSWNotFound, rows,
			func(ctx context.Context, r *store.Resource) (any, error) {
				if d.ids[r] == "" {
					return nil, nil
				}
				return d.describe(ctx, d.ids[r])
			})
		d.hard, d.transient = hard, transient
		kept, tagWarn := d.tags.hook(ctx, st, acct, region, &d.tagErrs)(rows)
		var noID skipTally
		for _, r := range kept {
			if d.ids[r] == "" {
				noID.skipped(r.NativeID, nil)
			}
		}
		return kept, func() {
			if noID.count > 0 {
				st.ReportWarning(store.ScanWarning{
					Provider: "aws", Service: d.op, Scope: acct.ID + "/" + region,
					Message: fmt.Sprintf("%d %s listed with an empty Id were stored as their list element, not described; first %s", noID.count, d.what, noID.first),
				})
			}
			getWarn()
			tagWarn()
		}
	}
}

// addPhase adds the listing's counts and err to p under op, and both reads'
// errors under their ops, as rowTagErrs.addPhase does. It reports whether the
// write failed, after which the scanner must stop.
func (d *iotSWDetails) addPhase(p *phaseTotals, op string, total, inserted int, err error) (storeWriteFailed bool) {
	if errors.Is(err, store.ErrStoreWrite) {
		err = errors.Join(err, d.hard, d.transient)
	} else {
		p.errs.add(d.op, d.hard)
		p.errs.add(d.op, d.transient)
	}
	return d.tagErrs.addPhase(p, d.tags.op, op, total, inserted, err)
}

// scanIoTSWAssetModels stores the region's asset models, each as its
// DescribeAssetModel body (which adds the hierarchy definitions the list
// element lacks) with its tags, and returns the stored ones that carry an
// Id, the parents of the asset phase.
func scanIoTSWAssetModels(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (models []childParent, storeWriteFailed bool) {
	const op = "iotsitewise:ListAssetModels"
	d := &iotSWDetails{
		op: "iotsitewise:DescribeAssetModel", what: "asset model(s)", tags: tags, ids: map[*store.Resource]string{},
		describe: func(ctx context.Context, id string) (any, error) {
			return client.DescribeAssetModel(ctx, &iotsitewise.DescribeAssetModelInput{AssetModelId: &id})
		},
	}
	total, inserted, models, err := storeParentListWith(ctx, st, acct, region, op, "asset models", "Id",
		func(ctx context.Context, token *string) ([]types.AssetModelSummary, *string, error) {
			out, err := client.ListAssetModels(ctx, &iotsitewise.ListAssetModelsInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AssetModelSummaries, out.NextToken, nil
		},
		func(m types.AssetModelSummary) (*store.Resource, string, string) {
			id := sv(m.Id)
			if sv(m.Arn) == "" {
				return nil, "asset models have an empty Arn", cmp.Or(sv(m.Name), id, "(an asset model with no name)")
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWAssetModel, sv(m.Arn), cmp.Or(sv(m.Name), id), m)
			d.ids[r] = id
			return r, "", ""
		},
		func(m types.AssetModelSummary) string { return sv(m.Id) },
		d.hook(ctx, st, acct, region))
	return models, d.addPhase(p, op, total, inserted, err)
}

// scanIoTSWAssets stores the assets of each asset model the model phase
// stored, with their tags. A model ListAssets no longer finds is skipped.
func scanIoTSWAssets(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, models []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "iotsitewise:ListAssets", iotSWNotFound, models,
		func(ctx context.Context, modelID string, token *string) ([]types.AssetSummary, *string, error) {
			out, err := client.ListAssets(ctx, &iotsitewise.ListAssetsInput{
				AssetModelId: &modelID, Filter: types.ListAssetsFilterAll, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AssetSummaries, out.NextToken, nil
		},
		func(modelID string, a types.AssetSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "assets have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(an asset with no name)") + " of asset model " + modelID
			}
			return iotSWRow(acct, region, scanID, TypeIoTSWAsset, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a), "", ""
		}, tags, p)
	return stop
}

// scanIoTSWComputationModels stores the region's computation models, each
// as its DescribeComputationModel body (which adds the data bindings to
// asset models and assets) with its tags.
func scanIoTSWComputationModels(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "iotsitewise:ListComputationModels"
	d := &iotSWDetails{
		op: "iotsitewise:DescribeComputationModel", what: "computation model(s)", tags: tags, ids: map[*store.Resource]string{},
		describe: func(ctx context.Context, id string) (any, error) {
			return client.DescribeComputationModel(ctx, &iotsitewise.DescribeComputationModelInput{ComputationModelId: &id})
		},
	}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		iotSWPages(func(ctx context.Context, token *string) ([]types.ComputationModelSummary, *string, error) {
			out, err := client.ListComputationModels(ctx, &iotsitewise.ListComputationModelsInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ComputationModelSummaries, out.NextToken, nil
		}),
		func(c types.ComputationModelSummary) (*store.Resource, string, string) {
			id := sv(c.Id)
			if sv(c.Arn) == "" {
				return nil, "computation models have an empty Arn", cmp.Or(sv(c.Name), id, "(a computation model with no name)")
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWComputationModel, sv(c.Arn), cmp.Or(sv(c.Name), id), c)
			d.ids[r] = id
			return r, "", ""
		},
		d.hook(ctx, st, acct, region))
	return d.addPhase(p, op, total, inserted, err)
}

// scanIoTSWGateways stores the region's gateways with their tags, each under
// the gateway ARN built from its GatewayId (the element carries no ARN).
func scanIoTSWGateways(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "iotsitewise:ListGateways",
		func(ctx context.Context, token *string) ([]types.GatewaySummary, *string, error) {
			out, err := client.ListGateways(ctx, &iotsitewise.ListGatewaysInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.GatewaySummaries, out.NextToken, nil
		},
		func(g types.GatewaySummary) (*store.Resource, string, string) {
			id := sv(g.GatewayId)
			if id == "" {
				return nil, "gateways have an empty GatewayId", cmp.Or(sv(g.GatewayName), "(a gateway with no name)")
			}
			return iotSWRow(acct, region, scanID, TypeIoTSWGateway, iotSWARN(acct.partition(), region, acct.ID, "gateway", id), cmp.Or(sv(g.GatewayName), id), g), "", ""
		},
		tags, p)
}

// scanIoTSWDatasets stores the region's datasets, each as its DescribeDataset
// body (which adds the Kendra knowledge base and role the source reads) with
// its tags. ListDatasets requires SourceType, whose only value is KENDRA.
func scanIoTSWDatasets(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "iotsitewise:ListDatasets"
	d := &iotSWDetails{
		op: "iotsitewise:DescribeDataset", what: "dataset(s)", tags: tags, ids: map[*store.Resource]string{},
		describe: func(ctx context.Context, id string) (any, error) {
			return client.DescribeDataset(ctx, &iotsitewise.DescribeDatasetInput{DatasetId: &id})
		},
	}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		iotSWPages(func(ctx context.Context, token *string) ([]types.DatasetSummary, *string, error) {
			out, err := client.ListDatasets(ctx, &iotsitewise.ListDatasetsInput{
				SourceType: types.DatasetSourceTypeKendra, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.DatasetSummaries, out.NextToken, nil
		}),
		func(ds types.DatasetSummary) (*store.Resource, string, string) {
			id := sv(ds.Id)
			if sv(ds.Arn) == "" {
				return nil, "datasets have an empty Arn", cmp.Or(sv(ds.Name), id, "(a dataset with no name)")
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWDataset, sv(ds.Arn), cmp.Or(sv(ds.Name), id), ds)
			d.ids[r] = id
			return r, "", ""
		},
		d.hook(ctx, st, acct, region))
	return d.addPhase(p, op, total, inserted, err)
}

// scanIoTSWWorkspaces stores the region's workspaces with their tags and
// returns the stored ones that carry a name, keyed by it: the workspace child
// listings take the name.
func scanIoTSWWorkspaces(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (workspaces []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "iotsitewise:ListWorkspaces", "workspaces", "Name",
		iotSWPages(func(ctx context.Context, token *string) ([]types.WorkspaceSummary, *string, error) {
			out, err := client.ListWorkspaces(ctx, &iotsitewise.ListWorkspacesInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkspaceSummaries, out.NextToken, nil
		}),
		func(w types.WorkspaceSummary) (*store.Resource, string, string) {
			if sv(w.Arn) == "" {
				return nil, "workspaces have an empty Arn", cmp.Or(sv(w.Name), "(a workspace with no name)")
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWWorkspace, sv(w.Arn), sv(w.Name), w)
			if w.Status != nil {
				r.Status = nonEmptyPtr(string(w.Status.State))
			}
			r.CreatedAt = tp(w.CreatedAt)
			return r, "", ""
		},
		func(w types.WorkspaceSummary) string { return sv(w.Name) },
		tags, p)
}

// iotSWContain records each stored child under the parent parentOf names by
// NativeID; a child without one is stored unlinked.
func iotSWContain(st *store.Store, acct *account, op string, children []*store.Resource, parentOf map[*store.Resource]string) error {
	pairs := make([][2]string, 0, len(children))
	for _, c := range children {
		if parent := parentOf[c]; parent != "" {
			pairs = append(pairs, [2]string{c.ID, store.ResourceID("aws", acct.ID, parent)})
		}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return fmt.Errorf("closure %s: %w", op, err)
	}
	return nil
}

// scanIoTSWApplications stores the region's applications with their tags.
// ListApplications lists account-wide (it takes no workspace), so each
// application is recorded under its workspace only when that workspace was
// stored this scan.
func scanIoTSWApplications(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "iotsitewise:ListApplications"
	workspaceARN := make(map[string]string, len(workspaces))
	for _, w := range workspaces {
		workspaceARN[w.id] = w.arn
	}
	var (
		parentOf = map[*store.Resource]string{}
		stored   []*store.Resource
		tagErrs  rowTagErrs
	)
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		iotSWPages(func(ctx context.Context, token *string) ([]types.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &iotsitewise.ListApplicationsInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		}),
		func(a types.ApplicationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "applications have an empty Arn", cmp.Or(sv(a.Name), sv(a.Id), "(an application with no name)")
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWApplication, sv(a.Arn), cmp.Or(sv(a.Name), sv(a.Id)), a)
			r.Status = nonEmptyPtr(string(a.Status))
			r.CreatedAt = tp(a.CreatedAt)
			parentOf[r] = workspaceARN[sv(a.WorkspaceName)]
			return r, "", ""
		},
		func(rows []*store.Resource) ([]*store.Resource, func()) {
			kept, warn := tags.hook(ctx, st, acct, region, &tagErrs)(rows)
			stored = kept
			return kept, warn
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		err = errors.Join(err, iotSWContain(st, acct, op, stored, parentOf))
	}
	return tagErrs.addPhase(p, tags.op, op, total, inserted, err)
}

// iotSWStoreChildren stores each parent's children with their tags
// (storeEachParentTags) and records every stored child under its parent. It
// returns the children stored, none after a failed write, and whether a
// write failed.
func iotSWStoreChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, notFound []string, parents []childParent,
	page iotSWParentPage[Item],
	row func(parentID string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (stored []*store.Resource, storeWriteFailed bool) {
	arnOf := make(map[string]string, len(parents))
	for _, pr := range parents {
		arnOf[pr.id] = pr.arn
	}
	parentOf := map[*store.Resource]string{}
	stored, stop := storeEachParentTags(ctx, st, acct, region, op, notFound, parents, page,
		func(parentID string, it Item) (*store.Resource, string, string) {
			r, why, key := row(parentID, it)
			if r != nil {
				parentOf[r] = arnOf[parentID]
			}
			return r, why, key
		}, tags, p)
	if stop {
		return nil, true
	}
	return stored, p.addPhase(op, 0, 0, iotSWContain(st, acct, op, stored, parentOf))
}

// scanIoTSWPipelines stores the pipelines of each workspace the workspace
// phase stored, with their tags, under their workspace. A workspace
// ListPipelines no longer finds is skipped.
func scanIoTSWPipelines(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := iotSWStoreChildren(ctx, st, acct, region, "iotsitewise:ListPipelines", iotSWNotFound, workspaces,
		iotSWParentPages(func(ctx context.Context, workspace string, token *string) ([]types.PipelineSummary, *string, error) {
			out, err := client.ListPipelines(ctx, &iotsitewise.ListPipelinesInput{WorkspaceName: &workspace, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PipelineSummaries, out.NextToken, nil
		}),
		func(workspace string, pl types.PipelineSummary) (*store.Resource, string, string) {
			if sv(pl.PipelineArn) == "" {
				return nil, "pipelines have an empty PipelineArn", cmp.Or(sv(pl.PipelineName), "(a pipeline with no name)") + " of workspace " + workspace
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWPipeline, sv(pl.PipelineArn), sv(pl.PipelineName), pl)
			if pl.Status != nil {
				r.Status = nonEmptyPtr(string(pl.Status.State))
			}
			r.CreatedAt = tp(pl.CreatedAt)
			return r, "", ""
		}, tags, p)
	return stop
}

// scanIoTSWTasks stores the tasks of each workspace the workspace phase
// stored, with their tags, under their workspace. A workspace ListTasks no
// longer finds is skipped.
func scanIoTSWTasks(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := iotSWStoreChildren(ctx, st, acct, region, "iotsitewise:ListTasks", iotSWNotFound, workspaces,
		iotSWParentPages(func(ctx context.Context, workspace string, token *string) ([]types.TaskSummary, *string, error) {
			out, err := client.ListTasks(ctx, &iotsitewise.ListTasksInput{WorkspaceName: &workspace, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TaskSummaries, out.NextToken, nil
		}),
		func(workspace string, tk types.TaskSummary) (*store.Resource, string, string) {
			if sv(tk.TaskArn) == "" {
				return nil, "tasks have an empty TaskArn", cmp.Or(sv(tk.TaskName), "(a task with no name)") + " of workspace " + workspace
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWTask, sv(tk.TaskArn), sv(tk.TaskName), tk)
			if tk.Status != nil {
				r.Status = nonEmptyPtr(string(tk.Status.State))
			}
			r.CreatedAt = tp(tk.CreatedAt)
			return r, "", ""
		}, tags, p)
	return stop
}

// scanIoTSWPortals stores the region's portals with their tags, each under
// the portal ARN built from its Id, and returns the stored ones, the parents
// of the project and portal access-policy phases.
func scanIoTSWPortals(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (portals []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "iotsitewise:ListPortals", "portals", "Id",
		func(ctx context.Context, token *string) ([]types.PortalSummary, *string, error) {
			out, err := client.ListPortals(ctx, &iotsitewise.ListPortalsInput{NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PortalSummaries, out.NextToken, nil
		},
		func(pt types.PortalSummary) (*store.Resource, string, string) {
			id := sv(pt.Id)
			if id == "" {
				return nil, "portals have an empty Id", cmp.Or(sv(pt.Name), "(a portal with no name)")
			}
			return iotSWRow(acct, region, scanID, TypeIoTSWPortal, iotSWARN(acct.partition(), region, acct.ID, "portal", id), cmp.Or(sv(pt.Name), id), pt), "", ""
		},
		func(pt types.PortalSummary) string { return sv(pt.Id) },
		tags, p)
}

// scanIoTSWProjects stores the projects of each portal the portal phase
// stored, with their tags, under their portal, and returns the stored ones,
// the parents of the dashboard and project access-policy phases. ListProjects
// models no not-found code, so a portal deleted since it was listed fails the
// phase (decision #14).
func scanIoTSWProjects(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, portals []childParent, tags rowTagReader, p *phaseTotals) (projects []childParent, storeWriteFailed bool) {
	ids := map[*store.Resource]string{}
	stored, stop := iotSWStoreChildren(ctx, st, acct, region, "iotsitewise:ListProjects", nil, portals,
		func(ctx context.Context, portalID string, token *string) ([]types.ProjectSummary, *string, error) {
			out, err := client.ListProjects(ctx, &iotsitewise.ListProjectsInput{PortalId: &portalID, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ProjectSummaries, out.NextToken, nil
		},
		func(portalID string, pj types.ProjectSummary) (*store.Resource, string, string) {
			id := sv(pj.Id)
			if id == "" {
				return nil, "projects have an empty Id", cmp.Or(sv(pj.Name), "(a project with no name)") + " of portal " + portalID
			}
			r := iotSWRow(acct, region, scanID, TypeIoTSWProject, iotSWARN(acct.partition(), region, acct.ID, "project", id), cmp.Or(sv(pj.Name), id), pj)
			ids[r] = id
			return r, "", ""
		}, tags, p)
	for _, r := range stored {
		projects = append(projects, childParent{id: ids[r], arn: r.NativeID})
	}
	return projects, stop
}

// scanIoTSWDashboards stores the dashboards of each project the project
// phase stored, with their tags, under their project. ListDashboards models
// no not-found code, so a project deleted since it was listed fails the
// phase (decision #14).
func scanIoTSWDashboards(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, projects []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := iotSWStoreChildren(ctx, st, acct, region, "iotsitewise:ListDashboards", nil, projects,
		func(ctx context.Context, projectID string, token *string) ([]types.DashboardSummary, *string, error) {
			out, err := client.ListDashboards(ctx, &iotsitewise.ListDashboardsInput{ProjectId: &projectID, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.DashboardSummaries, out.NextToken, nil
		},
		func(projectID string, db types.DashboardSummary) (*store.Resource, string, string) {
			id := sv(db.Id)
			if id == "" {
				return nil, "dashboards have an empty Id", cmp.Or(sv(db.Name), "(a dashboard with no name)") + " of project " + projectID
			}
			return iotSWRow(acct, region, scanID, TypeIoTSWDashboard, iotSWARN(acct.partition(), region, acct.ID, "dashboard", id), cmp.Or(sv(db.Name), id), db), "", ""
		}, tags, p)
	return stop
}

// scanIoTSWAccessPolicies stores the access policies of each portal or
// project (rType) an earlier phase stored, with their tags, each under the
// access-policy ARN built from its Id. ListAccessPolicies models no not-found
// code, so a parent deleted since it was listed fails the phase (decision
// #14).
func scanIoTSWAccessPolicies(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, rType types.ResourceType, parents []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "iotsitewise:ListAccessPolicies", nil, parents,
		func(ctx context.Context, parentID string, token *string) ([]types.AccessPolicySummary, *string, error) {
			out, err := client.ListAccessPolicies(ctx, &iotsitewise.ListAccessPoliciesInput{
				ResourceType: rType, ResourceId: &parentID, NextToken: token, MaxResults: sdkaws.Int32(iotSWPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessPolicySummaries, out.NextToken, nil
		},
		func(parentID string, ap types.AccessPolicySummary) (*store.Resource, string, string) {
			id := sv(ap.Id)
			if id == "" {
				return nil, "access policies have an empty Id", "(an access policy) of " + string(rType) + " " + parentID
			}
			return iotSWRow(acct, region, scanID, TypeIoTSWAccessPolicy, iotSWARN(acct.partition(), region, acct.ID, "access-policy", id), id, ap), "", ""
		}, tags, p)
	return stop
}
