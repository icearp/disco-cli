package aws

import (
	"context"
	"fmt"

	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	"github.com/icearp/disco-cli/store"
)

// AgentCore resource scanners added after the initial set (configuration
// bundles, datasets, harnesses + endpoints, registries + records, policy
// generations). All store list summaries, carrying no secrets.

func scanBACConfigurationBundles(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListConfigurationBundlesPaginator(client, &bac.ListConfigurationBundlesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListConfigurationBundles", "configuration-bundles", acct.ID, region, perr)
		}
		for _, b := range out.Bundles {
			arn := sv(b.BundleArn)
			if arn == "" {
				continue
			}
			label := sv(b.BundleName)
			if label == "" {
				label = sv(b.BundleId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreConfigurationBundle, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b),
				CreatedAt: tp(b.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore configuration-bundles")
}

func scanBACDatasets(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListDatasetsPaginator(client, &bac.ListDatasetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListDatasets", "datasets", acct.ID, region, perr)
		}
		for _, d := range out.Datasets {
			arn := sv(d.DatasetArn)
			if arn == "" {
				continue
			}
			label := sv(d.DatasetName)
			if label == "" {
				label = sv(d.DatasetId)
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreDataset, NativeID: arn,
				Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(d),
				CreatedAt: tp(d.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore datasets")
}

// scanBACHarnesses lists harnesses and returns their IDs for the endpoint fan-out.
func scanBACHarnesses(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListHarnessesPaginator(client, &bac.ListHarnessesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			t, i, e := bacListSkip(st, batch, "bedrockagentcore:ListHarnesses", "harnesses", acct.ID, region, perr)
			return ids, t, i, e
		}
		for _, h := range out.Harnesses {
			arn := sv(h.Arn)
			if arn == "" {
				continue
			}
			if id := sv(h.HarnessId); id != "" {
				ids = append(ids, id)
			}
			label := sv(h.HarnessName)
			if label == "" {
				label = sv(h.HarnessId)
			}
			status := string(h.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreHarness, NativeID: arn,
				Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(h),
				CreatedAt: tp(h.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	t, i, e := upsertBatch(st, batch, "bedrockagentcore harnesses")
	return ids, t, i, e
}

func scanBACHarnessEndpoints(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, harnessIDs []string) (int, int, error) {
	if len(harnessIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, hid := range harnessIDs {
		id := hid
		pager := bac.NewListHarnessEndpointsPaginator(client, &bac.ListHarnessEndpointsInput{HarnessId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListHarnessEndpoints %s: %w", hid, perr)
			}
			for _, e := range out.Endpoints {
				arn := sv(e.Arn)
				if arn == "" {
					continue
				}
				label := sv(e.EndpointName)
				if label == "" {
					label = arn
				}
				status := string(e.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreHarnessEndpoint, NativeID: arn,
					Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(e),
					CreatedAt: tp(e.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore harness-endpoints")
}

// scanBACRegistries lists registries and returns their IDs for the record fan-out.
func scanBACRegistries(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListRegistriesPaginator(client, &bac.ListRegistriesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			t, i, e := bacListSkip(st, batch, "bedrockagentcore:ListRegistries", "registries", acct.ID, region, perr)
			return ids, t, i, e
		}
		for _, rg := range out.Registries {
			arn := sv(rg.RegistryArn)
			if arn == "" {
				continue
			}
			if id := sv(rg.RegistryId); id != "" {
				ids = append(ids, id)
			}
			label := sv(rg.Name)
			if label == "" {
				label = sv(rg.RegistryId)
			}
			status := string(rg.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreRegistry, NativeID: arn,
				Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(rg),
				CreatedAt: tp(rg.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	t, i, e := upsertBatch(st, batch, "bedrockagentcore registries")
	return ids, t, i, e
}

func scanBACRegistryRecords(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, registryIDs []string) (int, int, error) {
	if len(registryIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, rgid := range registryIDs {
		id := rgid
		pager := bac.NewListRegistryRecordsPaginator(client, &bac.ListRegistryRecordsInput{RegistryId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListRegistryRecords %s: %w", rgid, perr)
			}
			for _, rec := range out.RegistryRecords {
				arn := sv(rec.RecordArn)
				if arn == "" {
					continue
				}
				label := sv(rec.Name)
				if label == "" {
					label = sv(rec.RecordId)
				}
				status := string(rec.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreRegistryRecord, NativeID: arn,
					Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(rec),
					CreatedAt: tp(rec.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore registry-records")
}

// scanBACPolicyGenerations fans out per policy-engine ID (enumerated by
// scanBACPolicyEngines).
func scanBACPolicyGenerations(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, engineIDs []string) (int, int, error) {
	if len(engineIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, eid := range engineIDs {
		id := eid
		pager := bac.NewListPolicyGenerationsPaginator(client, &bac.ListPolicyGenerationsInput{PolicyEngineId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListPolicyGenerations %s: %w", eid, perr)
			}
			for _, g := range out.PolicyGenerations {
				arn := sv(g.PolicyGenerationArn)
				if arn == "" {
					continue
				}
				label := sv(g.Name)
				if label == "" {
					label = sv(g.PolicyGenerationId)
				}
				status := string(g.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCorePolicyGeneration, NativeID: arn,
					Name: &label, Region: &region, Status: &status, AttributesJSON: mustJSON(g),
					CreatedAt: tp(g.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore policy-generations")
}

// bacListSkip soft-skips access-denied (preserving rows already accumulated by
// the caller) and otherwise wraps the error as fatal.
func bacListSkip(st *store.Store, batch []*store.Resource, op, label, acctID, region string, perr error) (int, int, error) {
	if isAccessDenied(perr) {
		_ = skipIfAccessDenied(st, op, acctID, region, perr)
		return upsertBatch(st, batch, "bedrockagentcore "+label)
	}
	// Newer AgentCore ops aren't deployed in every region. Some 404 with
	// UnknownOperationException; others (e.g. ListRegistries) reach a front-end
	// that resolves but is not provisioned, which returns a 500
	// AuthorizerConfigurationException. Both are region gaps, not real failures —
	// keep accumulated rows and move on without a warning. Swallowing to nil is
	// what lets the sequential scanBedrockAgentCore continue to the later ops
	// (payments, the parallel batch) instead of aborting the whole service on the
	// first such gap. The retry burn this 500 would otherwise cause is cut
	// separately by marking the code non-retryable on the client (see
	// scanBedrockAgentCore / withNonRetryableCodes).
	if isAPIErrorCode(perr, "UnknownOperationException", "AuthorizerConfigurationException") {
		return upsertBatch(st, batch, "bedrockagentcore "+label)
	}
	return 0, 0, fmt.Errorf("%s: %w", op, perr)
}
