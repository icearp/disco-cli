package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

// stubPortalAPI pages each list with stubPage. The page lists are keyed by
// portal product id and called from childFanOut goroutines, so calls are
// recorded under mu.
type stubPortalAPI struct {
	portalPages  [][]apigwv2types.PortalSummary
	portalErr    error
	productPages [][]apigwv2types.PortalProductSummary
	productErr   error
	pagePages    map[string][][]apigwv2types.ProductPageSummaryNoBody
	restPages    map[string][][]apigwv2types.ProductRestEndpointPageSummaryNoBody
	childErr     map[string]error

	mu        sync.Mutex
	pageCalls []string
	restCalls []string
}

func (s *stubPortalAPI) ListPortals(_ context.Context, in *apigatewayv2.ListPortalsInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.ListPortalsOutput, error) {
	items, next, err := stubPage(s.portalPages, in.NextToken, s.portalErr)
	if err != nil {
		return nil, err
	}
	return &apigatewayv2.ListPortalsOutput{Items: items, NextToken: next}, nil
}

func (s *stubPortalAPI) ListPortalProducts(_ context.Context, in *apigatewayv2.ListPortalProductsInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.ListPortalProductsOutput, error) {
	items, next, err := stubPage(s.productPages, in.NextToken, s.productErr)
	if err != nil {
		return nil, err
	}
	return &apigatewayv2.ListPortalProductsOutput{Items: items, NextToken: next}, nil
}

func (s *stubPortalAPI) ListProductPages(_ context.Context, in *apigatewayv2.ListProductPagesInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.ListProductPagesOutput, error) {
	id := sv(in.PortalProductId)
	s.mu.Lock()
	s.pageCalls = append(s.pageCalls, id)
	s.mu.Unlock()
	items, next, err := stubPage(s.pagePages[id], in.NextToken, s.childErr[id])
	if err != nil {
		return nil, err
	}
	return &apigatewayv2.ListProductPagesOutput{Items: items, NextToken: next}, nil
}

func (s *stubPortalAPI) ListProductRestEndpointPages(_ context.Context, in *apigatewayv2.ListProductRestEndpointPagesInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.ListProductRestEndpointPagesOutput, error) {
	id := sv(in.PortalProductId)
	s.mu.Lock()
	s.restCalls = append(s.restCalls, id)
	s.mu.Unlock()
	items, next, err := stubPage(s.restPages[id], in.NextToken, s.childErr[id])
	if err != nil {
		return nil, err
	}
	return &apigatewayv2.ListProductRestEndpointPagesOutput{Items: items, NextToken: next}, nil
}

func portalTestARN(account string, path ...string) string {
	arn := "arn:aws:apigateway:" + testRegion + ":" + account + ":"
	for _, p := range path {
		arn += "/" + p
	}
	return arn
}

func testPortal(id string) apigwv2types.PortalSummary {
	arn := portalTestARN(testAccountID, "portals", id)
	if id == "" {
		arn = ""
	}
	return apigwv2types.PortalSummary{
		PortalArn: &arn, PortalId: &id, PublishStatus: apigwv2types.PublishStatusPublished,
		PortalContent: &apigwv2types.PortalContent{DisplayName: sdkaws.String("portal " + id)},
		Tags:          map[string]string{"env": "prod"},
	}
}

func testPortalProduct(account, id string) apigwv2types.PortalProductSummary {
	return apigwv2types.PortalProductSummary{
		PortalProductArn: sdkaws.String(portalTestARN(account, "portalproducts", id)),
		PortalProductId:  &id, DisplayName: sdkaws.String("product " + id),
	}
}

func productPageARN(productID, pageID string) string {
	return portalTestARN(testAccountID, "portalproducts", productID, "productpages", pageID)
}

func testProductPage(productID, pageID string) apigwv2types.ProductPageSummaryNoBody {
	arn := productPageARN(productID, pageID)
	if pageID == "" {
		arn = ""
	}
	return apigwv2types.ProductPageSummaryNoBody{
		ProductPageArn: &arn, ProductPageId: &pageID, PageTitle: sdkaws.String("title " + pageID),
	}
}

