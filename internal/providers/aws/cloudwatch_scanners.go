package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudWatchAlarm, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchCompositeAlarm, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchAlarmMuteRule, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchAnomalyDetector, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchInsightRule, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchMetricStream, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeCloudWatchOTelEnrichment, Service: "cloudwatch", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:cloudwatch",
		fn:   scanCloudWatch,
	})
}

const (
	cwDescribeAlarmsOp     = "cloudwatch:DescribeAlarms"
	cwListAlarmMuteRulesOp = "cloudwatch:ListAlarmMuteRules"
	cwDescribeInsightOp    = "cloudwatch:DescribeInsightRules"
	cwListMetricStreamsOp  = "cloudwatch:ListMetricStreams"
	cwGetMetricStreamOp    = "cloudwatch:GetMetricStream"
	cwTagsOp               = "cloudwatch:ListTagsForResource"
	// cwRecordPageLimit is the model's cap on the MaxRecords of
	// DescribeAlarms and ListAlarmMuteRules (range 1..100).
	cwRecordPageLimit int32 = 100
	// cwResultPageLimit is the model's cap on the MaxResults of
	// DescribeInsightRules and ListMetricStreams (range 1..500).
	cwResultPageLimit int32 = 500
)

// cwNotFound is the code ListTagsForResource and GetMetricStream model for a
// resource that does not exist.
var cwNotFound = []string{"ResourceNotFoundException"}

// cloudwatchAPI is the narrow set of CloudWatch operations called by the
// scanCloudWatch sub-phases and scanCWDashboards.
type cloudwatchAPI interface {
	DescribeAlarms(context.Context, *cloudwatch.DescribeAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error)
	ListAlarmMuteRules(context.Context, *cloudwatch.ListAlarmMuteRulesInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListAlarmMuteRulesOutput, error)
	DescribeAnomalyDetectors(context.Context, *cloudwatch.DescribeAnomalyDetectorsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAnomalyDetectorsOutput, error)
	ListDashboards(context.Context, *cloudwatch.ListDashboardsInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListDashboardsOutput, error)
	GetDashboard(context.Context, *cloudwatch.GetDashboardInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetDashboardOutput, error)
	DescribeInsightRules(context.Context, *cloudwatch.DescribeInsightRulesInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeInsightRulesOutput, error)
	ListMetricStreams(context.Context, *cloudwatch.ListMetricStreamsInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricStreamsOutput, error)
	GetMetricStream(context.Context, *cloudwatch.GetMetricStreamInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStreamOutput, error)
	GetOTelEnrichment(context.Context, *cloudwatch.GetOTelEnrichmentInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetOTelEnrichmentOutput, error)
	ListTagsForResource(context.Context, *cloudwatch.ListTagsForResourceInput, ...func(*cloudwatch.Options)) (*cloudwatch.ListTagsForResourceOutput, error)
}

