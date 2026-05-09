package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	sctypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:servicecatalog",
		fn:   scanServiceCatalog,
		emits: []coverage.TypeDecl{
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogPortfolio},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogProduct, Leaf: true},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogAcceptedPortfolioShare, Leaf: true},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogCloudFormationProvisionedProduct},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogServiceAction, Leaf: true},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogTagOption, Leaf: true},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogPortfolioShare},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogPortfolioPrincipalAssociation},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogTagOptionAssociation},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogLaunchRoleConstraint},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogLaunchNotificationConstraint},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogLaunchTemplateConstraint},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogResourceUpdateConstraint},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogStackSetConstraint},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogPortfolioProductAssociation},
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogServiceActionAssociation},
		},
	})
}

// servicecatalogAPI is the narrow set of Service Catalog operations called by
// the scanServiceCatalog sub-phases.
type servicecatalogAPI interface {
	ListPortfolios(context.Context, *servicecatalog.ListPortfoliosInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListPortfoliosOutput, error)
	ListConstraintsForPortfolio(context.Context, *servicecatalog.ListConstraintsForPortfolioInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListConstraintsForPortfolioOutput, error)
	SearchProductsAsAdmin(context.Context, *servicecatalog.SearchProductsAsAdminInput, ...func(*servicecatalog.Options)) (*servicecatalog.SearchProductsAsAdminOutput, error)
	ListAcceptedPortfolioShares(context.Context, *servicecatalog.ListAcceptedPortfolioSharesInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListAcceptedPortfolioSharesOutput, error)
	SearchProvisionedProducts(context.Context, *servicecatalog.SearchProvisionedProductsInput, ...func(*servicecatalog.Options)) (*servicecatalog.SearchProvisionedProductsOutput, error)
	ListServiceActions(context.Context, *servicecatalog.ListServiceActionsInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListServiceActionsOutput, error)
	ListTagOptions(context.Context, *servicecatalog.ListTagOptionsInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListTagOptionsOutput, error)
	DescribePortfolioShares(context.Context, *servicecatalog.DescribePortfolioSharesInput, ...func(*servicecatalog.Options)) (*servicecatalog.DescribePortfolioSharesOutput, error)
	ListPrincipalsForPortfolio(context.Context, *servicecatalog.ListPrincipalsForPortfolioInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListPrincipalsForPortfolioOutput, error)
	ListResourcesForTagOption(context.Context, *servicecatalog.ListResourcesForTagOptionInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListResourcesForTagOptionOutput, error)
	DescribeConstraint(context.Context, *servicecatalog.DescribeConstraintInput, ...func(*servicecatalog.Options)) (*servicecatalog.DescribeConstraintOutput, error)
	ListPortfoliosForProduct(context.Context, *servicecatalog.ListPortfoliosForProductInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListPortfoliosForProductOutput, error)
	ListProvisioningArtifacts(context.Context, *servicecatalog.ListProvisioningArtifactsInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListProvisioningArtifactsOutput, error)
	ListServiceActionsForProvisioningArtifact(context.Context, *servicecatalog.ListServiceActionsForProvisioningArtifactInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListServiceActionsForProvisioningArtifactOutput, error)
}

