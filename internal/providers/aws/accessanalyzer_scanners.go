package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:accessanalyzer",
		fn:   scanAccessAnalyzer,
		emits: []coverage.TypeDecl{
			{Service: "accessanalyzer", DiscoType: TypeAccessAnalyzerAnalyzer, Leaf: true},
		},
	})
}

// accessAnalyzerAPI is the narrow surface scanAccessAnalyzerAnalyzers uses; the
// SDK's *accessanalyzer.Client satisfies it. ListAnalyzers populates the full
// AnalyzerSummary body — no per-item Describe fan-out needed.
type accessAnalyzerAPI interface {
	ListAnalyzers(context.Context, *accessanalyzer.ListAnalyzersInput, ...func(*accessanalyzer.Options)) (*accessanalyzer.ListAnalyzersOutput, error)
}

func scanAccessAnalyzer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := accessanalyzer.NewFromConfig(acct.cfg, func(o *accessanalyzer.Options) { o.Region = region })
	return scanAccessAnalyzerAnalyzers(ctx, client, acct, region, st, scanID)
}

func scanAccessAnalyzerAnalyzers(ctx context.Context, client accessAnalyzerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := accessanalyzer.NewListAnalyzersPaginator(client, &accessanalyzer.ListAnalyzersInput{})
	return pageScan(ctx, "accessanalyzer:ListAnalyzers", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*accessanalyzer.ListAnalyzersOutput, error) { return p.NextPage(c) },
		func(o *accessanalyzer.ListAnalyzersOutput) []aatypes.AnalyzerSummary { return o.Analyzers },
		func(a aatypes.AnalyzerSummary) *store.Resource {
			arn := sv(a.Arn)
			if arn == "" {
				return nil
			}
			name := sv(a.Name)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAccessAnalyzerAnalyzer,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
		})
}
