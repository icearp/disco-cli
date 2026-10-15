package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/observabilityadmin"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLogsLogGroup, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsLogStream, Service: "logs", Volatile: []string{"UploadSequenceToken"}})
	registerType(restype.Descriptor{Type: TypeLogsMetricFilter, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsSubscriptionFilter, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsQueryDefinition, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsScheduledQuery, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsResourcePolicy, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsAccountPolicy, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsDestination, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsDelivery, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsDeliverySource, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsDeliveryDest, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsLogAnomalyDetector, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsTransformer, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsIntegration, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsLookupTable, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsIndexPolicy, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsSyslogConfig, Service: "logs"})
	registerType(restype.Descriptor{Type: TypeLogsSource, Service: "logs"})
	registerService(serviceEntry{
		name: "aws:logs",
		fn:   scanLogs,
	})
}

// cwlogsAPI is the narrow set of CloudWatch Logs operations called by the
// scanLogs sub-phases.
type cwlogsAPI interface {
	DescribeLogGroups(context.Context, *cwlogs.DescribeLogGroupsInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeLogGroupsOutput, error)
	DescribeLogStreams(context.Context, *cwlogs.DescribeLogStreamsInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeLogStreamsOutput, error)
	DescribeAccountPolicies(context.Context, *cwlogs.DescribeAccountPoliciesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeAccountPoliciesOutput, error)
	DescribeMetricFilters(context.Context, *cwlogs.DescribeMetricFiltersInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeMetricFiltersOutput, error)
	DescribeSubscriptionFilters(context.Context, *cwlogs.DescribeSubscriptionFiltersInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeSubscriptionFiltersOutput, error)
	DescribeDeliveries(context.Context, *cwlogs.DescribeDeliveriesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeDeliveriesOutput, error)
	DescribeDeliveryDestinations(context.Context, *cwlogs.DescribeDeliveryDestinationsInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeDeliveryDestinationsOutput, error)
	DescribeDeliverySources(context.Context, *cwlogs.DescribeDeliverySourcesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeDeliverySourcesOutput, error)
	DescribeDestinations(context.Context, *cwlogs.DescribeDestinationsInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeDestinationsOutput, error)
	DescribeQueryDefinitions(context.Context, *cwlogs.DescribeQueryDefinitionsInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeQueryDefinitionsOutput, error)
	DescribeResourcePolicies(context.Context, *cwlogs.DescribeResourcePoliciesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeResourcePoliciesOutput, error)
	ListLogAnomalyDetectors(context.Context, *cwlogs.ListLogAnomalyDetectorsInput, ...func(*cwlogs.Options)) (*cwlogs.ListLogAnomalyDetectorsOutput, error)
	ListIntegrations(context.Context, *cwlogs.ListIntegrationsInput, ...func(*cwlogs.Options)) (*cwlogs.ListIntegrationsOutput, error)
	GetIntegration(context.Context, *cwlogs.GetIntegrationInput, ...func(*cwlogs.Options)) (*cwlogs.GetIntegrationOutput, error)
	ListScheduledQueries(context.Context, *cwlogs.ListScheduledQueriesInput, ...func(*cwlogs.Options)) (*cwlogs.ListScheduledQueriesOutput, error)
	GetTransformer(context.Context, *cwlogs.GetTransformerInput, ...func(*cwlogs.Options)) (*cwlogs.GetTransformerOutput, error)
	DescribeLookupTables(context.Context, *cwlogs.DescribeLookupTablesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeLookupTablesOutput, error)
	DescribeIndexPolicies(context.Context, *cwlogs.DescribeIndexPoliciesInput, ...func(*cwlogs.Options)) (*cwlogs.DescribeIndexPoliciesOutput, error)
	ListSyslogConfigurations(context.Context, *cwlogs.ListSyslogConfigurationsInput, ...func(*cwlogs.Options)) (*cwlogs.ListSyslogConfigurationsOutput, error)
	ListSourcesForS3TableIntegration(context.Context, *cwlogs.ListSourcesForS3TableIntegrationInput, ...func(*cwlogs.Options)) (*cwlogs.ListSourcesForS3TableIntegrationOutput, error)
}

// logsS3TableIntegrationLister lists the S3 Table integrations whose sources
// scanLogsS3TableSources fans out over.
type logsS3TableIntegrationLister interface {
	ListS3TableIntegrations(context.Context, *observabilityadmin.ListS3TableIntegrationsInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListS3TableIntegrationsOutput, error)
}

