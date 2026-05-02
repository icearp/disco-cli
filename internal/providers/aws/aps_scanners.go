package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/amp"
)

func init() {
	registerService(serviceEntry{
		name: "aws:aps",
		fn:   scanAPS,
		emits: []coverage.TypeDecl{
			{Service: "aps", DiscoType: TypeAPSWorkspace},
			{Service: "aps", DiscoType: TypeAPSScraper},
			{Service: "aps", DiscoType: TypeAPSAnomalyDetector},
			{Service: "aps", DiscoType: TypeAPSRuleGroupsNamespace},
			{Service: "aps", DiscoType: TypeAPSResourcePolicy},
		},
	})
}

// apsAPI is the narrow surface scanAPS uses. ListWorkspaces / ListScrapers
// return full WorkspaceSummary / ScraperSummary bodies; no Describe fan-out.
type apsAPI interface {
	ListWorkspaces(context.Context, *amp.ListWorkspacesInput, ...func(*amp.Options)) (*amp.ListWorkspacesOutput, error)
	ListScrapers(context.Context, *amp.ListScrapersInput, ...func(*amp.Options)) (*amp.ListScrapersOutput, error)
	ListAnomalyDetectors(context.Context, *amp.ListAnomalyDetectorsInput, ...func(*amp.Options)) (*amp.ListAnomalyDetectorsOutput, error)
	ListRuleGroupsNamespaces(context.Context, *amp.ListRuleGroupsNamespacesInput, ...func(*amp.Options)) (*amp.ListRuleGroupsNamespacesOutput, error)
	DescribeResourcePolicy(context.Context, *amp.DescribeResourcePolicyInput, ...func(*amp.Options)) (*amp.DescribeResourcePolicyOutput, error)
}

func scanAPS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := amp.NewFromConfig(acct.cfg, func(o *amp.Options) { o.Region = region })
	w, wi, err := scanAPSWorkspaces(ctx, client, acct, region, st, scanID)
	if err != nil {
		return w, wi, err
	}
	s, si, err := scanAPSScrapers(ctx, client, acct, region, st, scanID)
	if err != nil {
		return w + s, wi + si, err
	}
	e, ei, err := scanAPSExtended(ctx, client, acct, region, st, scanID)
	if err != nil {
		return w + s + e, wi + si + ei, err
	}
	return w + s + e, wi + si + ei, nil
}

func scanAPSWorkspaces(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := amp.NewListWorkspacesPaginator(client, &amp.ListWorkspacesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "amp:ListWorkspaces", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("amp:ListWorkspaces: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Workspaces))
		for _, w := range page.Workspaces {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			name := sv(w.Alias)
			if name == "" {
				name = sv(w.WorkspaceId)
			}
			var status string
			if w.Status != nil {
				status = string(w.Status.StatusCode)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPSWorkspace,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(w.CreatedAt),
				AttributesJSON: mustJSON(w),
				TagsJSON:       mapTagsJSON(w.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert aps workspaces: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAPSScrapers(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := amp.NewListScrapersPaginator(client, &amp.ListScrapersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "amp:ListScrapers", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("amp:ListScrapers: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Scrapers))
		for _, s := range page.Scrapers {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			name := sv(s.Alias)
			if name == "" {
				name = sv(s.ScraperId)
			}
			var status string
			if s.Status != nil {
				status = string(s.Status.StatusCode)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPSScraper,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(s.CreatedAt),
				AttributesJSON: mustJSON(s),
				TagsJSON:       mapTagsJSON(s.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert aps scrapers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
