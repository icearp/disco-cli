package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveServiceCatalogPortfolioProducts,
		EdgeDecl{TypeServiceCatalogPortfolio, TypeServiceCatalogProduct, store.RelContains},
	)
	registerResolver(
		resolveServiceCatalogPortfolioProductAssociations,
		EdgeDecl{TypeServiceCatalogPortfolioProductAssociation, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogPortfolioProductAssociation, TypeServiceCatalogProduct, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceCatalogServiceActionAssociations,
		EdgeDecl{TypeServiceCatalogServiceActionAssociation, TypeServiceCatalogProduct, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogServiceActionAssociation, TypeServiceCatalogServiceAction, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceCatalogProvisionedProductRefs,
		EdgeDecl{TypeServiceCatalogCloudFormationProvisionedProduct, TypeServiceCatalogProduct, store.RelUses},
	)
	registerResolver(
		resolveServiceCatalogPortfolioShares,
		EdgeDecl{TypeServiceCatalogPortfolioShare, TypeServiceCatalogPortfolio, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceCatalogPortfolioPrincipals,
		EdgeDecl{TypeServiceCatalogPortfolioPrincipalAssociation, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogPortfolioPrincipalAssociation, TypeIAMRole, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogPortfolioPrincipalAssociation, TypeIAMUser, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogPortfolioPrincipalAssociation, TypeIAMGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceCatalogTagOptionAssociations,
		EdgeDecl{TypeServiceCatalogTagOptionAssociation, TypeServiceCatalogTagOption, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceCatalogConstraints,
		EdgeDecl{TypeServiceCatalogLaunchRoleConstraint, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogLaunchRoleConstraint, TypeServiceCatalogProduct, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogLaunchNotificationConstraint, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogLaunchNotificationConstraint, TypeServiceCatalogProduct, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogLaunchTemplateConstraint, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogLaunchTemplateConstraint, TypeServiceCatalogProduct, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogResourceUpdateConstraint, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogResourceUpdateConstraint, TypeServiceCatalogProduct, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogStackSetConstraint, TypeServiceCatalogPortfolio, store.RelAttachedTo},
		EdgeDecl{TypeServiceCatalogStackSetConstraint, TypeServiceCatalogProduct, store.RelAttachedTo},
	)
}

// scPortfolioBareIDIndex builds a `bare-portfolio-id → store.ResourceID`
// map by listing every scanned portfolio and reading `attrs.Portfolio.ID`
// — portfolios are stored with their full ARN as NativeID, but
// associations reference portfolios by bare id (e.g. "port-abc123").
func scPortfolioBareIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogPortfolio}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rs))
	for _, r := range rs {
		var attrs struct {
			Portfolio struct {
				ID string `json:"Id"`
			} `json:"Portfolio"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Portfolio.ID != "" {
			out[attrs.Portfolio.ID] = r.ID
		}
	}
	return out, nil
}

// scProductBareIDIndex builds a `bare-product-id → store.ResourceID` map
// by reading each scanned product's `ProductViewSummary.ProductID`. The
// product NativeID is the full ARN.
func scProductBareIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogProduct}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rs))
	for _, r := range rs {
		var attrs struct {
			ProductViewSummary struct {
				ProductID string `json:"ProductId"`
			} `json:"ProductViewSummary"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ProductViewSummary.ProductID != "" {
			out[attrs.ProductViewSummary.ProductID] = r.ID
		}
	}
	return out, nil
}

// resolveServiceCatalogPortfolioProductAssociations links each
// portfolio-product-association row to its parent portfolio AND the
// referenced product. Both edges use `attached-to`. Cross-account or
// otherwise unscanned targets skip silently.
func resolveServiceCatalogPortfolioProductAssociations(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogPortfolioProductAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assocs) == 0 {
		return nil
	}
	portIdx, err := scPortfolioBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	prodIdx, err := scProductBareIDIndex(acct, st)
	if err != nil {
		return err
	}

	for _, a := range assocs {
		// NativeID shape: arn:aws:servicecatalog:{r}:{a}:portfolio-product-association/{pid}/{prodID}.
		// Bare ids are the closing two path segments.
		parts := splitNativePath(a.NativeID, ":portfolio-product-association/")
		if len(parts) != 2 {
			continue
		}
		pid, prodID := parts[0], parts[1]
		if portRID, ok := portIdx[pid]; ok {
			if err := st.UpsertRelationship(a.ID, portRID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog ppa→portfolio: %w", err)
			}
		}
		if prodRID, ok := prodIdx[prodID]; ok {
			if err := st.UpsertRelationship(a.ID, prodRID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog ppa→product: %w", err)
			}
		}
	}
	return nil
}

