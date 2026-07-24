package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/osis"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOSISPipeline, Service: "osis", Upstream: "AWS::OSIS::Pipeline", Leaf: true})
	registerType(restype.Descriptor{Type: TypeOSISPipelineBlueprint, Service: "osis", Upstream: "AWS::osis::pipeline-blueprint", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeOSISPipelineEndpoint, Service: "osis", Upstream: "AWS::osis::pipeline-endpoint"})
	registerService(serviceEntry{
		name: "aws:osis",
		fn:   scanOSIS,
	})
}

// scanOSIS discovers OpenSearch Ingestion Service (OSIS) pipelines, the
// AWS-managed configuration blueprints, and VPC pipeline endpoints.
func scanOSIS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := osis.NewFromConfig(acct.cfg, func(o *osis.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOSISPipelines(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOSISPipelineBlueprints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOSISPipelineEndpoints(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanOSISPipelines(ctx context.Context, client *osis.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPipelines(ctx, &osis.ListPipelinesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "osis:ListPipelines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("osis:ListPipelines: %w", err)
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
	return upsertBatch(st, batch, "osis pipelines")
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
