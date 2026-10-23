package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"

	"github.com/icearp/disco-cli/store"
)

var (
	scTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: servicecatalog:List")
	errSCTest    = errors.New("servicecatalog test failure")
	// errSCUnexpectedInput answers a request a stub does not expect.
	errSCUnexpectedInput = errors.New("servicecatalog stub: unexpected request input")
)

const (
	scPPWebARN = "arn:aws:servicecatalog:us-east-1:123456789012:stack/web/pp-web"
	scPPDBARN  = "arn:aws:servicecatalog:us-east-1:123456789012:stack/db/pp-db"
	scActionID = "act-1"
)

// scTaggedPP is a tagged provisioned product; scUntaggedPP has neither tags
// nor a name, so its row is named after its id.
var (
	scTaggedPP = sctypes.ProvisionedProductAttribute{
		Arn: ptrStr(scPPWebARN), Id: ptrStr("pp-web"), Name: ptrStr("web"), Type: ptrStr("CFN_STACK"),
		Status: sctypes.ProvisionedProductStatusAvailable,
		Tags:   []sctypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("")}},
	}
	scUntaggedPP = sctypes.ProvisionedProductAttribute{Arn: ptrStr(scPPDBARN), Id: ptrStr("pp-db")}
)

// scPPPages answers SearchProvisionedProducts with two pages: the tagged
// product and an element without an Arn, then the untagged product. A request
// with any input other than the token and the page size cap is refused.
func scPPPages(in any) (any, error) {
	req := in.(*servicecatalog.SearchProvisionedProductsInput)
	if req.PageSize != 100 || req.AccessLevelFilter != nil || req.Filters != nil ||
		req.SortBy != nil || req.SortOrder != "" || req.AcceptLanguage != nil {
		return nil, errSCUnexpectedInput
	}
	switch sv(req.PageToken) {
	case "":
		return &servicecatalog.SearchProvisionedProductsOutput{
			ProvisionedProducts: []sctypes.ProvisionedProductAttribute{scTaggedPP, {Id: ptrStr("pp-noarn")}},
			NextPageToken:       ptrStr("p2"),
		}, nil
	case "p2":
		return &servicecatalog.SearchProvisionedProductsOutput{ProvisionedProducts: []sctypes.ProvisionedProductAttribute{scUntaggedPP}}, nil
	}
	return nil, errSCUnexpectedInput
}

// scHandlers answers SearchProvisionedProducts with scPPPages and
// ListServiceActions with one action; every other op is denied.
func scHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"SearchProvisionedProducts": scPPPages,
		"ListServiceActions": func(any) (any, error) {
			return &servicecatalog.ListServiceActionsOutput{ServiceActionSummaries: []sctypes.ServiceActionSummary{{Id: ptrStr(scActionID), Name: ptrStr("restart")}}}, nil
		},
	}
}

// scanSCTopLevel runs scanServiceCatalog against handlers on a fresh store,
// where every stored row is new, so it fails the test unless inserted equals
// total.
func scanSCTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	return scanSCCtx(context.Background(), t, st, ignoreCtx(handlers))
}

func scanSCCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(handlers, scTestDenied)
	total, inserted, err := scanServiceCatalog(ctx, acct, testRegion, st, testScanID)
	if inserted != total {
		t.Errorf("inserted = %d, want total %d", inserted, total)
	}
	return total, err
}

// Each listed provisioned product is stored from its element: NativeID the
// ARN, Name (the id when it has none), the element verbatim as attributes, and
// its tags. An element without an ARN is not stored and is warned about once
// under its id.
func TestScanServiceCatalog_ProvisionedProductRows(t *testing.T) {
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == scSearchProvisionedProductsOp {
			warns = append(warns, w)
		}
	}
	total, err := scanSCTopLevel(t, st, scHandlers())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2+1 {
		t.Errorf("total = %d, want 3 (2 provisioned products, 1 service action)", total)
	}
	rows := rowsByNativeID(t, st, TypeServiceCatalogCloudFormationProvisionedProduct)
	if len(rows) != 2 {
		t.Fatalf("provisioned product rows = %v, want 2", rows)
	}
	web, db := rows[scPPWebARN], rows[scPPDBARN]
	if sv(web.Name) != "web" || sv(web.Region) != testRegion {
		t.Errorf("web row = name %q region %q", sv(web.Name), sv(web.Region))
	}
	assertJSONEqual(t, "web attributes", web.AttributesJSON, mustJSON(scTaggedPP))
	assertTagsMap(t, web.TagsJSON, map[string]string{"env": "prod", "team": ""})
	if sv(db.Name) != "pp-db" || db.TagsJSON != nil {
		t.Errorf("db row = name %q tags %v; want pp-db, nil", sv(db.Name), db.TagsJSON)
	}
	assertJSONEqual(t, "db attributes", db.AttributesJSON, mustJSON(scUntaggedPP))
	if len(warns) != 1 || warns[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(warns[0].Message, "pp-noarn") {
		t.Errorf("warnings = %+v, want one naming pp-noarn", warns)
	}
}

