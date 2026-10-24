package aws

import (
	"cmp"
	"context"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/store"
)

// AgentCore resource scanners added after the initial set (configuration
// bundles, datasets, harnesses + endpoints, registries + records, policy
// generations). All store list summaries, carrying no secrets.

// bacWithStatus sets status and created on the row bacRow built, if any.
func bacWithStatus(r *store.Resource, why, key, status string, created *time.Time) (*store.Resource, string, string) {
	if r != nil {
		r.Status = &status
		r.CreatedAt = tp(created)
	}
	return r, why, key
}

// scanBACConfigurationBundles lists the region's configuration bundles. A
// first page refused with a region-gap code (isBACRegionGap) means the op is
// not deployed in this region.
func scanBACConfigurationBundles(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListConfigurationBundles",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.ConfigurationBundleSummary, *string, error) {
			out, err := client.ListConfigurationBundles(ctx, &bac.ListConfigurationBundlesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Bundles, out.NextToken, nil
		}),
		func(b bactypes.ConfigurationBundleSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreConfigurationBundle, "configuration bundles",
				b.BundleArn, cmp.Or(sv(b.BundleName), sv(b.BundleId)), sv(b.BundleId), b)
			if r != nil {
				r.CreatedAt = tp(b.CreatedAt)
			}
			return r, why, key
		}, tags, p)
}

// scanBACDatasets lists the region's datasets; a first-page region gap is
// handled as scanBACConfigurationBundles says.
func scanBACDatasets(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListDatasets",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.DatasetSummary, *string, error) {
			out, err := client.ListDatasets(ctx, &bac.ListDatasetsInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Datasets, out.NextToken, nil
		}),
		func(d bactypes.DatasetSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreDataset, "datasets",
				d.DatasetArn, cmp.Or(sv(d.DatasetName), sv(d.DatasetId)), sv(d.DatasetId), d)
			return bacWithStatus(r, why, key, string(d.Status), d.CreatedAt)
		}, tags, p)
}

// scanBACHarnesses stores the region's harnesses with their tags and returns
// the stored ones that carry a HarnessId, the parents of the endpoint phase.
// A first-page region gap is handled as scanBACConfigurationBundles says.
func scanBACHarnesses(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (harnesses []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "bedrockagentcore:ListHarnesses", "harnesses", "HarnessId",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.HarnessSummary, *string, error) {
			out, err := client.ListHarnesses(ctx, &bac.ListHarnessesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Harnesses, out.NextToken, nil
		}),
		func(h bactypes.HarnessSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreHarness, "harnesses",
				h.Arn, cmp.Or(sv(h.HarnessName), sv(h.HarnessId)), sv(h.HarnessId), h)
			return bacWithStatus(r, why, key, string(h.Status), h.CreatedAt)
		},
		func(h bactypes.HarnessSummary) string { return sv(h.HarnessId) },
		tags, p)
}

// scanBACHarnessEndpoints stores, with their tags, the endpoints of each
// harness the harness phase stored. A harness ResourceNotFoundException says
// was deleted since it was listed is skipped (storeTokenEachParent).
func scanBACHarnessEndpoints(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, harnesses []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return bacStoreEachParentTags(ctx, st, acct, region, "bedrockagentcore:ListHarnessEndpoints", []string{"ResourceNotFoundException"}, harnesses,
		bacParentPages(nil, func(ctx context.Context, harness string, token *string) ([]bactypes.HarnessEndpoint, *string, error) {
			out, err := client.ListHarnessEndpoints(ctx, &bac.ListHarnessEndpointsInput{HarnessId: &harness, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Endpoints, out.NextToken, nil
		}),
		func(harness string, e bactypes.HarnessEndpoint) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreHarnessEndpoint, "harness endpoints",
				e.Arn, cmp.Or(sv(e.EndpointName), sv(e.Arn)), cmp.Or(sv(e.EndpointName), "(an endpoint with no name) of harness "+harness), e)
			return bacWithStatus(r, why, key, string(e.Status), e.CreatedAt)
		}, tags, p)
}

// scanBACRegistries stores the region's registries and returns the stored
// ones that carry a RegistryId, the parents of the record phase. A
// first-page region gap is handled as scanBACConfigurationBundles says.
func scanBACRegistries(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, "bedrockagentcore:ListRegistries", "registries", "RegistryId",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.RegistrySummary, *string, error) {
			out, err := client.ListRegistries(ctx, &bac.ListRegistriesInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Registries, out.NextToken, nil
		}),
		func(rg bactypes.RegistrySummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreRegistry, "registries",
				rg.RegistryArn, cmp.Or(sv(rg.Name), sv(rg.RegistryId)), sv(rg.RegistryId), rg)
			return bacWithStatus(r, why, key, string(rg.Status), rg.CreatedAt)
		},
		func(rg bactypes.RegistrySummary) string { return sv(rg.RegistryId) })
}

// scanBACRegistryRecords lists the records of each registry the registry
// phase stored. A registry ResourceNotFoundException says was deleted since
// it was listed is skipped (storeTokenEachParent).
func scanBACRegistryRecords(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, registries []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListRegistryRecords", []string{"ResourceNotFoundException"}, bacParentIDs(registries),
		bacParentPages(nil, func(ctx context.Context, registry string, token *string) ([]bactypes.RegistryRecordSummary, *string, error) {
			out, err := client.ListRegistryRecords(ctx, &bac.ListRegistryRecordsInput{RegistryId: &registry, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RegistryRecords, out.NextToken, nil
		}),
		func(_ string, rec bactypes.RegistryRecordSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreRegistryRecord, "registry records",
				rec.RecordArn, cmp.Or(sv(rec.Name), sv(rec.RecordId)), sv(rec.RecordId), rec)
			return bacWithStatus(r, why, key, string(rec.Status), rec.CreatedAt)
		})
}

// scanBACPolicyGenerations lists the policy generations of each engine the
// policy-engine phase stored. An engine ResourceNotFoundException says was
// deleted since it was listed is skipped (storeTokenEachParent).
func scanBACPolicyGenerations(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, engines []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListPolicyGenerations", []string{"ResourceNotFoundException"}, bacParentIDs(engines),
		bacParentPages(nil, func(ctx context.Context, engine string, token *string) ([]bactypes.PolicyGeneration, *string, error) {
			out, err := client.ListPolicyGenerations(ctx, &bac.ListPolicyGenerationsInput{PolicyEngineId: &engine, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PolicyGenerations, out.NextToken, nil
		}),
		func(_ string, g bactypes.PolicyGeneration) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCorePolicyGeneration, "policy generations",
				g.PolicyGenerationArn, cmp.Or(sv(g.Name), sv(g.PolicyGenerationId)), sv(g.PolicyGenerationId), g)
			return bacWithStatus(r, why, key, string(g.Status), g.CreatedAt)
		})
}
