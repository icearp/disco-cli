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