// A failed later page keeps the products read before it: a denial warns once
// and is not returned, any other error and a repeated token are returned.
func TestScanServiceCatalog_ProvisionedProductLaterPageFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   error
		wantWarns int
	}{
		{"denied", scTestDenied, nil, 1},
		{"other error", errSCTest, errSCTest, 0},
		{"repeated token", nil, errRepeatedNextToken, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, scSearchProvisionedProductsOp)
			h := scHandlers()
			h["SearchProvisionedProducts"] = func(in any) (any, error) {
				if sv(in.(*servicecatalog.SearchProvisionedProductsInput).PageToken) != "p2" {
					return scPPPages(in)
				}
				if c.err != nil {
					return nil, c.err
				}
				return &servicecatalog.SearchProvisionedProductsOutput{NextPageToken: ptrStr("p2")}, nil
			}
			total, err := scanSCTopLevel(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 1+1 {
				t.Errorf("total = %d, want 2 (1 provisioned product, 1 service action)", total)
			}
			if *warns != c.wantWarns+1 {
				t.Errorf("warnings = %d, want %d (+1 for pp-noarn)", *warns, c.wantWarns+1)
			}
			rows := rowsByNativeID(t, st, TypeServiceCatalogCloudFormationProvisionedProduct)
			if _, ok := rows[scPPWebARN]; !ok || len(rows) != 1 {
				t.Errorf("provisioned product rows = %v, want only %s", rows, scPPWebARN)
			}
		})
	}
}

// A hard error in any phase is returned, and the other phases still run: an
// earlier phase's failure does not cost the provisioned products, and theirs
// does not cost the service actions.
func TestScanServiceCatalog_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op                 string
		wantTotal, wantPPs int
	}{
		{"ListPortfolios", 2 + 1, 2},
		{"SearchProductsAsAdmin", 2 + 1, 2},
		{"ListAcceptedPortfolioShares", 2 + 1, 2},
		{"SearchProvisionedProducts", 1, 0},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := scHandlers()
			h[c.op] = func(any) (any, error) { return nil, errSCTest }
			total, err := scanSCTopLevel(t, st, h)
			if !errors.Is(err, errSCTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeServiceCatalogCloudFormationProvisionedProduct)); n != c.wantPPs {
				t.Errorf("provisioned product rows = %d, want %d", n, c.wantPPs)
			}
			if n := len(rowsByNativeID(t, st, TypeServiceCatalogServiceAction)); n != 1 {
				t.Errorf("service action rows = %d, want 1", n)
			}
		})
	}
}

// scFullPortfolioARN is the portfolio scFullHandlers lists.
const scFullPortfolioARN = "arn:aws:catalog:us-east-1:123456789012:portfolio/port-1"

// scFullCalls records the portfolio ids each portfolio-keyed op was called
// with.
type scFullCalls struct {
	shares, principals, constraints parentCalls
}

