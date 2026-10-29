package aws

import (
	"cmp"
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
	ListTagsForResource(context.Context, *cwlogs.ListTagsForResourceInput, ...func(*cwlogs.Options)) (*cwlogs.ListTagsForResourceOutput, error)
}

// logsS3TableIntegrationLister lists the S3 Table integrations whose sources
// scanLogsS3TableSources fans out over.
type logsS3TableIntegrationLister interface {
	ListS3TableIntegrations(context.Context, *observabilityadmin.ListS3TableIntegrationsInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListS3TableIntegrationsOutput, error)
}

// scanLogs discovers all CloudWatch Logs resources in one region: phase 1
// scans independent resources, phase 2 scans per-log-group resources that
// depend on phase 1's log groups being in the DB first, and phase 3 scans
// syslog configurations and S3 Table integration sources. The phases run in
// that order, one listing of phase 1 at a time (runHostPhases): a listing's
// error does not stop the later ones; a failed write and ctx's end do. The
// tag denials of the whole scan are one warning (sharingDenials).
func scanLogs(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := cwlogs.NewFromConfig(acct.cfg, func(o *cwlogs.Options) { o.Region = region })
	obs := observabilityadmin.NewFromConfig(acct.cfg, func(o *observabilityadmin.Options) { o.Region = region })
	tags := logsTags(client).sharingDenials()

	var (
		groups       []store.Resource
		groupsDenied bool
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "logs", 0, []hostPhase{
		{op: logsDescribeLogGroupsOp, run: func(p *phaseTotals) bool {
			return scanLogsLogGroups(ctx, client, acct, region, st, scanID, tags, &groupsDenied, p)
		}},
		countedPhase(0, "logs:DescribeAccountPolicies", func() (int, int, error) {
			return scanLogsAccountPolicies(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, logsDescribeDeliveriesOp, func() (int, int, error) {
			return scanLogsDeliveries(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, logsDescribeDeliveryDestinationsOp, func() (int, int, error) {
			return scanLogsDeliveryDestinations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, logsDescribeDeliverySourcesOp, func() (int, int, error) {
			return scanLogsDeliverySources(ctx, client, acct, region, st, scanID)
		}),
		{op: logsDescribeDestinationsOp, run: func(p *phaseTotals) bool {
			return scanLogsDestinations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "logs:ListIntegrations", func() (int, int, error) {
			return scanLogsIntegrations(ctx, client, acct, region, st, scanID)
		}),
		{op: logsListLogAnomalyDetectorsOp, run: func(p *phaseTotals) bool {
			return scanLogsLogAnomalyDetectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "logs:DescribeMetricFilters", func() (int, int, error) {
			return scanLogsMetricFilters(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "logs:DescribeQueryDefinitions", func() (int, int, error) {
			return scanLogsQueryDefinitions(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "logs:DescribeResourcePolicies", func() (int, int, error) {
			return scanLogsResourcePolicies(ctx, client, acct, region, st, scanID)
		}),
		{op: logsListScheduledQueriesOp, run: func(p *phaseTotals) bool {
			return scanLogsScheduledQueries(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: logsDescribeLookupTablesOp, run: func(p *phaseTotals) bool {
			return scanLogsLookupTables(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "logs:DescribeLogStreams", run: func(p *phaseTotals) bool {
			var err error
			if groups, err = loadLogGroupsForRegion(acct, region, st, scanID, groupsDenied); err != nil {
				// The later phases need these rows, and the store failed to read
				// them, so the scan stops as it would on a failed write.
				p.errs.add("logs", storeReadError("load log groups for phase 2", err))
				return true
			}
			t, n, err := scanLogsPerGroup(ctx, client, acct, region, groups, st, scanID)
			return addOpErrsPhase(p, "logs:DescribeLogStreams", t, n, err)
		}},
		countedPhase(0, "logs:ListSyslogConfigurations", func() (int, int, error) {
			return scanLogsSyslogConfigurations(ctx, client, acct, region, groups, st, scanID)
		}),
		countedPhase(0, "logs:ListSourcesForS3TableIntegration", func() (int, int, error) {
			return scanLogsS3TableSources(ctx, client, obs, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// Op labels of the listings whose rows carry tags.
const (
	logsDescribeLogGroupsOp            = "logs:DescribeLogGroups"
	logsDescribeDeliveriesOp           = "logs:DescribeDeliveries"
	logsDescribeDeliveryDestinationsOp = "logs:DescribeDeliveryDestinations"
	logsDescribeDeliverySourcesOp      = "logs:DescribeDeliverySources"
	logsDescribeDestinationsOp         = "logs:DescribeDestinations"
	logsListLogAnomalyDetectorsOp      = "logs:ListLogAnomalyDetectors"
	logsListScheduledQueriesOp         = "logs:ListScheduledQueries"
	logsDescribeLookupTablesOp         = "logs:DescribeLookupTables"
	logsTagsOp                         = "logs:ListTagsForResource"
)

// Page sizes: each listing's model maximum.
const (
	// logsDescribePageSize is the maximum limit of DescribeLogGroups,
	// DescribeDestinations and the three delivery listings (DescribeLimit,
	// 1-50) and of ListLogAnomalyDetectors (1-50).
	logsDescribePageSize = 50
	// logsLookupTablesPageSize is DescribeLookupTables' maximum maxResults.
	logsLookupTablesPageSize = 100
	// logsScheduledQueriesPageSize is ListScheduledQueries' maximum
	// maxResults (1-1000).
	logsScheduledQueriesPageSize = 1000
)

// logsNoIdentifier names, in a warning, a listed element with neither an
// ARN nor a name.
const logsNoIdentifier = "(an element with no identifier)"

// logsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned, a log group's without the ":*"
// DescribeLogGroups appends to its Arn (the form the op documents, and the
// element's LogGroupArn). ListTagsForResource returns every tag in one
// response, and models ResourceNotFoundException for a resource deleted
// since it was listed.
//
// The Service Reference lists log groups, destinations, anomaly detectors
// and lookup tables under ListTagsForResource. It omits scheduled queries,
// which are read too: its TagResource entry lists them, CreateScheduledQuery
// takes tags, and no other op returns their tags. Whether the op accepts
// their ARNs is unverified. Log streams are not read: no tagging op lists
// them and CreateLogStream takes no tags.
//
// The op models its resourceArn as ^[\w+=/:,.@-]*$ (1-1011 characters). A
// log group's name may hold '#' (^[\.\-_/#A-Za-z0-9]+$) and a destination's
// almost any character but ':' and '*' (^[^:*]*$), so the ARN of such a row
// is outside that pattern; whether the service rejects it is unverified. The
// SDK does not check the pattern, so the call is made either way.
func logsTags(client cwlogsAPI) rowTagReader {
	return rowTagReader{
		op:       logsTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &cwlogs.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// logsRow returns r, the row of a listed element stored under its ARN, or no
// row when arn is empty: one empty NativeID would merge every such element
// into one resource. what names the elements in the warning, name the
// element.
func logsRow(what, arn, name string, r *store.Resource) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(name, logsNoIdentifier)
	}
	return r, "", ""
}

// scanLogsPerGroup runs the per-log-group scanners (phase 2) concurrently.
// They hit independent CloudWatch Logs APIs; DescribeLogStreams,
// DescribeSubscriptionFilters and GetTransformer each have their own
// per-log-group 5 TPS bucket, so they overlap cleanly. DescribeIndexPolicies
// adds one call per Standard-class log group; a call still throttled after the
// SDK's retries is warned about and skipped (scanLogsIndexPolicies).
// The scanners share one ListResources load of the region's log-group set
// (else loadLogGroupsForRegion runs once per scanner for the same rows).
// Each scanner's error is returned under its own op (opErrs) for scanLogs to
// join.
func scanLogsPerGroup(ctx context.Context, client cwlogsAPI, acct *account, region string, groups []store.Resource, st *store.Store, scanID string) (int, int, error) {
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
		p  phaseTotals
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, phase := range phase2 {
		wg.Go(func() {
			t, n, err := phase.fn()
			mu.Lock()
			p.addPhase(phase.op, t, n, err)
			mu.Unlock()
		})
	}
	wg.Wait()
	return opErrsResult(&p)
}

// logGroupARN strips the trailing ":*" the CloudWatch Logs API appends to log
// group ARNs; the clean ARN is used as the NativeID.
func logGroupARN(arn *string) string {
	return strings.TrimSuffix(sv(arn), ":*")
}

// --- Phase 1 scanners ---

// scanLogsLogGroups stores the region's log groups a page at a time, each
// with its tags (logsTags). DescribeLogGroups returns no tags. It sets
// *denied when a page was denied. Counts and errors go to p; it reports
// whether a write failed.
func scanLogsLogGroups(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, denied *bool, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, logsDescribeLogGroupsOp,
		func(ctx context.Context, token *string) ([]logsTypes.LogGroup, *string, error) {
			out, err := client.DescribeLogGroups(ctx, &cwlogs.DescribeLogGroupsInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					*denied = true
				}
				return nil, nil, err
			}
			return out.LogGroups, out.NextToken, nil
		},
		func(g logsTypes.LogGroup) (*store.Resource, string, string) {
			name := sv(g.LogGroupName)
			nativeID := logGroupARN(g.Arn)
			return logsRow("log groups", nativeID, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsLogGroup, NativeID: nativeID, Name: &name, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}, tags, p)
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

// scanLogsDeliveries stores the region's deliveries with the tags each
// element carries.
func scanLogsDeliveries(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, logsDescribeDeliveriesOp,
		func(ctx context.Context, token *string) ([]logsTypes.Delivery, *string, error) {
			out, err := client.DescribeDeliveries(ctx, &cwlogs.DescribeDeliveriesInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Deliveries, out.NextToken, nil
		},
		func(d logsTypes.Delivery) (*store.Resource, string, string) {
			name := sv(d.DeliverySourceName) // human-readable name
			return logsRow("deliveries", sv(d.Arn), cmp.Or(sv(d.Id), name), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsDelivery, NativeID: sv(d.Arn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
			})
		})
}

// scanLogsDeliveryDestinations stores the region's delivery destinations with
// the tags each element carries.
func scanLogsDeliveryDestinations(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, logsDescribeDeliveryDestinationsOp,
		func(ctx context.Context, token *string) ([]logsTypes.DeliveryDestination, *string, error) {
			out, err := client.DescribeDeliveryDestinations(ctx, &cwlogs.DescribeDeliveryDestinationsInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeliveryDestinations, out.NextToken, nil
		},
		func(d logsTypes.DeliveryDestination) (*store.Resource, string, string) {
			name := sv(d.Name)
			return logsRow("delivery destinations", sv(d.Arn), name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsDeliveryDest, NativeID: sv(d.Arn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
			})
		})
}

// scanLogsDeliverySources stores the region's delivery sources with the tags
// each element carries.
func scanLogsDeliverySources(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, logsDescribeDeliverySourcesOp,
		func(ctx context.Context, token *string) ([]logsTypes.DeliverySource, *string, error) {
			out, err := client.DescribeDeliverySources(ctx, &cwlogs.DescribeDeliverySourcesInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeliverySources, out.NextToken, nil
		},
		func(s logsTypes.DeliverySource) (*store.Resource, string, string) {
			name := sv(s.Name)
			return logsRow("delivery sources", sv(s.Arn), name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsDeliverySource, NativeID: sv(s.Arn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), TagsJSON: mapTagsJSON(s.Tags), DiscoveredBy: scanID,
			})
		})
}

// scanLogsDestinations stores the region's cross-account destinations a page
// at a time, each with its tags (logsTags). Counts and errors go to p; it
// reports whether a write failed.
func scanLogsDestinations(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, logsDescribeDestinationsOp,
		func(ctx context.Context, token *string) ([]logsTypes.Destination, *string, error) {
			out, err := client.DescribeDestinations(ctx, &cwlogs.DescribeDestinationsInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Destinations, out.NextToken, nil
		},
		func(d logsTypes.Destination) (*store.Resource, string, string) {
			name := sv(d.DestinationName)
			return logsRow("destinations", sv(d.Arn), name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsDestination, NativeID: sv(d.Arn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags, p)
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

// scanLogsLogAnomalyDetectors stores the region's log anomaly detectors a
// page at a time, each with its tags (logsTags). Counts and errors go to p;
// it reports whether a write failed.
func scanLogsLogAnomalyDetectors(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, logsListLogAnomalyDetectorsOp,
		func(ctx context.Context, token *string) ([]logsTypes.AnomalyDetector, *string, error) {
			out, err := client.ListLogAnomalyDetectors(ctx, &cwlogs.ListLogAnomalyDetectorsInput{Limit: sdkaws.Int32(logsDescribePageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AnomalyDetectors, out.NextToken, nil
		},
		func(d logsTypes.AnomalyDetector) (*store.Resource, string, string) {
			name := sv(d.DetectorName)
			return logsRow("log anomaly detectors", sv(d.AnomalyDetectorArn), name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsLogAnomalyDetector, NativeID: sv(d.AnomalyDetectorArn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags, p)
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
					logGroupARNIn(acct.partition(), acct.ID, region, lgName))

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
			nativeID := arnFor(acct.partition(), "logs", region, acct.ID, "resource-policy:"+name)
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

// scanLogsLookupTables stores the region's lookup tables a page at a time,
// each with its tags (logsTags). DescribeLookupTables has no SDK paginator.
// Counts and errors go to p; it reports whether a write failed.
func scanLogsLookupTables(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, logsDescribeLookupTablesOp,
		func(ctx context.Context, token *string) ([]logsTypes.LookupTable, *string, error) {
			out, err := client.DescribeLookupTables(ctx, &cwlogs.DescribeLookupTablesInput{MaxResults: logsLookupTablesPageSize, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.LookupTables, out.NextToken, nil
		},
		func(lt logsTypes.LookupTable) (*store.Resource, string, string) {
			return logsRow("lookup tables", sv(lt.LookupTableArn), sv(lt.LookupTableName), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsLookupTable, NativeID: sv(lt.LookupTableArn), Name: lt.LookupTableName, Region: &region,
				AttributesJSON: mustJSON(lt), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanLogsScheduledQueries stores the region's scheduled queries a page at a
// time, each with its tags (logsTags). Counts and errors go to p; it reports
// whether a write failed.
func scanLogsScheduledQueries(ctx context.Context, client cwlogsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, logsListScheduledQueriesOp,
		func(ctx context.Context, token *string) ([]logsTypes.ScheduledQuerySummary, *string, error) {
			out, err := client.ListScheduledQueries(ctx, &cwlogs.ListScheduledQueriesInput{MaxResults: sdkaws.Int32(logsScheduledQueriesPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ScheduledQueries, out.NextToken, nil
		},
		func(q logsTypes.ScheduledQuerySummary) (*store.Resource, string, string) {
			name := sv(q.Name)
			return logsRow("scheduled queries", sv(q.ScheduledQueryArn), name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLogsScheduledQuery, NativeID: sv(q.ScheduledQueryArn), Name: &name, Region: &region,
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// --- Phase 2 helpers ---

// loadLogGroupsForRegion loads the log groups scan scanID stored for the
// account and region, for use by per-log-group Phase 2 scanners. A group
// stored by an earlier scan and not listed by this one (deleted since, or
// dropped as not found by its tag read) is not loaded, unless listDenied: a
// denied DescribeLogGroups confirms none of the groups after the denied page,
// so every group stored for the region is loaded rather than none of them.
func loadLogGroupsForRegion(acct *account, region string, st *store.Store, scanID string, listDenied bool) ([]store.Resource, error) {
	f := store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeLogsLogGroup},
		Regions:   []string{region},
		SeenBy:    scanID,
		Limit:     util.AllResources,
	}
	if listDenied {
		f.SeenBy = ""
	}
	return st.ListResources(f)
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
	var transient transientTally
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isLogsParentGone,
		func(ctx context.Context, p childParent) (int, int, error) {
			rows, err := listLogGroupIndexPolicy(ctx, client, acct, region, p.arn, names[p.arn], scanID)
			if isTransientNetworkError(err) {
				transient.skipped(p.arn, err)
				return 0, 0, nil
			}
			// A listing that ended on a repeated token returns the rows it read
			// with its error: they are stored and the error is returned with
			// their counts. Any other error comes with no rows.
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			children := make([]containedChild, len(rows))
			for i, r := range rows {
				children[i] = containedChild{row: r, parentID: parentID}
			}
			t, i, uerr := upsertContained(st, op, children)
			return t, i, errors.Join(err, uerr)
		})
	transient.warnSkipped(st, op, acct, region)
	return total, inserted, err
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
		// DescribeIndexPolicies has no paginator; a repeated token would loop
		// forever.
		if done, err := lastPage(input.NextToken, out.NextToken); done {
			return rows, err
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
		// ListSyslogConfigurations has no paginator; a repeated token would loop
		// forever.
		done, err := lastPage(input.NextToken, out.NextToken)
		if err != nil {
			return total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		if done {
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
	return childFanOutStoredSkipTransient(ctx, st, acct, region, op, parents, isLogsParentGone, byParentARN,
		func(ctx context.Context, p childParent) (int, int, error) {
			return storeLogsS3TableSources(ctx, client, acct, region, p.arn, st, scanID)
		})
}

// listLogsS3TableIntegrations returns the ARNs of the region's S3 Table
// integrations. observabilityadmin owns them and its scan stores them; they are
// listed again here because services scan concurrently, so that scan's rows
// cannot be waited for. The call goes to observabilityadmin's endpoint, so it
// is skipped where the dispatcher would skip that service, and an NXDOMAIN on
// the first page is that service's region gap. Any other error, a transient
// one included, is returned for scanLogs to join.
func listLogsS3TableIntegrations(ctx context.Context, client logsS3TableIntegrationLister, acct *account, region string, st *store.Store) ([]string, error) {
	const op = "observabilityadmin:ListS3TableIntegrations"
	const svc = "aws:observabilityadmin"
	if !acct.dispatchesTo(svc, region) {
		return nil, nil
	}
	pager := observabilityadmin.NewListS3TableIntegrationsPaginator(client, &observabilityadmin.ListS3TableIntegrationsInput{},
		func(o *observabilityadmin.ListS3TableIntegrationsPaginatorOptions) { o.Limit = 100 })
	var arns []string
	listed := false
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		switch {
		case err == nil:
			listed = true
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
		case !listed && isDNSNotFound(err):
			return nil, nil
		default:
			if isDNSNotFound(err) {
				return nil, lateNXDOMAIN(op, err)
			}
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
