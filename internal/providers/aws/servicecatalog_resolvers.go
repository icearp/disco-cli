package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveServiceCatalogPortfolioProducts)
}

// servicecatalogPortfolioAttrs mirrors the wrapped attrs JSON written by
// scanServiceCatalogPortfolios — `{"Portfolio": ..., "Constraints":
// [...], "ProductARNs": [...]}`. ProductARNs holds the list of product
// ARNs returned by SearchProductsAsAdmin filtered by this portfolio.
type servicecatalogPortfolioAttrs struct {
	ProductARNs []string `json:"ProductARNs"`
}

// resolveServiceCatalogPortfolioProducts emits portfolio → product
// `contains` edges. Service Catalog products can belong to multiple
// portfolios (many-to-many), so this is a regular relationship edge,
// not a hierarchy closure entry. FK-safe via scanned-product id set.
// Cross-account / shared-portfolio products skip silently.
func resolveServiceCatalogPortfolioProducts(acct *account, st *store.Store) error {
	portfolios, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeServiceCatalogPortfolio},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(portfolios) == 0 {
		return nil
	}

	productIDs, err := resourceIDSet(st, acct.ID, TypeServiceCatalogProduct)
	if err != nil {
		return err
	}
	if len(productIDs) == 0 {
		return nil
	}

	for _, p := range portfolios {
		var attrs servicecatalogPortfolioAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]struct{}{}
		for _, prodARN := range attrs.ProductARNs {
			if prodARN == "" {
				continue
			}
			if _, dup := seen[prodARN]; dup {
				continue
			}
			seen[prodARN] = struct{}{}
			pID := store.ResourceID("aws", acct.ID, TypeServiceCatalogProduct, prodARN)
			if _, ok := productIDs[pID]; !ok {
				continue
			}
			if err := st.UpsertRelationship(p.ID, pID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog portfolio→product: %w", err)
			}
		}
	}
	return nil
}