// scFullHandlers is scHandlers plus one portfolio (port-1) with one share,
// principal and constraint, one product (prod-1) and one TagOption (tag-1)
// with one association, so every phase of scanServiceCatalog stores a row.
// ListConstraintsForPortfolio is called by the portfolio phase first and by
// the constraint phase second.
func scFullHandlers(rec *scFullCalls) map[string]func(any) (any, error) {
	h := scHandlers()
	h["ListPortfolios"] = func(any) (any, error) {
		return &servicecatalog.ListPortfoliosOutput{PortfolioDetails: []sctypes.PortfolioDetail{{Id: ptrStr("port-1"), ARN: ptrStr(scFullPortfolioARN)}}}, nil
	}
	h["SearchProductsAsAdmin"] = func(in any) (any, error) {
		if in.(*servicecatalog.SearchProductsAsAdminInput).PortfolioId != nil {
			return &servicecatalog.SearchProductsAsAdminOutput{}, nil
		}
		return &servicecatalog.SearchProductsAsAdminOutput{ProductViewDetails: []sctypes.ProductViewDetail{{
			ProductARN:         ptrStr("arn:aws:catalog:us-east-1:123456789012:product/prod-1"),
			ProductViewSummary: &sctypes.ProductViewSummary{ProductId: ptrStr("prod-1"), Name: ptrStr("app")},
		}}}, nil
	}
	h["ListPortfoliosForProduct"] = func(any) (any, error) { return &servicecatalog.ListPortfoliosForProductOutput{}, nil }
	h["ListTagOptions"] = func(any) (any, error) {
		return &servicecatalog.ListTagOptionsOutput{TagOptionDetails: []sctypes.TagOptionDetail{{Id: ptrStr("tag-1"), Key: ptrStr("cc"), Value: ptrStr("42")}}}, nil
	}
	h["ListResourcesForTagOption"] = func(any) (any, error) {
		return &servicecatalog.ListResourcesForTagOptionOutput{ResourceDetails: []sctypes.ResourceDetail{{Id: ptrStr("port-1")}}}, nil
	}
	h["DescribePortfolioShares"] = func(in any) (any, error) {
		req := in.(*servicecatalog.DescribePortfolioSharesInput)
		rec.shares.add(sv(req.PortfolioId))
		if req.Type != sctypes.DescribePortfolioShareTypeAccount {
			return &servicecatalog.DescribePortfolioSharesOutput{}, nil
		}
		return &servicecatalog.DescribePortfolioSharesOutput{PortfolioShareDetails: []sctypes.PortfolioShareDetail{{
			PrincipalId: ptrStr("210987654321"), Type: sctypes.DescribePortfolioShareTypeAccount, Accepted: true,
		}}}, nil
	}
	h["ListPrincipalsForPortfolio"] = func(in any) (any, error) {
		rec.principals.add(sv(in.(*servicecatalog.ListPrincipalsForPortfolioInput).PortfolioId))
		return &servicecatalog.ListPrincipalsForPortfolioOutput{Principals: []sctypes.Principal{{
			PrincipalARN: ptrStr("arn:aws:iam::123456789012:role/dev"), PrincipalType: sctypes.PrincipalTypeIam,
		}}}, nil
	}
	h["ListConstraintsForPortfolio"] = func(in any) (any, error) {
		rec.constraints.add(sv(in.(*servicecatalog.ListConstraintsForPortfolioInput).PortfolioId))
		return &servicecatalog.ListConstraintsForPortfolioOutput{ConstraintDetails: []sctypes.ConstraintDetail{{
			ConstraintId: ptrStr("cons-1"), Type: ptrStr("LAUNCH"), PortfolioId: ptrStr("port-1"), ProductId: ptrStr("prod-1"),
		}}}, nil
	}
	return h
}

// scFullTotal is the rows scFullHandlers stores: 2 provisioned products, a
// service action, a portfolio, a product, a TagOption and its association, a
// share, a principal association and a constraint.
const scFullTotal = 10

// scFailPhase makes op fail with err in its own phase only:
// ListConstraintsForPortfolio after the portfolio phase's call, and
// SearchProductsAsAdmin outside a portfolio.
func scFailPhase(h map[string]func(any) (any, error), op string, err error) {
	orig := h[op]
	calls := 0
	h[op] = func(in any) (any, error) {
		calls++
		switch {
		case op == "ListConstraintsForPortfolio" && calls == 1,
			op == "SearchProductsAsAdmin" && in.(*servicecatalog.SearchProductsAsAdminInput).PortfolioId != nil:
			return orig(in)
		}
		return nil, err
	}
}

