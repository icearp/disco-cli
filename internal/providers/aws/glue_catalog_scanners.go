package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueCrawler},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueConnection},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueClassifier, Leaf: true},
	)
}

func scanGlueCatalog(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGlueCrawlers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueConnections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueClassifiers(ctx, client, acct, region, st, scanID) },
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

func scanGlueCrawlers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetCrawlersPaginator(client, &glue.GetCrawlersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetCrawlers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetCrawlers: %w", perr)
		}
		for _, c := range out.Crawlers {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueCrawler,
				NativeID:       glueResourceARN(region, acct.ID, "crawler", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue crawlers: %w", uerr)
	}
	return len(batch), n, nil
}

func scanGlueConnections(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetConnectionsPaginator(client, &glue.GetConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetConnections", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetConnections: %w", perr)
		}
		for _, c := range out.ConnectionList {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueConnection,
				NativeID:       glueResourceARN(region, acct.ID, "connection", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue connections: %w", uerr)
	}
	return len(batch), n, nil
}

// glueClassifierName extracts the name from a Classifier tagged-union — any
// of CsvClassifier / GrokClassifier / JsonClassifier / XMLClassifier may be
// non-nil. SDK returns one variant per row.
func glueClassifierName(c gluetypes.Classifier) string {
	switch {
	case c.CsvClassifier != nil:
		return sv(c.CsvClassifier.Name)
	case c.GrokClassifier != nil:
		return sv(c.GrokClassifier.Name)
	case c.JsonClassifier != nil:
		return sv(c.JsonClassifier.Name)
	case c.XMLClassifier != nil:
		return sv(c.XMLClassifier.Name)
	}
	return ""
}

func scanGlueClassifiers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetClassifiersPaginator(client, &glue.GetClassifiersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetClassifiers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetClassifiers: %w", perr)
		}
		for _, c := range out.Classifiers {
			name := glueClassifierName(c)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueClassifier,
				NativeID:       glueResourceARN(region, acct.ID, "classifier", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue classifiers: %w", uerr)
	}
	return len(batch), n, nil
}
