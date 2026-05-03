package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testSCPortfolioID = "port-aaa1111bbb"
	testSCProductIDA  = "prod-aaa1111ccc"
	testSCProductIDB  = "prod-bbb2222ddd"
)

func scPortfolioARN(id string) string {
	return fmt.Sprintf("arn:aws:catalog:%s:%s:portfolio/%s", testRegion, testAccountID, id)
}

func scProductARN(id string) string {
	return fmt.Sprintf("arn:aws:catalog:%s:%s:product/%s", testRegion, testAccountID, id)
}

// TestResolveServiceCatalogPortfolioProducts_HappyPath verifies portfolio
// → product `contains` edges land for each scanned product ARN listed
// in the portfolio's attrs.
func TestResolveServiceCatalogPortfolioProducts_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	prodAARN := scProductARN(testSCProductIDA)
	prodBARN := scProductARN(testSCProductIDB)
	pAID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct, prodAARN, testRegion, "{}")
	pBID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct, prodBARN, testRegion, "{}")

	portfolioARN := scPortfolioARN(testSCPortfolioID)
	portfolioAttrs := fmt.Sprintf(`{"Portfolio":{"Id":%q,"DisplayName":"Prod"},"Constraints":[],"ProductARNs":[%q,%q]}`,
		testSCPortfolioID, prodAARN, prodBARN)
	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio, portfolioARN, testRegion, portfolioAttrs)

	if err := resolveServiceCatalogPortfolioProducts(acct, st); err != nil {
		t.Fatalf("resolveServiceCatalogPortfolioProducts: %v", err)
	}
	rels, err := st.RelationshipsFrom(portfolioID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, portfolioID, pAID, store.RelContains)
	assertRelationship(t, rels, portfolioID, pBID, store.RelContains)
}

// scServiceActionARN rebuilds the synthesized service-action NativeID
// shape used by scanSCServiceActions (`scARN` with kind=service-action).
func scServiceActionARN(id string) string {
	return fmt.Sprintf("arn:aws:servicecatalog:%s:%s:service-action/%s", testRegion, testAccountID, id)
}

// scTagOptionARN rebuilds the synthesized tag-option NativeID.
func scTagOptionARN(id string) string {
	return fmt.Sprintf("arn:aws:servicecatalog:%s:%s:tag-option/%s", testRegion, testAccountID, id)
}

// scPortfolioAttrs builds the wrapped `{"Portfolio":{"Id":...}}` shape
// scanSCPortfolios writes (constraints / product list omitted).
func scPortfolioAttrs(bareID string) string {
	return fmt.Sprintf(`{"Portfolio":{"Id":%q,"DisplayName":"Prod"},"Constraints":[],"ProductARNs":[]}`, bareID)
}

// scProductAttrs builds the `{"ProductViewSummary":{"ProductId":...}}`
// shape scanSCProducts writes (raw `ProductViewDetail`).
func scProductAttrs(bareID string) string {
	return fmt.Sprintf(`{"ProductViewSummary":{"ProductId":%q,"Name":"P"}}`, bareID)
}

// TestResolveServiceCatalogPortfolioProducts_FKSafe verifies products
// not in the store skip without erroring.
func TestResolveServiceCatalogPortfolioProducts_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed a different product so the resolver iterates rather than
	// fast-pathing past on empty id-set.
	upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct, scProductARN("prod-other"), testRegion, "{}")

	portfolioARN := scPortfolioARN(testSCPortfolioID)
	portfolioAttrs := fmt.Sprintf(`{"ProductARNs":[%q]}`, scProductARN("prod-ghost"))
	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio, portfolioARN, testRegion, portfolioAttrs)

	if err := resolveServiceCatalogPortfolioProducts(acct, st); err != nil {
		t.Fatalf("resolveServiceCatalogPortfolioProducts: %v", err)
	}
	rels, err := st.RelationshipsFrom(portfolioID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges for unscanned product, got %d", len(rels))
	}
}

