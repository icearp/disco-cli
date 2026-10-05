package aws

import (
	"context"
	"fmt"
	"sync"

	bacdata "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreCapacityProvider, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreConsentPortal, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreGatewayRule, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreGatewayRateLimit, Service: "bedrockagentcore"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCoreABTest, Service: "bedrockagentcore"})
}

// bedrockAgentCoreDataAPI is the data-plane (bedrock-agentcore) surface: A/B
// tests are durable resources that only the data-plane SDK lists.
type bedrockAgentCoreDataAPI interface {
	ListABTests(context.Context, *bacdata.ListABTestsInput, ...func(*bacdata.Options)) (*bacdata.ListABTestsOutput, error)
}

func scanBACCapacityProviders(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListCapacityProvidersPaginator(client, &bac.ListCapacityProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListCapacityProviders", "capacity-providers", acct.ID, region, perr)
		}
		for _, p := range out.CapacityProviders {
			arn := sv(p.CapacityProviderArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.CapacityProviderId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreCapacityProvider, NativeID: arn,
				Name: &label, Region: &region, Status: bacStatus(string(p.Status)),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore capacity-providers")
}

func scanBACConsentPortals(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bac.NewListConsentPortalsPaginator(client, &bac.ListConsentPortalsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListConsentPortals", "consent-portals", acct.ID, region, perr)
		}
		for _, p := range out.ConsentPortals {
			arn := sv(p.ConsentPortalArn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.ConsentPortalId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreConsentPortal, NativeID: arn,
				Name: &label, Region: &region, Status: bacStatus(string(p.Status)),
				AttributesJSON: mustJSON(p), CreatedAt: tp(p.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore consent-portals")
}

func scanBACABTests(ctx context.Context, client bedrockAgentCoreDataAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bacdata.NewListABTestsPaginator(client, &bacdata.ListABTestsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return bacListSkip(st, batch, "bedrockagentcore:ListABTests", "ab-tests", acct.ID, region, perr)
		}
		for _, a := range out.AbTests {
			arn := sv(a.AbTestArn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.AbTestId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockAgentCoreABTest, NativeID: arn,
				Name: &label, Region: &region, Status: bacStatus(string(a.Status)),
				AttributesJSON: mustJSON(a), CreatedAt: tp(a.CreatedAt), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrockagentcore ab-tests")
}

// scanBACGatewayRules fans out per gateway. A rule carries GatewayArn but no
// ARN of its own, so NativeID is `{gatewayNativeID}/rule/{ruleId}`, extending
// the NativeID scanBACGateways stored so the parent link always matches it.
func scanBACGatewayRules(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gws []bacGateway) (int, int, error) {
	batch, err := bacGatewayFanout(ctx, acct, region, st, "bedrockagentcore:ListGatewayRules", gws, func(ctx context.Context, gw bacGateway) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := bac.NewListGatewayRulesPaginator(client, &bac.ListGatewayRulesInput{GatewayIdentifier: &gw.id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if isAPIErrorCode(perr, "ResourceNotFoundException") {
				return rows, nil
			}
			if perr != nil {
				return rows, perr
			}
			for _, r := range out.GatewayRules {
				rid := sv(r.RuleId)
				if rid == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreGatewayRule, NativeID: gw.arn + "/rule/" + rid,
					Name: &rid, Region: &region, Status: bacStatus(string(r.Status)),
					AttributesJSON: mustJSON(r), CreatedAt: tp(r.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "bedrockagentcore gateway-rules")
}

// scanBACGatewayRateLimits fans out per gateway; rate limits carry no ARN, so
// NativeID is `{gatewayNativeID}/rate-limit/{rateLimitId}`.
func scanBACGatewayRateLimits(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gws []bacGateway) (int, int, error) {
	batch, err := bacGatewayFanout(ctx, acct, region, st, "bedrockagentcore:ListGatewayRateLimits", gws, func(ctx context.Context, gw bacGateway) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := bac.NewListGatewayRateLimitsPaginator(client, &bac.ListGatewayRateLimitsInput{GatewayIdentifier: &gw.id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if isAPIErrorCode(perr, "ResourceNotFoundException") {
				return rows, nil
			}
			if perr != nil {
				return rows, perr
			}
			for _, l := range out.RateLimits {
				lid := sv(l.RateLimitId)
				if lid == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeBedrockAgentCoreGatewayRateLimit, NativeID: gw.arn + "/rate-limit/" + lid,
					Name: &lid, Region: &region, Status: bacStatus(string(l.Status)),
					AttributesJSON: mustJSON(l), CreatedAt: tp(l.CreatedAt), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "bedrockagentcore gateway-rate-limits")
}

// bacGatewayFanout runs list once per gateway under fanoutMed and gathers the
// rows. A denial warns once per op, since the warning names the op rather than
// the gateway, and the other gateways still run (IAM may deny only some
// gateway ARNs); the region-gap codes bacListSkip tolerates are skipped
// silently. Either way the rows list returned before the error are kept. A
// gateway deleted since it was listed is list's to skip. Any other error fails
// the phase.
func bacGatewayFanout(ctx context.Context, acct *account, region string, st *store.Store, op string, gws []bacGateway,
	list func(ctx context.Context, gw bacGateway) ([]*store.Resource, error),
) ([]*store.Resource, error) {
	var (
		mu       sync.Mutex
		batch    []*store.Resource
		denyOnce sync.Once
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, gw := range gws {
		g.Go(func() error {
			rows, err := list(gctx, gw)
			switch {
			case err == nil:
			case isAccessDenied(err):
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
			case isAPIErrorCode(err, "UnknownOperationException", "AuthorizerConfigurationException"):
			default:
				return fmt.Errorf("%s %s: %w", op, gw.id, err)
			}
			mu.Lock()
			batch = append(batch, rows...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return batch, nil
}

func bacStatus(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
