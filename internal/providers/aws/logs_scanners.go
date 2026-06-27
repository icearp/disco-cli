package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:logs",
		fn:   scanLogs,
		emits: []coverage.TypeDecl{
			{Service: "logs", DiscoType: TypeLogsLogGroup, Leaf: true},
			{Service: "logs", DiscoType: TypeLogsLogStream},
			{Service: "logs", DiscoType: TypeLogsMetricFilter},
			{Service: "logs", DiscoType: TypeLogsSubscriptionFilter},
			{Service: "logs", DiscoType: TypeLogsQueryDefinition},
			{Service: "logs", DiscoType: TypeLogsScheduledQuery, Leaf: true},
			{Service: "logs", DiscoType: TypeLogsResourcePolicy, Leaf: true},
			{Service: "logs", DiscoType: TypeLogsAccountPolicy, Leaf: true},
			{Service: "logs", DiscoType: TypeLogsDestination},
			{Service: "logs", DiscoType: TypeLogsDelivery},
			{Service: "logs", DiscoType: TypeLogsDeliverySource, Leaf: true},
			{Service: "logs", DiscoType: TypeLogsDeliveryDest},
			{Service: "logs", DiscoType: TypeLogsLogAnomalyDetector},
			{Service: "logs", DiscoType: TypeLogsTransformer},
			{Service: "logs", DiscoType: TypeLogsIntegration, Leaf: true},
		},
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
}

// scanLogs discovers all CloudWatch Logs resources in one region.
// Phase 1 scans independent resources; phase 2 scans per-log-group resources
// that depend on phase 1 log groups being in the DB first.
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
	}
	for _, fn := range phase1 {
		t, i, e := fn(ctx, client, acct, region, st, scanID)
		if e != nil {
			return total, inserted, e
		}
		total += t
		inserted += i
	}

	// Phase 2: per-log-group enrichments. The three sub-scanners hit
	// independent CloudWatch Logs APIs (DescribeLogStreams,
	// DescribeSubscriptionFilters, GetTransformer), each with its own per-log-
	// group 5 TPS bucket — so they overlap cleanly. Run them concurrently and
	// share one ListResources load of the region's log-group set across all
	// three (loadLogGroupsForRegion is otherwise called three times for the
	// same rows).
	groups, err := loadLogGroupsForRegion(acct, region, st)
	if err != nil {
		return total, inserted, fmt.Errorf("load log groups for phase 2: %w", err)
	}
	if len(groups) == 0 {
		return total, inserted, nil
	}

	type phase2Fn func(context.Context, cwlogsAPI, *account, string, []store.Resource, *store.Store, string) (int, int, error)
	phase2 := []phase2Fn{
		scanLogsLogStreams,
		scanLogsSubscriptionFilters,
		scanLogsTransformers,
	}
	var (
		t2, n2 atomic.Int64
		errMu  sync.Mutex
		errs   []error
		wg     sync.WaitGroup
	)
	for _, fn := range phase2 {
		wg.Go(func() {
			tt, nn, e := fn(ctx, client, acct, region, groups, st, scanID)
			t2.Add(int64(tt))
			n2.Add(int64(nn))
			if e != nil {
				errMu.Lock()
				errs = append(errs, e)
				errMu.Unlock()
			}
		})
	}
	wg.Wait()
	total += int(t2.Load())
	inserted += int(n2.Load())
	if len(errs) > 0 {
		return total, inserted, errors.Join(errs...)
	}
	return total, inserted, nil
}

// logGroupARN strips the trailing ":*" that the CloudWatch Logs API appends to
// log group ARNs. The clean ARN is used as the NativeID.
func logGroupARN(arn *string) string {
	return strings.TrimSuffix(sv(arn), ":*")
}

// --- Phase 1 scanners ---

// scanLogsLogGroups discovers all CloudWatch Logs log groups in the region.
// Tags are not returned inline by DescribeLogGroups and require a separate
// ListTagsForResource call; we skip tag fetching to keep scanning fast.
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
// The API requires a PolicyType parameter; we iterate all known types.
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

	// Fetch full details for each integration concurrently.
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
// region. Called without a log group filter to retrieve all filters at once.
// Hierarchy closure is recorded so each filter points back to its log group.
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
				parentID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup,
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
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLogsResourcePolicy,
				NativeID:       name,
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

// loadLogGroupsForRegion loads all log group resources from the store for
// the given account and region, for use by per-log-group Phase 2 scanners.
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

// scanLogsLogStreams fetches log streams for every log group in the region.
// Hierarchy closure is recorded so each stream points back to its log group.
//
// Concurrency uses fanoutMed (10): DescribeLogStreams' 5 TPS limit is
// per-log-group, so concurrent calls to N distinct groups consume N
// independent buckets. Within one group the paginator is sequential, so
// per-group TPS stays ≤ 1. Account-wide pressure is absorbed by the SDK's
// adaptive retry (aws_config.go).
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

// scanLogsTransformers fetches log transformers for every log group. Each
// log group has at most one transformer; groups without one return
// ResourceNotFoundException, which is skipped. Concurrency: fanoutMed —
// GetTransformer is per-log-group like the other phase-2 APIs.
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
			r.ID = store.ResourceID("aws", acct.ID, TypeLogsTransformer, nativeID)
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