// scanLogs discovers all CloudWatch Logs resources in one region: phase 1
// scans independent resources, phase 2 scans per-log-group resources that
// depend on phase 1's log groups being in the DB first, and phase 3 scans
// syslog configurations and S3 Table integration sources.
func scanLogs(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cwlogs.NewFromConfig(acct.cfg, func(o *cwlogs.Options) { o.Region = region })

	type subFn func(context.Context, cwlogsAPI, *account, string, *store.Store, string) (int, int, error)

	// Phase 1: independent scanners — run sequentially.
	phase1 := []subFn{
		scanLogsLogGroups,
		scanLogsAccountPolicies,
		scanLogsDeliveries,
		scanLogsDeliveryDestinations,
		scanLogsDeliverySources,
		scanLogsDestinations,
		scanLogsIntegrations,
		scanLogsLogAnomalyDetectors,
		scanLogsMetricFilters,
		scanLogsQueryDefinitions,
		scanLogsResourcePolicies,
		scanLogsScheduledQueries,
		scanLogsLookupTables,
	}
	for _, fn := range phase1 {
		t, i, e := fn(ctx, client, acct, region, st, scanID)
		if e != nil {
			return total, inserted, e
		}
		total += t
		inserted += i
	}

	groups, err := loadLogGroupsForRegion(acct, region, st)
	if err != nil {
		return total, inserted, fmt.Errorf("load log groups for phase 2: %w", err)
	}
	t2, n2, err2 := scanLogsPerGroup(ctx, client, acct, region, groups, st, scanID)
	total += t2
	inserted += n2

	// Phase 3 runs after phase 2 and joins its errors with phase 2's, so a
	// failure here cannot stop any earlier scan.
	obs := observabilityadmin.NewFromConfig(acct.cfg, func(o *observabilityadmin.Options) { o.Region = region })
	errs := []error{err2}
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{"logs:ListSyslogConfigurations", func() (int, int, error) {
			return scanLogsSyslogConfigurations(ctx, client, acct, region, groups, st, scanID)
		}},
		{"logs:ListSourcesForS3TableIntegration", func() (int, int, error) {
			return scanLogsS3TableSources(ctx, client, obs, acct, region, st, scanID)
		}},
	} {
		t, i, e := phase.fn()
		total += t
		inserted += i
		errs = append(errs, logsDemoteTransient(st, phase.op, acct, region, e))
	}
	return total, inserted, errors.Join(errs...)
}

// logsDemoteTransient reports a phase's transient error as a warning and drops
// it. The phases' errors are joined, and the dispatcher classifies a join as
// transient when any member is a network error or its first API error is a
// throttle or 5xx, so a joined transient error would hide another phase's hard
// error and discard the counts of every phase.
func logsDemoteTransient(st *store.Store, op string, acct *account, region string, err error) error {
	if isTransientNetworkError(err) {
		return skipIfTransient(st, op, acct.ID, region, err)
	}
	return err
}

// scanLogsPerGroup runs the per-log-group scanners (phase 2) concurrently.
// They hit independent CloudWatch Logs APIs; DescribeLogStreams,
// DescribeSubscriptionFilters and GetTransformer each have their own
// per-log-group 5 TPS bucket, so they overlap cleanly. DescribeIndexPolicies
// adds one call per Standard-class log group; a call still throttled after the
// SDK's retries is warned about and skipped (scanLogsIndexPolicies).
// The scanners share one ListResources load of the region's log-group set
// (else loadLogGroupsForRegion runs once per scanner for the same rows).
func scanLogsPerGroup(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(groups) == 0 {
		return 0, 0, nil
	}

	phase2 := []struct {
		op string
		fn func() (int, int, error)
	}{
		{"logs:DescribeLogStreams", func() (int, int, error) {
			return scanLogsLogStreams(ctx, client, acct, region, groups, st, scanID)
		}},
		{"logs:DescribeSubscriptionFilters", func() (int, int, error) {
			return scanLogsSubscriptionFilters(ctx, client, acct, region, groups, st, scanID)
		}},
		{"logs:GetTransformer", func() (int, int, error) {
			return scanLogsTransformers(ctx, client, acct, region, groups, st, scanID)
		}},
		{"logs:DescribeIndexPolicies", func() (int, int, error) {
			return scanLogsIndexPolicies(ctx, client, acct, region, groups, st, scanID)
		}},
	}
	var (
		t2, n2 atomic.Int64
		errMu  sync.Mutex
		errs   []error
		wg     sync.WaitGroup
	)
	for _, phase := range phase2 {
		wg.Go(func() {
			tt, nn, e := phase.fn()
			t2.Add(int64(tt))
			n2.Add(int64(nn))
			if e = logsDemoteTransient(st, phase.op, acct, region, e); e != nil {
				errMu.Lock()
				errs = append(errs, e)
				errMu.Unlock()
			}
		})
	}
	wg.Wait()
	return int(t2.Load()), int(n2.Load()), errors.Join(errs...)
}

