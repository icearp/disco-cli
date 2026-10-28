package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/icearp/disco-cli/store"
)

// agv2Page is one response of a listing: its items, the NextToken it
// returns, or the error returned in its place.
type agv2Page struct {
	items []any
	next  *string
	err   error
}

// agv2World answers scanAPIGatewayV2's calls from pages keyed by op name and
// parent key ("" for a region-wide listing, the API id, "apiID/childID" or the
// domain name). A request's NextToken is the index of the page it asks for;
// a parent with no pages lists nothing. block, when set for an op, runs before
// it answers, with the call's ctx.
type agv2World struct {
	mu    sync.Mutex
	pages map[string]map[string][]agv2Page
	block map[string]func(ctx context.Context) error
	calls map[string]int
}

func (w *agv2World) set(op, parent string, pages ...agv2Page) {
	if w.pages[op] == nil {
		w.pages[op] = map[string][]agv2Page{}
	}
	w.pages[op][parent] = pages
}

// agv2Answer is the handler of one op: in names the parent and token, out
// wraps a page's items in the op's output.
func agv2Answer[In, Item, Out any](w *agv2World, op string, in func(*In) (string, *string), out func([]Item, *string) *Out) func(context.Context, any) (any, error) {
	return func(ctx context.Context, raw any) (any, error) {
		if b := w.block[op]; b != nil {
			if err := b(ctx); err != nil {
				return nil, err
			}
		}
		parent, token := in(raw.(*In))
		w.mu.Lock()
		w.calls[op]++
		pages := w.pages[op][parent]
		w.mu.Unlock()
		idx := 0
		if token != nil {
			var err error
			if idx, err = strconv.Atoi(*token); err != nil {
				return nil, err
			}
		}
		if idx >= len(pages) {
			return out(nil, nil), nil
		}
		p := pages[idx]
		if p.err != nil {
			return nil, p.err
		}
		items := make([]Item, len(p.items))
		for i, it := range p.items {
			items[i] = it.(Item)
		}
		return out(items, p.next), nil
	}
}

