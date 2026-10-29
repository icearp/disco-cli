package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lookoutequipment"
	letypes "github.com/aws/aws-sdk-go-v2/service/lookoutequipment/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLookoutEquipmentInferenceScheduler, Service: "lookout-equipment"})
	registerType(restype.Descriptor{Type: TypeLookoutEquipmentDataset, Service: "lookout-equipment"})
	registerType(restype.Descriptor{Type: TypeLookoutEquipmentLabelGroup, Service: "lookout-equipment"})
	registerType(restype.Descriptor{Type: TypeLookoutEquipmentModel, Service: "lookout-equipment"})
	registerType(restype.Descriptor{Type: TypeLookoutEquipmentModelVersion, Service: "lookout-equipment"})
	registerService(serviceEntry{
		name: "aws:lookout-equipment",
		fn:   scanLookoutEquipment,
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
	ListTagsForResource(context.Context, *lookoutequipment.ListTagsForResourceInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListTagsForResourceOutput, error)
}

const (
	leTagsOp = "lookoutequipment:ListTagsForResource"
	// lePageCap is the MaxResults maximum of every Lookout for Equipment
	// listing read here.
	lePageCap = 500
)

// leNotFound is the not-found code that ListTagsForResource,
// DescribeInferenceScheduler and ListModelVersions each model: the resource
// (for ListModelVersions, the model) was deleted after it was listed.
var leNotFound = []string{"ResourceNotFoundException"}

// errLEVersionNoARN fails the tag read of a model version listed without the
// ModelVersionArn ListTagsForResource needs; the row is stored without tags.
var errLEVersionNoARN = errors.New("the model version was listed without a ModelVersionArn, so its tags were not read; report it if it recurs")

// errLESchedulerNoName fails the detail read of an inference scheduler listed
// without the name DescribeInferenceScheduler takes; the row is stored as its
// list element.
var errLESchedulerNoName = errors.New("the inference scheduler was listed without a name, so its details were not read; report it if it recurs")

// scanLookoutEquipment discovers Lookout for Equipment inference schedulers,
// datasets, label groups, models and each model's versions, each with its
// tags (leTags). Every listing runs as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do, and so does a first phase that stores nothing and fails only with
// NXDOMAIN, since the region does not serve the service (runHostPhases). The
// model versions are read for the models stored this scan. The tag reads'
// denials are one warning for the whole scan.
func scanLookoutEquipment(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := lookoutequipment.NewFromConfig(acct.cfg, func(o *lookoutequipment.Options) { o.Region = region })
	tags := leTags(client).sharingDenials()
	var models []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "lookoutequipment", 0, []hostPhase{
		{op: "lookoutequipment:ListInferenceSchedulers", run: func(p *phaseTotals) bool {
			return scanLEInferenceSchedulers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "lookoutequipment:ListDatasets", run: func(p *phaseTotals) bool {
			return scanLEDatasets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "lookoutequipment:ListLabelGroups", run: func(p *phaseTotals) bool {
			return scanLELabelGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "lookoutequipment:ListModels", run: func(p *phaseTotals) (failed bool) {
			models, failed = scanLEModels(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "lookoutequipment:ListModelVersions", run: func(p *phaseTotals) bool {
			return scanLEModelVersions(ctx, client, acct, region, st, scanID, models, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// leTags reads a row's tags with ListTagsForResource, which the Service
// Reference lists for every type stored here (dataset, inference-scheduler,
// label-group, model, model-version). It is sent the ARN the row's listing
// returned (leTagARN); the op takes no page token and returns every tag at
// once. ResourceNotFoundException, its modeled not-found, drops a resource
// deleted after it was listed.
func leTags(client lookoutEquipmentAPI) rowTagReader {
	return rowTagReader{
		op:       leTagsOp,
		notFound: leNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn, err := leTagARN(r)
			if err != nil {
				return nil, err
			}
			out, err := client.ListTagsForResource(ctx, &lookoutequipment.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t letypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// leTagARN is the ARN a row's tags are read with: its NativeID, the ARN its
// listing returned, for every type but a model version. A model version's
// NativeID is built ({modelArn}/version/{n}) and is not its ARN, so it is read
// with the ModelVersionArn of its listed element, kept in its attributes.
func leTagARN(r *store.Resource) (string, error) {
	if r.Type != TypeLookoutEquipmentModelVersion {
		return r.NativeID, nil
	}
	var el struct{ ModelVersionArn string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &el); err != nil {
		return "", fmt.Errorf("model version attributes: %w", err)
	}
	if el.ModelVersionArn == "" {
		return "", errLEVersionNoARN
	}
	return el.ModelVersionArn, nil
}

// leRow is the row of a listed element stored under its ARN, named name; an
// element without an ARN is not stored (arnRow), and what names such
// elements in the listing's warning.
func leRow(acct *account, region, scanID, typ, what string, arn, name *string, status string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, Status: nonEmptyPtr(status),
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanLEInferenceSchedulers stores the inference schedulers, each page
// written as soon as its rows' details and tags are read. A row's attributes
// are its DescribeInferenceScheduler body, which carries what the list
// summary lacks (RoleArn, ServerSideKmsKeyId, the data input and output
// configurations). A scheduler the Describe reports not found was deleted
// since it was listed and is not stored; any other failed Describe stores
// the list summary as the row (readRowDetails). It reports whether a write
// failed, after which the scan must stop.
func scanLEInferenceSchedulers(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: "lookoutequipment:DescribeInferenceScheduler", what: "inference scheduler(s)", notFound: leNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			if r.Name == nil {
				return nil, errLESchedulerNoName
			}
			out, err := client.DescribeInferenceScheduler(ctx, &lookoutequipment.DescribeInferenceSchedulerInput{InferenceSchedulerName: r.Name})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "lookoutequipment:ListInferenceSchedulers",
		func(ctx context.Context, token *string) ([]letypes.InferenceSchedulerSummary, *string, error) {
			out, err := client.ListInferenceSchedulers(ctx, &lookoutequipment.ListInferenceSchedulersInput{MaxResults: sdkaws.Int32(lePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InferenceSchedulerSummaries, out.NextToken, nil
		},
		func(s letypes.InferenceSchedulerSummary) (*store.Resource, string, string) {
			return leRow(acct, region, scanID, TypeLookoutEquipmentInferenceScheduler, "inference schedulers", s.InferenceSchedulerArn, s.InferenceSchedulerName, string(s.Status), s)
		},
		steps, tags, p)
}

// scanLEDatasets stores the datasets, each page written as soon as its
// rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanLEDatasets(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "lookoutequipment:ListDatasets",
		func(ctx context.Context, token *string) ([]letypes.DatasetSummary, *string, error) {
			out, err := client.ListDatasets(ctx, &lookoutequipment.ListDatasetsInput{MaxResults: sdkaws.Int32(lePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DatasetSummaries, out.NextToken, nil
		},
		func(d letypes.DatasetSummary) (*store.Resource, string, string) {
			return leRow(acct, region, scanID, TypeLookoutEquipmentDataset, "datasets", d.DatasetArn, d.DatasetName, string(d.Status), d)
		},
		tags, p)
}

// scanLELabelGroups stores the label groups, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which
// the scan must stop.
func scanLELabelGroups(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "lookoutequipment:ListLabelGroups",
		func(ctx context.Context, token *string) ([]letypes.LabelGroupSummary, *string, error) {
			out, err := client.ListLabelGroups(ctx, &lookoutequipment.ListLabelGroupsInput{MaxResults: sdkaws.Int32(lePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LabelGroupSummaries, out.NextToken, nil
		},
		func(g letypes.LabelGroupSummary) (*store.Resource, string, string) {
			return leRow(acct, region, scanID, TypeLookoutEquipmentLabelGroup, "label groups", g.LabelGroupArn, g.LabelGroupName, "", g)
		},
		tags, p)
}

// scanLEModels stores the models, each page written as soon as its rows'
// tags are read, and returns the stored ones that have a name, keyed by it
// (ListModelVersions takes ModelName): the parents of the model version
// phase. It reports whether a write failed, after which the scan must stop.
func scanLEModels(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "lookoutequipment:ListModels", "models", "ModelName",
		func(ctx context.Context, token *string) ([]letypes.ModelSummary, *string, error) {
			out, err := client.ListModels(ctx, &lookoutequipment.ListModelsInput{MaxResults: sdkaws.Int32(lePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelSummaries, out.NextToken, nil
		},
		func(m letypes.ModelSummary) (*store.Resource, string, string) {
			return leRow(acct, region, scanID, TypeLookoutEquipmentModel, "models", m.ModelArn, m.ModelName, string(m.Status), m)
		},
		func(m letypes.ModelSummary) string { return sv(m.ModelName) },
		tags, p)
}

// scanLEModelVersions stores each model's versions, the models read
// concurrently and each page written as soon as its rows' tags are read
// (storeEachParentPagesTags): scheduled retraining keeps adding versions, and one
// model's failure costs the others none of theirs. A version's NativeID is
// built as {modelArn}/version/{n}, the identity its rows were first stored
// under; it is not the version's ARN (model/{name}/{id}/model-version/{n}),
// which its tags are read with (leTagARN). ModelArn on the summary feeds the
// model-version → model resolver. It reports whether a write failed, after
// which the scan must stop.
func scanLEModelVersions(ctx context.Context, client lookoutEquipmentAPI, acct *account, region string, st *store.Store, scanID string, models []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "lookoutequipment:ListModelVersions", leNotFound, models,
		func(ctx context.Context, model string, token *string) ([]letypes.ModelVersionSummary, *string, error) {
			out, err := client.ListModelVersions(ctx, &lookoutequipment.ListModelVersionsInput{ModelName: &model, MaxResults: sdkaws.Int32(lePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelVersionSummaries, out.NextToken, nil
		},
		func(model string, v letypes.ModelVersionSummary) (*store.Resource, string, string) {
			modelARN := sv(v.ModelArn)
			if modelARN == "" || v.ModelVersion == nil {
				return nil, "model versions have no ModelArn or ModelVersion", cmp.Or(sv(v.ModelVersionArn), model)
			}
			label := fmt.Sprintf("%s/%d", sv(v.ModelName), *v.ModelVersion)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentModelVersion, NativeID: fmt.Sprintf("%s/version/%d", modelARN, *v.ModelVersion),
				Name: &label, Region: &region, Status: nonEmptyPtr(string(v.Status)),
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}
