package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:cloudwatch", fn: scanCloudWatch}) }

// scanCloudWatch discovers all CloudWatch resources in one region by calling
// each sub-scanner in sequence and accumulating the totals.
func scanCloudWatch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	type subFn func(context.Context, *account, string, *store.Store, string) (int, int, error)
	fns := []subFn{
		scanCWAlarms,
		scanCWAlarmMuteRules,
		scanCWAnomalyDetectors,
		scanCWDashboards,
		scanCWInsightRules,
		scanCWMetricStreams,
	}
	for _, fn := range fns {
		t, i, err := fn(ctx, acct, region, st, scanID)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanCWAlarms discovers CloudWatch metric alarms and composite alarms. Both
// types are returned by DescribeAlarms; we split them into their own disco
// resource types so gap-analysis and filtering work correctly.
func scanCWAlarms(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	pager := cloudwatch.NewDescribeAlarmsPaginator(client, &cloudwatch.DescribeAlarmsInput{
		AlarmTypes: []cwtypes.AlarmType{
			cwtypes.AlarmTypeMetricAlarm,
			cwtypes.AlarmTypeCompositeAlarm,
		},
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:DescribeAlarms", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:DescribeAlarms: %w", err)
		}
		var batch []*store.Resource
		for _, a := range page.MetricAlarms {
			arn := sv(a.AlarmArn)
			name := sv(a.AlarmName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchAlarm,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		for _, a := range page.CompositeAlarms {
			arn := sv(a.AlarmArn)
			name := sv(a.AlarmName)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchCompositeAlarm,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch alarms: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanCWAlarmMuteRules discovers CloudWatch alarm mute rules. Mute rules are
// not paginated via a SDK Paginator type; we iterate manually using NextToken.
func scanCWAlarmMuteRules(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	pager := cloudwatch.NewListAlarmMuteRulesPaginator(client, &cloudwatch.ListAlarmMuteRulesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:ListAlarmMuteRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:ListAlarmMuteRules: %w", err)
		}
		var batch []*store.Resource
		for _, m := range page.AlarmMuteRuleSummaries {
			arn := sv(m.AlarmMuteRuleArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchAlarmMuteRule,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch alarm mute rules: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanCWAnomalyDetectors discovers CloudWatch anomaly detection models.
// DescribeAnomalyDetectors uses manual NextToken iteration.
// NativeID is derived from the metric coordinates because the API does not
// expose an ARN: "<Namespace>/<MetricName>/<Stat>" for single-metric detectors,
// or "<first-query-id>" for metric-math detectors.
func scanCWAnomalyDetectors(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	var nextToken *string
	for {
		out, err := client.DescribeAnomalyDetectors(ctx, &cloudwatch.DescribeAnomalyDetectorsInput{
			MaxResults: sdkaws.Int32(100),
			NextToken:  nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:DescribeAnomalyDetectors", acct.ID, region, err)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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

// scanCWDashboards discovers CloudWatch dashboards and fetches their body via
// GetDashboard concurrently (one goroutine per dashboard in each page).
func scanCWDashboards(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	pager := cloudwatch.NewListDashboardsPaginator(client, &cloudwatch.ListDashboardsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:ListDashboards", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:ListDashboards: %w", err)
		}
		// Fetch each dashboard body concurrently.
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, entry := range page.DashboardEntries {
			g.Go(func() error {
				out, err := client.GetDashboard(gctx, &cloudwatch.GetDashboardInput{
					DashboardName: entry.DashboardName,
				})
				if err != nil {
					if isAccessDenied(err) {
						return nil // skip individual inaccessible dashboards
					}
					return fmt.Errorf("cloudwatch:GetDashboard %s: %w", sv(entry.DashboardName), err)
				}
				arn := sv(out.DashboardArn)
				name := sv(out.DashboardName)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeCloudWatchDashboard,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(out),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch dashboards: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanCWInsightRules discovers CloudWatch Contributor Insights rules.
// InsightRules are identified by name; there is no ARN exposed in the API.
func scanCWInsightRules(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	pager := cloudwatch.NewDescribeInsightRulesPaginator(client, &cloudwatch.DescribeInsightRulesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:DescribeInsightRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:DescribeInsightRules: %w", err)
		}
		var batch []*store.Resource
		for _, rule := range page.InsightRules {
			name := sv(rule.Name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchInsightRule,
				NativeID:       name,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(rule),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch insight rules: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanCWMetricStreams discovers CloudWatch metric streams and fetches their
// full configuration via GetMetricStream concurrently (one goroutine per
// stream in each page).
func scanCWMetricStreams(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })

	pager := cloudwatch.NewListMetricStreamsPaginator(client, &cloudwatch.ListMetricStreamsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudwatch:ListMetricStreams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudwatch:ListMetricStreams: %w", err)
		}
		// Fetch each stream's full detail concurrently.
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, entry := range page.Entries {
			g.Go(func() error {
				out, err := client.GetMetricStream(gctx, &cloudwatch.GetMetricStreamInput{
					Name: entry.Name,
				})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("cloudwatch:GetMetricStream %s: %w", sv(entry.Name), err)
				}
				arn := sv(out.Arn)
				name := sv(out.Name)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeCloudWatchMetricStream,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(out),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert CloudWatch metric streams: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