// The portfolio-keyed phases each read the portfolio the portfolio phase
// listed and store its share, principal association and constraint.
func TestScanServiceCatalog_PortfolioPhasesStoreRows(t *testing.T) {
	st := newTestStore(t)
	var rec scFullCalls
	total, err := scanSCTopLevel(t, st, scFullHandlers(&rec))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != scFullTotal {
		t.Errorf("total = %d, want %d", total, scFullTotal)
	}
	for _, c := range []struct {
		op        string
		got, want []string
	}{
		{"DescribePortfolioShares", rec.shares.ids, []string{"port-1", "port-1", "port-1", "port-1"}},
		{"ListPrincipalsForPortfolio", rec.principals.ids, []string{"port-1"}},
		{"ListConstraintsForPortfolio", rec.constraints.ids, []string{"port-1", "port-1"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s portfolio ids = %v, want %v", c.op, c.got, c.want)
		}
	}
	for typ, native := range map[string]string{
		TypeServiceCatalogPortfolioShare:                scARN(testRegion, testAccountID, "portfolio-share", "port-1/ACCOUNT/210987654321"),
		TypeServiceCatalogPortfolioPrincipalAssociation: scARN(testRegion, testAccountID, "portfolio-principal-association", "port-1/arn:aws:iam::123456789012:role/dev"),
		TypeServiceCatalogLaunchRoleConstraint:          scARN(testRegion, testAccountID, "constraint", "cons-1"),
	} {
		if rows := rowsByNativeID(t, st, typ); len(rows) != 1 || rows[native].NativeID != native {
			t.Errorf("%s rows = %v, want only %s", typ, rows, native)
		}
	}
}

// A portfolio-keyed phase's error is returned, and the other phases' rows are
// stored.
func TestScanServiceCatalog_PortfolioPhaseErrorPropagates(t *testing.T) {
	cases := []struct{ op, typ string }{
		{"DescribePortfolioShares", TypeServiceCatalogPortfolioShare},
		{"ListPrincipalsForPortfolio", TypeServiceCatalogPortfolioPrincipalAssociation},
		{"ListConstraintsForPortfolio", TypeServiceCatalogLaunchRoleConstraint},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := scFullHandlers(&scFullCalls{})
			scFailPhase(h, c.op, errSCTest)
			total, err := scanSCTopLevel(t, st, h)
			if !errors.Is(err, errSCTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != scFullTotal-1 {
				t.Errorf("total = %d, want %d", total, scFullTotal-1)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%s rows = %d, want 0", c.typ, n)
			}
		})
	}
}

// A throttled phase beside another phase's hard error is demoted to one
// warning under that phase's op, so the scan reads as an error, not as
// transient.
func TestScanServiceCatalog_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, op := range []string{
		"ListPortfolios", "SearchProductsAsAdmin", "ListAcceptedPortfolioShares", "SearchProvisionedProducts",
		"ListServiceActions", "ListTagOptions", "ListResourcesForTagOption", "DescribePortfolioShares",
		"ListPrincipalsForPortfolio", "ListConstraintsForPortfolio", "ListPortfoliosForProduct",
	} {
		t.Run(op, func(t *testing.T) {
			hardOp := "ListServiceActions"
			if op == hardOp {
				hardOp = "SearchProvisionedProducts"
			}
			st := newTestStore(t)
			h := scFullHandlers(&scFullCalls{})
			scFailPhase(h, op, throttle)
			scFailPhase(h, hardOp, errSCTest)
			warnings := countOpWarnings(st, "servicecatalog:"+op)
			_, err := scanSCTopLevel(t, st, h)
			if !errors.Is(err, errSCTest) {
				t.Fatalf("err = %v, want the %s error", err, hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("servicecatalog:%s warnings = %d, want 1", op, *warnings)
			}
		})
	}
}

// The TagOption association phase reads the TagOptions the phase before it
// listed, and the product association phase runs over the products listed
// first, with its error returned beside the stored rows.
func TestScanServiceCatalog_LaterPhasesGetEarlierIDs(t *testing.T) {
	st := newTestStore(t)
	h := scHandlers()
	h["SearchProductsAsAdmin"] = func(any) (any, error) {
		return &servicecatalog.SearchProductsAsAdminOutput{ProductViewDetails: []sctypes.ProductViewDetail{{
			ProductARN:         ptrStr("arn:aws:catalog:us-east-1:123456789012:product/prod-1"),
			ProductViewSummary: &sctypes.ProductViewSummary{ProductId: ptrStr("prod-1"), Name: ptrStr("app")},
		}}}, nil
	}
	var assocProducts []string
	h["ListPortfoliosForProduct"] = func(in any) (any, error) {
		assocProducts = append(assocProducts, sv(in.(*servicecatalog.ListPortfoliosForProductInput).ProductId))
		return nil, errSCTest
	}
	h["ListTagOptions"] = func(any) (any, error) {
		return &servicecatalog.ListTagOptionsOutput{TagOptionDetails: []sctypes.TagOptionDetail{{Id: ptrStr("tag-1"), Key: ptrStr("cc"), Value: ptrStr("42")}}}, nil
	}
	h["ListResourcesForTagOption"] = func(in any) (any, error) {
		if sv(in.(*servicecatalog.ListResourcesForTagOptionInput).TagOptionId) != "tag-1" {
			return nil, errSCUnexpectedInput
		}
		return &servicecatalog.ListResourcesForTagOptionOutput{ResourceDetails: []sctypes.ResourceDetail{{Id: ptrStr("port-1")}}}, nil
	}
	total, err := scanSCTopLevel(t, st, h)
	if !errors.Is(err, errSCTest) {
		t.Fatalf("err = %v, want the ListPortfoliosForProduct error", err)
	}
	if total != 2+1+1+1+1 {
		t.Errorf("total = %d, want 6 (2 provisioned products, action, product, tag option, its association)", total)
	}
	if !slices.Equal(assocProducts, []string{"prod-1"}) {
		t.Errorf("ListPortfoliosForProduct products = %v, want [prod-1]", assocProducts)
	}
	if _, ok := rowsByNativeID(t, st, TypeServiceCatalogTagOptionAssociation)["arn:aws:servicecatalog:us-east-1:123456789012:tag-option-association/tag-1/port-1"]; !ok {
		t.Error("no tag-option-association row for tag-1/port-1")
	}
}

