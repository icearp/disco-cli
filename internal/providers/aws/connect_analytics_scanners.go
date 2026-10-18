package aws

import (
	"context"
	"errors"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	connectListExtractionDefinitionsOp    = "connect:ListExtractionDefinitions"
	connectDescribeExtractionDefinitionOp = "connect:DescribeExtractionDefinition"
	connectListMetricsOp                  = "connect:ListMetrics"
	connectDescribeMetricOp               = "connect:DescribeMetric"
	// connectAnalyticsPageCap is the MaxResult100 cap both list ops share.
	connectAnalyticsPageCap = 100
	// connectSavedQualifier selects a metric's saved version. DescribeMetric's
	// doc says to add the $SAVED qualifier but gives no separator. The colon
	// form is inferred from two sources: the qualified-metric ARN format
	// (metric/${MetricId}:${MetricQualifier}) and DescribeView's doc, "The view
	// identifier can contain an optional qualifier, for example, :$SAVED".
	// It is unverified live for metrics.
	connectSavedQualifier = ":$SAVED"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectExtractionDefinition, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectMetric, Service: "connect"})
}

// connectAnalyticsAPI is the narrow surface for the analytics-configuration
// family: extraction definitions and custom metrics, both per-instance leaves.
type connectAnalyticsAPI interface {
	ListExtractionDefinitions(context.Context, *connect.ListExtractionDefinitionsInput, ...func(*connect.Options)) (*connect.ListExtractionDefinitionsOutput, error)
	DescribeExtractionDefinition(context.Context, *connect.DescribeExtractionDefinitionInput, ...func(*connect.Options)) (*connect.DescribeExtractionDefinitionOutput, error)
	ListMetrics(context.Context, *connect.ListMetricsInput, ...func(*connect.Options)) (*connect.ListMetricsOutput, error)
	DescribeMetric(context.Context, *connect.DescribeMetricInput, ...func(*connect.Options)) (*connect.DescribeMetricOutput, error)
}

// scanConnectAnalytics runs both phases concurrently; neither depends on the
// other. Each error passes through demoteTransient before the join, so a
// throttle on one cannot make the dispatcher classify the service transient
// and hide the other's hard error.
func scanConnectAnalytics(ctx context.Context, client connectAnalyticsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		wg             sync.WaitGroup
		t1, i1, t2, i2 int
		eerr, merr     error
	)
	wg.Go(func() {
		t1, i1, eerr = scanConnectExtractionDefinitions(ctx, client, instances, acct, region, st, scanID)
	})
	wg.Go(func() { t2, i2, merr = scanConnectMetrics(ctx, client, instances, acct, region, st, scanID) })
	wg.Wait()
	return t1 + t2, i1 + i2, errors.Join(
		demoteTransient(st, connectListExtractionDefinitionsOp, acct, region, eerr),
		demoteTransient(st, connectListMetricsOp, acct, region, merr),
	)
}

// connectListDescribe is the per-instance list-then-describe skeleton: list
// pages one instance's summaries and hands each to emit with its ARN; an ARN
// already emitted is dropped, so a summary listed twice is described and
// upserted once. A list error is classified per instance: a not-found is
// skipped and a denial warned once, and in both cases the summaries that
// instance emitted before the error are still described; any other error is
// returned and nothing is described. describe builds the row, nil to drop it;
// a describe not-found drops that row, a denial warns once.
func connectListDescribe[S any](
	ctx context.Context, st *store.Store, acct *account, region, listOp, describeOp, label string,
	instances []cttypes.InstanceSummary,
	list func(ctx context.Context, instanceID string, emit func(arn string, s S)) error,
	describe func(ctx context.Context, instanceID, arn string, s S) (*store.Resource, error),
) (int, int, error) {
	type listed struct {
		instanceID string
		summary    S
	}
	listErrs := &fanOutErrs{st: st, acct: acct, region: region, op: listOp, isSkip: isConnectNotFound}
	byARN := map[string]listed{}
	var arns []string
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		err := list(ctx, instID, func(arn string, s S) {
			if _, dup := byARN[arn]; dup {
				return
			}
			arns = append(arns, arn)
			byARN[arn] = listed{instID, s}
		})
		if err == nil {
			continue
		}
		if kerr := listErrs.keyErr(instID, err); kerr != nil {
			return 0, 0, kerr
		}
	}
	rows, err := describeFanOut(ctx, st, acct, region, describeOp, arns, isConnectNotFound,
		func(gctx context.Context, arn string) (*store.Resource, error) {
			k := byARN[arn]
			return describe(gctx, k.instanceID, arn, k.summary)
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, label)
}

