package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:bedrockagentcore",
		fn:   scanBedrockAgentCore,
		emits: []coverage.TypeDecl{
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreAPIKeyCredentialProvider, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreBrowser, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreBrowserCustom, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreBrowserProfile, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreCodeInterpreter, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreCodeInterpreterCustom, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreEvaluator, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreGateway, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreGatewayTarget},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreMemory, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreOAuth2CredentialProvider, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreOnlineEvaluationConfig, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCorePolicy},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCorePolicyEngine, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreRuntime, Leaf: true},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreRuntimeEndpoint},
			{Service: "bedrockagentcore", DiscoType: TypeBedrockAgentCoreWorkloadIdentity, Leaf: true},
		},
	})
}

type bedrockAgentCoreAPI interface {
	ListApiKeyCredentialProviders(context.Context, *bac.ListApiKeyCredentialProvidersInput, ...func(*bac.Options)) (*bac.ListApiKeyCredentialProvidersOutput, error)
	ListBrowsers(context.Context, *bac.ListBrowsersInput, ...func(*bac.Options)) (*bac.ListBrowsersOutput, error)
	ListBrowserProfiles(context.Context, *bac.ListBrowserProfilesInput, ...func(*bac.Options)) (*bac.ListBrowserProfilesOutput, error)
	ListCodeInterpreters(context.Context, *bac.ListCodeInterpretersInput, ...func(*bac.Options)) (*bac.ListCodeInterpretersOutput, error)
	ListEvaluators(context.Context, *bac.ListEvaluatorsInput, ...func(*bac.Options)) (*bac.ListEvaluatorsOutput, error)
	ListGateways(context.Context, *bac.ListGatewaysInput, ...func(*bac.Options)) (*bac.ListGatewaysOutput, error)
	ListGatewayTargets(context.Context, *bac.ListGatewayTargetsInput, ...func(*bac.Options)) (*bac.ListGatewayTargetsOutput, error)
	ListMemories(context.Context, *bac.ListMemoriesInput, ...func(*bac.Options)) (*bac.ListMemoriesOutput, error)
	ListOauth2CredentialProviders(context.Context, *bac.ListOauth2CredentialProvidersInput, ...func(*bac.Options)) (*bac.ListOauth2CredentialProvidersOutput, error)
	ListOnlineEvaluationConfigs(context.Context, *bac.ListOnlineEvaluationConfigsInput, ...func(*bac.Options)) (*bac.ListOnlineEvaluationConfigsOutput, error)
	ListPolicies(context.Context, *bac.ListPoliciesInput, ...func(*bac.Options)) (*bac.ListPoliciesOutput, error)
	ListPolicyEngines(context.Context, *bac.ListPolicyEnginesInput, ...func(*bac.Options)) (*bac.ListPolicyEnginesOutput, error)
	ListAgentRuntimes(context.Context, *bac.ListAgentRuntimesInput, ...func(*bac.Options)) (*bac.ListAgentRuntimesOutput, error)
	ListAgentRuntimeEndpoints(context.Context, *bac.ListAgentRuntimeEndpointsInput, ...func(*bac.Options)) (*bac.ListAgentRuntimeEndpointsOutput, error)
	ListWorkloadIdentities(context.Context, *bac.ListWorkloadIdentitiesInput, ...func(*bac.Options)) (*bac.ListWorkloadIdentitiesOutput, error)
}