func (w *agv2World) config() sdkaws.Config {
	return opStubConfigCtx(map[string]func(context.Context, any) (any, error){
		"GetApis": agv2Answer(w, "GetApis",
			func(in *apigatewayv2.GetApisInput) (string, *string) { return "", in.NextToken },
			func(items []apigwv2types.Api, next *string) *apigatewayv2.GetApisOutput {
				return &apigatewayv2.GetApisOutput{Items: items, NextToken: next}
			}),
		"GetAuthorizers": agv2Answer(w, "GetAuthorizers",
			func(in *apigatewayv2.GetAuthorizersInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Authorizer, next *string) *apigatewayv2.GetAuthorizersOutput {
				return &apigatewayv2.GetAuthorizersOutput{Items: items, NextToken: next}
			}),
		"GetDeployments": agv2Answer(w, "GetDeployments",
			func(in *apigatewayv2.GetDeploymentsInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Deployment, next *string) *apigatewayv2.GetDeploymentsOutput {
				return &apigatewayv2.GetDeploymentsOutput{Items: items, NextToken: next}
			}),
		"GetIntegrations": agv2Answer(w, "GetIntegrations",
			func(in *apigatewayv2.GetIntegrationsInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Integration, next *string) *apigatewayv2.GetIntegrationsOutput {
				return &apigatewayv2.GetIntegrationsOutput{Items: items, NextToken: next}
			}),
		"GetIntegrationResponses": agv2Answer(w, "GetIntegrationResponses",
			func(in *apigatewayv2.GetIntegrationResponsesInput) (string, *string) {
				return sv(in.ApiId) + "/" + sv(in.IntegrationId), in.NextToken
			},
			func(items []apigwv2types.IntegrationResponse, next *string) *apigatewayv2.GetIntegrationResponsesOutput {
				return &apigatewayv2.GetIntegrationResponsesOutput{Items: items, NextToken: next}
			}),
		"GetModels": agv2Answer(w, "GetModels",
			func(in *apigatewayv2.GetModelsInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Model, next *string) *apigatewayv2.GetModelsOutput {
				return &apigatewayv2.GetModelsOutput{Items: items, NextToken: next}
			}),
		"GetRoutes": agv2Answer(w, "GetRoutes",
			func(in *apigatewayv2.GetRoutesInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Route, next *string) *apigatewayv2.GetRoutesOutput {
				return &apigatewayv2.GetRoutesOutput{Items: items, NextToken: next}
			}),
		"GetRouteResponses": agv2Answer(w, "GetRouteResponses",
			func(in *apigatewayv2.GetRouteResponsesInput) (string, *string) {
				return sv(in.ApiId) + "/" + sv(in.RouteId), in.NextToken
			},
			func(items []apigwv2types.RouteResponse, next *string) *apigatewayv2.GetRouteResponsesOutput {
				return &apigatewayv2.GetRouteResponsesOutput{Items: items, NextToken: next}
			}),
		"GetStages": agv2Answer(w, "GetStages",
			func(in *apigatewayv2.GetStagesInput) (string, *string) { return sv(in.ApiId), in.NextToken },
			func(items []apigwv2types.Stage, next *string) *apigatewayv2.GetStagesOutput {
				return &apigatewayv2.GetStagesOutput{Items: items, NextToken: next}
			}),
		"GetDomainNames": agv2Answer(w, "GetDomainNames",
			func(in *apigatewayv2.GetDomainNamesInput) (string, *string) { return "", in.NextToken },
			func(items []apigwv2types.DomainName, next *string) *apigatewayv2.GetDomainNamesOutput {
				return &apigatewayv2.GetDomainNamesOutput{Items: items, NextToken: next}
			}),
		"GetApiMappings": agv2Answer(w, "GetApiMappings",
			func(in *apigatewayv2.GetApiMappingsInput) (string, *string) { return sv(in.DomainName), in.NextToken },
			func(items []apigwv2types.ApiMapping, next *string) *apigatewayv2.GetApiMappingsOutput {
				return &apigatewayv2.GetApiMappingsOutput{Items: items, NextToken: next}
			}),
		"ListRoutingRules": agv2Answer(w, "ListRoutingRules",
			func(in *apigatewayv2.ListRoutingRulesInput) (string, *string) {
				if sdkaws.ToInt32(in.MaxResults) != 100 {
					return "MaxResults not 100", in.NextToken
				}
				return sv(in.DomainName), in.NextToken
			},
			func(items []apigwv2types.RoutingRule, next *string) *apigatewayv2.ListRoutingRulesOutput {
				return &apigatewayv2.ListRoutingRulesOutput{RoutingRules: items, NextToken: next}
			}),
		"GetVpcLinks": agv2Answer(w, "GetVpcLinks",
			func(in *apigatewayv2.GetVpcLinksInput) (string, *string) { return "", in.NextToken },
			func(items []apigwv2types.VpcLink, next *string) *apigatewayv2.GetVpcLinksOutput {
				return &apigatewayv2.GetVpcLinksOutput{Items: items, NextToken: next}
			}),
		"ListPortals": agv2Answer(w, "ListPortals",
			func(in *apigatewayv2.ListPortalsInput) (string, *string) { return "", in.NextToken },
			func(items []apigwv2types.PortalSummary, next *string) *apigatewayv2.ListPortalsOutput {
				return &apigatewayv2.ListPortalsOutput{Items: items, NextToken: next}
			}),
		"ListPortalProducts": agv2Answer(w, "ListPortalProducts",
			func(in *apigatewayv2.ListPortalProductsInput) (string, *string) { return "", in.NextToken },
			func(items []apigwv2types.PortalProductSummary, next *string) *apigatewayv2.ListPortalProductsOutput {
				return &apigatewayv2.ListPortalProductsOutput{Items: items, NextToken: next}
			}),
	}, errors.New("agv2World: op not answered"))
}

