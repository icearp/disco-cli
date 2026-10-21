package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/machinelearning"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// The Describe* elements carry no credential: the database passwords
	// exist only on the CreateDataSourceFrom{RDS,Redshift} inputs
	// (RDSDatabaseCredentials / RedshiftDatabaseCredentials), and the stored
	// RDSMetadata / RedshiftMetadata keep the user name alone. No Redact rule.
	registerType(restype.Descriptor{Type: TypeMachineLearningDataSource, Service: "machinelearning"})
	registerType(restype.Descriptor{Type: TypeMachineLearningMLModel, Service: "machinelearning"})
	registerType(restype.Descriptor{Type: TypeMachineLearningEvaluation, Service: "machinelearning"})
	registerType(restype.Descriptor{Type: TypeMachineLearningBatchPrediction, Service: "machinelearning"})
	registerService(serviceEntry{
		name: "aws:machinelearning",
		fn:   scanMachineLearning,
	})
}

const (
	// mlPageLimit is the model's PageLimit cap (range 1-100).
	mlPageLimit = 100

	mlDescribeDataSourcesOp      = "machinelearning:DescribeDataSources"
	mlDescribeMLModelsOp         = "machinelearning:DescribeMLModels"
	mlDescribeEvaluationsOp      = "machinelearning:DescribeEvaluations"
	mlDescribeBatchPredictionsOp = "machinelearning:DescribeBatchPredictions"
)

type machineLearningAPI interface {
	DescribeDataSources(context.Context, *machinelearning.DescribeDataSourcesInput, ...func(*machinelearning.Options)) (*machinelearning.DescribeDataSourcesOutput, error)
	DescribeMLModels(context.Context, *machinelearning.DescribeMLModelsInput, ...func(*machinelearning.Options)) (*machinelearning.DescribeMLModelsOutput, error)
	DescribeEvaluations(context.Context, *machinelearning.DescribeEvaluationsInput, ...func(*machinelearning.Options)) (*machinelearning.DescribeEvaluationsOutput, error)
	DescribeBatchPredictions(context.Context, *machinelearning.DescribeBatchPredictionsInput, ...func(*machinelearning.Options)) (*machinelearning.DescribeBatchPredictionsOutput, error)
}

// scanMachineLearning lists the four Amazon ML object kinds. AWS no longer
// accepts new Amazon ML users; its documentation still serves existing ones,
// and no shutdown date was found as of 2026-10-20, so the service is scanned. The SDK endpoint table lists
// us-east-1 and eu-west-1 only, which scopes every other region out before
// dispatch.
func scanMachineLearning(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := machinelearning.NewFromConfig(acct.cfg, func(o *machinelearning.Options) { o.Region = region })
	return scanMachineLearningAll(ctx, client, acct, region, st, scanID)
}

// scanMachineLearningAll runs every phase even when one fails, and passes each
// phase's error through demoteTransient before joining, so a throttle on one
// phase cannot make the dispatcher classify the whole service transient and
// report the other phases' hard errors as transient ones.
func scanMachineLearningAll(ctx context.Context, client machineLearningAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if err := gateMachineLearning(ctx, client); err != nil {
		return 0, 0, err
	}
	phases := []struct {
		op string
		fn func(context.Context, machineLearningAPI, *account, string, *store.Store, string) (int, int, error)
	}{
		{mlDescribeDataSourcesOp, scanMLDataSources},
		{mlDescribeMLModelsOp, scanMLModels},
		{mlDescribeEvaluationsOp, scanMLEvaluations},
		{mlDescribeBatchPredictionsOp, scanMLBatchPredictions},
	}
	var (
		total, inserted int
		errs            []error
	)
	for _, p := range phases {
		t, i, err := p.fn(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, p.op, acct, region, err))
	}
	return total, inserted, errors.Join(errs...)
}

// isMachineLearningClosed matches the two answers taken to mean the account
// cannot use Amazon ML, which AWS no longer offers to new users:
//   - an AccessDeniedException whose message contains "no longer available to
//     new customers". The full message, "Thank you for your interest in Amazon
//     Machine Learning. AmazonML is no longer available to new customers. Please
//     consider using Amazon SageMaker.", was reported in 2019 on
//     DescribeDataSources, DescribeEvaluations and DescribeBatchPredictions in
//     us-east-1 (https://github.com/rusoto/rusoto/issues/1306). We have not seen
//     it live.
//   - an AccessDeniedException with an empty message: the repo-wide
//     isClosedToNewCustomers predicate, kept as a fallback.
//
// A denial whose message has neither shape is an IAM denial and still warns.
func isMachineLearningClosed(err error) bool {
	return isAccessDeniedWithMessage(err, "no longer available to new customers") || isClosedToNewCustomers(err)
}

// gateMachineLearning probes DescribeDataSources once. None of the Describe
// operations models a "not subscribed" error, so an isMachineLearningClosed
// answer ends the scan as not entitled. Any other result is left to the phases.
func gateMachineLearning(ctx context.Context, client machineLearningAPI) error {
	_, err := client.DescribeDataSources(ctx, &machinelearning.DescribeDataSourcesInput{Limit: sdkaws.Int32(1)})
	if err != nil && isMachineLearningClosed(err) {
		return markServiceNotEntitled(err)
	}
	return nil
}

// mlNativeID builds the Service Reference ARN
// arn:${Partition}:machinelearning:${Region}:${Account}:<kind>/<id>; the API
// returns ids only. Shared with machinelearning_resolvers.go.
func mlNativeID(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:machinelearning:%s:%s:%s/%s", region, acctID, kind, id)
}

