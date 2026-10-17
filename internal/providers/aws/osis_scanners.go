package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/osis"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOSISPipeline, Service: "osis"})
	registerType(restype.Descriptor{Type: TypeOSISPipelineBlueprint, Service: "osis", Managed: true})
	registerType(restype.Descriptor{Type: TypeOSISPipelineEndpoint, Service: "osis"})
	registerType(restype.Descriptor{Type: TypeOSISPipelineEndpointConnection, Service: "osis"})
	registerService(serviceEntry{
		name: "aws:osis",
		fn:   scanOSIS,
	})
}

const (
	osisEndpointConnectionsOp = "osis:ListPipelineEndpointConnections"
	// osisEndpointConnectionPageLimit is the model's cap on
	// ListPipelineEndpointConnections MaxResults (range 1..100).
	osisEndpointConnectionPageLimit int32 = 100
)

// scanOSIS discovers OpenSearch Ingestion Service (OSIS) pipelines, the
// AWS-managed configuration blueprints and VPC pipeline endpoints, then the
// endpoint connections to this account's pipelines. The connection phase runs
// whatever the earlier phases returned, so their failure costs it nothing, and
// the counts from both sides are summed.
// Each side's transient error is demoted to a warning, since one transient
// member would make the dispatcher read the whole join as transient.
func scanOSIS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := osis.NewFromConfig(acct.cfg, func(o *osis.Options) { o.Region = region })
	total, inserted, pipelines, err := scanOSISPhases(ctx, client, acct, region, st, scanID)
	t, i, cerr := scanOSISPipelineEndpointConnections(ctx, client, acct, region, st, scanID, pipelines)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, "osis", acct, region, err),
		demoteTransient(st, osisEndpointConnectionsOp, acct, region, cerr),
	)
}

// scanOSISPhases runs the pipeline, blueprint and endpoint phases in order,
// stopping at the first error, and returns the ARNs of the pipelines stored.
func scanOSISPhases(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, pipelines map[string]bool, err error) {
	total, inserted, pipelines, err = scanOSISPipelines(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, pipelines, err
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOSISPipelineBlueprints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOSISPipelineEndpoints(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, pipelines, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, pipelines, nil
}

// scanOSISPipelines stores the region's pipelines and returns their ARNs.
func scanOSISPipelines(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPipelines(ctx, &osis.ListPipelinesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "osis:ListPipelines", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("osis:ListPipelines: %w", err)
		}
		for _, p := range out.Pipelines {
			arn := sv(p.PipelineArn)
			if arn == "" {
				continue
			}
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSISPipeline, NativeID: arn,
				Name: p.PipelineName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "osis pipelines")
	if err != nil {
		return 0, 0, nil, err
	}
	pipelines := make(map[string]bool, len(batch))
	for _, r := range batch {
		pipelines[r.NativeID] = true
	}
	return total, inserted, pipelines, nil
}

// scanOSISPipelineBlueprints lists the AWS-managed pipeline configuration
// blueprints. They have a BlueprintName but no ARN, so the NativeID is
// synthesised. Provider-managed — these are AWS-owned catalogue items.
func scanOSISPipelineBlueprints(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListPipelineBlueprints(ctx, &osis.ListPipelineBlueprintsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "osis:ListPipelineBlueprints", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("osis:ListPipelineBlueprints: %w", err)
	}
	var batch []*store.Resource
	for _, b := range out.Blueprints {
		name := sv(b.BlueprintName)
		if name == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:osis:%s:%s:blueprint/%s", region, acct.ID, name)
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
				return 0, 0, skipIfAccessDenied(st, "osis:ListPipelineEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("osis:ListPipelineEndpoints: %w", err)
		}
		for _, e := range out.PipelineEndpoints {
			id := sv(e.EndpointId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:osis:%s:%s:pipeline-endpoint/%s", region, acct.ID, id)
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