// logGroupARN strips the trailing ":*" the CloudWatch Logs API appends to log
// group ARNs; the clean ARN is used as the NativeID.
func logGroupARN(arn *string) string {
	return strings.TrimSuffix(sv(arn), ":*")
}

// --- Phase 1 scanners ---

// scanLogsLogGroups discovers all CloudWatch Logs log groups in the region.
// DescribeLogGroups doesn't return tags inline (needs a separate
// ListTagsForResource call); tag fetching is skipped to keep scanning fast.
func scanLogsLogGroups(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeLogGroupsPaginator(client, &cwlogs.DescribeLogGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeLogGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeLogGroups: %w", err)
		}
		var batch []*store.Resource
		for _, g := range page.LogGroups {
			name := sv(g.LogGroupName)
			nativeID := logGroupARN(g.Arn)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsLogGroup,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert log groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsAccountPolicies discovers all CloudWatch Logs account-level policies.
// The API requires a PolicyType parameter, so iterate every known type.
func scanLogsAccountPolicies(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, pt := range logsTypes.PolicyType("").Values() {
		var nextToken *string
		for {
			out, err := client.DescribeAccountPolicies(ctx, &cwlogs.DescribeAccountPoliciesInput{
				PolicyType: pt,
				NextToken:  nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					break // skip inaccessible policy types
				}
				return total, inserted, fmt.Errorf("logs:DescribeAccountPolicies(%s): %w", pt, err)
			}
			var batch []*store.Resource
			for _, p := range out.AccountPolicies {
				name := sv(p.PolicyName)
				nativeID := string(p.PolicyType) + "/" + name
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLogsAccountPolicy,
					NativeID:       nativeID,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(p),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert account policies: %w", err)
				}
				total += len(batch)
				inserted += n
			}
			nextToken = out.NextToken
			if nextToken == nil {
				break
			}
		}
	}
	return
}

// scanLogsDeliveries discovers all CloudWatch Logs deliveries in the region.
func scanLogsDeliveries(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeDeliveriesPaginator(client, &cwlogs.DescribeDeliveriesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeDeliveries", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeDeliveries: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.Deliveries {
			arn := sv(d.Arn)
			name := sv(d.DeliverySourceName) // human-readable name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsDelivery,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert deliveries: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsDeliveryDestinations discovers all CloudWatch Logs delivery destinations.
func scanLogsDeliveryDestinations(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeDeliveryDestinationsPaginator(client, &cwlogs.DescribeDeliveryDestinationsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeDeliveryDestinations", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeDeliveryDestinations: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.DeliveryDestinations {
			arn := sv(d.Arn)
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsDeliveryDest,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert delivery destinations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsDeliverySources discovers all CloudWatch Logs delivery sources.
func scanLogsDeliverySources(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeDeliverySourcesPaginator(client, &cwlogs.DescribeDeliverySourcesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeDeliverySources", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeDeliverySources: %w", err)
		}
		var batch []*store.Resource
		for _, s := range page.DeliverySources {
			arn := sv(s.Arn)
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsDeliverySource,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert delivery sources: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsDestinations discovers all CloudWatch Logs cross-account destinations.
func scanLogsDestinations(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeDestinationsPaginator(client, &cwlogs.DescribeDestinationsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeDestinations", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeDestinations: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.Destinations {
			arn := sv(d.Arn)
			name := sv(d.DestinationName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsDestination,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert destinations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsIntegrations discovers all CloudWatch Logs integrations, fetching
// full details for each via GetIntegration concurrently.
func scanLogsIntegrations(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.ListIntegrations(ctx, &cwlogs.ListIntegrationsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "logs:ListIntegrations", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("logs:ListIntegrations: %w", err)
	}
	if len(out.IntegrationSummaries) == 0 {
		return 0, 0, nil
	}

	// Fetch each integration's full details concurrently.
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(fanoutLow)
	for _, s := range out.IntegrationSummaries {
		name := sv(s.IntegrationName)
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			detail, err := client.GetIntegration(gctx, &cwlogs.GetIntegrationInput{
				IntegrationName: &name,
			})
			if err != nil {
				if isAccessDenied(err) {
					return nil // skip inaccessible integrations
				}
				return fmt.Errorf("logs:GetIntegration(%s): %w", name, err)
			}
			n := name
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsIntegration,
				NativeID:       n,
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(detail),
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
			return 0, 0, fmt.Errorf("upsert integrations: %w", err)
		}
		return len(batch), n, nil
	}
	return 0, 0, nil
}

// scanLogsLogAnomalyDetectors discovers all CloudWatch Logs anomaly detectors.
func scanLogsLogAnomalyDetectors(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewListLogAnomalyDetectorsPaginator(client, &cwlogs.ListLogAnomalyDetectorsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:ListLogAnomalyDetectors", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:ListLogAnomalyDetectors: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.AnomalyDetectors {
			arn := sv(d.AnomalyDetectorArn)
			name := sv(d.DetectorName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsLogAnomalyDetector,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert log anomaly detectors: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsMetricFilters discovers all CloudWatch Logs metric filters in the
// region (no log-group filter — retrieves all at once); records hierarchy
// closure so each filter points back to its log group.
func scanLogsMetricFilters(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewDescribeMetricFiltersPaginator(client, &cwlogs.DescribeMetricFiltersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeMetricFilters", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeMetricFilters: %w", err)
		}
		var batch []*store.Resource
		for _, f := range page.MetricFilters {
			lg := sv(f.LogGroupName)
			name := sv(f.FilterName)
			nativeID := lg + "/filter/" + name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsMetricFilter,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert metric filters: %w", err)
			}
			// Build closure pairs: filter child → log group parent.
			pairs := make([][2]string, 0, len(batch))
			for i, r := range batch {
				lgName := sv(page.MetricFilters[i].LogGroupName)
				parentID := store.ResourceID("aws", acct.ID,
					logGroupNativeIDFromName(acct.ID, region, lgName))

				pairs = append(pairs, [2]string{r.ID, parentID})
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return total, inserted, fmt.Errorf("hierarchy closure for metric filters: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsQueryDefinitions discovers all CloudWatch Logs Insights saved query
// definitions using manual NextToken pagination.
func scanLogsQueryDefinitions(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, err := client.DescribeQueryDefinitions(ctx, &cwlogs.DescribeQueryDefinitionsInput{
			NextToken: nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeQueryDefinitions", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeQueryDefinitions: %w", err)
		}
		var batch []*store.Resource
		for _, q := range out.QueryDefinitions {
			id := sv(q.QueryDefinitionId)
			name := sv(q.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsQueryDefinition,
				NativeID:       id,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(q),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert query definitions: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}
	return
}

// scanLogsResourcePolicies discovers all CloudWatch Logs resource policies
// using manual NextToken pagination.
func scanLogsResourcePolicies(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, err := client.DescribeResourcePolicies(ctx, &cwlogs.DescribeResourcePoliciesInput{
			NextToken: nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeResourcePolicies", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeResourcePolicies: %w", err)
		}
		var batch []*store.Resource
		for _, p := range out.ResourcePolicies {
			name := sv(p.PolicyName)
			// Region+account-qualified synthetic ARN: the bare policy name is unique
			// only per (account, region), so it must carry region to be a global id.
			nativeID := fmt.Sprintf("arn:aws:logs:%s:%s:resource-policy:%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsResourcePolicy,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert resource policies: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}
	return
}

// scanLogsLookupTables discovers all CloudWatch Logs lookup tables in the
// region; DescribeLookupTables has no SDK paginator, so use a manual
// NextToken loop.
func scanLogsLookupTables(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, err := client.DescribeLookupTables(ctx, &cwlogs.DescribeLookupTablesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:DescribeLookupTables", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:DescribeLookupTables: %w", err)
		}
		var batch []*store.Resource
		for _, lt := range out.LookupTables {
			arn := sv(lt.LookupTableArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsLookupTable,
				NativeID:       arn,
				Name:           lt.LookupTableName,
				Region:         &region,
				AttributesJSON: mustJSON(lt),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert lookup tables: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}
	return
}

// scanLogsScheduledQueries discovers all CloudWatch Logs Insights Live Tail
// scheduled queries in the region.
func scanLogsScheduledQueries(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewListScheduledQueriesPaginator(client, &cwlogs.ListScheduledQueriesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "logs:ListScheduledQueries", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("logs:ListScheduledQueries: %w", err)
		}
		var batch []*store.Resource
		for _, q := range page.ScheduledQueries {
			arn := sv(q.ScheduledQueryArn)
			name := sv(q.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsScheduledQuery,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(q),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert scheduled queries: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Phase 2 helpers ---

// loadLogGroupsForRegion loads all log-group resources for the account and
// region, for use by per-log-group Phase 2 scanners.
func loadLogGroupsForRegion(acct *account, region string, st *store.Store) ([]store.Resource, error) {
	return st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeLogsLogGroup},
		Regions:   []string{region},
		Limit:     util.AllResources,
	})
}

// --- Phase 2 scanners ---

// scanLogsLogStreams fetches log streams for every log group in the region
// and records hierarchy closure so each stream points back to its log group.
//
// Uses fanoutMed (10): DescribeLogStreams' 5 TPS limit is per-log-group, so
// concurrent calls to N distinct groups consume N independent buckets. Within
// one group the paginator is sequential, so per-group TPS stays ≤ 1.
// Account-wide pressure is absorbed by the SDK's adaptive retry (aws_config.go).
func scanLogsLogStreams(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(groups) == 0 {
		return 0, 0, nil
	}

	var t, n atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(fanoutMed)

	for _, grp := range groups {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			tt, nn, err := scanLogStreamsForGroup(gctx, client, acct, region, grp, st, scanID)
			t.Add(int64(tt))
			n.Add(int64(nn))
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return int(t.Load()), int(n.Load()), err
	}
	return int(t.Load()), int(n.Load()), nil
}

// scanLogStreamsForGroup paginates log streams for one log group and upserts them.
func scanLogStreamsForGroup(ctx context.Context, client cwlogsAPI, acct *account, region string, grp store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	lgName := ""
	if grp.Name != nil {
		lgName = *grp.Name
	}
	pager := cwlogs.NewDescribeLogStreamsPaginator(client, &cwlogs.DescribeLogStreamsInput{
		LogGroupName: &lgName,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil // skip inaccessible log groups
			}
			return total, inserted, fmt.Errorf("logs:DescribeLogStreams(%s): %w", lgName, err)
		}
		var (
			batch []*store.Resource
			pairs [][2]string
		)
		for _, s := range page.LogStreams {
			sName := sv(s.LogStreamName)
			nativeID := lgName + "/stream/" + sName
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsLogStream,
				NativeID:       nativeID,
				Name:           &sName,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert log streams for %s: %w", lgName, err)
			}
			for _, r := range batch {
				pairs = append(pairs, [2]string{r.ID, grp.ID})
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return total, inserted, fmt.Errorf("hierarchy closure for log streams (%s): %w", lgName, err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsSubscriptionFilters fetches subscription filters for every log
// group. Concurrency: fanoutMed — DescribeSubscriptionFilters' 5 TPS limit
// is per-log-group; same rationale as scanLogsLogStreams above.
func scanLogsSubscriptionFilters(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(groups) == 0 {
		return 0, 0, nil
	}

	var t, n atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(fanoutMed)

	for _, grp := range groups {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			tt, nn, err := scanSubscriptionFiltersForGroup(gctx, client, acct, region, grp, st, scanID)
			t.Add(int64(tt))
			n.Add(int64(nn))
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return int(t.Load()), int(n.Load()), err
	}
	return int(t.Load()), int(n.Load()), nil
}

// scanSubscriptionFiltersForGroup paginates subscription filters for one log group.
func scanSubscriptionFiltersForGroup(ctx context.Context, client cwlogsAPI, acct *account, region string, grp store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	lgName := ""
	if grp.Name != nil {
		lgName = *grp.Name
	}
	pager := cwlogs.NewDescribeSubscriptionFiltersPaginator(client, &cwlogs.DescribeSubscriptionFiltersInput{
		LogGroupName: &lgName,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("logs:DescribeSubscriptionFilters(%s): %w", lgName, err)
		}
		var (
			batch []*store.Resource
			pairs [][2]string
		)
		for _, f := range page.SubscriptionFilters {
			fName := sv(f.FilterName)
			nativeID := lgName + "/subscription/" + fName
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsSubscriptionFilter,
				NativeID:       nativeID,
				Name:           &fName,
				Region:         &region,
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert subscription filters for %s: %w", lgName, err)
			}
			for _, r := range batch {
				pairs = append(pairs, [2]string{r.ID, grp.ID})
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return total, inserted, fmt.Errorf("hierarchy closure for subscription filters (%s): %w", lgName, err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLogsTransformers fetches log transformers for every log group (at most
// one per group); groups without one return ResourceNotFoundException,
// skipped. Concurrency: fanoutMed — GetTransformer is per-log-group like the
// other phase-2 APIs.
func scanLogsTransformers(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(groups) == 0 {
		return 0, 0, nil
	}

	var (
		t, n  atomic.Int64
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(fanoutMed)

	for _, grp := range groups {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			// NativeID for log group is the clean ARN (no trailing :*).
			lgARN := grp.NativeID
			out, err := client.GetTransformer(gctx, &cwlogs.GetTransformerInput{
				LogGroupIdentifier: &lgARN,
			})
			if err != nil {
				// ResourceNotFoundException means no transformer exists — skip.
				var nfe *logsTypes.ResourceNotFoundException
				if isAccessDenied(err) || errors.As(err, &nfe) {
					return nil
				}
				return fmt.Errorf("logs:GetTransformer(%s): %w", lgARN, err)
			}
			// NativeID is the clean log group ARN (strip ":*" if present).
			nativeID := logGroupARN(out.LogGroupIdentifier)
			name := nativeID
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsTransformer,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			// Pre-compute ID so the closure pair is correct before UpsertResources.
			r.ID = store.ResourceID("aws", acct.ID, nativeID)
			mu.Lock()
			batch = append(batch, r)
			pairs = append(pairs, [2]string{r.ID, grp.ID})
			mu.Unlock()
			t.Add(1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return int(t.Load()), int(n.Load()), err
	}
	if len(batch) > 0 {
		// IDs were pre-computed before collection; pairs are already correct.
		ins, err := st.UpsertResources(batch)
		if err != nil {
			return int(t.Load()), int(n.Load()), fmt.Errorf("upsert transformers: %w", err)
		}
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return int(t.Load()), int(n.Load()), fmt.Errorf("hierarchy closure for transformers: %w", err)
		}
		n.Add(int64(ins))
	}
	return int(t.Load()), int(n.Load()), nil
}

// isLogsParentGone matches a log group or S3 Table integration deleted between
// the list that named it and the per-parent call.
func isLogsParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanLogsIndexPolicies stores each log group's own field index policy under
// its log group. The model caps DescribeIndexPolicies' LogGroupIdentifiers at
// one entry, so it fans out per group like the other phase-2 scanners. Only
// Standard-class log groups support field index policies (PutIndexPolicy), so
// only those are queried; a group stored without a class is treated as
// Standard, the class a log group gets when none is chosen. Each group's
// policy is stored as soon as it is read, and groups still throttled after
// the SDK's retries are skipped and reported in one warning, so one slow group
// cannot cost the others' rows.
func scanLogsIndexPolicies(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (int, int, error) {
	const op = "logs:DescribeIndexPolicies"
	parents := make([]childParent, 0, len(groups))
	names := make(map[string]*string, len(groups))
	for _, g := range groups {
		var attrs struct{ LogGroupClass logsTypes.LogGroupClass }
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			return 0, 0, fmt.Errorf("decode log group %s: %w", g.NativeID, err)
		}
		if attrs.LogGroupClass != "" && attrs.LogGroupClass != logsTypes.LogGroupClassStandard {
			continue
		}
		parents = append(parents, childParent{id: g.NativeID, arn: g.NativeID})
		names[g.NativeID] = g.Name
	}
	var transient logsTransientTally
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isLogsParentGone,
		func(ctx context.Context, p childParent) (int, int, error) {
			rows, err := listLogGroupIndexPolicy(ctx, client, acct, region, p.arn, names[p.arn], scanID)
			if isTransientNetworkError(err) {
				transient.skipped(p.arn, err)
				return 0, 0, nil
			}
			if err != nil {
				return 0, 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			children := make([]containedChild, len(rows))
			for i, r := range rows {
				children[i] = containedChild{row: r, parentID: parentID}
			}
			return upsertContained(st, op, children)
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// logsTransientTally collects the parents of a fan-out whose call still failed
// transiently after the SDK's retries, so that a throttled account reports one
// warning per fan-out rather than one per parent.
type logsTransientTally struct {
	mu    sync.Mutex
	count int
	first string
	err   error
}

func (t *logsTransientTally) skipped(parent string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		t.first, t.err = parent, err
	}
	t.count++
}

// warnSkipped warns once for every parent skipped, naming the first and its error.
func (t *logsTransientTally) warnSkipped(st *store.Store, op string, acct *account, region string) {
	if t.count == 0 {
		return
	}
	_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf("%d parent(s) skipped, first %s: %w", t.count, t.first, t.err))
}

// listLogGroupIndexPolicy returns the index policy set on the log group itself,
// keyed {logGroupARN}:index-policy (the policy has no ARN or name of its own;
// ":" cannot occur in a log group name, so no log group shares the key).
// A group without one gets the account-wide policy that applies to it, with
// Source ACCOUNT; that policy is already stored once as an account-policy
// (DescribeAccountPolicies with FIELD_INDEX_POLICY), so storing it here would
// repeat it under every log group.
func listLogGroupIndexPolicy(ctx context.Context, client cwlogsAPI, acct *account, region, groupARN string, groupName *string, scanID string) ([]*store.Resource, error) {
	input := &cwlogs.DescribeIndexPoliciesInput{LogGroupIdentifiers: []string{groupARN}}
	var rows []*store.Resource
	for {
		out, err := client.DescribeIndexPolicies(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, p := range out.IndexPolicies {
			if p.Source != logsTypes.IndexSourceLogGroup {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsIndexPolicy, NativeID: groupARN + ":index-policy",
				Name: groupName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		// Mirrors the SDK paginators' StopOnDuplicateToken: DescribeIndexPolicies
		// has no paginator, and a repeated token would loop forever.
		if next := sv(out.NextToken); next == "" || next == sv(input.NextToken) {
			return rows, nil
		}
		input.NextToken = out.NextToken
	}
}

// logsSyslogPageSize is ListSyslogConfigurations' maximum maxResults (0-50).
const logsSyslogPageSize = 50

// scanLogsSyslogConfigurations stores the region's syslog configurations, each
// under its log group when phase 1 listed that group. A configuration has no
// ARN; PutSyslogConfiguration and DeleteSyslogConfiguration identify it by its
// log group and VPC endpoint, so its NativeID is
// {logGroupARN}:syslog-configuration:{vpcEndpointId} (":" cannot occur in a
// log group name, so no log group shares the key).
func scanLogsSyslogConfigurations(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (total, inserted int, err error) {
	const op = "logs:ListSyslogConfigurations"
	groupIDs := make(map[string]string, len(groups))
	for _, g := range groups {
		groupIDs[g.NativeID] = g.ID
	}
	input := &cwlogs.ListSyslogConfigurationsInput{MaxResults: logsSyslogPageSize}
	for {
		out, err := client.ListSyslogConfigurations(ctx, input)
		switch {
		case err == nil:
		case isAccessDenied(err):
			return total, inserted, skipIfAccessDenied(st, op, acct.ID, region, err)
		case input.NextToken == nil && isOpNotDeployed(err):
			return 0, 0, nil
		default:
			return total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		var (
			batch     []*store.Resource
			parentIDs []string
		)
		for _, c := range out.SyslogConfigurations {
			groupARN, vpce := logGroupARN(c.LogGroupArn), sv(c.VpcEndpointId)
			if groupARN == "" || vpce == "" {
				continue
			}
			_, groupName, _ := strings.Cut(groupARN, ":log-group:")
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsSyslogConfig, NativeID: groupARN + ":syslog-configuration:" + vpce,
				Name: nonEmptyPtr(groupName), Region: &region,
				CreatedAt:      tp(epochMillisToTime(sdkaws.ToInt64(c.CreatedAt))),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
			parentIDs = append(parentIDs, groupIDs[groupARN])
		}
		t, i, err := upsertBatch(st, batch, op)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
		var pairs [][2]string
		for k, r := range batch {
			if parentIDs[k] != "" {
				pairs = append(pairs, [2]string{r.ID, parentIDs[k]})
			}
		}
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return total, inserted, fmt.Errorf("hierarchy closure for syslog configurations: %w", err)
		}
		// Mirrors the SDK paginators' StopOnDuplicateToken:
		// ListSyslogConfigurations has no paginator, and a repeated token would
		// loop forever.
		if next := sv(out.NextToken); next == "" || next == sv(input.NextToken) {
			return total, inserted, nil
		}
		input.NextToken = out.NextToken
	}
}

// logsS3TableSourcesPageSize is ListSourcesForS3TableIntegration's maximum
// maxResults (1-100).
const logsS3TableSourcesPageSize = 100

// scanLogsS3TableSources stores the data sources associated with each of the
// region's S3 Table integrations, keyed {integrationARN}/source/{identifier}.
// resolveLogsSourceIntegrations places each under its integration: the
// observabilityadmin scan stores the integrations concurrently with this one,
// so their rows may not exist yet here. Integrations still throttled after the
// SDK's retries are skipped and reported in one warning; the sources already
// stored for them are kept.
func scanLogsS3TableSources(ctx context.Context, client cwlogsAPI, obs logsS3TableIntegrationLister, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	integrations, err := listLogsS3TableIntegrations(ctx, obs, acct, region, st)
	if err != nil || len(integrations) == 0 {
		return 0, 0, err
	}
	parents := make([]childParent, 0, len(integrations))
	for _, arn := range integrations {
		parents = append(parents, childParent{id: arn, arn: arn})
	}
	const op = "logs:ListSourcesForS3TableIntegration"
	var transient logsTransientTally
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isLogsParentGone,
		func(ctx context.Context, p childParent) (int, int, error) {
			t, i, err := storeLogsS3TableSources(ctx, client, acct, region, p.arn, st, scanID)
			if isTransientNetworkError(err) {
				transient.skipped(p.arn, err)
				return t, i, nil
			}
			return t, i, err
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// listLogsS3TableIntegrations returns the ARNs of the region's S3 Table
// integrations. observabilityadmin owns them and its scan stores them; they are
// listed again here because services scan concurrently, so that scan's rows
// cannot be waited for. The call goes to observabilityadmin's endpoint, so it
// is skipped where the dispatcher would skip that service, and a network error
// is warned about here: joined into the logs errors, it would make the
// dispatcher classify the whole logs scan by it and discard its counts.
func listLogsS3TableIntegrations(ctx context.Context, client logsS3TableIntegrationLister, acct *account, region string, st *store.Store) ([]string, error) {
	const op = "observabilityadmin:ListS3TableIntegrations"
	const svc = "aws:observabilityadmin"
	if !acct.regionScopeDisabled && !serviceAvailableInRegion(acct.availByCode, svc, regionAvailabilityCode(svc), region) {
		return nil, nil
	}
	pager := observabilityadmin.NewListS3TableIntegrationsPaginator(client, &observabilityadmin.ListS3TableIntegrationsInput{},
		func(o *observabilityadmin.ListS3TableIntegrationsPaginatorOptions) { o.Limit = 100 })
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		switch {
		case err == nil:
		// The observabilityadmin scan reports its own denial of this call as a
		// warning; here it only explains the missing sources.
		case isAccessDenied(err):
			st.ReportNotice(store.ScanNotice{
				Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
				Message: "S3 Table integration sources not scanned: " + err.Error(),
			})
			return arns, nil
		// No observabilityadmin endpoint in this region, as the dispatcher
		// treats it for that service.
		case isDNSNotFound(err):
			return arns, nil
		case isTransientNetworkError(err):
			return arns, skipIfTransient(st, op, acct.ID, region, err)
		default:
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		for _, s := range out.IntegrationSummaries {
			if arn := sv(s.Arn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	return arns, nil
}

// storeLogsS3TableSources stores one integration's sources a page at a time.
func storeLogsS3TableSources(ctx context.Context, client cwlogsAPI, acct *account, region, integrationARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cwlogs.NewListSourcesForS3TableIntegrationPaginator(client,
		&cwlogs.ListSourcesForS3TableIntegrationInput{IntegrationArn: &integrationARN},
		func(o *cwlogs.ListSourcesForS3TableIntegrationPaginatorOptions) { o.Limit = logsS3TableSourcesPageSize })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var batch []*store.Resource
		for _, s := range out.Sources {
			id := sv(s.Identifier)
			if id == "" {
				continue
			}
			var name *string
			if s.DataSource != nil {
				name = s.DataSource.Name
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsSource, NativeID: logsSourceNativeID(integrationARN, id),
				Name: name, Region: &region, Status: nonEmptyPtr(string(s.Status)),
				CreatedAt:      tp(epochMillisToTime(sdkaws.ToInt64(s.CreatedTimeStamp))),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		t, i, err := upsertBatch(st, batch, "logs:ListSourcesForS3TableIntegration")
		total += t
		inserted += i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

const logsSourceSegment = "/source/"

// logsSourceNativeID keys an S3 Table integration source under its
// integration: nothing documents the source identifier as unique beyond it.
func logsSourceNativeID(integrationARN, id string) string {
	return integrationARN + logsSourceSegment + id
}

// logsSourceIntegrationARN recovers the integration ARN from a source NativeID.
func logsSourceIntegrationARN(nativeID string) string {
	arn, _, _ := strings.Cut(nativeID, logsSourceSegment)
	return arn
}