// splitNativePath returns the slash-separated segments of `s` after
// `marker`. Empty segments are dropped. Empty result on marker miss.
func splitNativePath(s, marker string) []string {
	i := strings.Index(s, marker)
	if i < 0 {
		return nil
	}
	raw := strings.Split(s[i+len(marker):], "/")
	out := raw[:0]
	for _, p := range raw {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveServiceCatalogServiceActionAssociations links each
// service-action-association row to its product AND service-action.
// NativeID encodes (productId, artifactId, serviceActionId) — only
// product + service-action correspond to first-class resources;
// provisioning artifact has no own type.
func resolveServiceCatalogServiceActionAssociations(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogServiceActionAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assocs) == 0 {
		return nil
	}
	prodIdx, err := scProductBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	saIDs, err := scannedIDSet(acct, st, TypeServiceCatalogServiceAction)
	if err != nil {
		return err
	}

	for _, a := range assocs {
		parts := splitNativePath(a.NativeID, ":service-action-association/")
		if len(parts) != 3 {
			continue
		}
		prodID, _, saID := parts[0], parts[1], parts[2]
		if rID, ok := prodIdx[prodID]; ok {
			if err := st.UpsertRelationship(a.ID, rID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog saa→product: %w", err)
			}
		}
		// Service-action NativeID is `arn:aws:servicecatalog:{r}:{a}:service-action/{saID}`.
		saARN := scARN(sv(a.Region), acct.ID, "service-action", saID)
		saRID := store.ResourceID("aws", acct.ID, saARN)
		if saIDs[saRID] {
			if err := st.UpsertRelationship(a.ID, saRID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog saa→service-action: %w", err)
			}
		}
	}
	return nil
}

// servicecatalogProvisionedProductAttrs mirrors the SDK
// `ProvisionedProductAttribute` JSON written by scanSCProvisionedProducts.
type servicecatalogProvisionedProductAttrs struct {
	ProductID              string `json:"ProductId"`
	ProvisioningArtifactID string `json:"ProvisioningArtifactId"`
}

// resolveServiceCatalogProvisionedProductRefs emits provisioned-product
// → product `uses` edges. Provisioning artifact has no first-class
// resource type, so the artifact reference is recorded only in attrs.
func resolveServiceCatalogProvisionedProductRefs(acct *account, st *store.Store) error {
	pps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogCloudFormationProvisionedProduct}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pps) == 0 {
		return nil
	}
	prodIdx, err := scProductBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, p := range pps {
		var attrs servicecatalogProvisionedProductAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ProductID == "" {
			continue
		}
		prodRID, ok := prodIdx[attrs.ProductID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(p.ID, prodRID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert servicecatalog provisioned-product→product: %w", err)
		}
	}
	return nil
}

// resolveServiceCatalogPortfolioShares attaches every portfolio-share
// row to its parent portfolio. NativeID encodes the bare portfolio id
// in the first path segment after `/portfolio-share/`.
func resolveServiceCatalogPortfolioShares(acct *account, st *store.Store) error {
	shares, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogPortfolioShare}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(shares) == 0 {
		return nil
	}
	portIdx, err := scPortfolioBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, s := range shares {
		parts := splitNativePath(s.NativeID, ":portfolio-share/")
		if len(parts) < 1 {
			continue
		}
		pid := parts[0]
		portRID, ok := portIdx[pid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, portRID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert servicecatalog portfolio-share→portfolio: %w", err)
		}
	}
	return nil
}

// servicecatalogPrincipalAttrs mirrors the SDK `Principal` shape from
// ListPrincipalsForPortfolio — `PrincipalARN` is the IAM principal ARN,
// `PrincipalType` differentiates IAM / IAM_PATTERN.
type servicecatalogPrincipalAttrs struct {
	PrincipalARN  string `json:"PrincipalARN"`
	PrincipalType string `json:"PrincipalType"`
}

