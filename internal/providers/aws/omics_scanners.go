package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/omics"
)

func init() {
	registerService(serviceEntry{
		name: "aws:omics",
		fn:   scanOmics,
		emits: []coverage.TypeDecl{
			{Service: "omics", DiscoType: TypeOmicsAnnotationStore},
			{Service: "omics", DiscoType: TypeOmicsConfiguration},
			{Service: "omics", DiscoType: TypeOmicsReferenceStore},
			{Service: "omics", DiscoType: TypeOmicsRunGroup},
			{Service: "omics", DiscoType: TypeOmicsSequenceStore},
			{Service: "omics", DiscoType: TypeOmicsVariantStore},
			{Service: "omics", DiscoType: TypeOmicsWorkflow},
			{Service: "omics", DiscoType: TypeOmicsWorkflowVersion},
		},
	})
}

type omicsAPI interface {
	ListAnnotationStores(context.Context, *omics.ListAnnotationStoresInput, ...func(*omics.Options)) (*omics.ListAnnotationStoresOutput, error)
	ListConfigurations(context.Context, *omics.ListConfigurationsInput, ...func(*omics.Options)) (*omics.ListConfigurationsOutput, error)
	ListReferenceStores(context.Context, *omics.ListReferenceStoresInput, ...func(*omics.Options)) (*omics.ListReferenceStoresOutput, error)
	ListRunGroups(context.Context, *omics.ListRunGroupsInput, ...func(*omics.Options)) (*omics.ListRunGroupsOutput, error)
	ListSequenceStores(context.Context, *omics.ListSequenceStoresInput, ...func(*omics.Options)) (*omics.ListSequenceStoresOutput, error)
	ListVariantStores(context.Context, *omics.ListVariantStoresInput, ...func(*omics.Options)) (*omics.ListVariantStoresOutput, error)
	ListWorkflows(context.Context, *omics.ListWorkflowsInput, ...func(*omics.Options)) (*omics.ListWorkflowsOutput, error)
	ListWorkflowVersions(context.Context, *omics.ListWorkflowVersionsInput, ...func(*omics.Options)) (*omics.ListWorkflowVersionsOutput, error)
}

func scanOmics(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := omics.NewFromConfig(acct.cfg, func(o *omics.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOmicsAnnotationStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsReferenceStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsRunGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsSequenceStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsVariantStores(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Workflows + per-workflow versions.
	wfIDs, t, i, ferr := scanOmicsWorkflows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, wid := range wfIDs {
		t, i, perr := scanOmicsWorkflowVersions(ctx, client, acct, region, st, scanID, wid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanOmicsAnnotationStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListAnnotationStoresPaginator(client, &omics.ListAnnotationStoresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListAnnotationStores", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListAnnotationStores: %w", perr)
		}
		for _, s := range out.AnnotationStores {
			arn := sv(s.StoreArn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsAnnotationStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics annotation-stores")
}

func scanOmicsConfigurations(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListConfigurationsPaginator(client, &omics.ListConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListConfigurations: %w", perr)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics configurations")
}

func scanOmicsReferenceStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListReferenceStoresPaginator(client, &omics.ListReferenceStoresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListReferenceStores", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListReferenceStores: %w", perr)
		}
		for _, r := range out.ReferenceStores {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = sv(r.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsReferenceStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics reference-stores")
}

func scanOmicsRunGroups(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListRunGroupsPaginator(client, &omics.ListRunGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListRunGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListRunGroups: %w", perr)
		}
		for _, rg := range out.Items {
			arn := sv(rg.Arn)
			if arn == "" {
				continue
			}
			label := sv(rg.Name)
			if label == "" {
				label = sv(rg.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsRunGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(rg), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics run-groups")
}

func scanOmicsSequenceStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListSequenceStoresPaginator(client, &omics.ListSequenceStoresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListSequenceStores", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListSequenceStores: %w", perr)
		}
		for _, s := range out.SequenceStores {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsSequenceStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics sequence-stores")
}

func scanOmicsVariantStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListVariantStoresPaginator(client, &omics.ListVariantStoresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListVariantStores", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListVariantStores: %w", perr)
		}
		for _, s := range out.VariantStores {
			arn := sv(s.StoreArn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsVariantStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics variant-stores")
}

func scanOmicsWorkflows(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := omics.NewListWorkflowsPaginator(client, &omics.ListWorkflowsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListWorkflows", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("omics:ListWorkflows: %w", perr)
		}
		for _, w := range out.Items {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			label := sv(w.Name)
			if label == "" {
				label = sv(w.Id)
			}
			if id := sv(w.Id); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsWorkflow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "omics workflows")
	return ids, t, i, err
}

func scanOmicsWorkflowVersions(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID, workflowID string) (int, int, error) {
	wid := workflowID
	pager := omics.NewListWorkflowVersionsPaginator(client, &omics.ListWorkflowVersionsInput{WorkflowId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListWorkflowVersions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListWorkflowVersions: %w", perr)
		}
		for _, v := range out.Items {
			arn := sv(v.Arn)
			if arn == "" {
				continue
			}
			label := sv(v.VersionName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsWorkflowVersion, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics workflow-versions")
}