func scanBedrockAgentCore(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := bac.NewFromConfig(acct.cfg, func(o *bac.Options) { o.Region = region })

	gwIDs, t, i, ferr := scanBACGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	rtIDs, t, i, ferr := scanBACRuntimes(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	engineIDs, t, i, ferr := scanBACPolicyEngines(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBACGatewayTargets(ctx, client, acct, region, st, scanID, gwIDs) },
		func() (int, int, error) { return scanBACRuntimeEndpoints(ctx, client, acct, region, st, scanID, rtIDs) },
		func() (int, int, error) { return scanBACApiKeyCreds(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACOauth2Creds(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACBrowsers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACBrowserProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACCodeInterpreters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACEvaluators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACMemories(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACOnlineEvalConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBACPolicies(ctx, client, acct, region, st, scanID, engineIDs) },
		func() (int, int, error) { return scanBACWorkloadIdentities(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func bacARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:%s/%s", region, acct, kind, id)
}

func scanBACApiKeyCreds(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListApiKeyCredentialProvidersPaginator(client, &bac.ListApiKeyCredentialProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListApiKeyCredentialProviders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListApiKeyCredentialProviders: %w", perr)
		}
		for _, p := range out.CredentialProviders {
			arn := sv(p.CredentialProviderArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreAPIKeyCredentialProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore api-key-credential-providers")
}

func scanBACOauth2Creds(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListOauth2CredentialProvidersPaginator(client, &bac.ListOauth2CredentialProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListOauth2CredentialProviders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListOauth2CredentialProviders: %w", perr)
		}
		for _, p := range out.CredentialProviders {
			arn := sv(p.CredentialProviderArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreOAuth2CredentialProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore oauth2-credential-providers")
}

// scanBACBrowsers lists both CUSTOM browsers (customer-created) as
// aws:bedrockagentcore:browser-custom and SYSTEM browsers (the AWS built-in
// browser tools, e.g. aws.browser.v1) as aws:bedrockagentcore:browser with
// ManagedByProvider set — the two are distinct upstream resource types.
func scanBACBrowsers(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, rt := range []bactypes.ResourceType{bactypes.ResourceTypeCustom, bactypes.ResourceTypeSystem} {
		dtype := TypeBedrockAgentCoreBrowserCustom
		managed := false
		if rt == bactypes.ResourceTypeSystem {
			dtype = TypeBedrockAgentCoreBrowser
			managed = true
		}
		pager := bac.NewListBrowsersPaginator(client, &bac.ListBrowsersInput{Type: rt})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "bedrockagentcore:ListBrowsers", acct.ID, region, perr)
					return upsertBatch(st, batch, "bedrockagentcore browsers")
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListBrowsers: %w", perr)
			}
			for _, b := range out.BrowserSummaries {
				arn := sv(b.BrowserArn)
				if arn == "" {
					continue
				}
				label := sv(b.BrowserId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: dtype, NativeID: arn,
					Name: &label, Region: &region, ManagedByProvider: managed,
					AttributesJSON: mustJSON(b), CreatedAt: tp(b.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore browsers")
}

func scanBACBrowserProfiles(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListBrowserProfilesPaginator(client, &bac.ListBrowserProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListBrowserProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListBrowserProfiles: %w", perr)
		}
		for _, p := range out.ProfileSummaries {
			arn := sv(p.ProfileArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.ProfileId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreBrowserProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore browser-profiles")
}

// scanBACCodeInterpreters lists CUSTOM code interpreters as
// aws:bedrockagentcore:code-interpreter-custom and SYSTEM ones (the AWS built-in
// interpreter, e.g. aws.codeinterpreter.v1) as aws:bedrockagentcore:code-
// interpreter with ManagedByProvider set.
func scanBACCodeInterpreters(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, rt := range []bactypes.ResourceType{bactypes.ResourceTypeCustom, bactypes.ResourceTypeSystem} {
		dtype := TypeBedrockAgentCoreCodeInterpreterCustom
		managed := false
		if rt == bactypes.ResourceTypeSystem {
			dtype = TypeBedrockAgentCoreCodeInterpreter
			managed = true
		}
		pager := bac.NewListCodeInterpretersPaginator(client, &bac.ListCodeInterpretersInput{Type: rt})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "bedrockagentcore:ListCodeInterpreters", acct.ID, region, perr)
					return upsertBatch(st, batch, "bedrockagentcore code-interpreters")
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListCodeInterpreters: %w", perr)
			}
			for _, c := range out.CodeInterpreterSummaries {
				arn := sv(c.CodeInterpreterArn)
				if arn == "" {
					continue
				}
				label := sv(c.CodeInterpreterId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: dtype, NativeID: arn,
					Name: &label, Region: &region, ManagedByProvider: managed,
					AttributesJSON: mustJSON(c), CreatedAt: tp(c.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore code-interpreters")
}

func scanBACEvaluators(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListEvaluatorsPaginator(client, &bac.ListEvaluatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Per-region feature gap: gateway-level "Unable to determine
			// service/operation name to be authorized" means the op is not
			// recognised by the regional endpoint. Silent-skip.
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListEvaluators", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListEvaluators: %w", perr)
		}
		for _, e := range out.Evaluators {
			arn := sv(e.EvaluatorArn)
			if arn == "" {
				continue
			}
			label := sv(e.EvaluatorName)
			if label == "" {
				label = sv(e.EvaluatorId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreEvaluator, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				// AWS-supplied evaluators carry EvaluatorType="Builtin"; customer
				// evaluators carry "Custom". Case-insensitive match guards drift.
				ManagedByProvider: strings.EqualFold(string(e.EvaluatorType), "Builtin"),
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore evaluators")
}

func scanBACGateways(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListGatewaysPaginator(client, &bac.ListGatewaysInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListGateways", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagentcore:ListGateways: %w", perr)
		}
		for _, g := range out.Items {
			id := sv(g.GatewayId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			label := sv(g.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreGateway, NativeID: bacARN(region, acct.ID, "gateway", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "bedrockagentcore gateways")
	return ids, t, i, err
}

func scanBACGatewayTargets(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gwIDs []string) (int, int, error) {
	if len(gwIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, gid := range gwIDs {
		id := gid
		pager := bac.NewListGatewayTargetsPaginator(client, &bac.ListGatewayTargetsInput{GatewayIdentifier: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListGatewayTargets %s: %w", gid, perr)
			}
			for _, t := range out.Items {
				tid := sv(t.TargetId)
				if tid == "" {
					continue
				}
				label := sv(t.Name)
				if label == "" {
					label = tid
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreGatewayTarget, NativeID: bacARN(region, acct.ID, "gateway-target", gid+"/"+tid),
					Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore gateway-targets")
}

func scanBACMemories(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListMemoriesPaginator(client, &bac.ListMemoriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListMemories", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListMemories: %w", perr)
		}
		for _, m := range out.Memories {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			label := sv(m.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreMemory, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore memories")
}

func scanBACOnlineEvalConfigs(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListOnlineEvaluationConfigsPaginator(client, &bac.ListOnlineEvaluationConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Per-region feature gap: gateway-level "Unable to determine
			// service/operation name to be authorized" means the op is not
			// recognised by the regional endpoint. Silent-skip.
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListOnlineEvaluationConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListOnlineEvaluationConfigs: %w", perr)
		}
		for _, c := range out.OnlineEvaluationConfigs {
			arn := sv(c.OnlineEvaluationConfigArn)
			if arn == "" {
				continue
			}
			label := sv(c.OnlineEvaluationConfigName)
			if label == "" {
				label = sv(c.OnlineEvaluationConfigId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreOnlineEvaluationConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore online-eval-configs")
}

// scanBACPolicies requires PolicyEngineId per call. Fan-out across engine IDs
// enumerated by scanBACPolicyEngines.
func scanBACPolicies(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, engineIDs []string) (int, int, error) {
	if len(engineIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, eid := range engineIDs {
		engineID := eid
		pager := bac.NewListPoliciesPaginator(client, &bac.ListPoliciesInput{PolicyEngineId: &engineID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "bedrockagentcore:ListPolicies", acct.ID, region, perr)
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListPolicies %s: %w", engineID, perr)
			}
			for _, p := range out.Policies {
				arn := sv(p.PolicyArn)
				if arn == "" {
					continue
				}
				label := sv(p.Name)
				if label == "" {
					label = sv(p.PolicyId)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCorePolicy, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore policies")
}

// scanBACPolicyEngines lists engines and returns their IDs so scanBACPolicies
// can fan-out per engine.
func scanBACPolicyEngines(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListPolicyEnginesPaginator(client, &bac.ListPolicyEnginesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListPolicyEngines", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagentcore:ListPolicyEngines: %w", perr)
		}
		for _, p := range out.PolicyEngines {
			arn := sv(p.PolicyEngineArn)
			if arn == "" {
				continue
			}
			if id := sv(p.PolicyEngineId); id != "" {
				ids = append(ids, id)
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.PolicyEngineId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCorePolicyEngine, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "bedrockagentcore policy-engines")
	return ids, t, i, err
}

func scanBACRuntimes(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := bac.NewListAgentRuntimesPaginator(client, &bac.ListAgentRuntimesInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListAgentRuntimes", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("bedrockagentcore:ListAgentRuntimes: %w", perr)
		}
		for _, r := range out.AgentRuntimes {
			arn := sv(r.AgentRuntimeArn)
			if arn == "" {
				continue
			}
			id := sv(r.AgentRuntimeId)
			ids = append(ids, id)
			label := sv(r.AgentRuntimeName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreRuntime, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "bedrockagentcore runtimes")
	return ids, t, i, err
}

func scanBACRuntimeEndpoints(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, rtIDs []string) (int, int, error) {
	if len(rtIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, rid := range rtIDs {
		id := rid
		pager := bac.NewListAgentRuntimeEndpointsPaginator(client, &bac.ListAgentRuntimeEndpointsInput{AgentRuntimeId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("bedrockagentcore:ListAgentRuntimeEndpoints %s: %w", rid, perr)
			}
			for _, e := range out.RuntimeEndpoints {
				arn := sv(e.AgentRuntimeEndpointArn)
				if arn == "" {
					continue
				}
				label := sv(e.Name)
				if label == "" {
					label = sv(e.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreRuntimeEndpoint, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore runtime-endpoints")
}

func scanBACWorkloadIdentities(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListWorkloadIdentitiesPaginator(client, &bac.ListWorkloadIdentitiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "bedrockagentcore:ListWorkloadIdentities", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("bedrockagentcore:ListWorkloadIdentities: %w", perr)
		}
		for _, w := range out.WorkloadIdentities {
			arn := sv(w.WorkloadIdentityArn)
			if arn == "" {
				continue
			}
			label := sv(w.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreWorkloadIdentity, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore workload-identities")
}