func restPageARN(productID, pageID string) string {
	return portalTestARN(testAccountID, "portalproducts", productID, "productrestendpointpages", pageID)
}

func testRestPage(productID, pageID string) apigwv2types.ProductRestEndpointPageSummaryNoBody {
	arn := restPageARN(productID, pageID)
	if pageID == "" {
		arn = ""
	}
	return apigwv2types.ProductRestEndpointPageSummaryNoBody{
		ProductRestEndpointPageArn: &arn, ProductRestEndpointPageId: &pageID,
		Endpoint: sdkaws.String("GET /pets/" + pageID), Status: apigwv2types.StatusAvailable,
	}
}

// portalRow is the stored row expected for one listed element.
type portalRow struct {
	nativeID, rtype, name, status, attrKey, attrVal string
}

func assertPortalRow(t *testing.T, st *store.Store, w portalRow) *store.Resource {
	t.Helper()
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, w.nativeID))
	if err != nil {
		t.Fatalf("row %s missing: %v", w.nativeID, err)
	}
	if r.Type != w.rtype {
		t.Errorf("%s Type = %q; want %q", w.nativeID, r.Type, w.rtype)
	}
	if sv(r.Region) != testRegion {
		t.Errorf("%s Region = %q; want %q", w.nativeID, sv(r.Region), testRegion)
	}
	if sv(r.Name) != w.name {
		t.Errorf("%s Name = %q; want %q", w.nativeID, sv(r.Name), w.name)
	}
	if sv(r.Status) != w.status {
		t.Errorf("%s Status = %q; want %q", w.nativeID, sv(r.Status), w.status)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs: %v", w.nativeID, err)
	}
	if got, _ := attrs[w.attrKey].(string); got != w.attrVal {
		t.Errorf("%s attrs[%s] = %q; want %q", w.nativeID, w.attrKey, got, w.attrVal)
	}
	return r
}

func countPortalRows(t *testing.T, st *store.Store, rtype string) int {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", rtype, err)
	}
	return len(rows)
}

