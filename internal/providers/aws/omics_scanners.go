package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/omics"
)

func init() {
	registerService(serviceEntry{
		name: "aws:omics",
		fn:   scanOmics,
		emits: []coverage.TypeDecl{
			{Service: "omics", DiscoType: TypeOmicsAnnotationStore},
			{Service: "omics", DiscoType: TypeOmicsConfiguration, Leaf: true},
			{Service: "omics", DiscoType: TypeOmicsReferenceStore},
			{Service: "omics", DiscoType: TypeOmicsRunGroup, Leaf: true},
			{Service: "omics", DiscoType: TypeOmicsSequenceStore},
			{Service: "omics", DiscoType: TypeOmicsVariantStore},
			{Service: "omics", DiscoType: TypeOmicsWorkflow, Leaf: true},
			{Service: "omics", DiscoType: TypeOmicsWorkflowVersion},
			{Service: "omics", DiscoType: TypeOmicsAnnotationStoreVersion},
			{Service: "omics", DiscoType: TypeOmicsReference},
			{Service: "omics", DiscoType: TypeOmicsRunCache, Leaf: true},
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
	ListAnnotationStoreVersions(context.Context, *omics.ListAnnotationStoreVersionsInput, ...func(*omics.Options)) (*omics.ListAnnotationStoreVersionsOutput, error)
	ListReferences(context.Context, *omics.ListReferencesInput, ...func(*omics.Options)) (*omics.ListReferencesOutput, error)
	ListRunCaches(context.Context, *omics.ListRunCachesInput, ...func(*omics.Options)) (*omics.ListRunCachesOutput, error)
}

func scanOmics(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := omics.NewFromConfig(acct.cfg, func(o *omics.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOmicsConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsRunGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsSequenceStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsVariantStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOmicsRunCaches(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Annotation stores + per-store versions (ListAnnotationStoreVersions requires the store name).
	asNames, t, i, ferr := scanOmicsAnnotationStores(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, name := range asNames {
		t, i, perr := scanOmicsAnnotationStoreVersions(ctx, client, acct, region, st, scanID, name)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Reference stores + per-store references (ListReferences requires the store id).
	rsIDs, t, i, ferr := scanOmicsReferenceStores(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, id := range rsIDs {
		t, i, perr := scanOmicsReferences(ctx, client, acct, region, st, scanID, id)
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

func scanOmicsAnnotationStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := omics.NewListAnnotationStoresPaginator(client, &omics.ListAnnotationStoresInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return nil, 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListAnnotationStores", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("omics:ListAnnotationStores: %w", perr)
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
			if n := sv(s.Name); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsAnnotationStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "omics annotation-stores")
	return names, t, i, err
}

// scanOmicsAnnotationStoreVersions lists versions for one annotation store
// (ListAnnotationStoreVersions requires the store Name). NativeID = the version
// ARN; the resolver wires version→annotation-store via StoreId.
func scanOmicsAnnotationStoreVersions(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID, storeName string) (int, int, error) {
	name := storeName
	pager := omics.NewListAnnotationStoreVersionsPaginator(client, &omics.ListAnnotationStoreVersionsInput{Name: &name})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListAnnotationStoreVersions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListAnnotationStoreVersions: %w", perr)
		}
		for _, v := range out.AnnotationStoreVersions {
			arn := sv(v.VersionArn)
			if arn == "" {
				continue
			}
			label := sv(v.VersionName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsAnnotationStoreVersion, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics annotation-store-versions")
}

func scanOmicsConfigurations(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListConfigurationsPaginator(client, &omics.ListConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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

func scanOmicsReferenceStores(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := omics.NewListReferenceStoresPaginator(client, &omics.ListReferenceStoresInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return nil, 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListReferenceStores", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("omics:ListReferenceStores: %w", perr)
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
			if id := sv(r.Id); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsReferenceStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "omics reference-stores")
	return ids, t, i, err
}

// scanOmicsReferences lists references for one reference store (ListReferences
// requires ReferenceStoreId). NativeID = the reference ARN; the resolver wires
// reference→reference-store via ReferenceStoreId.
func scanOmicsReferences(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID, refStoreID string) (int, int, error) {
	rsid := refStoreID
	pager := omics.NewListReferencesPaginator(client, &omics.ListReferencesInput{ReferenceStoreId: &rsid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListReferences", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListReferences: %w", perr)
		}
		for _, ref := range out.References {
			arn := sv(ref.Arn)
			if arn == "" {
				continue
			}
			label := sv(ref.Name)
			if label == "" {
				label = sv(ref.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsReference, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ref), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics references")
}

// scanOmicsRunCaches lists run caches account-wide. Leaf — no outbound edges.
func scanOmicsRunCaches(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListRunCachesPaginator(client, &omics.ListRunCachesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr)
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "omics:ListRunCaches", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("omics:ListRunCaches: %w", perr)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOmicsRunCache, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "omics run-caches")
}

func scanOmicsRunGroups(ctx context.Context, client omicsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := omics.NewListRunGroupsPaginator(client, &omics.ListRunGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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
			if isServiceNotAvailableInRegion(perr) {
				return nil, 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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
			if isServiceNotAvailableInRegion(perr) {
				return 0, 0, markServiceUnavailable(perr) // whole service absent in this region
			}
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
