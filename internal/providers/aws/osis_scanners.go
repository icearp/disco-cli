package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/osis"
	osistypes "github.com/aws/aws-sdk-go-v2/service/osis/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOSISPipeline, Service: "osis"})
	registerType(restype.Descriptor{Type: TypeOSISPipelineBlueprint, Service: "osis", Managed: true, Untaggable: true})
	registerType(restype.Descriptor{Type: TypeOSISPipelineEndpoint, Service: "osis"})
	registerType(restype.Descriptor{Type: TypeOSISPipelineEndpointConnection, Service: "osis"})
	registerService(serviceEntry{
		name: "aws:osis",
		fn:   scanOSIS,
	})
}

const (
	osisListPipelinesOp          = "osis:ListPipelines"
	osisListPipelineBlueprintsOp = "osis:ListPipelineBlueprints"
	osisListPipelineEndpointsOp  = "osis:ListPipelineEndpoints"
	osisEndpointConnectionsOp    = "osis:ListPipelineEndpointConnections"
	// osisPipelinePageLimit and osisEndpointConnectionPageLimit are the
	// model's caps on ListPipelines and ListPipelineEndpointConnections
	// MaxResults (range 1..100).
	osisPipelinePageLimit           int32 = 100
	osisEndpointConnectionPageLimit int32 = 100
)

// scanOSIS discovers OpenSearch Ingestion Service (OSIS) pipelines, the
// AWS-managed configuration blueprints and VPC pipeline endpoints, then the
// endpoint connections to this account's pipelines. Every phase runs whatever
// the others returned, and their counts are summed; only a store write failure
// stops the scan. A failed pipeline phase costs the connection phase only the
// links to the pipelines it did not store.
func scanOSIS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := osis.NewFromConfig(acct.cfg, func(o *osis.Options) { o.Region = region })
	var phases phaseTotals
	t, i, pipelines, err := scanOSISPipelines(ctx, client, acct, region, st, scanID)
	if phases.addPhase(osisListPipelinesOp, t, i, err) {
		return phases.result(st, acct, region)
	}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{osisListPipelineBlueprintsOp, func() (int, int, error) {
			return scanOSISPipelineBlueprints(ctx, client, acct, region, st, scanID)
		}},
		{osisListPipelineEndpointsOp, func() (int, int, error) {
			return scanOSISPipelineEndpoints(ctx, client, acct, region, st, scanID)
		}},
		{osisEndpointConnectionsOp, func() (int, int, error) {
			return scanOSISPipelineEndpointConnections(ctx, client, acct, region, st, scanID, pipelines)
		}},
	} {
		t, i, err := phase.run()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// scanOSISPipelines stores the region's pipelines, those read before a failed
// ListPipelines page included, each with the tags its ListPipelines element
// carries, and returns the ARNs of the pipelines stored. A pipeline listed
// without an ARN is not stored and is warned about under its name. A denial is
// warned about once; any other error is returned after the rows are stored. A
// failed write returns no ARNs.
func scanOSISPipelines(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	total, inserted, stored, err := storeParentList(ctx, st, acct, region, osisListPipelinesOp, "pipelines", "PipelineArn",
		func(ctx context.Context, token *string) ([]osistypes.PipelineSummary, *string, error) {
			out, err := client.ListPipelines(ctx, &osis.ListPipelinesInput{NextToken: token, MaxResults: sdkaws.Int32(osisPipelinePageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.Pipelines, out.NextToken, nil
		},
		func(p osistypes.PipelineSummary) (*store.Resource, string, string) {
			arn := sv(p.PipelineArn)
			if arn == "" {
				return nil, "pipelines have an empty PipelineArn", sv(p.PipelineName)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSISPipeline, NativeID: arn,
				Name: p.PipelineName, Region: &region, Status: nonEmptyPtr(string(p.Status)),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				TagsJSON: kvTagsJSON(p.Tags, func(t osistypes.Tag) (*string, *string) { return t.Key, t.Value }),
			}, "", ""
		},
		func(p osistypes.PipelineSummary) string { return sv(p.PipelineArn) })
	pipelines := make(map[string]bool, len(stored))
	for _, p := range stored {
		pipelines[p.arn] = true
	}
	return total, inserted, pipelines, err
}

// scanOSISPipelineBlueprints lists the AWS-managed pipeline configuration
// blueprints. They have a BlueprintName but no ARN, so the NativeID is
// synthesised. Provider-managed — these are AWS-owned catalogue items.
func scanOSISPipelineBlueprints(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListPipelineBlueprints(ctx, &osis.ListPipelineBlueprintsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, osisListPipelineBlueprintsOp, acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("%s: %w", osisListPipelineBlueprintsOp, err)
	}
	var batch []*store.Resource
	for _, b := range out.Blueprints {
		name := sv(b.BlueprintName)
		if name == "" {
			continue
		}
		arn := arnFor(acct.partition(), "osis", region, acct.ID, "blueprint/"+name)
		label := name
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeOSISPipelineBlueprint, NativeID: arn,
			Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "osis pipeline-blueprints")
}

// scanOSISPipelineEndpoints lists VPC pipeline endpoints. They have no ARN, so
// the NativeID is synthesised from EndpointId; the resolver wires the endpoint
// to its pipeline via the PipelineArn attribute.
func scanOSISPipelineEndpoints(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := osis.NewListPipelineEndpointsPaginator(client, &osis.ListPipelineEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, osisListPipelineEndpointsOp, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", osisListPipelineEndpointsOp, err)
		}
		for _, e := range out.PipelineEndpoints {
			id := sv(e.EndpointId)
			if id == "" {
				continue
			}
			arn := arnFor(acct.partition(), "osis", region, acct.ID, "pipeline-endpoint/"+id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSISPipelineEndpoint, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "osis pipeline-endpoints")
}

// osisEndpointConnectionNativeID keys a connection under the pipeline it
// reaches: the API returns no ARN for one and the Service Reference defines
// none. EndpointId's model pattern admits no "/".
func osisEndpointConnectionNativeID(pipelineARN, endpointID string) string {
	return pipelineARN + "/endpoint-connection/" + endpointID
}

// scanOSISPipelineEndpointConnections stores the region's connections from VPC
// pipeline endpoints, which other accounts may own, to this account's
// pipelines, and links each under its pipeline when pipelines holds it.
// pipelines holds only pipelines stored this scan. Rows read before an error
// are stored.
func scanOSISPipelineEndpointConnections(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string, pipelines map[string]bool) (int, int, error) {
	pager := osis.NewListPipelineEndpointConnectionsPaginator(client, &osis.ListPipelineEndpointConnectionsInput{},
		func(o *osis.ListPipelineEndpointConnectionsPaginatorOptions) {
			o.Limit = osisEndpointConnectionPageLimit
			o.StopOnDuplicateToken = true
		})
	var children []containedChild
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return upsertRegionChildren(st, acct, region, osisEndpointConnectionsOp, children, err)
		}
		for _, c := range out.PipelineEndpointConnections {
			pipelineARN, endpointID := sv(c.PipelineArn), sv(c.EndpointId)
			if pipelineARN == "" || endpointID == "" {
				continue
			}
			var parentID string
			if pipelines[pipelineARN] {
				parentID = store.ResourceID("aws", acct.ID, pipelineARN)
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSISPipelineEndpointConnection, NativeID: osisEndpointConnectionNativeID(pipelineARN, endpointID),
				Name: &endpointID, Region: &region, Status: nonEmptyPtr(string(c.Status)),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}})
		}
	}
	return upsertRegionChildren(st, acct, region, osisEndpointConnectionsOp, children, nil)
}
