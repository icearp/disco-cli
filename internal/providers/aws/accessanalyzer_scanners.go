package aws

import (
	"context"
	"fmt"

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
			{Service: "accessanalyzer", DiscoType: TypeAccessAnalyzerArchiveRule, Leaf: true},
		},
	})
}

// accessAnalyzerAPI is the narrow surface the accessanalyzer scanners use;
// *accessanalyzer.Client satisfies it. ListAnalyzers returns the full
// AnalyzerSummary body — no per-item Describe needed. ListArchiveRules is
// keyed by analyzer name, so archive rules fan out per analyzer.
type accessAnalyzerAPI interface {
	ListAnalyzers(context.Context, *accessanalyzer.ListAnalyzersInput, ...func(*accessanalyzer.Options)) (*accessanalyzer.ListAnalyzersOutput, error)
	ListArchiveRules(context.Context, *accessanalyzer.ListArchiveRulesInput, ...func(*accessanalyzer.Options)) (*accessanalyzer.ListArchiveRulesOutput, error)
}

func scanAccessAnalyzer(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := accessanalyzer.NewFromConfig(acct.cfg, func(o *accessanalyzer.Options) { o.Region = region })
	total, inserted, analyzers, err := scanAccessAnalyzerAnalyzers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanAccessAnalyzerArchiveRules(ctx, client, acct, region, st, scanID, analyzers)
	return total + t, inserted + i, err
}

// analyzerRef carries the parent identity archive rules hang off of.
type analyzerRef struct{ arn, name string }

func scanAccessAnalyzerAnalyzers(ctx context.Context, client accessAnalyzerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []analyzerRef, error) {
	var analyzers []analyzerRef
	p := accessanalyzer.NewListAnalyzersPaginator(client, &accessanalyzer.ListAnalyzersInput{})
	total, inserted, err := pageScan(ctx, "accessanalyzer:ListAnalyzers", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*accessanalyzer.ListAnalyzersOutput, error) { return p.NextPage(c) },
		func(o *accessanalyzer.ListAnalyzersOutput) []aatypes.AnalyzerSummary { return o.Analyzers },
		func(a aatypes.AnalyzerSummary) *store.Resource {
			arn := sv(a.Arn)
			if arn == "" {
				return nil
			}
			name := sv(a.Name)
			analyzers = append(analyzers, analyzerRef{arn: arn, name: name})
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
	return total, inserted, analyzers, err
}

// scanAccessAnalyzerArchiveRules fans out ListArchiveRules per analyzer.
// Archive rules carry no AWS-issued ARN — NativeID is synthesized as
// {analyzerARN}/archive-rule/{ruleName}; each rule is closure-wired to its
// parent analyzer.
func scanAccessAnalyzerArchiveRules(ctx context.Context, client accessAnalyzerAPI, acct *account, region string, st *store.Store, scanID string, analyzers []analyzerRef) (total, inserted int, err error) {
	if len(analyzers) == 0 {
		return 0, 0, nil
	}
	var (
		batch []*store.Resource
		pairs [][2]string
	)
	for _, az := range analyzers {
		parentID := store.ResourceID("aws", acct.ID, TypeAccessAnalyzerAnalyzer, az.arn)
		p := accessanalyzer.NewListArchiveRulesPaginator(client, &accessanalyzer.ListArchiveRulesInput{AnalyzerName: &az.name})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "accessanalyzer:ListArchiveRules", acct.ID, region, perr)
					break
				}
				return total, inserted, fmt.Errorf("accessanalyzer:ListArchiveRules %s: %w", az.name, perr)
			}
			for _, ar := range page.ArchiveRules {
				ruleName := sv(ar.RuleName)
				if ruleName == "" {
					continue
				}
				name := ruleName
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAccessAnalyzerArchiveRule,
					NativeID:       az.arn + "/archive-rule/" + ruleName,
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(ar.CreatedAt),
					AttributesJSON: mustJSON(ar),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
				pairs = append(pairs, [2]string{"", parentID})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return total, inserted, fmt.Errorf("upsert accessanalyzer archive-rules: %w", err)
	}
	total += len(batch)
	inserted += n
	idPairs := make([][2]string, len(batch))
	for i, r := range batch {
		idPairs[i] = [2]string{r.ID, pairs[i][1]}
	}
	if err := st.RecordHierarchyBatch(idPairs); err != nil {
		return total, inserted, fmt.Errorf("closure accessanalyzer archive-rules: %w", err)
	}
	return total, inserted, nil
}
