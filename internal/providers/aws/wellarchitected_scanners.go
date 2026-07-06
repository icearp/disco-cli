package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/wellarchitected"
)

func init() {
	registerService(serviceEntry{
		name: "aws:wellarchitected",
		fn:   scanWellArchitected,
		emits: []coverage.TypeDecl{
			{Service: "wellarchitected", DiscoType: TypeWellArchitectedWorkload, Leaf: true},
			{Service: "wellarchitected", DiscoType: TypeWellArchitectedLens, Leaf: true},
			{Service: "wellarchitected", DiscoType: TypeWellArchitectedProfile, Leaf: true},
			{Service: "wellarchitected", DiscoType: TypeWellArchitectedReviewTemplate, Leaf: true},
		},
	})
}

type wellArchitectedAPI interface {
	ListWorkloads(context.Context, *wellarchitected.ListWorkloadsInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListWorkloadsOutput, error)
	ListLenses(context.Context, *wellarchitected.ListLensesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListLensesOutput, error)
	ListProfiles(context.Context, *wellarchitected.ListProfilesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListProfilesOutput, error)
	ListReviewTemplates(context.Context, *wellarchitected.ListReviewTemplatesInput, ...func(*wellarchitected.Options)) (*wellarchitected.ListReviewTemplatesOutput, error)
}

// scanWellArchitected discovers Well-Architected workloads, lenses, profiles,
// and review templates. All expose native ARNs; the lens-review graph is left
// unscanned (Leaf inventory rows).
func scanWellArchitected(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := wellarchitected.NewFromConfig(acct.cfg, func(o *wellarchitected.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWAWorkloads(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWALenses(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWAProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWAReviewTemplates(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanWAWorkloads(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := wellarchitected.NewListWorkloadsPaginator(client, &wellarchitected.ListWorkloadsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListWorkloads", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListWorkloads: %w", err)
		}
		for _, w := range out.WorkloadSummaries {
			arn := sv(w.WorkloadArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedWorkload, NativeID: arn,
				Name: w.WorkloadName, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "wellarchitected workloads")
}

func scanWALenses(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := wellarchitected.NewListLensesPaginator(client, &wellarchitected.ListLensesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListLenses", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListLenses: %w", err)
		}
		for _, l := range out.LensSummaries {
			arn := sv(l.LensArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedLens, NativeID: arn,
				Name: l.LensName, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "wellarchitected lenses")
}

func scanWAProfiles(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := wellarchitected.NewListProfilesPaginator(client, &wellarchitected.ListProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListProfiles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListProfiles: %w", err)
		}
		for _, p := range out.ProfileSummaries {
			arn := sv(p.ProfileArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedProfile, NativeID: arn,
				Name: p.ProfileName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "wellarchitected profiles")
}

func scanWAReviewTemplates(ctx context.Context, client wellArchitectedAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := wellarchitected.NewListReviewTemplatesPaginator(client, &wellarchitected.ListReviewTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "wellarchitected:ListReviewTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("wellarchitected:ListReviewTemplates: %w", err)
		}
		for _, r := range out.ReviewTemplates {
			arn := sv(r.TemplateArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWellArchitectedReviewTemplate, NativeID: arn,
				Name: r.TemplateName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "wellarchitected review-templates")
}
