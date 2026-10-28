package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/icearp/disco-cli/store"
)

// apigatewayv2API is the API Gateway v2 (HTTP/WebSocket) ops the scan calls
// outside the developer portals (apigatewayv2PortalAPI). Every listing pages
// by NextToken by hand (readTokenPages), even where the SDK has a paginator
// (ListRoutingRules): a repeated token then fails the listing, where an SDK
// paginator would loop or stop as if complete.
type apigatewayv2API interface {
	GetApis(context.Context, *apigatewayv2.GetApisInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error)
	GetAuthorizers(context.Context, *apigatewayv2.GetAuthorizersInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetAuthorizersOutput, error)
	GetDomainNames(context.Context, *apigatewayv2.GetDomainNamesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetDomainNamesOutput, error)
	GetApiMappings(context.Context, *apigatewayv2.GetApiMappingsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApiMappingsOutput, error)
	GetVpcLinks(context.Context, *apigatewayv2.GetVpcLinksInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetVpcLinksOutput, error)
	ListRoutingRules(context.Context, *apigatewayv2.ListRoutingRulesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListRoutingRulesOutput, error)
	apigatewayv2ExtAPI
}

const (
	agv2GetApisOp          = "apigatewayv2:GetApis"
	agv2GetDomainNamesOp   = "apigatewayv2:GetDomainNames"
	agv2GetAPIMappingsOp   = "apigatewayv2:GetApiMappings"
	agv2ListRoutingRulesOp = "apigatewayv2:ListRoutingRules"
	agv2GetVpcLinksOp      = "apigatewayv2:GetVpcLinks"
	// agv2RoutingRulePageLimit is ListRoutingRules' MaxResults range maximum.
	// The other listings model MaxResults as an unbounded string, so they
	// send none.
	agv2RoutingRulePageLimit = 100
)

// apigatewayv2NotFound is the not-found code every per-parent listing of the
// scan models: the parent (an API, integration, route or domain name) was
// deleted after it was listed.
var apigatewayv2NotFound = []string{"NotFoundException"}

// scanAPIGatewayV2 runs four families concurrently on ctx itself
// (runFamilies): the HTTP/WebSocket APIs with their children, the custom
// domain names with their mappings and routing rules, the VPC links, and the
// developer portals. No family's error cancels another or discards its
// counts. Every op calls one endpoint, so once any row was stored an NXDOMAIN
// is no region gap (resultLateNXDOMAIN), and a ctx end is one error.
func scanAPIGatewayV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := apigatewayv2.NewFromConfig(acct.cfg, func(o *apigatewayv2.Options) { o.Region = region })
	p := runFamilies(ctx, "apigatewayv2",
		phaseFamily(func(p *phaseTotals) { scanAPIGatewayHTTPAPIs(ctx, client, acct, region, st, scanID, p) }),
		phaseFamily(func(p *phaseTotals) { scanAPIGatewayV2DomainNames(ctx, client, acct, region, st, scanID, p) }),
		countedFamily(agv2GetVpcLinksOp, func() (int, int, error) {
			return scanAPIGatewayV2VpcLinks(ctx, client, acct, region, st, scanID)
		}),
		// Its errors span four ops and are filed under the service.
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayV2Portals(ctx, client, acct, region, st, scanID)
		},
	)
	p.foldStop(ctx, "apigatewayv2", 0)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// agv2Row is an API Gateway v2 row of acct in region, its attributes the
// listed element.
func agv2Row(acct *account, region, scanID, typ, nativeID, name string, attrs any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID, Name: &name, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}