// A failed write in the portfolio, product or provisioned product phase stops
// the scan before any later phase. The failed provisioned product write warns
// about nothing: no row was stored.
func TestScanServiceCatalog_WriteFailureStops(t *testing.T) {
	cases := []struct {
		typ, op   string
		wantTotal int
		laterOps  []string
	}{
		{TypeServiceCatalogPortfolio, "ListPortfolios", 0, []string{"SearchProvisionedProducts", "ListServiceActions"}},
		{TypeServiceCatalogProduct, "SearchProductsAsAdmin", 1, []string{"SearchProvisionedProducts", "ListServiceActions"}},
		{TypeServiceCatalogCloudFormationProvisionedProduct, "SearchProvisionedProducts", 2, []string{"ListServiceActions", "ListTagOptions", "ListResourcesForTagOption"}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, c.typ)
			warns := countOpWarnings(st, scSearchProvisionedProductsOp)
			h := scHandlers()
			h["ListPortfolios"] = func(any) (any, error) {
				return &servicecatalog.ListPortfoliosOutput{PortfolioDetails: []sctypes.PortfolioDetail{{
					Id: ptrStr("port-1"), ARN: ptrStr("arn:aws:catalog:us-east-1:123456789012:portfolio/port-1"),
				}}}, nil
			}
			h["SearchProductsAsAdmin"] = func(in any) (any, error) {
				if in.(*servicecatalog.SearchProductsAsAdminInput).PortfolioId != nil {
					return &servicecatalog.SearchProductsAsAdminOutput{}, nil
				}
				return &servicecatalog.SearchProductsAsAdminOutput{ProductViewDetails: []sctypes.ProductViewDetail{{
					ProductARN: ptrStr("arn:aws:catalog:us-east-1:123456789012:product/prod-1"),
				}}}, nil
			}
			var later []string
			for _, op := range c.laterOps {
				h[op] = func(any) (any, error) {
					later = append(later, op)
					return nil, errSCTest
				}
			}
			total, err := scanSCTopLevel(t, st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d (the rows of the phases before)", total, c.wantTotal)
			}
			if len(later) != 0 || *warns != 0 {
				t.Errorf("later ops, %s warnings = %v, %d; want none, 0", scSearchProvisionedProductsOp, later, *warns)
			}
		})
	}
}

// When ctx ends on the first SearchProvisionedProducts page, the second is not
// read: the first page's product is stored and the stop is returned. A ctx
// stop is not a store write failure, so the next phase still makes its call.
func TestScanServiceCatalog_ContextEndsDuringProvisionedProducts(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []string
	h := map[string]func(context.Context, any) (any, error){
		"SearchProvisionedProducts": func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cancel()
			return scPPPages(in)
		},
		"ListServiceActions": func(ctx context.Context, _ any) (any, error) {
			called = append(called, "ListServiceActions")
			return nil, ctx.Err()
		},
	}
	total, err := scanSCCtx(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the first page's product)", total)
	}
	if !slices.Equal(called, []string{"ListServiceActions"}) {
		t.Errorf("later calls = %v, want [ListServiceActions]", called)
	}
	rows := rowsByNativeID(t, st, TypeServiceCatalogCloudFormationProvisionedProduct)
	if _, ok := rows[scPPWebARN]; !ok || len(rows) != 1 {
		t.Errorf("provisioned product rows = %v, want only %s", rows, scPPWebARN)
	}
}