// mlFields are the parts of a listed element that become row columns.
type mlFields struct {
	id, name  *string
	status    string
	createdAt *time.Time
}

// scanMLObjects pages one Describe* operation with a hand-rolled loop: the SDK
// paginator either follows a repeated token forever or, with
// StopOnDuplicateToken, stops without an error. Every page is stored before
// its token is checked, so a failing later page keeps the earlier rows. A
// DELETED object is stored with its status: the Describe operations list it
// and disco stores every listed item.
func scanMLObjects[T any](
	ctx context.Context, st *store.Store, acct *account, region, scanID string,
	op, typ, kind string,
	list func(ctx context.Context, next *string) ([]T, *string, error),
	fields func(T) mlFields,
) (total, inserted int, err error) {
	noID := 0
	defer func() {
		if noID > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d %s object(s) not stored: the response gave no id", noID, kind),
			})
		}
	}()
	var next *string
	for {
		items, token, lerr := list(ctx, next)
		if lerr != nil {
			if isMachineLearningClosed(lerr) {
				return total, inserted, nil
			}
			if isAccessDenied(lerr) {
				return total, inserted, skipIfAccessDenied(st, op, acct.ID, region, lerr)
			}
			return total, inserted, fmt.Errorf("%s: %w", op, lerr)
		}
		batch := make([]*store.Resource, 0, len(items))
		for _, it := range items {
			f := fields(it)
			id := sv(f.id)
			if id == "" {
				noID++
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           typ,
				NativeID:       mlNativeID(region, acct.ID, kind, id),
				Name:           f.name,
				Status:         nonEmptyPtr(f.status),
				Region:         &region,
				CreatedAt:      tp(f.createdAt),
				AttributesJSON: mustJSON(it),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert %s: %w", op, uerr)
			}
			total += len(batch)
			inserted += n
		}
		if sv(token) == "" {
			return total, inserted, nil
		}
		if sv(token) == sv(next) {
			return total, inserted, fmt.Errorf("%s: NextToken %q: %w", op, sv(token), errRepeatedNextToken)
		}
		next = token
	}
}

func scanMLDataSources(ctx context.Context, client machineLearningAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanMLObjects(ctx, st, acct, region, scanID, mlDescribeDataSourcesOp, TypeMachineLearningDataSource, "datasource",
		func(ctx context.Context, next *string) ([]mltypes.DataSource, *string, error) {
			out, err := client.DescribeDataSources(ctx, &machinelearning.DescribeDataSourcesInput{Limit: sdkaws.Int32(mlPageLimit), NextToken: next})
			if err != nil {
				return nil, nil, err
			}
			return out.Results, out.NextToken, nil
		},
		func(d mltypes.DataSource) mlFields {
			return mlFields{id: d.DataSourceId, name: d.Name, status: string(d.Status), createdAt: d.CreatedAt}
		})
}

func scanMLModels(ctx context.Context, client machineLearningAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanMLObjects(ctx, st, acct, region, scanID, mlDescribeMLModelsOp, TypeMachineLearningMLModel, "mlmodel",
		func(ctx context.Context, next *string) ([]mltypes.MLModel, *string, error) {
			out, err := client.DescribeMLModels(ctx, &machinelearning.DescribeMLModelsInput{Limit: sdkaws.Int32(mlPageLimit), NextToken: next})
			if err != nil {
				return nil, nil, err
			}
			return out.Results, out.NextToken, nil
		},
		func(m mltypes.MLModel) mlFields {
			return mlFields{id: m.MLModelId, name: m.Name, status: string(m.Status), createdAt: m.CreatedAt}
		})
}

// scanMLEvaluations stores evaluations as resources although each records one
// run: an evaluation is a named object with its own ARN, kept until
// DeleteEvaluation (AWS may remove objects unused for over two years), and
// renamed with UpdateEvaluation and tagged with AddTags like the other kinds.
func scanMLEvaluations(ctx context.Context, client machineLearningAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanMLObjects(ctx, st, acct, region, scanID, mlDescribeEvaluationsOp, TypeMachineLearningEvaluation, "evaluation",
		func(ctx context.Context, next *string) ([]mltypes.Evaluation, *string, error) {
			out, err := client.DescribeEvaluations(ctx, &machinelearning.DescribeEvaluationsInput{Limit: sdkaws.Int32(mlPageLimit), NextToken: next})
			if err != nil {
				return nil, nil, err
			}
			return out.Results, out.NextToken, nil
		},
		func(e mltypes.Evaluation) mlFields {
			return mlFields{id: e.EvaluationId, name: e.Name, status: string(e.Status), createdAt: e.CreatedAt}
		})
}

// scanMLBatchPredictions stores batch predictions as resources for the same
// reason as evaluations: named, ARN-bearing, kept until
// DeleteBatchPrediction, renamable and taggable.
func scanMLBatchPredictions(ctx context.Context, client machineLearningAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanMLObjects(ctx, st, acct, region, scanID, mlDescribeBatchPredictionsOp, TypeMachineLearningBatchPrediction, "batchprediction",
		func(ctx context.Context, next *string) ([]mltypes.BatchPrediction, *string, error) {
			out, err := client.DescribeBatchPredictions(ctx, &machinelearning.DescribeBatchPredictionsInput{Limit: sdkaws.Int32(mlPageLimit), NextToken: next})
			if err != nil {
				return nil, nil, err
			}
			return out.Results, out.NextToken, nil
		},
		func(b mltypes.BatchPrediction) mlFields {
			return mlFields{id: b.BatchPredictionId, name: b.Name, status: string(b.Status), createdAt: b.CreatedAt}
		})
}
