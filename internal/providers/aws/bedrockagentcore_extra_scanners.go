package aws

import (
	"cmp"
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bacdata "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	bacdatatypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

// scanBACCapacityProviders stores the region's capacity providers with their
// tags. A first page refused with a region-gap code (isBACRegionGap) means
// the op is not deployed in this region.
func scanBACCapacityProviders(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrockagentcore:ListCapacityProviders",
		bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.CapacityProviderSummary, *string, error) {
			out, err := client.ListCapacityProviders(ctx, &bac.ListCapacityProvidersInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CapacityProviders, out.NextToken, nil
		}),
		func(c bactypes.CapacityProviderSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreCapacityProvider, "capacity providers",
				c.CapacityProviderArn, cmp.Or(sv(c.Name), sv(c.CapacityProviderId)), sv(c.CapacityProviderId), c)
			if r != nil {
				r.Status = nonEmptyPtr(string(c.Status))
			}
			return r, why, key
		}, tags, p)
}

// scanBACConsentPortals stores the region's consent portals with their tags.
// A first-page region gap is handled as scanBACCapacityProviders says.
func scanBACConsentPortals(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "bedrockagentcore:ListConsentPortals"
	var unauthorized bacUnauthorized
	stop := storeTokenListTags(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bactypes.ConsentPortalSummary, *string, error) {
			out, err := client.ListConsentPortals(ctx, &bac.ListConsentPortalsInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConsentPortals, out.NextToken, nil
		})),
		func(c bactypes.ConsentPortalSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreConsentPortal, "consent portals",
				c.ConsentPortalArn, cmp.Or(sv(c.Name), sv(c.ConsentPortalId)), sv(c.ConsentPortalId), c)
			if r != nil {
				r.Status = nonEmptyPtr(string(c.Status))
				r.CreatedAt = tp(c.CreatedAt)
			}
			return r, why, key
		}, tags, p)
	unauthorized.warn(st, op, acct, region, stop)
	return stop
}

// scanBACABTests lists the region's A/B tests from the data-plane host. A
// first-page region gap is handled as scanBACCapacityProviders says. A
// first-page NXDOMAIN is a region gap too, skipped silently whether or not
// the control plane answered: an op absent from a region whose service is
// present is a sub-feature gap, as the region-gap codes are, and a real DNS
// failure is a timeout or SERVFAIL, never an NXDOMAIN. Returned, it would
// also be read by scanBACAll's resultLateNXDOMAIN as the control plane's.
// One after a page was read is a plain error (lateNXDOMAIN): that page
// proves the host resolved.
func scanBACABTests(ctx context.Context, client bedrockAgentCoreDataAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "bedrockagentcore:ListABTests"
	var unauthorized bacUnauthorized
	total, inserted, err := storeTokenList(ctx, st, acct, region, op,
		bacStopUnauthorized(&unauthorized, bacPages(isBACRegionGap, func(ctx context.Context, token *string) ([]bacdatatypes.ABTestSummary, *string, error) {
			out, err := client.ListABTests(ctx, &bacdata.ListABTestsInput{NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			switch {
			case token == nil && isDNSNotFound(err):
				return nil, nil, nil
			case err != nil:
				return nil, nil, lateNXDOMAIN(op, err)
			}
			return out.AbTests, out.NextToken, nil
		})),
		func(a bacdatatypes.ABTestSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockAgentCoreABTest, "A/B tests",
				a.AbTestArn, cmp.Or(sv(a.Name), sv(a.AbTestId)), sv(a.AbTestId), a)
			if r != nil {
				r.Status = nonEmptyPtr(string(a.Status))
				r.CreatedAt = tp(a.CreatedAt)
			}
			return r, why, key
		})
	unauthorized.warn(st, op, acct, region, errors.Is(err, store.ErrStoreWrite))
	return total, inserted, err
}

// bacGatewayARNs maps each gateway's id to its NativeID, which the NativeIDs
// of its rules and rate limits extend so the parent link always matches it.
func bacGatewayARNs(gateways []childParent) map[string]string {
	arns := make(map[string]string, len(gateways))
	for _, g := range gateways {
		arns[g.id] = g.arn
	}
	return arns
}

// scanBACGatewayRules lists the rules of each gateway the gateway phase
// stored. A rule carries GatewayArn but no ARN of its own, so NativeID is
// `{gatewayNativeID}/rule/{ruleId}`. A gateway ResourceNotFoundException says
// was deleted since it was listed is skipped (storeTokenEachParent); a first
// page refused with a region-gap code (isBACRegionGap) means the op is not
// deployed in this region.
func scanBACGatewayRules(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent) (int, int, error) {
	arns := bacGatewayARNs(gateways)
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListGatewayRules", []string{"ResourceNotFoundException"}, bacParentIDs(gateways),
		bacParentPages(isBACRegionGap, func(ctx context.Context, gateway string, token *string) ([]bactypes.GatewayRuleDetail, *string, error) {
			out, err := client.ListGatewayRules(ctx, &bac.ListGatewayRulesInput{GatewayIdentifier: &gateway, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.GatewayRules, out.NextToken, nil
		}),
		func(gateway string, r bactypes.GatewayRuleDetail) (*store.Resource, string, string) {
			rid := sv(r.RuleId)
			if rid == "" {
				return nil, "gateway rules have an empty RuleId", "(a rule with no id) of gateway " + gateway
			}
			row := bacRowAt(acct, region, scanID, TypeBedrockAgentCoreGatewayRule, arns[gateway]+"/rule/"+rid, rid, r)
			row.Status = nonEmptyPtr(string(r.Status))
			row.CreatedAt = tp(r.CreatedAt)
			return row, "", ""
		})
}

// scanBACGatewayRateLimits lists the rate limits of each gateway, as
// scanBACGatewayRules lists its rules; NativeID is
// `{gatewayNativeID}/rate-limit/{rateLimitId}`.
func scanBACGatewayRateLimits(ctx context.Context, client bedrockAgentCoreAPI, acct *account, region string, st *store.Store, scanID string, gateways []childParent) (int, int, error) {
	arns := bacGatewayARNs(gateways)
	return storeTokenEachParent(ctx, st, acct, region, "bedrockagentcore:ListGatewayRateLimits", []string{"ResourceNotFoundException"}, bacParentIDs(gateways),
		bacParentPages(isBACRegionGap, func(ctx context.Context, gateway string, token *string) ([]bactypes.GatewayRateLimitDetail, *string, error) {
			out, err := client.ListGatewayRateLimits(ctx, &bac.ListGatewayRateLimitsInput{GatewayIdentifier: &gateway, NextToken: token, MaxResults: sdkaws.Int32(bacPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.RateLimits, out.NextToken, nil
		}),
		func(gateway string, l bactypes.GatewayRateLimitDetail) (*store.Resource, string, string) {
			lid := sv(l.RateLimitId)
			if lid == "" {
				return nil, "gateway rate limits have an empty RateLimitId", "(a rate limit with no id) of gateway " + gateway
			}
			row := bacRowAt(acct, region, scanID, TypeBedrockAgentCoreGatewayRateLimit, arns[gateway]+"/rate-limit/"+lid, lid, l)
			row.Status = nonEmptyPtr(string(l.Status))
			row.CreatedAt = tp(l.CreatedAt)
			return row, "", ""
		})
}