// scanServiceCatalog discovers Service Catalog portfolios and products
// (admin view) in one region. Two phases: portfolios via ListPortfolios
// paginator, products via SearchProductsAsAdmin paginator. Per-portfolio
// constraint enrichment via ListConstraintsForPortfolio fan-out (errgroup
// + fanoutMed) embeds constraint summaries into the portfolio's attrs.
// Provisioned products, launch paths, share invitations, and TagOption
// associations deferred — narrow value relative to portfolio + product
// catalog itself.
func scanServiceCatalog(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := servicecatalog.NewFromConfig(acct.cfg, func(o *servicecatalog.Options) { o.Region = region })

	portfolioIDs, t, i, ferr := scanServiceCatalogPortfolios(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	var productIDs []string
	{
		ids, t, i, ferr := scanServiceCatalogProducts(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
		productIDs = ids
	}
	{
		t, i, ferr := scanServiceCatalogExtended(ctx, client, acct, region, st, scanID, portfolioIDs)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	if len(productIDs) > 0 {
		t, i, ferr := scanServiceCatalogAssociations(ctx, client, acct, region, st, scanID, productIDs)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanServiceCatalogPortfolios(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (portfolioIDs []string, total, inserted int, err error) {
	pager := servicecatalog.NewListPortfoliosPaginator(client, &servicecatalog.ListPortfoliosInput{})
	var details []portfolioWithConstraints
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:ListPortfolios", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("servicecatalog:ListPortfolios: %w", perr)
		}
		for _, p := range out.PortfolioDetails {
			if p.Id != nil {
				details = append(details, portfolioWithConstraints{detail: p})
				portfolioIDs = append(portfolioIDs, *p.Id)
			}
		}
	}
	if len(details) == 0 {
		return nil, 0, 0, nil
	}

	// Per-portfolio enrichment fan-out: constraints + member products.
	// ListProductsForPortfolio returns ProductViewDetails (with ProductARN)
	// — store ARNs so the resolver can FK-safe match scanned products.
	sem := semaphore.NewWeighted(fanoutMed)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i := range details {
		idx := i
		portfolioID := *details[i].detail.Id
		if err := sem.Acquire(gctx, 1); err != nil {
			return nil, 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)

			// Constraints
			cpager := servicecatalog.NewListConstraintsForPortfolioPaginator(client, &servicecatalog.ListConstraintsForPortfolioInput{PortfolioId: &portfolioID})
			var cs []map[string]any
			for cpager.HasMorePages() {
				out, derr := cpager.NextPage(gctx)
				if derr != nil {
					if isAccessDenied(derr) {
						break
					}
					return fmt.Errorf("servicecatalog:ListConstraintsForPortfolio %s: %w", portfolioID, derr)
				}
				for _, c := range out.ConstraintDetails {
					cs = append(cs, map[string]any{
						"ConstraintId": sv(c.ConstraintId),
						"Type":         sv(c.Type),
						"Description":  sv(c.Description),
						"Owner":        sv(c.Owner),
						"ProductId":    sv(c.ProductId),
						"PortfolioId":  sv(c.PortfolioId),
					})
				}
			}

			// Member products
			ppager := servicecatalog.NewSearchProductsAsAdminPaginator(client, &servicecatalog.SearchProductsAsAdminInput{PortfolioId: &portfolioID})
			var prods []string
			for ppager.HasMorePages() {
				out, derr := ppager.NextPage(gctx)
				if derr != nil {
					if isAccessDenied(derr) {
						break
					}
					return fmt.Errorf("servicecatalog:SearchProductsAsAdmin (portfolio %s): %w", portfolioID, derr)
				}
				for _, p := range out.ProductViewDetails {
					if arn := sv(p.ProductARN); arn != "" {
						prods = append(prods, arn)
					}
				}
			}

			mu.Lock()
			details[idx].constraints = cs
			details[idx].productARNs = prods
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return nil, 0, 0, werr
	}

	batch := make([]*store.Resource, 0, len(details))
	for _, d := range details {
		arn := sv(d.detail.ARN)
		if arn == "" {
			continue
		}
		name := sv(d.detail.DisplayName)
		batch = append(batch, &store.Resource{
			Provider:    "aws",
			AccountID:   acct.ID,
			AccountName: &acct.Name,
			Type:        TypeServiceCatalogPortfolio,
			NativeID:    arn,
			Name:        &name,
			Region:      &region,
			AttributesJSON: mustJSON(map[string]any{
				"Portfolio":   d.detail,
				"Constraints": d.constraints,
				"ProductARNs": d.productARNs,
			}),
			DiscoveredBy: scanID,
		})
	}
	if len(batch) == 0 {
		return portfolioIDs, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert servicecatalog portfolios: %w", uerr)
	}
	return portfolioIDs, len(batch), n, nil
}

type portfolioWithConstraints struct {
	detail      sctypes.PortfolioDetail
	constraints []map[string]any
	productARNs []string
}

func scanServiceCatalogProducts(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (productIDs []string, total, inserted int, err error) {
	pager := servicecatalog.NewSearchProductsAsAdminPaginator(client, &servicecatalog.SearchProductsAsAdminInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:SearchProductsAsAdmin", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("servicecatalog:SearchProductsAsAdmin: %w", perr)
		}
		for _, p := range out.ProductViewDetails {
			arn := sv(p.ProductARN)
			if arn == "" {
				continue
			}
			var name, prodID string
			if p.ProductViewSummary != nil {
				name = sv(p.ProductViewSummary.Name)
				prodID = sv(p.ProductViewSummary.ProductId)
			}
			if prodID != "" {
				productIDs = append(productIDs, prodID)
			}
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeServiceCatalogProduct,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return productIDs, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert servicecatalog products: %w", uerr)
	}
	return productIDs, len(batch), n, nil
}

// scanServiceCatalogAssociations fans out per-product to:
//
//  1. ListPortfoliosForProduct → emit aws:servicecatalog:portfolio-product-association
//     (one row per (product, portfolio) edge).
//  2. ListProvisioningArtifacts then per-(product, artifact) ListServiceActionsForProvisioningArtifact
//     → emit aws:servicecatalog:service-action-association (one row per
//     (product, artifact, service-action) tuple).
//
// Concurrency capped at fanoutMed across products. Per-product errors
// tolerate AccessDenied + ResourceNotFoundException without aborting siblings.
func scanServiceCatalogAssociations(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string, productIDs []string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu       sync.Mutex
		ppaBatch []*store.Resource
		saaBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, prodID := range productIDs {
		prodID := prodID
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)

			// Portfolio associations.
			lout, lerr := client.ListPortfoliosForProduct(gctx, &servicecatalog.ListPortfoliosForProductInput{
				ProductId: &prodID,
			})
			if lerr != nil {
				if !isAccessDenied(lerr) && !isAPIErrorCode(lerr, "ResourceNotFoundException") {
					return fmt.Errorf("servicecatalog:ListPortfoliosForProduct %s: %w", prodID, lerr)
				}
			} else {
				for _, p := range lout.PortfolioDetails {
					pid := sv(p.Id)
					if pid == "" {
						continue
					}
					arn := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:portfolio-product-association/%s/%s", region, acct.ID, pid, prodID)
					label := fmt.Sprintf("%s/%s", pid, prodID)
					mu.Lock()
					ppaBatch = append(ppaBatch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeServiceCatalogPortfolioProductAssociation, NativeID: arn,
						Name: &label, Region: &region,
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
					mu.Unlock()
				}
			}

			// Provisioning artifacts → service-action associations.
			paOut, paErr := client.ListProvisioningArtifacts(gctx, &servicecatalog.ListProvisioningArtifactsInput{
				ProductId: &prodID,
			})
			if paErr != nil {
				if isAccessDenied(paErr) || isAPIErrorCode(paErr, "ResourceNotFoundException") {
					return nil
				}
				return fmt.Errorf("servicecatalog:ListProvisioningArtifacts %s: %w", prodID, paErr)
			}
			for _, art := range paOut.ProvisioningArtifactDetails {
				artID := sv(art.Id)
				if artID == "" {
					continue
				}
				var nextToken *string
				for {
					sout, serr := client.ListServiceActionsForProvisioningArtifact(gctx, &servicecatalog.ListServiceActionsForProvisioningArtifactInput{
						ProductId:              &prodID,
						ProvisioningArtifactId: &artID,
						PageToken:              nextToken,
					})
					if serr != nil {
						if isAccessDenied(serr) || isAPIErrorCode(serr, "ResourceNotFoundException") {
							break
						}
						return fmt.Errorf("servicecatalog:ListServiceActionsForProvisioningArtifact %s/%s: %w", prodID, artID, serr)
					}
					for _, sa := range sout.ServiceActionSummaries {
						saID := sv(sa.Id)
						if saID == "" {
							continue
						}
						arn := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:service-action-association/%s/%s/%s", region, acct.ID, prodID, artID, saID)
						label := fmt.Sprintf("%s/%s/%s", prodID, artID, saID)
						mu.Lock()
						saaBatch = append(saaBatch, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeServiceCatalogServiceActionAssociation, NativeID: arn,
							Name: &label, Region: &region,
							AttributesJSON: mustJSON(sa), DiscoveredBy: scanID,
						})
						mu.Unlock()
					}
					if sout.NextPageToken == nil || *sout.NextPageToken == "" {
						break
					}
					nextToken = sout.NextPageToken
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	tp, ip, perr := upsertBatch(st, ppaBatch, "servicecatalog portfolio-product-associations")
	if perr != nil {
		return tp, ip, perr
	}
	ts, is, serr := upsertBatch(st, saaBatch, "servicecatalog service-action-associations")
	return tp + ts, ip + is, serr
}