// scanAPIGatewayV2VpcLinks stores the region's VPC links with the tags each
// listed link carries. NativeID = arn:{partition}:apigateway:{region}::/vpclinks/{id}.
func scanAPIGatewayV2VpcLinks(ctx context.Context, client apigatewayv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, agv2GetVpcLinksOp,
		func(ctx context.Context, token *string) ([]apigwv2types.VpcLink, *string, error) {
			out, err := client.GetVpcLinks(ctx, &apigatewayv2.GetVpcLinksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(v apigwv2types.VpcLink) (*store.Resource, string, string) {
			id := sv(v.VpcLinkId)
			if id == "" {
				return nil, "VPC links have no VpcLinkId", cmp.Or(sv(v.Name), "(unnamed)")
			}
			r := agv2Row(acct, region, scanID, TypeAPIGatewayV2VpcLink, apigatewayARNIn(acct.partition(), region, "vpclinks", id), sv(v.Name), v)
			status := string(v.VpcLinkStatus)
			r.Status = &status
			r.CreatedAt = tp(v.CreatedDate)
			r.TagsJSON = mapTagsJSON(v.Tags)
			return r, "", ""
		})
}

// scanAPIGatewayHTTPAPIs stores the HTTP and WebSocket APIs with the tags each
// listed API carries, then the children of the APIs it stored
// (scanAPIGatewayV2APIChildren). Counts and errors go to p, each error under
// the op that failed; a failed write of the APIs stops it.
// NativeID = arn:{partition}:apigateway:{region}::/apis/{id}.
func scanAPIGatewayHTTPAPIs(ctx context.Context, client apigatewayv2API, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) {
	total, inserted, apis, err := storeParentList(ctx, st, acct, region, agv2GetApisOp, "APIs", "ApiId",
		func(ctx context.Context, token *string) ([]apigwv2types.Api, *string, error) {
			out, err := client.GetApis(ctx, &apigatewayv2.GetApisInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(api apigwv2types.Api) (*store.Resource, string, string) {
			id := sv(api.ApiId)
			if id == "" {
				return nil, "APIs have no ApiId", cmp.Or(sv(api.Name), "(unnamed)")
			}
			r := agv2Row(acct, region, scanID, TypeAPIGatewayV2API, apigatewayARNIn(acct.partition(), region, "apis", id), sv(api.Name), api)
			r.TagsJSON = mapTagsJSON(api.Tags)
			return r, "", ""
		},
		func(api apigwv2types.Api) string { return sv(api.ApiId) })
	if p.addPhase(agv2GetApisOp, total, inserted, err) {
		return
	}
	apiIDs := make([]string, len(apis))
	for i, api := range apis {
		apiIDs[i] = api.id
	}
	total, inserted, err = opErrsResult(scanAPIGatewayV2APIChildren(ctx, client, acct, region, apiIDs, st, scanID))
	addOpErrsPhase(p, "apigatewayv2", total, inserted, err)
}

// scanAPIGatewayV2DomainNames stores the custom domain names with the tags
// each listed domain carries, then each stored domain's API mappings, then
// its routing rules. Counts and errors go to p, each error under the op that
// failed; a failed write stops it.
// Domain name NativeID: arn:{partition}:apigateway:{region}::/domainnames/{domainName}
// API mapping NativeID: arn:{partition}:apigateway:{region}::/domainnames/{domainName}/apimappings/{mappingId}
// Routing rule NativeID: the RoutingRuleArn the listing returns.
func scanAPIGatewayV2DomainNames(ctx context.Context, client apigatewayv2API, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) {
	total, inserted, domains, err := storeParentList(ctx, st, acct, region, agv2GetDomainNamesOp, "domain names", "DomainName",
		func(ctx context.Context, token *string) ([]apigwv2types.DomainName, *string, error) {
			out, err := client.GetDomainNames(ctx, &apigatewayv2.GetDomainNamesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(d apigwv2types.DomainName) (*store.Resource, string, string) {
			name := sv(d.DomainName)
			if name == "" {
				return nil, "domain names have no DomainName", cmp.Or(sv(d.DomainNameArn), "(no ARN)")
			}
			r := agv2Row(acct, region, scanID, TypeAPIGatewayDomainNameV2, apigatewayARNIn(acct.partition(), region, "domainnames", name), name, d)
			r.TagsJSON = mapTagsJSON(d.Tags)
			return r, "", ""
		},
		func(d apigwv2types.DomainName) string { return sv(d.DomainName) })
	if p.addPhase(agv2GetDomainNamesOp, total, inserted, err) {
		return
	}
	names := make([]string, len(domains))
	for i, d := range domains {
		names[i] = d.id
	}
	total, inserted, err = storeTokenEachParent(ctx, st, acct, region, agv2GetAPIMappingsOp, apigatewayv2NotFound, names,
		func(ctx context.Context, domain string, token *string) ([]apigwv2types.ApiMapping, *string, error) {
			out, err := client.GetApiMappings(ctx, &apigatewayv2.GetApiMappingsInput{DomainName: &domain, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(domain string, m apigwv2types.ApiMapping) (*store.Resource, string, string) {
			id := sv(m.ApiMappingId)
			if id == "" {
				return nil, "API mappings have no ApiMappingId", "domain " + domain
			}
			name := fmt.Sprintf("%s → %s", domain, sv(m.ApiMappingKey))
			return agv2Row(acct, region, scanID, TypeAPIGatewayBasePathMappingV2, apigatewayARNIn(acct.partition(), region, "domainnames", domain, "apimappings", id), name, m), "", ""
		})
	if p.addPhase(agv2GetAPIMappingsOp, total, inserted, err) {
		return
	}
	total, inserted, err = storeTokenEachParent(ctx, st, acct, region, agv2ListRoutingRulesOp, apigatewayv2NotFound, names,
		func(ctx context.Context, domain string, token *string) ([]apigwv2types.RoutingRule, *string, error) {
			out, err := client.ListRoutingRules(ctx, &apigatewayv2.ListRoutingRulesInput{
				DomainName: &domain, MaxResults: sdkaws.Int32(agv2RoutingRulePageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RoutingRules, out.NextToken, nil
		},
		func(domain string, rr apigwv2types.RoutingRule) (*store.Resource, string, string) {
			arn := sv(rr.RoutingRuleArn)
			if arn == "" {
				return nil, "routing rules have no RoutingRuleArn", "domain " + domain
			}
			return agv2Row(acct, region, scanID, TypeAPIGatewayV2RoutingRule, arn, arn, rr), "", ""
		})
	p.addPhase(agv2ListRoutingRulesOp, total, inserted, err)
}
