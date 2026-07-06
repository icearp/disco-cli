package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/lookoutequipment"
)

func init() {
	registerService(serviceEntry{
		name: "aws:lookout-equipment",
		fn:   scanLookoutEquipment,
		emits: []coverage.TypeDecl{
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentInferenceScheduler},
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentDataset, Leaf: true},
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentLabelGroup, Leaf: true},
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentModel, Leaf: true},
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentModelVersion},
		},
	})
}

// lookoutEquipmentAPI is the narrow set of Lookout for Equipment operations
// called by the scanLookoutEquipment sub-phases.
type lookoutEquipmentAPI interface {
	ListInferenceSchedulers(context.Context, *lookoutequipment.ListInferenceSchedulersInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListInferenceSchedulersOutput, error)
	DescribeInferenceScheduler(context.Context, *lookoutequipment.DescribeInferenceSchedulerInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.DescribeInferenceSchedulerOutput, error)
	ListDatasets(context.Context, *lookoutequipment.ListDatasetsInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListDatasetsOutput, error)
	ListLabelGroups(context.Context, *lookoutequipment.ListLabelGroupsInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListLabelGroupsOutput, error)
	ListModels(context.Context, *lookoutequipment.ListModelsInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListModelsOutput, error)
	ListModelVersions(context.Context, *lookoutequipment.ListModelVersionsInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListModelVersionsOutput, error)
}

// scanLookoutEquipment discovers Lookout for Equipment inference schedulers,
// datasets, label groups, models, and model versions.
func scanLookoutEquipment(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lookoutequipment.NewFromConfig(acct.cfg, func(o *lookoutequipment.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLEInferenceSchedulers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLEDatasets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLELabelGroups(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	modelNames, t, i, ferr := scanLEModels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanLEModelVersions(ctx, client, acct, region, st, scanID, modelNames)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanLEInferenceSchedulers(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListInferenceSchedulers(ctx, &lookoutequipment.ListInferenceSchedulersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lookoutequipment:ListInferenceSchedulers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lookoutequipment:ListInferenceSchedulers: %w", err)
		}
		for _, s := range out.InferenceSchedulerSummaries {
			arn := sv(s.InferenceSchedulerArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			// Enrich with DescribeInferenceScheduler body — RoleArn,
			// ServerSideKmsKeyId, DataInput/OutputConfiguration aren't on the
			// list-summary shape. Fall back to summary on per-row failure.
			attrs := mustJSON(s)
			if s.InferenceSchedulerName != nil {
				dout, derr := client.DescribeInferenceScheduler(ctx, &lookoutequipment.DescribeInferenceSchedulerInput{InferenceSchedulerName: s.InferenceSchedulerName})
				if derr != nil {
					if isAccessDenied(derr) {
						_ = skipIfAccessDenied(st, "lookoutequipment:DescribeInferenceScheduler", acct.ID, region, derr)
					}
				} else if dout != nil {
					attrs = mustJSON(dout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentInferenceScheduler, NativeID: arn,
				Name: s.InferenceSchedulerName, Region: &region, Status: &status,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "lookout-equipment inference-schedulers")
}

func scanLEDatasets(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lookoutequipment.NewListDatasetsPaginator(client, &lookoutequipment.ListDatasetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lookoutequipment:ListDatasets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lookoutequipment:ListDatasets: %w", err)
		}
		for _, d := range out.DatasetSummaries {
			arn := sv(d.DatasetArn)
			if arn == "" {
				continue
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentDataset, NativeID: arn,
				Name: d.DatasetName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lookout-equipment datasets")
}

func scanLELabelGroups(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := lookoutequipment.NewListLabelGroupsPaginator(client, &lookoutequipment.ListLabelGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lookoutequipment:ListLabelGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lookoutequipment:ListLabelGroups: %w", err)
		}
		for _, g := range out.LabelGroupSummaries {
			arn := sv(g.LabelGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentLabelGroup, NativeID: arn,
				Name: g.LabelGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lookout-equipment label-groups")
}

// scanLEModels returns model names so the model-version phase can fan out
// (ListModelVersions requires ModelName).
func scanLEModels(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := lookoutequipment.NewListModelsPaginator(client, &lookoutequipment.ListModelsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "lookoutequipment:ListModels", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("lookoutequipment:ListModels: %w", err)
		}
		for _, m := range out.ModelSummaries {
			arn := sv(m.ModelArn)
			if arn == "" {
				continue
			}
			if n := sv(m.ModelName); n != "" {
				names = append(names, n)
			}
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentModel, NativeID: arn,
				Name: m.ModelName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "lookout-equipment models")
	return names, t, i, err
}

// scanLEModelVersions fans out over each model. ModelVersionSummary carries no
// ARN; the NativeID is synthesized as {modelArn}/version/{n}. ModelArn on the
// summary feeds the model-version → model resolver.
func scanLEModelVersions(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, modelNames []string) (int, int, error) {
	if len(modelNames) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, modelName := range modelNames {
		name := modelName
		pager := lookoutequipment.NewListModelVersionsPaginator(client, &lookoutequipment.ListModelVersionsInput{ModelName: &name})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "lookoutequipment:ListModelVersions", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("lookoutequipment:ListModelVersions %s: %w", modelName, err)
			}
			for _, v := range out.ModelVersionSummaries {
				modelARN := sv(v.ModelArn)
				if modelARN == "" || v.ModelVersion == nil {
					continue
				}
				arn := fmt.Sprintf("%s/version/%d", modelARN, *v.ModelVersion)
				label := fmt.Sprintf("%s/%d", sv(v.ModelName), *v.ModelVersion)
				status := string(v.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeLookoutEquipmentModelVersion, NativeID: arn,
					Name: &label, Region: &region, Status: &status,
					AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "lookout-equipment model-versions")
}
