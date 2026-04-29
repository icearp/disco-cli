package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
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
			{Service: "servicecatalog", DiscoType: TypeServiceCatalogProduct},
		},
	})
}

// servicecatalogAPI is the narrow set of Service Catalog operations called by
// the scanServiceCatalog sub-phases.
type servicecatalogAPI interface {
	ListPortfolios(context.Context, *servicecatalog.ListPortfoliosInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListPortfoliosOutput, error)
	ListConstraintsForPortfolio(context.Context, *servicecatalog.ListConstraintsForPortfolioInput, ...func(*servicecatalog.Options)) (*servicecatalog.ListConstraintsForPortfolioOutput, error)
	SearchProductsAsAdmin(context.Context, *servicecatalog.SearchProductsAsAdminInput, ...func(*servicecatalog.Options)) (*servicecatalog.SearchProductsAsAdminOutput, error)
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

	{
		t, i, ferr := scanServiceCatalogProducts(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	_ = portfolioIDs // reserved for future cross-resolver use

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

func scanServiceCatalogProducts(ctx context.Context, client servicecatalogAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := servicecatalog.NewSearchProductsAsAdminPaginator(client, &servicecatalog.SearchProductsAsAdminInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "servicecatalog:SearchProductsAsAdmin", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("servicecatalog:SearchProductsAsAdmin: %w", perr)
		}
		for _, p := range out.ProductViewDetails {
			arn := sv(p.ProductARN)
			if arn == "" {
				continue
			}
			var name string
			if p.ProductViewSummary != nil {
				name = sv(p.ProductViewSummary.Name)
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
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert servicecatalog products: %w", uerr)
	}
	return len(batch), n, nil
}