// TestResolveServiceCatalogPortfolioProductAssociations_HappyPath
// verifies the association row links to both portfolio and product.
func TestResolveServiceCatalogPortfolioProductAssociations_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio,
		scPortfolioARN(testSCPortfolioID), testRegion, scPortfolioAttrs(testSCPortfolioID))
	productID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct,
		scProductARN(testSCProductIDA), testRegion, scProductAttrs(testSCProductIDA))

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-product-association/%s/%s",
		testRegion, testAccountID, testSCPortfolioID, testSCProductIDA)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioProductAssociation,
		assocARN, testRegion, "{}")

	if err := resolveServiceCatalogPortfolioProductAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assocID, portfolioID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, productID, store.RelAttachedTo)
}

// TestResolveServiceCatalogPortfolioProductAssociations_FKSafe verifies
// missing portfolio/product targets skip silently.
func TestResolveServiceCatalogPortfolioProductAssociations_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-product-association/%s/%s",
		testRegion, testAccountID, "port-ghost", "prod-ghost")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioProductAssociation,
		assocARN, testRegion, "{}")

	if err := resolveServiceCatalogPortfolioProductAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

// TestResolveServiceCatalogServiceActionAssociations_HappyPath verifies
// the association row links to product + service-action.
func TestResolveServiceCatalogServiceActionAssociations_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	const saID = "act-aaa1111eee"
	productID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct,
		scProductARN(testSCProductIDA), testRegion, scProductAttrs(testSCProductIDA))
	saResourceID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogServiceAction,
		scServiceActionARN(saID), testRegion, "{}")

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:service-action-association/%s/%s/%s",
		testRegion, testAccountID, testSCProductIDA, "pa-art1", saID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogServiceActionAssociation,
		assocARN, testRegion, "{}")

	if err := resolveServiceCatalogServiceActionAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assocID, productID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, saResourceID, store.RelAttachedTo)
}

// TestResolveServiceCatalogServiceActionAssociations_NoAssocs verifies
// a store with no association rows fast-paths to nil.
func TestResolveServiceCatalogServiceActionAssociations_NoAssocs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveServiceCatalogServiceActionAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// TestResolveServiceCatalogProvisionedProductRefs_HappyPath verifies
// the provisioned-product row links to its product via ProductId.
func TestResolveServiceCatalogProvisionedProductRefs_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	productID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct,
		scProductARN(testSCProductIDA), testRegion, scProductAttrs(testSCProductIDA))

	ppARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:stack/PP-foo/pp-aaa1", testRegion, testAccountID)
	ppAttrs := fmt.Sprintf(`{"ProductId":%q,"ProvisioningArtifactId":"pa-x"}`, testSCProductIDA)
	ppID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogCloudFormationProvisionedProduct,
		ppARN, testRegion, ppAttrs)

	if err := resolveServiceCatalogProvisionedProductRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(ppID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ppID, productID, store.RelUses)
}

// TestResolveServiceCatalogProvisionedProductRefs_EmptyAttrs verifies
// missing/empty attrs skip cleanly without panic or edge.
func TestResolveServiceCatalogProvisionedProductRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ppARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:stack/PP-foo/pp-aaa1", testRegion, testAccountID)
	ppID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogCloudFormationProvisionedProduct,
		ppARN, testRegion, "{}")

	if err := resolveServiceCatalogProvisionedProductRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(ppID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges with empty attrs, got %d", len(rels))
	}
}

// TestResolveServiceCatalogPortfolioShares_HappyPath verifies a share
// row links to its parent portfolio.
func TestResolveServiceCatalogPortfolioShares_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio,
		scPortfolioARN(testSCPortfolioID), testRegion, scPortfolioAttrs(testSCPortfolioID))

	shareARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-share/%s/ACCOUNT/999999999999",
		testRegion, testAccountID, testSCPortfolioID)
	shareID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioShare,
		shareARN, testRegion, `{"PrincipalId":"999999999999","Type":"ACCOUNT"}`)

	if err := resolveServiceCatalogPortfolioShares(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(shareID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, shareID, portfolioID, store.RelAttachedTo)
}