// agv2Listing is one listing of the scan: the op, the type it stores, the
// parent key of the fixture's pages, the element with a given key (empty for
// a keyless element), and the NativeID of the row stored for that key.
type agv2Listing struct {
	op, typ, parent string
	perParent       bool
	item            func(key string) any
	nativeID        func(partition, region, key string) string
	name            func(key string) string // the stored row's Name
}

func agv2ARN(partition, region, path string) string {
	return "arn:" + partition + ":apigateway:" + region + "::/" + path
}

// agv2Listings are every listing of scanAPIGatewayV2 outside the portals.
// The fixture (newAGV2World) gives each two pages of one element, keyed
// <prefix>1 and <prefix>2; children hang off API a1, integration a1/i1,
// route a1/r1 and domain d1. Where the Name falls back from a label to the
// id, only the first element carries the label, so both are stored.
func agv2Listings() []agv2Listing {
	s := sdkaws.String
	opt := func(k string) *string {
		if k == "" {
			return nil
		}
		return s(k)
	}
	// label is the label of the first element only.
	label := func(prefix, k string) *string {
		if !strings.HasSuffix(k, "1") {
			return nil
		}
		return s(prefix + k)
	}
	labelOr := func(prefix, k string) string { return cmp.Or(sv(label(prefix, k)), k) }
	self := func(k string) string { return k }
	return []agv2Listing{
		{
			"GetApis", TypeAPIGatewayV2API, "", false,
			func(k string) any { return apigwv2types.Api{ApiId: opt(k), Name: s("api " + k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/"+k) },
			func(k string) string { return "api " + k },
		},
		{
			"GetAuthorizers", TypeAPIGatewayV2Authorizer, "a1", true,
			func(k string) any { return apigwv2types.Authorizer{AuthorizerId: opt(k), Name: s("auth " + k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/authorizers/"+k) },
			func(k string) string { return "auth " + k },
		},
		{
			"GetDeployments", TypeAPIGatewayV2Deployment, "a1", true,
			func(k string) any { return apigwv2types.Deployment{DeploymentId: opt(k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/deployments/"+k) },
			self,
		},
		{
			"GetIntegrations", TypeAPIGatewayV2Integration, "a1", true,
			func(k string) any { return apigwv2types.Integration{IntegrationId: opt(k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/integrations/"+k) },
			self,
		},
		{
			"GetIntegrationResponses", TypeAPIGatewayV2IntegrationResponse, "a1/i1", true,
			func(k string) any {
				return apigwv2types.IntegrationResponse{IntegrationResponseId: opt(k), IntegrationResponseKey: label("irk ", k)}
			},
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/integrations/i1/integrationresponses/"+k) },
			func(k string) string { return labelOr("irk ", k) },
		},
		{
			"GetModels", TypeAPIGatewayV2Model, "a1", true,
			func(k string) any { return apigwv2types.Model{ModelId: opt(k), Name: label("model ", k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/models/"+k) },
			func(k string) string { return labelOr("model ", k) },
		},
		{
			"GetRoutes", TypeAPIGatewayV2Route, "a1", true,
			func(k string) any { return apigwv2types.Route{RouteId: opt(k), RouteKey: label("GET /", k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/routes/"+k) },
			func(k string) string { return labelOr("GET /", k) },
		},
		{
			"GetRouteResponses", TypeAPIGatewayV2RouteResponse, "a1/r1", true,
			func(k string) any {
				return apigwv2types.RouteResponse{RouteResponseId: opt(k), RouteResponseKey: label("rrk ", k)}
			},
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/routes/r1/routeresponses/"+k) },
			func(k string) string { return labelOr("rrk ", k) },
		},
		{
			"GetStages", TypeAPIGatewayV2Stage, "a1", true,
			func(k string) any { return apigwv2types.Stage{StageName: opt(k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "apis/a1/stages/"+k) },
			self,
		},
		{
			"GetDomainNames", TypeAPIGatewayDomainNameV2, "", false,
			func(k string) any { return apigwv2types.DomainName{DomainName: opt(k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "domainnames/"+k) },
			self,
		},
		{
			"GetApiMappings", TypeAPIGatewayBasePathMappingV2, "d1", true,
			func(k string) any { return apigwv2types.ApiMapping{ApiMappingId: opt(k), ApiMappingKey: s("map " + k)} },
			func(p, r, k string) string { return agv2ARN(p, r, "domainnames/d1/apimappings/"+k) },
			func(k string) string { return "d1 → map " + k },
		},
		{
			"ListRoutingRules", TypeAPIGatewayV2RoutingRule, "d1", true,
			func(k string) any {
				if k == "" {
					return apigwv2types.RoutingRule{}
				}
				return apigwv2types.RoutingRule{RoutingRuleArn: s(agv2ARN("aws", testRegion, "domainnames/d1/routingrules/"+k))}
			},
			func(_, _, k string) string { return agv2ARN("aws", testRegion, "domainnames/d1/routingrules/"+k) },
			func(k string) string { return agv2ARN("aws", testRegion, "domainnames/d1/routingrules/"+k) },
		},
		{
			"GetVpcLinks", TypeAPIGatewayV2VpcLink, "", false,
			func(k string) any {
				return apigwv2types.VpcLink{VpcLinkId: opt(k), Name: s("link " + k), VpcLinkStatus: apigwv2types.VpcLinkStatusAvailable, CreatedDate: &agv2Created}
			},
			func(p, r, k string) string { return agv2ARN(p, r, "vpclinks/"+k) },
			func(k string) string { return "link " + k },
		},
	}
}

// agv2Key is the key of a listing's element on page i (1 or 2): the parent's
// own key prefix letter for the top listings (a, d), else the op's.
func agv2Key(l agv2Listing, i int) string {
	prefix := map[string]string{
		"GetApis": "a", "GetIntegrations": "i", "GetRoutes": "r", "GetDomainNames": "d",
	}[l.op]
	if prefix == "" {
		prefix = l.op
	}
	return prefix + strconv.Itoa(i)
}

// newAGV2World is the fixture: two pages of one element for every listing.
func newAGV2World() *agv2World {
	w := &agv2World{pages: map[string]map[string][]agv2Page{}, block: map[string]func(context.Context) error{}, calls: map[string]int{}}
	for _, l := range agv2Listings() {
		w.set(l.op, l.parent,
			agv2Page{items: []any{l.item(agv2Key(l, 1))}, next: sdkaws.String("1")},
			agv2Page{items: []any{l.item(agv2Key(l, 2))}})
	}
	return w
}

func scanAGV2World(t *testing.T, st *store.Store, acct *account, region string, w *agv2World) (int, int, error) {
	t.Helper()
	acct.cfg = w.config()
	return scanAPIGatewayV2(context.Background(), acct, region, st, testScanID)
}

func TestScanAPIGatewayV2_StoresEveryListing(t *testing.T) {
	for _, partition := range []string{"aws", "aws-cn", "aws-us-gov"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			acct, region := newTestAccount(testAccountID), testRegion
			if partition != "aws" {
				acct = partitionAccount(partition)
				region = acct.Regions[0]
			}
			listings := agv2Listings()
			total, inserted, err := scanAGV2World(t, st, acct, region, newAGV2World())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := 2 * len(listings); total != want || inserted != want {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
			}
			for _, l := range listings {
				rows := rowsByNativeID(t, st, l.typ)
				if len(rows) != 2 {
					t.Errorf("%s: %d rows; want 2", l.op, len(rows))
				}
				for i := 1; i <= 2; i++ {
					key := agv2Key(l, i)
					id := l.nativeID(partition, region, key)
					r, ok := rows[id]
					if !ok {
						t.Errorf("%s: no row %s", l.op, id)
						continue
					}
					if got := sv(r.Name); got != l.name(key) {
						t.Errorf("%s %s: Name = %q; want %q", l.op, id, got, l.name(key))
					}
					assertAttrsOf(t, r.AttributesJSON, l.item(key))
					wantStatus, wantCreated := "", ""
					if l.op == "GetVpcLinks" {
						wantStatus, wantCreated = string(apigwv2types.VpcLinkStatusAvailable), *tp(&agv2Created)
					}
					if sv(r.Status) != wantStatus || sv(r.CreatedAt) != wantCreated {
						t.Errorf("%s %s: Status, CreatedAt = %q, %q; want %q, %q", l.op, id, sv(r.Status), sv(r.CreatedAt), wantStatus, wantCreated)
					}
				}
			}
		})
	}
}

// agv2Created is the CreatedDate of the fixture's VPC links.
var agv2Created = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// assertAttrsOf fails unless attrs decodes to what the element marshals to:
// the row's attributes are the listed element, verbatim.
func assertAttrsOf(t *testing.T, attrs string, element any) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal([]byte(attrs), &got); err != nil {
		t.Fatalf("attributes %q: %v", attrs, err)
	}
	if err := json.Unmarshal([]byte(mustJSON(element)), &want); err != nil {
		t.Fatalf("element %T: %v", element, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributes = %s; want %s", attrs, mustJSON(element))
	}
}

// The tags each listed API, domain name, VPC link and stage carries are
// stored; an element without tags stores none.
func TestScanAPIGatewayV2_StoresElementTags(t *testing.T) {
	tags := map[string]string{"env": "prod", "team": "api"}
	w := newAGV2World()
	w.set("GetApis", "", agv2Page{items: []any{apigwv2types.Api{ApiId: sdkaws.String("a1"), Tags: tags}, apigwv2types.Api{ApiId: sdkaws.String("a2")}}})
	w.set("GetDomainNames", "", agv2Page{items: []any{apigwv2types.DomainName{DomainName: sdkaws.String("d1"), Tags: tags}, apigwv2types.DomainName{DomainName: sdkaws.String("d2")}}})
	w.set("GetVpcLinks", "", agv2Page{items: []any{apigwv2types.VpcLink{VpcLinkId: sdkaws.String("v1"), Tags: tags}, apigwv2types.VpcLink{VpcLinkId: sdkaws.String("v2")}}})
	w.set("GetStages", "a1", agv2Page{items: []any{apigwv2types.Stage{StageName: sdkaws.String("prod"), Tags: tags}, apigwv2types.Stage{StageName: sdkaws.String("dev")}}})
	st := newTestStore(t)
	if _, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range []struct{ typ, tagged, untagged string }{
		{TypeAPIGatewayV2API, "apis/a1", "apis/a2"},
		{TypeAPIGatewayDomainNameV2, "domainnames/d1", "domainnames/d2"},
		{TypeAPIGatewayV2VpcLink, "vpclinks/v1", "vpclinks/v2"},
		{TypeAPIGatewayV2Stage, "apis/a1/stages/prod", "apis/a1/stages/dev"},
	} {
		rows := rowsByNativeID(t, st, c.typ)
		assertTagsMap(t, rows[agv2ARN("aws", testRegion, c.tagged)].TagsJSON, tags)
		if r, ok := rows[agv2ARN("aws", testRegion, c.untagged)]; !ok || r.TagsJSON != nil {
			t.Errorf("%s: stored=%v TagsJSON=%v; want stored without tags", c.untagged, ok, r.TagsJSON)
		}
	}

	// A rescan of the unchanged listings inserts none.
	if total, inserted, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w); err != nil || inserted != 0 || total == 0 {
		t.Errorf("rescan = %d, %d, %v; want >0, 0, nil", total, inserted, err)
	}
}

// Every listing, given a failure of its own, keeps what it read, warns or
// returns under its own op, and costs no other listing its rows.
func TestScanAPIGatewayV2_ListingFailures(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET")
	type outcome struct {
		rows    int              // rows of the failing listing
		errOK   func(error) bool // accepts the scan's error
		warning bool             // one warning under the listing's op
	}
	noErr := func(err error) bool { return err == nil }
	badRequest := func(err error) bool { return isAPIErrorCode(err, "BadRequestException") }
	repeated := func(err error) bool { return errors.Is(err, errRepeatedNextToken) }
	cases := []struct {
		name      string
		perParent bool // only for listings under a parent
		pages     func(l agv2Listing) []agv2Page
		want      outcome
	}{
		{"later page fails", false, func(l agv2Listing) []agv2Page {
			return []agv2Page{{items: []any{l.item(agv2Key(l, 1))}, next: sdkaws.String("1")}, {err: apiErr("BadRequestException", "bad")}}
		}, outcome{1, badRequest, false}},
		{"later page denied", false, func(l agv2Listing) []agv2Page {
			return []agv2Page{{items: []any{l.item(agv2Key(l, 1))}, next: sdkaws.String("1")}, {err: denied}}
		}, outcome{1, noErr, true}},
		{"repeated token", false, func(l agv2Listing) []agv2Page {
			return []agv2Page{{items: []any{l.item(agv2Key(l, 1))}, next: sdkaws.String("1")}, {items: []any{l.item(agv2Key(l, 2))}, next: sdkaws.String("1")}}
		}, outcome{2, repeated, false}},
		{"empty token ends", false, func(l agv2Listing) []agv2Page {
			return []agv2Page{{items: []any{l.item(agv2Key(l, 1))}, next: sdkaws.String("")}, {items: []any{l.item(agv2Key(l, 2))}}}
		}, outcome{1, noErr, false}},
		{"keyless element", false, func(l agv2Listing) []agv2Page {
			return []agv2Page{{items: []any{l.item(agv2Key(l, 1)), l.item("")}}}
		}, outcome{1, noErr, true}},
		{"parent deleted", true, func(agv2Listing) []agv2Page {
			return []agv2Page{{err: apiErr("NotFoundException", "Invalid identifier specified")}}
		}, outcome{0, noErr, false}},
	}
	for _, c := range cases {
		for _, l := range agv2Listings() {
			if c.perParent && !l.perParent {
				continue
			}
			t.Run(c.name+"/"+l.op, func(t *testing.T) {
				w := newAGV2World()
				w.set(l.op, l.parent, c.pages(l)...)
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				_, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
				if !c.want.errOK(err) {
					t.Fatalf("err = %v; want the %s outcome", err, c.name)
				}
				if n := len(rowsByNativeID(t, st, l.typ)); n != c.want.rows {
					t.Errorf("%s rows = %d; want %d", l.op, n, c.want.rows)
				}
				for _, other := range agv2Listings() {
					if other.op == l.op || agv2DependsOn(other, l) {
						continue
					}
					if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
						t.Errorf("%s rows = %d; want 2 (untouched by %s)", other.op, n, l.op)
					}
				}
				var got []store.ScanWarning
				for _, wn := range warns() {
					if wn.Service == "apigatewayv2:"+l.op {
						got = append(got, wn)
					}
				}
				if want := map[bool]int{true: 1}[c.want.warning]; len(got) != want || len(warns()) != want {
					t.Errorf("warnings = %+v; want %d under apigatewayv2:%s and no other", warns(), want, l.op)
				}
			})
		}
	}
}

// agv2DependsOn reports whether child's fixture rows hang off a parent that
// parent's listing returns, so a failed parent listing may leave it short.
func agv2DependsOn(child, parent agv2Listing) bool {
	deps := map[string][]string{
		"GetApis":         {"GetAuthorizers", "GetDeployments", "GetIntegrations", "GetIntegrationResponses", "GetModels", "GetRoutes", "GetRouteResponses", "GetStages"},
		"GetIntegrations": {"GetIntegrationResponses"},
		"GetRoutes":       {"GetRouteResponses"},
		"GetDomainNames":  {"GetApiMappings", "ListRoutingRules"},
	}
	for _, d := range deps[parent.op] {
		if d == child.op {
			return true
		}
	}
	return false
}

// One integration's failed responses listing costs the other integrations
// none of theirs, and is returned under GetIntegrationResponses.
func TestScanAPIGatewayV2_OneParentFailureKeepsSiblingParents(t *testing.T) {
	w := newAGV2World()
	w.set("GetIntegrationResponses", "a1/i1", agv2Page{err: apiErr("BadRequestException", "bad")})
	w.set("GetIntegrationResponses", "a1/i2", agv2Page{items: []any{apigwv2types.IntegrationResponse{IntegrationResponseId: sdkaws.String("x")}}})
	st := newTestStore(t)
	_, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
	if !isAPIErrorCode(err, "BadRequestException") {
		t.Fatalf("err = %v; want the BadRequestException", err)
	}
	rows := rowsByNativeID(t, st, TypeAPIGatewayV2IntegrationResponse)
	if _, ok := rows[agv2ARN("aws", testRegion, "apis/a1/integrations/i2/integrationresponses/x")]; !ok || len(rows) != 1 {
		t.Errorf("integration responses = %v; want only i2's x", rows)
	}
	// A transient of the same op beside it stays in that op's error, which
	// holds a hard member, so it is not warned about on its own.
	w.set("GetIntegrationResponses", "a1/i2", agv2Page{err: apiErr("TooManyRequestsException", "slow down")})
	st = newTestStore(t)
	warns := rhWarnRecorder(st)
	if _, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w); !isAPIErrorCode(err, "BadRequestException") {
		t.Fatalf("err = %v; want the BadRequestException", err)
	}
	if got := warns(); len(got) != 0 {
		t.Errorf("warnings = %+v; want none: the throttle is joined with the hard error of its own op", got)
	}
}

// A transient error beside another listing's hard error is demoted to one
// warning under its own op, which shows the op each error is filed under;
// the hard error alone is returned.
func TestScanAPIGatewayV2_TransientBesideHardErrorWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct{ op, parent string }{
		{"GetStages", "a1"},
		{"GetIntegrationResponses", "a1/i1"},
		{"GetRouteResponses", "a1/r1"},
		{"ListRoutingRules", "d1"},
	} {
		t.Run(c.op, func(t *testing.T) {
			throttle := apiErr("TooManyRequestsException", "slow down")
			w := newAGV2World()
			w.set(c.op, c.parent, agv2Page{err: throttle})
			w.set("GetDeployments", "a1", agv2Page{err: apiErr("BadRequestException", "bad")})
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			_, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
			if !isAPIErrorCode(err, "BadRequestException") || isAPIErrorCode(err, "TooManyRequestsException") {
				t.Fatalf("err = %v; want the BadRequestException without the throttle", err)
			}
			got := warns()
			if len(got) != 1 || got[0].Service != "apigatewayv2:"+c.op {
				t.Errorf("warnings = %+v; want one under apigatewayv2:%s", got, c.op)
			}
		})
	}
}

// An error in one family stores every other family's rows, the developer
// portals' included, and counts them.
func TestScanAPIGatewayV2_FamilyErrorKeepsOtherFamilies(t *testing.T) {
	w := newAGV2World()
	w.set("GetStages", "a1", agv2Page{err: apiErr("BadRequestException", "bad")})
	w.set("GetVpcLinks", "", agv2Page{err: apiErr("ConflictException", "boom")})
	w.set("ListPortals", "", agv2Page{items: []any{testPortal("p")}})
	st := newTestStore(t)
	total, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
	if !hasMemberCode(err, "BadRequestException") || !hasMemberCode(err, "ConflictException") {
		t.Fatalf("err = %v; want both errors", err)
	}
	// Every listing's two rows but the stages' and VPC links', plus the portal.
	if want := 2*(len(agv2Listings())-2) + 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	for _, typ := range []string{TypeAPIGatewayDomainNameV2, TypeAPIGatewayV2RoutingRule, TypeAPIGatewayV2Route, TypeAPIGatewayV2Authorizer} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s rows = %d; want 2", typ, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeAPIGatewayV2Portal)); n != 1 {
		t.Errorf("portal rows = %d; want 1", n)
	}
}

// An NXDOMAIN after rows were stored from the endpoint is no region gap; one
// on every first call, with nothing stored, stays the region-unavailable
// signal.
func TestScanAPIGatewayV2_NXDOMAIN(t *testing.T) {
	dns := &net.DNSError{Err: "no such host", Name: "apigateway.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("late", func(t *testing.T) {
		w := newAGV2World()
		w.set("GetStages", "a1", agv2Page{err: dns})
		st := newTestStore(t)
		total, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
		if err == nil || isDNSNotFound(err) {
			t.Fatalf("err = %v; want an error not read as NXDOMAIN", err)
		}
		if want := 2 * (len(agv2Listings()) - 1); total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("first calls", func(t *testing.T) {
		w := &agv2World{pages: map[string]map[string][]agv2Page{}, block: map[string]func(context.Context) error{}, calls: map[string]int{}}
		for _, op := range []string{"GetApis", "GetDomainNames", "GetVpcLinks", "ListPortals"} {
			w.set(op, "", agv2Page{err: dns})
		}
		st := newTestStore(t)
		total, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
		if !isDNSNotFound(err) || total != 0 {
			t.Fatalf("scan = %d, %v; want 0 and an NXDOMAIN", total, err)
		}
	})
}

// hasMemberCode reports whether any member of err's tree carries the API
// error code; isAPIErrorCode sees only the first.
func hasMemberCode(err error, code string) bool {
	if m, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range m.Unwrap() {
			if hasMemberCode(e, code) {
				return true
			}
		}
		return false
	}
	return isAPIErrorCode(err, code)
}

// countStops counts the members of err's tree that are ctx's stop.
func countStops(err error) int {
	if err == nil {
		return 0
	}
	if m, ok := err.(interface{ Unwrap() []error }); ok {
		n := 0
		for _, e := range m.Unwrap() {
			n += countStops(e)
		}
		return n
	}
	if errors.Is(err, context.Canceled) {
		return 1
	}
	return 0
}

// A ctx end seen by several families at once is one error, not one per family.
func TestScanAPIGatewayV2_CtxEndIsOneStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newAGV2World()
	waitForStop := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	w.block["GetVpcLinks"] = waitForStop
	w.block["GetDomainNames"] = waitForStop
	w.block["GetStages"] = func(context.Context) error {
		cancel()
		return context.Canceled
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = w.config()
	_, _, err := scanAPIGatewayV2(ctx, acct, testRegion, st, testScanID)
	if n := countStops(err); n != 1 {
		t.Errorf("err = %v holds %d stops; want 1", err, n)
	}
}

// A failed write returns the write error and sends no warning: the rows the
// warnings vouch for were not stored.
func TestScanAPIGatewayV2_FailedWriteSendsNoWarning(t *testing.T) {
	w := newAGV2World()
	w.set("GetStages", "a1", agv2Page{items: []any{apigwv2types.Stage{StageName: sdkaws.String("prod")}, apigwv2types.Stage{}}, next: sdkaws.String("1")},
		agv2Page{err: apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET")})
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeAPIGatewayV2Stage)
	_, _, err := scanAGV2World(t, st, newTestAccount(testAccountID), testRegion, w)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	for _, wn := range warns() {
		if wn.Service == "apigatewayv2:GetStages" {
			t.Errorf("warning %+v sent after the failed write", wn)
		}
	}
}

func TestAGV2ChildKey_RoundTrips(t *testing.T) {
	for _, c := range [][2]string{{"a1b2c3", "i9"}, {"x", ""}} {
		if api, child := agv2SplitKey(agv2ChildKey(c[0], c[1])); api != c[0] || child != c[1] {
			t.Errorf("agv2SplitKey(agv2ChildKey(%q, %q)) = %q, %q", c[0], c[1], api, child)
		}
	}
}