// scanConnectExtractionDefinitions lists extraction-definition ids per
// instance, then describes each: only the describe carries the extraction
// configuration and the tags.
func scanConnectExtractionDefinitions(ctx context.Context, client connectAnalyticsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return connectListDescribe(ctx, st, acct, region, connectListExtractionDefinitionsOp, connectDescribeExtractionDefinitionOp, "connect extraction definitions", instances,
		func(ctx context.Context, instID string, emit func(string, string)) error {
			pager := connect.NewListExtractionDefinitionsPaginator(client, &connect.ListExtractionDefinitionsInput{InstanceId: &instID},
				func(o *connect.ListExtractionDefinitionsPaginatorOptions) {
					o.Limit = connectAnalyticsPageCap
					o.StopOnDuplicateToken = true
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				for _, s := range out.ExtractionDefinitionSummaryList {
					arn, id := sv(s.ExtractionDefinitionArn), sv(s.ExtractionDefinitionId)
					if arn != "" && id != "" {
						emit(arn, id)
					}
				}
			}
			return nil
		},
		func(gctx context.Context, instID, arn, id string) (*store.Resource, error) {
			out, err := client.DescribeExtractionDefinition(gctx, &connect.DescribeExtractionDefinitionInput{InstanceId: &instID, ExtractionDefinitionId: &id})
			if err != nil {
				return nil, err
			}
			d := out.ExtractionDefinition
			if d == nil {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectExtractionDefinition, NativeID: arn,
				Name: d.Name, Region: &region, CreatedAt: tp(d.CreatedTime),
				TagsJSON: mapTagsJSON(d.Tags), AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, nil
		})
}

// scanConnectMetrics lists the customer-managed metrics per instance, then
// describes each: only the describe carries the calculation and the tags. The
// AWS_MANAGED metrics are the built-in catalog every instance shares, so they
// are not requested. An unqualified DescribeMetric reads the published
// version, so a SAVED metric is described with the $SAVED qualifier. NativeID
// is the summary's ARN, because the describe's ARN may carry a qualifier.
func scanConnectMetrics(ctx context.Context, client connectAnalyticsAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var savedRejected skipTally
	total, inserted, err := connectListDescribe(ctx, st, acct, region, connectListMetricsOp, connectDescribeMetricOp, "connect metrics", instances,
		func(ctx context.Context, instID string, emit func(string, cttypes.MetricSummary)) error {
			pager := connect.NewListMetricsPaginator(client, &connect.ListMetricsInput{InstanceId: &instID, Type: cttypes.MetricTypeCustomerManaged},
				func(o *connect.ListMetricsPaginatorOptions) {
					o.Limit = connectAnalyticsPageCap
					o.StopOnDuplicateToken = true
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return err
				}
				for _, s := range out.MetricSummaryList {
					if sv(s.Arn) != "" && sv(s.Id) != "" {
						emit(*s.Arn, s)
					}
				}
			}
			return nil
		},
		func(gctx context.Context, instID, arn string, s cttypes.MetricSummary) (*store.Resource, error) {
			saved := s.Status == cttypes.MetricStatusSaved
			metricID := *s.Id
			if saved {
				metricID += connectSavedQualifier
			}
			out, err := client.DescribeMetric(gctx, &connect.DescribeMetricInput{InstanceId: &instID, MetricId: &metricID})
			// The qualifier form is unverified, so its rejection costs only
			// that metric, with one warning, never the phase.
			if saved && savedRejected.dropCode("ValidationException", metricID, err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			m := out.Metric
			if m == nil {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectMetric, NativeID: arn,
				Name: m.Name, Region: &region, Status: nonEmptyPtr(string(m.Status)), CreatedAt: tp(m.CreatedTime),
				TagsJSON: mapTagsJSON(m.Tags), AttributesJSON: mustJSON(flattenConnectMetric(m)), DiscoveredBy: scanID,
			}, nil
		})
	savedRejected.warnCode(st, connectDescribeMetricOp, acct, region, "ValidationException", "saved metric(s)",
		"the metric id"+connectSavedQualifier+" describe form is inferred from DescribeView's doc, not documented for metrics")
	return total, inserted, err
}

// connectMetricAttrs is the DescribeMetric body with its CreatedByInfo unions
// keyed by member name. Each union member marshals as an unlabelled
// {"Value": ...}, so a Connect user and an IAM identity would both store as
// {"Value":"<arn>"}. A member newer than the pinned SDK is kept under
// UnknownMember with its tag (precedent: supportPermitAttrs).
type connectMetricAttrs struct {
	*cttypes.MetricDefinition
	CreatedUser      connectCreatedByAttrs
	LastModifiedUser connectCreatedByAttrs
}

type connectCreatedByAttrs struct {
	AWSIdentityArn *string                     `json:",omitempty"`
	ConnectUserArn *string                     `json:",omitempty"`
	UnknownMember  *cttypes.UnknownUnionMember `json:",omitempty"`
}

func flattenConnectMetric(m *cttypes.MetricDefinition) connectMetricAttrs {
	return connectMetricAttrs{
		MetricDefinition: m,
		CreatedUser:      flattenConnectCreatedBy(m.CreatedUser),
		LastModifiedUser: flattenConnectCreatedBy(m.LastModifiedUser),
	}
}

func flattenConnectCreatedBy(u cttypes.CreatedByInfo) connectCreatedByAttrs {
	var a connectCreatedByAttrs
	switch v := u.(type) {
	case *cttypes.CreatedByInfoMemberAWSIdentityArn:
		a.AWSIdentityArn = &v.Value
	case *cttypes.CreatedByInfoMemberConnectUserArn:
		a.ConnectUserArn = &v.Value
	case *cttypes.UnknownUnionMember:
		a.UnknownMember = v
	}
	return a
}