// TestResolveServiceCatalogPortfolioShares_FKSafe verifies an unscanned
// portfolio target skips without error.
func TestResolveServiceCatalogPortfolioShares_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	shareARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-share/port-ghost/ACCOUNT/999999999999",
		testRegion, testAccountID)
	shareID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioShare,
		shareARN, testRegion, "{}")

	if err := resolveServiceCatalogPortfolioShares(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(shareID)
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

// TestResolveServiceCatalogPortfolioPrincipals_HappyPath verifies the
// association row links to portfolio + IAM role principal.
func TestResolveServiceCatalogPortfolioPrincipals_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio,
		scPortfolioARN(testSCPortfolioID), testRegion, scPortfolioAttrs(testSCPortfolioID))

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/MyRole", testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-principal-association/%s/%s",
		testRegion, testAccountID, testSCPortfolioID, roleARN)
	assocAttrs := fmt.Sprintf(`{"PrincipalARN":%q,"PrincipalType":"IAM"}`, roleARN)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioPrincipalAssociation,
		assocARN, testRegion, assocAttrs)

	if err := resolveServiceCatalogPortfolioPrincipals(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assocID, portfolioID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, roleID, store.RelAttachedTo)
}

// TestResolveServiceCatalogPortfolioPrincipals_PatternSkipsIAM verifies
// IAM_PATTERN principals attach to the portfolio but emit no IAM edge
// (the wildcard ARN cannot match a concrete IAM resource).
func TestResolveServiceCatalogPortfolioPrincipals_PatternSkipsIAM(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio,
		scPortfolioARN(testSCPortfolioID), testRegion, scPortfolioAttrs(testSCPortfolioID))

	patternARN := fmt.Sprintf("arn:aws:iam:::role/foo*")
	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-principal-association/%s/%s",
		testRegion, testAccountID, testSCPortfolioID, patternARN)
	assocAttrs := fmt.Sprintf(`{"PrincipalARN":%q,"PrincipalType":"IAM_PATTERN"}`, patternARN)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolioPrincipalAssociation,
		assocARN, testRegion, assocAttrs)

	if err := resolveServiceCatalogPortfolioPrincipals(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, portfolioID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("expected 1 edge (portfolio only), got %d", len(rels))
	}
}

// TestResolveServiceCatalogTagOptionAssociations_HappyPath verifies
// the association row links to its parent tag-option.
func TestResolveServiceCatalogTagOptionAssociations_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	const toID = "tag-aaa1bb"
	toResourceID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogTagOption,
		scTagOptionARN(toID), testRegion, "{}")

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:tag-option-association/%s/some-resource-id",
		testRegion, testAccountID, toID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogTagOptionAssociation,
		assocARN, testRegion, "{}")

	if err := resolveServiceCatalogTagOptionAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assocID, toResourceID, store.RelAttachedTo)
}

// TestResolveServiceCatalogTagOptionAssociations_FKSafe verifies an
// unscanned tag-option target skips silently.
func TestResolveServiceCatalogTagOptionAssociations_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	assocARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:tag-option-association/tag-ghost/some-resource",
		testRegion, testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogTagOptionAssociation,
		assocARN, testRegion, "{}")

	if err := resolveServiceCatalogTagOptionAssociations(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

// TestResolveServiceCatalogConstraints_HappyPath verifies a launch-role
// constraint links to both portfolio and product.
func TestResolveServiceCatalogConstraints_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	portfolioID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogPortfolio,
		scPortfolioARN(testSCPortfolioID), testRegion, scPortfolioAttrs(testSCPortfolioID))
	productID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct,
		scProductARN(testSCProductIDA), testRegion, scProductAttrs(testSCProductIDA))

	const cid = "cons-aaa1"
	cARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:constraint/%s", testRegion, testAccountID, cid)
	cAttrs := fmt.Sprintf(`{"ConstraintId":%q,"Type":"LAUNCH","PortfolioId":%q,"ProductId":%q}`,
		cid, testSCPortfolioID, testSCProductIDA)
	cResourceID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogLaunchRoleConstraint,
		cARN, testRegion, cAttrs)

	if err := resolveServiceCatalogConstraints(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(cResourceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, cResourceID, portfolioID, store.RelAttachedTo)
	assertRelationship(t, rels, cResourceID, productID, store.RelAttachedTo)
}

// TestResolveServiceCatalogConstraints_EmptyAttrs verifies a constraint
// row with empty attrs skips both edges without panic.
func TestResolveServiceCatalogConstraints_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:constraint/cons-empty", testRegion, testAccountID)
	cResourceID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogStackSetConstraint,
		cARN, testRegion, "{}")

	if err := resolveServiceCatalogConstraints(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cResourceID)
	if len(rels) != 0 {
		t.Errorf("expected zero edges with empty attrs, got %d", len(rels))
	}
}
