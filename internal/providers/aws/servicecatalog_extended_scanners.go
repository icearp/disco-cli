package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"
)

func scanServiceCatalogExtended(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, portfolioIDs []string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSCAcceptedShares(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSCProvisionedProducts(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSCServiceActions(ctx, client, acct, region, st, scanID) },
		tagOptionPhase(ctx, client, acct, region, st, scanID),
		func() (int, int, error) {
			return scanSCPortfolioShares(ctx, client, acct, region, st, scanID, portfolioIDs)
		},
		func() (int, int, error) {
			return scanSCPortfolioPrincipals(ctx, client, acct, region, st, scanID, portfolioIDs)
		},
		func() (int, int, error) {
			return scanSCConstraints(ctx, client, acct, region, st, scanID, portfolioIDs)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// tagOptionPhase wraps the two-stage TagOption flow (list + per-tag-option
// associations) so the dispatcher loop can remain uniform.
func tagOptionPhase(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) func() (int, int, error) {
	return func() (int, int, error) {
		ids, t1, i1, err := scanSCTagOptions(ctx, client, acct, region, st, scanID)
		if err != nil {
			return 0, 0, err
		}
		t2, i2, err := scanSCTagOptionAssociations(ctx, client, acct, region, st, scanID, ids)
		return t1 + t2, i1 + i2, err
	}
}

func scARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:servicecatalog:%s:%s:%s/%s", region, acct, kind, id)
}

func scanSCAcceptedShares(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := servicecatalog.NewListAcceptedPortfolioSharesPaginator(client, &servicecatalog.ListAcceptedPortfolioSharesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:ListAcceptedPortfolioShares", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("servicecatalog:ListAcceptedPortfolioShares: %w", perr)
		}
		for _, p := range out.PortfolioDetails {
			arn := sv(p.ARN)
			if arn == "" {
				continue
			}
			label := sv(p.DisplayName)
			if label == "" {
				label = sv(p.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceCatalogAcceptedPortfolioShare, NativeID: arn + "/accepted-share",
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicecatalog accepted-portfolio-shares")
}

func scanSCProvisionedProducts(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := servicecatalog.NewSearchProvisionedProductsPaginator(client, &servicecatalog.SearchProvisionedProductsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:SearchProvisionedProducts", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("servicecatalog:SearchProvisionedProducts: %w", perr)
		}
		for _, p := range out.ProvisionedProducts {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceCatalogCloudFormationProvisionedProduct, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicecatalog provisioned-products")
}

func scanSCServiceActions(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := servicecatalog.NewListServiceActionsPaginator(client, &servicecatalog.ListServiceActionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:ListServiceActions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("servicecatalog:ListServiceActions: %w", perr)
		}
		for _, a := range out.ServiceActionSummaries {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceCatalogServiceAction, NativeID: scARN(region, acct.ID, "service-action", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicecatalog service-actions")
}

func scanSCTagOptions(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := servicecatalog.NewListTagOptionsPaginator(client, &servicecatalog.ListTagOptionsInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:ListTagOptions", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			// TagOptionNotMigratedException = catalog has not migrated TagOptions
			// to the new admin API; nothing to list in this account/region.
			if isAPIErrorCode(perr, "TagOptionNotMigratedException") {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("servicecatalog:ListTagOptions: %w", perr)
		}
		for _, t := range out.TagOptionDetails {
			id := sv(t.Id)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			label := sv(t.Key)
			if v := sv(t.Value); v != "" {
				label = label + "=" + v
			}
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServiceCatalogTagOption, NativeID: scARN(region, acct.ID, "tag-option", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "servicecatalog tag-options")
	return ids, t, i, err
}

func scanSCTagOptionAssociations(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, tagOptionIDs []string) (int, int, error) {
	if len(tagOptionIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, toID := range tagOptionIDs {
		id := toID
		pager := servicecatalog.NewListResourcesForTagOptionPaginator(client, &servicecatalog.ListResourcesForTagOptionInput{TagOptionId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("servicecatalog:ListResourcesForTagOption %s: %w", toID, perr)
			}
			for _, r := range out.ResourceDetails {
				rid := sv(r.Id)
				if rid == "" {
					continue
				}
				arn := scARN(region, acct.ID, "tag-option-association", toID+"/"+rid)
				label := toID + "/" + rid
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeServiceCatalogTagOptionAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "servicecatalog tag-option-associations")
}

// scanSCPortfolioShares iterates the four DescribePortfolioShareType
// values per portfolio (ACCOUNT, ORGANIZATION, ORGANIZATIONAL_UNIT,
// ORGANIZATION_MEMBER_ACCOUNT). Each row keyed on synthetic
// (portfolioId, shareType, principalId) — no native ARN.
func scanSCPortfolioShares(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, portfolioIDs []string) (int, int, error) {
	if len(portfolioIDs) == 0 {
		return 0, 0, nil
	}
	shareTypes := []sctypes.DescribePortfolioShareType{
		sctypes.DescribePortfolioShareTypeAccount,
		sctypes.DescribePortfolioShareTypeOrganization,
		sctypes.DescribePortfolioShareTypeOrganizationalUnit,
		sctypes.DescribePortfolioShareTypeOrganizationMemberAccount,
	}
	var batch []*store.Resource
	for _, pid := range portfolioIDs {
		id := pid
		for _, sType := range shareTypes {
			pager := servicecatalog.NewDescribePortfolioSharesPaginator(client, &servicecatalog.DescribePortfolioSharesInput{PortfolioId: &id, Type: sType})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("servicecatalog:DescribePortfolioShares %s/%s: %w", pid, sType, perr)
				}
				for _, s := range out.PortfolioShareDetails {
					principal := sv(s.PrincipalId)
					if principal == "" {
						continue
					}
					arn := scARN(region, acct.ID, "portfolio-share", pid+"/"+string(sType)+"/"+principal)
					label := string(sType) + ":" + principal
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeServiceCatalogPortfolioShare, NativeID: arn,
						Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "servicecatalog portfolio-shares")
}

func scanSCPortfolioPrincipals(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, portfolioIDs []string) (int, int, error) {
	if len(portfolioIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, pid := range portfolioIDs {
		id := pid
		pager := servicecatalog.NewListPrincipalsForPortfolioPaginator(client, &servicecatalog.ListPrincipalsForPortfolioInput{PortfolioId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("servicecatalog:ListPrincipalsForPortfolio %s: %w", pid, perr)
			}
			for _, p := range out.Principals {
				parn := sv(p.PrincipalARN)
				if parn == "" {
					continue
				}
				arn := scARN(region, acct.ID, "portfolio-principal-association", pid+"/"+parn)
				label := parn
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeServiceCatalogPortfolioPrincipalAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "servicecatalog portfolio-principals")
}

// scanSCConstraints lists per-portfolio constraints and routes by Type
// to one of five disco constraint types (LAUNCH→LaunchRoleConstraint,
// NOTIFICATION→LaunchNotificationConstraint, TEMPLATE→
// LaunchTemplateConstraint, RESOURCE_UPDATE→ResourceUpdateConstraint,
// STACKSET→StackSetConstraint).
func scanSCConstraints(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, portfolioIDs []string) (int, int, error) {
	if len(portfolioIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, pid := range portfolioIDs {
		id := pid
		pager := servicecatalog.NewListConstraintsForPortfolioPaginator(client, &servicecatalog.ListConstraintsForPortfolioInput{PortfolioId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("servicecatalog:ListConstraintsForPortfolio %s: %w", pid, perr)
			}
			for _, c := range out.ConstraintDetails {
				cid := sv(c.ConstraintId)
				if cid == "" {
					continue
				}
				var dtype string
				switch sv(c.Type) {
				case "LAUNCH":
					dtype = TypeServiceCatalogLaunchRoleConstraint
				case "NOTIFICATION":
					dtype = TypeServiceCatalogLaunchNotificationConstraint
				case "TEMPLATE":
					dtype = TypeServiceCatalogLaunchTemplateConstraint
				case "RESOURCE_UPDATE":
					dtype = TypeServiceCatalogResourceUpdateConstraint
				case "STACKSET":
					dtype = TypeServiceCatalogStackSetConstraint
				default:
					continue
				}
				arn := scARN(region, acct.ID, "constraint", cid)
				label := cid
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: dtype, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "servicecatalog constraints")
}