// resolveServiceCatalogPortfolioPrincipals attaches each principal
// association to its parent portfolio AND to the IAM target (role/user/
// group) when the ARN matches a scanned IAM resource. IAM_PATTERN
// principals carry a wildcard ARN (e.g. `arn:aws:iam:::role/foo*`) and
// skip the IAM edge.
func resolveServiceCatalogPortfolioPrincipals(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogPortfolioPrincipalAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assocs) == 0 {
		return nil
	}
	portIdx, err := scPortfolioBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	userIDs, err := resourceIDSet(st, acct.ID, TypeIAMUser)
	if err != nil {
		return err
	}
	groupIDs, err := resourceIDSet(st, acct.ID, TypeIAMGroup)
	if err != nil {
		return err
	}
	for _, a := range assocs {
		// Recover bare portfolio id from NativeID:
		// arn:aws:servicecatalog:{r}:{a}:portfolio-principal-association/{pid}/{principalARN}
		parts := splitNativePath(a.NativeID, ":portfolio-principal-association/")
		if len(parts) >= 1 {
			if portRID, ok := portIdx[parts[0]]; ok {
				if err := st.UpsertRelationship(a.ID, portRID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert servicecatalog ppa-principal→portfolio: %w", err)
				}
			}
		}

		var attrs servicecatalogPrincipalAttrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PrincipalARN == "" {
			continue
		}
		// IAM principals: try role/user/group in turn — they share the
		// IAM partition so a hit on one excludes the others.
		for _, t := range []struct {
			rtype string
			ids   map[string]struct{}
		}{
			{TypeIAMRole, roleIDs},
			{TypeIAMUser, userIDs},
			{TypeIAMGroup, groupIDs},
		} {
			rid := store.ResourceID("aws", acct.ID, attrs.PrincipalARN)
			if _, ok := t.ids[rid]; !ok {
				continue
			}
			if err := st.UpsertRelationship(a.ID, rid, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog ppa-principal→iam: %w", err)
			}
			break
		}
	}
	return nil
}

// resolveServiceCatalogTagOptionAssociations attaches each tag-option-
// association to its parent tag-option. Resource targets vary across
// many AWS types and are not classified here — `ResourceDetail.ID`
// carries a bare id with no type hint.
func resolveServiceCatalogTagOptionAssociations(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeServiceCatalogTagOptionAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assocs) == 0 {
		return nil
	}
	toIDs, err := scannedIDSet(acct, st, TypeServiceCatalogTagOption)
	if err != nil {
		return err
	}
	for _, a := range assocs {
		// NativeID: arn:aws:servicecatalog:{r}:{a}:tag-option-association/{toID}/{rid}.
		parts := splitNativePath(a.NativeID, ":tag-option-association/")
		if len(parts) < 1 {
			continue
		}
		toID := parts[0]
		toARN := scARN(sv(a.Region), acct.ID, "tag-option", toID)
		toRID := store.ResourceID("aws", acct.ID, toARN)
		if !toIDs[toRID] {
			continue
		}
		if err := st.UpsertRelationship(a.ID, toRID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert servicecatalog tag-option-assoc→tag-option: %w", err)
		}
	}
	return nil
}

// servicecatalogConstraintAttrs mirrors the SDK ConstraintDetail JSON:
// PortfolioID + ProductID both bare.
type servicecatalogConstraintAttrs struct {
	PortfolioID string `json:"PortfolioId"`
	ProductID   string `json:"ProductId"`
}

// resolveServiceCatalogConstraints attaches every constraint subtype
// (LaunchRole / LaunchNotification / LaunchTemplate / ResourceUpdate /
// StackSet) to its portfolio AND product. Constraint NativeID is the
// synthesized `constraint/{cid}` ARN, so the cross-refs come from
// AttributesJSON (raw `ConstraintDetail`).
func resolveServiceCatalogConstraints(acct *account, st *store.Store) error {
	types := []string{
		TypeServiceCatalogLaunchRoleConstraint,
		TypeServiceCatalogLaunchNotificationConstraint,
		TypeServiceCatalogLaunchTemplateConstraint,
		TypeServiceCatalogResourceUpdateConstraint,
		TypeServiceCatalogStackSetConstraint,
	}
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: types, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return nil
	}
	portIdx, err := scPortfolioBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	prodIdx, err := scProductBareIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs servicecatalogConstraintAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if portRID, ok := portIdx[attrs.PortfolioID]; ok && attrs.PortfolioID != "" {
			if err := st.UpsertRelationship(r.ID, portRID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog constraint→portfolio: %w", err)
			}
		}
		if prodRID, ok := prodIdx[attrs.ProductID]; ok && attrs.ProductID != "" {
			if err := st.UpsertRelationship(r.ID, prodRID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert servicecatalog constraint→product: %w", err)
			}
		}
	}
	return nil
}

// servicecatalogPortfolioAttrs mirrors the wrapped attrs JSON written by
// scanServiceCatalogPortfolios — `{"Portfolio": ..., "Constraints":
// [...], "ProductARNs": [...]}`. ProductARNs = product ARNs from
// SearchProductsAsAdmin, filtered to this portfolio.
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
		Providers: []string{"aws"},
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
			pID := store.ResourceID("aws", acct.ID, prodARN)
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