// scanCloudWatch discovers the CloudWatch resources of one region. Every
// listing runs as its own phase, one at a time, in the order below
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. The alarm, alarm mute rule, insight rule and metric
// stream listings are written a page at a time with each row's tags read
// (cwTags), and the tag denials of the whole scan are one warning. Dashboards
// are global and scanned once per account (scanCloudWatchDashboards).
func scanCloudWatch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })
	tags := cwTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "cloudwatch", 0, []hostPhase{
		{op: cwDescribeAlarmsOp, run: func(p *phaseTotals) bool {
			return scanCWAlarms(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: cwListAlarmMuteRulesOp, run: func(p *phaseTotals) bool {
			return scanCWAlarmMuteRules(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "cloudwatch:DescribeAnomalyDetectors", func() (int, int, error) {
			return scanCWAnomalyDetectors(ctx, client, acct, region, st, scanID)
		}),
		{op: cwDescribeInsightOp, run: func(p *phaseTotals) bool {
			return scanCWInsightRules(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: cwListMetricStreamsOp, run: func(p *phaseTotals) bool {
			return scanCWMetricStreams(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "cloudwatch:GetOTelEnrichment", func() (int, int, error) {
			return scanCWOTelEnrichment(ctx, client, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// cwTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned, or for an insight rule the ARN
// built in the Service Reference's format. The op returns every tag in one
// response (it takes no page token); ResourceNotFoundException, which it
// models, means the resource was deleted after it was listed.
//
// The reads are one call per row; an account can hold thousands of alarms.
// The phases run one at a time and readRowTags reads fanoutMed rows at a
// time, so at most fanoutMed tag reads are in flight per scanCloudWatch or
// scanCloudWatchDashboards call.
func cwTags(client cloudwatchAPI) rowTagReader {
	return rowTagReader{
		op:       cwTagsOp,
		notFound: cwNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t cwtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// cwListedAlarm is one alarm DescribeAlarms listed: a metric alarm or a
// composite alarm (body), stored as typ.
type cwListedAlarm struct {
	typ       string
	arn, name *string
	body      any
}

// scanCWAlarms stores the metric and composite alarms under their ARNs.
// DescribeAlarms returns both; they are stored as separate disco types for
// gap analysis and filtering. Both carry an ARN the Service Reference's
// alarm resource describes, and the tag op takes it.
func scanCWAlarms(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, cwDescribeAlarmsOp,
		func(ctx context.Context, token *string) ([]cwListedAlarm, *string, error) {
			out, err := client.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
				AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm},
				MaxRecords: sdkaws.Int32(cwRecordPageLimit),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			alarms := make([]cwListedAlarm, 0, len(out.MetricAlarms)+len(out.CompositeAlarms))
			for _, a := range out.MetricAlarms {
				alarms = append(alarms, cwListedAlarm{TypeCloudWatchAlarm, a.AlarmArn, a.AlarmName, a})
			}
			for _, a := range out.CompositeAlarms {
				alarms = append(alarms, cwListedAlarm{TypeCloudWatchCompositeAlarm, a.AlarmArn, a.AlarmName, a})
			}
			return alarms, out.NextToken, nil
		},
		func(a cwListedAlarm) (*store.Resource, string, string) {
			name := sv(a.name)
			return arnRow("alarms", a.arn, a.name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: a.typ, NativeID: sv(a.arn),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a.body), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanCWAlarmMuteRules stores the alarm mute rules under their ARNs. The
// summary carries no name, so the ARN names the row.
func scanCWAlarmMuteRules(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, cwListAlarmMuteRulesOp,
		func(ctx context.Context, token *string) ([]cwtypes.AlarmMuteRuleSummary, *string, error) {
			out, err := client.ListAlarmMuteRules(ctx, &cloudwatch.ListAlarmMuteRulesInput{MaxRecords: sdkaws.Int32(cwRecordPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AlarmMuteRuleSummaries, out.NextToken, nil
		},
		func(m cwtypes.AlarmMuteRuleSummary) (*store.Resource, string, string) {
			arn := sv(m.AlarmMuteRuleArn)
			return arnRow("alarm mute rules", m.AlarmMuteRuleArn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudWatchAlarmMuteRule, NativeID: arn,
				Name: &arn, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanCWAnomalyDetectors discovers CloudWatch anomaly detection models.
// NativeID is derived from the metric coordinates because the API does not
// expose an ARN: "<Namespace>/<MetricName>/<Stat>" for single-metric detectors,
// or "<first-query-id>" for metric-math detectors.
func scanCWAnomalyDetectors(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := cloudwatch.NewDescribeAnomalyDetectorsPaginator(client, &cloudwatch.DescribeAnomalyDetectorsInput{
		MaxResults: sdkaws.Int32(100),
	})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudwatch:DescribeAnomalyDetectors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:DescribeAnomalyDetectors: %w", err)
		}
		var batch []*store.Resource
		for _, d := range out.AnomalyDetectors {
			nativeID := anomalyDetectorID(d)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchAnomalyDetector,
				NativeID:       nativeID,
				Name:           &nativeID,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch anomaly detectors: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// anomalyDetectorID produces a stable NativeID string for an anomaly detector.
// Single-metric detectors use "<Namespace>/<MetricName>/<Stat>".
// Metric-math detectors use the ID of their first metric data query.
func anomalyDetectorID(d cwtypes.AnomalyDetector) string {
	if s := d.SingleMetricAnomalyDetector; s != nil {
		return sv(s.Namespace) + "/" + sv(s.MetricName) + "/" + sv(s.Stat)
	}
	if m := d.MetricMathAnomalyDetector; m != nil && len(m.MetricDataQueries) > 0 {
		return sv(m.MetricDataQueries[0].Id)
	}
	return "unknown"
}

// scanCWInsightRules stores the Contributor Insights rules. The API returns
// no ARN, so a rule is stored under the ARN the Service Reference defines
// for it (insight-rule/<name>): the bare name is unique only per account and
// region. A rule without a name has no such ARN and is not stored.
//
// Rules with ManagedRule set (created from a service's template with
// PutManagedInsightRules) are read like the others: that op takes Tags and
// its doc says to change an existing rule's tags with TagResource.
// Unverified live.
func scanCWInsightRules(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, cwDescribeInsightOp,
		func(ctx context.Context, token *string) ([]cwtypes.InsightRule, *string, error) {
			out, err := client.DescribeInsightRules(ctx, &cloudwatch.DescribeInsightRulesInput{MaxResults: sdkaws.Int32(cwResultPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InsightRules, out.NextToken, nil
		},
		func(rule cwtypes.InsightRule) (*store.Resource, string, string) {
			name := sv(rule.Name)
			if name == "" {
				return nil, "insight rules have no name", noIdentifier
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeCloudWatchInsightRule,
				NativeID: arnFor(acct.partition(), "cloudwatch", region, acct.ID, "insight-rule/"+name),
				Name:     &name, Region: &region,
				AttributesJSON: mustJSON(rule), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

// scanCWMetricStreams stores the metric streams under their ARNs, a page at
// a time, each row's attributes replaced with its GetMetricStream body
// (RoleArn, IncludeFilters, StatisticsConfigurations). A stream
// GetMetricStream no longer finds was deleted since the listing and is
// dropped; any other failure keeps the MetricStreamEntry as the row
// (rowDetailReader). Name comes from the MetricStreamEntry.
func scanCWMetricStreams(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: cwGetMetricStreamOp, what: "metric stream(s)", notFound: cwNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetMetricStream(ctx, &cloudwatch.GetMetricStreamInput{Name: r.Name})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, cwListMetricStreamsOp,
		func(ctx context.Context, token *string) ([]cwtypes.MetricStreamEntry, *string, error) {
			out, err := client.ListMetricStreams(ctx, &cloudwatch.ListMetricStreamsInput{MaxResults: sdkaws.Int32(cwResultPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(m cwtypes.MetricStreamEntry) (*store.Resource, string, string) {
			name := sv(m.Name)
			return arnRow("metric streams", m.Arn, m.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudWatchMetricStream, NativeID: sv(m.Arn),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
}

// scanCWOTelEnrichment discovers the singleton OTel enrichment configuration
// per (account, region). The API has no List op — GetOTelEnrichment returns
// a Status (Running/Stopped). Synth NativeID:
// arn:{p}:cloudwatch:{r}:{a}:otel-enrichment.
func scanCWOTelEnrichment(ctx context.Context, client cloudwatchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetOTelEnrichment(ctx, &cloudwatch.GetOTelEnrichmentInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "cloudwatch:GetOTelEnrichment", acct.ID, region, err)
		}
		// Soft skip: `InvalidAction` ("Operation not supported") fires where the
		// OTel enrichment op isn't deployed.
		if isAPIErrorCode(err, "ValidationException", "UnknownOperationException", "InvalidAction") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("cloudwatch:GetOTelEnrichment: %w", err)
	}
	arn := arnFor(acct.partition(), "cloudwatch", region, acct.ID, "otel-enrichment")
	name := "otel-enrichment"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeCloudWatchOTelEnrichment, NativeID: arn,
		Name: &name, Region: &region,
		// Per-(acct,region) singleton account-level enrichment config — not user-created.
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "cloudwatch otel-enrichment")
}