func TestScanAPIGatewayV2PortalList_Scenarios(t *testing.T) {
	cases := []struct {
		name      string
		stub      *stubPortalAPI
		wantRows  int
		wantWarns int
		wantErr   bool
	}{
		{name: "two pages both stored, empty arn skipped", stub: &stubPortalAPI{
			portalPages: [][]apigwv2types.PortalSummary{{testPortal("a"), testPortal("")}, {testPortal("b")}},
		}, wantRows: 2},
		{name: "empty", stub: &stubPortalAPI{}, wantRows: 0},
		{name: "access denied warns", stub: &stubPortalAPI{
			portalErr: apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET"),
		}, wantWarns: 1},
		{name: "bad request warns", stub: &stubPortalAPI{
			portalErr: apiErr("BadRequestException", "portals unavailable"),
		}, wantWarns: 1},
		{name: "region gap on page two is silent and keeps page one", stub: &stubPortalAPI{
			portalPages: [][]apigwv2types.PortalSummary{{testPortal("a")}},
			portalErr:   apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"),
		}, wantRows: 1},
		{name: "denied on page two keeps page one", stub: &stubPortalAPI{
			portalPages: [][]apigwv2types.PortalSummary{{testPortal("a")}},
			portalErr:   apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET"),
		}, wantRows: 1, wantWarns: 1},
		{name: "other error propagates", stub: &stubPortalAPI{
			portalErr: apiErr("ConflictException", "boom"),
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			total, _, err := scanAPIGatewayV2PortalList(context.Background(), tc.stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr {
				if !isAPIErrorCode(err, "ConflictException") {
					t.Fatalf("err = %v; want ConflictException", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if total != tc.wantRows || countPortalRows(t, st, TypeAPIGatewayV2Portal) != tc.wantRows {
				t.Errorf("total = %d; want %d stored", total, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanAPIGatewayV2PortalList_StoredRow(t *testing.T) {
	st := newTestStore(t)
	stub := &stubPortalAPI{portalPages: [][]apigwv2types.PortalSummary{{testPortal("a")}}}
	if _, _, err := scanAPIGatewayV2PortalList(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatal(err)
	}
	r := assertPortalRow(t, st, portalRow{
		nativeID: portalTestARN(testAccountID, "portals", "a"), rtype: TypeAPIGatewayV2Portal,
		name: "portal a", status: "PUBLISHED", attrKey: "PortalId", attrVal: "a",
	})
	if sv(r.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q; want {\"env\":\"prod\"}", sv(r.TagsJSON))
	}
}

// repeatTokenPortalAPI returns the token it was sent, as a broken service
// would; the loop must stop rather than spin.
type repeatTokenPortalAPI struct {
	apigatewayv2PortalAPI
	calls int
}

func (s *repeatTokenPortalAPI) ListPortals(_ context.Context, _ *apigatewayv2.ListPortalsInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.ListPortalsOutput, error) {
	s.calls++
	if s.calls > 3 {
		return nil, apiErr("ConflictException", "loop did not stop on a repeated token")
	}
	return &apigatewayv2.ListPortalsOutput{Items: []apigwv2types.PortalSummary{testPortal("a")}, NextToken: sdkaws.String("same")}, nil
}

func TestScanAPIGatewayV2PortalList_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	stub := &repeatTokenPortalAPI{}
	if _, _, err := scanAPIGatewayV2PortalList(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 2 {
		t.Errorf("ListPortals calls = %d; want 2 (stop when the token repeats)", stub.calls)
	}
}

func TestScanAPIGatewayV2PortalProducts_Scenarios(t *testing.T) {
	const otherAccount = "210987654321"
	cases := []struct {
		name         string
		stub         *stubPortalAPI
		wantProducts []string
		wantWarns    int
		wantErr      string
	}{
		{name: "two pages, empty id and shared product skipped", stub: &stubPortalAPI{
			productPages: [][]apigwv2types.PortalProductSummary{
				{testPortalProduct(testAccountID, "p1"), testPortalProduct(testAccountID, "")},
				{testPortalProduct(otherAccount, "shared"), testPortalProduct(testAccountID, "p2")},
			},
		}, wantProducts: []string{"p1", "p2"}},
		{name: "empty", stub: &stubPortalAPI{}},
		{name: "access denied warns", stub: &stubPortalAPI{
			productErr: apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET"),
		}, wantWarns: 1},
		{name: "bad request warns and keeps rows", stub: &stubPortalAPI{
			productPages: [][]apigwv2types.PortalProductSummary{{testPortalProduct(testAccountID, "p1")}},
			productErr:   apiErr("BadRequestException", "portals unavailable"),
		}, wantProducts: []string{"p1"}, wantWarns: 1},
		{name: "other error propagates", stub: &stubPortalAPI{
			productErr: apiErr("ConflictException", "boom"),
		}, wantErr: "ConflictException"},
		{name: "region gap is silent", stub: &stubPortalAPI{
			productErr: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"),
		}},
		{name: "arn without account fails", stub: &stubPortalAPI{
			productPages: [][]apigwv2types.PortalProductSummary{{testPortalProduct("", "p1")}},
		}, wantErr: "parse"},
		{name: "unparseable arn fails", stub: &stubPortalAPI{
			productPages: [][]apigwv2types.PortalProductSummary{{{
				PortalProductArn: sdkaws.String("not-an-arn"), PortalProductId: sdkaws.String("p1"),
			}}},
		}, wantErr: "parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			total, _, products, err := scanAPIGatewayV2PortalProducts(context.Background(), tc.stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			switch tc.wantErr {
			case "":
				if err != nil {
					t.Fatalf("err = %v", err)
				}
			case "parse":
				if err == nil || countPortalRows(t, st, TypeAPIGatewayV2PortalProduct) != 0 {
					t.Fatalf("err = %v; want an unusable-ARN error and no stored product", err)
				}
				return
			default:
				if !isAPIErrorCode(err, tc.wantErr) {
					t.Fatalf("err = %v; want %s", err, tc.wantErr)
				}
				return
			}
			var ids []string
			for _, p := range products {
				ids = append(ids, p.id)
				if p.arn != portalTestARN(testAccountID, "portalproducts", p.id) {
					t.Errorf("parent %s arn = %q", p.id, p.arn)
				}
			}
			if !slices.Equal(ids, tc.wantProducts) {
				t.Errorf("parents = %v; want %v", ids, tc.wantProducts)
			}
			if total != len(tc.wantProducts) || countPortalRows(t, st, TypeAPIGatewayV2PortalProduct) != len(tc.wantProducts) {
				t.Errorf("total = %d; want %d stored", total, len(tc.wantProducts))
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanAPIGatewayV2PortalProducts_StoredRow(t *testing.T) {
	st := newTestStore(t)
	stub := &stubPortalAPI{productPages: [][]apigwv2types.PortalProductSummary{{testPortalProduct(testAccountID, "p1")}}}
	if _, _, _, err := scanAPIGatewayV2PortalProducts(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatal(err)
	}
	assertPortalRow(t, st, portalRow{
		nativeID: portalTestARN(testAccountID, "portalproducts", "p1"), rtype: TypeAPIGatewayV2PortalProduct,
		name: "product p1", attrKey: "PortalProductId", attrVal: "p1",
	})
}

// seedPortalProducts stores the parent rows the page phases link under.
func seedPortalProducts(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	var parents []childParent
	for _, id := range ids {
		arn := portalTestARN(testAccountID, "portalproducts", id)
		upsertTestResource(t, st, "aws", testAccountID, TypeAPIGatewayV2PortalProduct, arn, testRegion, "{}")
		parents = append(parents, childParent{id: id, arn: arn})
	}
	return parents
}

// portalChildPhase runs one per-product page phase against the stub.
type portalChildPhase struct {
	name, rtype string
	scan        func(context.Context, apigatewayv2PortalAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
	calls       func(*stubPortalAPI) []string
	set         func(s *stubPortalAPI, productID string, pageIDs ...[]string)
	arn         func(productID, pageID string) string
	row         func(productID, pageID string) portalRow
}

var portalChildPhases = []portalChildPhase{
	{
		name: "product pages", rtype: TypeAPIGatewayV2ProductPage,
		scan:  scanAPIGatewayV2ProductPages,
		calls: func(s *stubPortalAPI) []string { return s.pageCalls },
		set: func(s *stubPortalAPI, productID string, pages ...[]string) {
			if s.pagePages == nil {
				s.pagePages = map[string][][]apigwv2types.ProductPageSummaryNoBody{}
			}
			for _, ids := range pages {
				var page []apigwv2types.ProductPageSummaryNoBody
				for _, id := range ids {
					page = append(page, testProductPage(productID, id))
				}
				s.pagePages[productID] = append(s.pagePages[productID], page)
			}
		},
		arn: productPageARN,
		row: func(productID, pageID string) portalRow {
			return portalRow{
				nativeID: productPageARN(productID, pageID), rtype: TypeAPIGatewayV2ProductPage,
				name: "title " + pageID, attrKey: "ProductPageId", attrVal: pageID,
			}
		},
	},
	{
		name: "rest endpoint pages", rtype: TypeAPIGatewayV2ProductRestEndpointPage,
		scan:  scanAPIGatewayV2ProductRestEndpointPages,
		calls: func(s *stubPortalAPI) []string { return s.restCalls },
		set: func(s *stubPortalAPI, productID string, pages ...[]string) {
			if s.restPages == nil {
				s.restPages = map[string][][]apigwv2types.ProductRestEndpointPageSummaryNoBody{}
			}
			for _, ids := range pages {
				var page []apigwv2types.ProductRestEndpointPageSummaryNoBody
				for _, id := range ids {
					page = append(page, testRestPage(productID, id))
				}
				s.restPages[productID] = append(s.restPages[productID], page)
			}
		},
		arn: restPageARN,
		row: func(productID, pageID string) portalRow {
			return portalRow{
				nativeID: restPageARN(productID, pageID), rtype: TypeAPIGatewayV2ProductRestEndpointPage,
				name: "GET /pets/" + pageID, status: "AVAILABLE", attrKey: "ProductRestEndpointPageId", attrVal: pageID,
			}
		},
	},
}

func TestScanAPIGatewayV2PortalChildren_FanOut(t *testing.T) {
	for _, ph := range portalChildPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			parents := seedPortalProducts(t, st, "p1", "p2", "gone")
			stub := &stubPortalAPI{childErr: map[string]error{"gone": apiErr("NotFoundException", "Invalid portal product identifier")}}
			ph.set(stub, "p1", []string{"x", ""}, []string{"y"})
			ph.set(stub, "p2", []string{"z"})

			total, _, err := ph.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			calls := slices.Compact(slices.Sorted(slices.Values(ph.calls(stub))))
			if !slices.Equal(calls, []string{"gone", "p1", "p2"}) {
				t.Errorf("products queried = %v; want every parent", calls)
			}
			if total != 3 || countPortalRows(t, st, ph.rtype) != 3 {
				t.Errorf("total = %d; want 3 stored", total)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0 (a deleted product skips silently)", *warns)
			}
			for _, c := range [][2]string{{"p1", "x"}, {"p1", "y"}, {"p2", "z"}} {
				assertPortalRow(t, st, ph.row(c[0], c[1]))
				parentID := store.ResourceID("aws", testAccountID, portalTestARN(testAccountID, "portalproducts", c[0]))
				rels, err := st.RelationshipsFrom(parentID, store.RelContains)
				if err != nil {
					t.Fatal(err)
				}
				assertRelationship(t, rels, parentID, store.ResourceID("aws", testAccountID, ph.arn(c[0], c[1])), store.RelContains)
			}
		})
	}
}

func TestScanAPIGatewayV2PortalChildren_Errors(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: x is not authorized to perform: apigateway:GET")
	for _, ph := range portalChildPhases {
		t.Run(ph.name+"/no products", func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubPortalAPI{}
			total, _, err := ph.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
			if err != nil || total != 0 || len(ph.calls(stub)) != 0 {
				t.Errorf("total=%d err=%v calls=%v; want 0, nil, none", total, err, ph.calls(stub))
			}
		})
		t.Run(ph.name+"/denied warns once and keeps siblings", func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			parents := seedPortalProducts(t, st, "d1", "d2", "ok")
			stub := &stubPortalAPI{childErr: map[string]error{"d1": deny, "d2": deny}}
			ph.set(stub, "ok", []string{"x"})
			total, _, err := ph.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if total != 1 {
				t.Errorf("total = %d; want 1 (sibling of the denied products)", total)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d; want 1", *warns)
			}
		})
		t.Run(ph.name+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			parents := seedPortalProducts(t, st, "p1")
			stub := &stubPortalAPI{childErr: map[string]error{"p1": apiErr("BadRequestException", "boom")}}
			if _, _, err := ph.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents); !isAPIErrorCode(err, "BadRequestException") {
				t.Errorf("err = %v; want BadRequestException", err)
			}
		})
	}
}

func apigwv2CfgWithStub(stub func(*smithymw.Stack) error) sdkaws.Config {
	return sdkaws.Config{
		Region:           testRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
		APIOptions:       []func(*smithymw.Stack) error{stub},
	}
}

// apigwv2CoreResponses answers the pre-existing scanAPIGatewayV2 phases with
// one VPC link and no APIs or domains.
func apigwv2CoreResponses(vpcLinkErr error) map[string][]stubCall {
	link := apigwv2types.VpcLink{VpcLinkId: sdkaws.String("vl1"), Name: sdkaws.String("link")}
	vpcLinks := stubCall{Output: &apigatewayv2.GetVpcLinksOutput{Items: []apigwv2types.VpcLink{link}}}
	if vpcLinkErr != nil {
		vpcLinks = stubCall{Err: vpcLinkErr}
	}
	return map[string][]stubCall{
		"GetApis":        {{Output: &apigatewayv2.GetApisOutput{}}},
		"GetDomainNames": {{Output: &apigatewayv2.GetDomainNamesOutput{}}},
		"GetVpcLinks":    {vpcLinks},
	}
}

func TestScanAPIGatewayV2_StoresPortalRows(t *testing.T) {
	st := newTestStore(t)
	responses := apigwv2CoreResponses(nil)
	responses["ListPortals"] = []stubCall{{Output: &apigatewayv2.ListPortalsOutput{Items: []apigwv2types.PortalSummary{testPortal("a")}}}}
	responses["ListPortalProducts"] = []stubCall{{Output: &apigatewayv2.ListPortalProductsOutput{Items: []apigwv2types.PortalProductSummary{testPortalProduct(testAccountID, "p1")}}}}
	responses["ListProductPages"] = []stubCall{{Output: &apigatewayv2.ListProductPagesOutput{Items: []apigwv2types.ProductPageSummaryNoBody{testProductPage("p1", "x")}}}}
	responses["ListProductRestEndpointPages"] = []stubCall{{Output: &apigatewayv2.ListProductRestEndpointPagesOutput{Items: []apigwv2types.ProductRestEndpointPageSummaryNoBody{testRestPage("p1", "y")}}}}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: apigwv2CfgWithStub(stubResponses(t, responses))}

	total, _, err := scanAPIGatewayV2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanAPIGatewayV2: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (vpc link, portal, product, two pages)", total)
	}
	for _, rtype := range []string{TypeAPIGatewayV2VpcLink, TypeAPIGatewayV2Portal, TypeAPIGatewayV2PortalProduct, TypeAPIGatewayV2ProductPage, TypeAPIGatewayV2ProductRestEndpointPage} {
		if n := countPortalRows(t, st, rtype); n != 1 {
			t.Errorf("%s rows = %d; want 1", rtype, n)
		}
	}
}

// TestScanAPIGatewayV2_PortalErrorAfterCorePhases checks that a failing portal
// op's error propagates while the core phases' rows are kept. The run order is
// pinned by TestScanAPIGatewayV2_CoreErrorSkipsPortals.
func TestScanAPIGatewayV2_PortalErrorAfterCorePhases(t *testing.T) {
	boom := apiErr("ConflictException", "boom")
	product := &apigatewayv2.ListPortalProductsOutput{Items: []apigwv2types.PortalProductSummary{testPortalProduct(testAccountID, "p1")}}
	cases := map[string]map[string][]stubCall{
		"ListPortals": {"ListPortals": {{Err: boom}}},
		"ListPortalProducts": {
			"ListPortals":        {{Output: &apigatewayv2.ListPortalsOutput{}}},
			"ListPortalProducts": {{Err: boom}},
		},
		"ListProductPages": {
			"ListPortals":        {{Output: &apigatewayv2.ListPortalsOutput{}}},
			"ListPortalProducts": {{Output: product}},
			"ListProductPages":   {{Err: boom}},
		},
		"ListProductRestEndpointPages": {
			"ListPortals":                  {{Output: &apigatewayv2.ListPortalsOutput{}}},
			"ListPortalProducts":           {{Output: product}},
			"ListProductPages":             {{Output: &apigatewayv2.ListProductPagesOutput{}}},
			"ListProductRestEndpointPages": {{Err: boom}},
		},
	}
	for failing, portal := range cases {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			responses := apigwv2CoreResponses(nil)
			for op, calls := range portal {
				responses[op] = calls
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: apigwv2CfgWithStub(stubResponses(t, responses))}
			if _, _, err := scanAPIGatewayV2(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "ConflictException") {
				t.Fatalf("err = %v; want the %s ConflictException", err, failing)
			}
			if n := countPortalRows(t, st, TypeAPIGatewayV2VpcLink); n != 1 {
				t.Errorf("vpc link rows = %d; want 1 (core phases finish before portals)", n)
			}
		})
	}
}

// TestScanAPIGatewayV2_CoreErrorSkipsPortals pins that portals run only after
// the core phases succeed: no portal op has a stubbed response, so calling
// one fails the test.
func TestScanAPIGatewayV2_CoreErrorSkipsPortals(t *testing.T) {
	st := newTestStore(t)
	responses := apigwv2CoreResponses(apiErr("ConflictException", "boom"))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: apigwv2CfgWithStub(stubResponses(t, responses))}
	if _, _, err := scanAPIGatewayV2(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want the GetVpcLinks ConflictException", err)
	}
}
