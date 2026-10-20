package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudTrailTrail, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailEventDataStore, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailChannel, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailDashboard, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailResourcePolicy, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailInsightSelector, Service: "cloudtrail"})
	registerType(restype.Descriptor{Type: TypeCloudTrailEventConfiguration, Service: "cloudtrail"})
	registerService(serviceEntry{
		name: "aws:cloudtrail",
		fn:   scanCloudTrail,
	})
}

// cloudtrailAPI is the narrow set of CloudTrail operations called by
// scanCloudTrailAll.
type cloudtrailAPI interface {
	DescribeTrails(context.Context, *cloudtrail.DescribeTrailsInput, ...func(*cloudtrail.Options)) (*cloudtrail.DescribeTrailsOutput, error)
	GetTrailStatus(context.Context, *cloudtrail.GetTrailStatusInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetTrailStatusOutput, error)
	ListTags(context.Context, *cloudtrail.ListTagsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListTagsOutput, error)
	ListEventDataStores(context.Context, *cloudtrail.ListEventDataStoresInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListEventDataStoresOutput, error)
	GetEventDataStore(context.Context, *cloudtrail.GetEventDataStoreInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetEventDataStoreOutput, error)
	ListChannels(context.Context, *cloudtrail.ListChannelsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListChannelsOutput, error)
	GetChannel(context.Context, *cloudtrail.GetChannelInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetChannelOutput, error)
	ListDashboards(context.Context, *cloudtrail.ListDashboardsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListDashboardsOutput, error)
	GetDashboard(context.Context, *cloudtrail.GetDashboardInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetDashboardOutput, error)
	GetResourcePolicy(context.Context, *cloudtrail.GetResourcePolicyInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetResourcePolicyOutput, error)
	GetInsightSelectors(context.Context, *cloudtrail.GetInsightSelectorsInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetInsightSelectorsOutput, error)
	GetEventConfiguration(context.Context, *cloudtrail.GetEventConfigurationInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetEventConfigurationOutput, error)
}

// scanCloudTrail discovers CloudTrail trails in one region. DescribeTrails
// with IncludeShadowTrails=false returns only trails whose home region matches
// the current region, avoiding cross-region duplicates.
func scanCloudTrail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	cfg := acct.cfg
	cfg.Region = region
	client := cloudtrail.NewFromConfig(cfg)
	return scanCloudTrailAll(ctx, client, acct, region, st, scanID)
}

// scanCloudTrailAll holds the testable scan body.
//
// The older phases stop at their first hard error. The insight-selector and
// event-configuration phases run after them whatever they returned, over the
// parents listed before any failure (a failed event-data-store page's parents
// can be unstored; RecordHierarchyBatch reports them as a store warning), so
// a failure on either side costs the other no rows. The older side's
// transient error is demoted to a warning,
// since one transient member would make the dispatcher read the whole join as
// transient; the new phases tally their own transients into warnings.
func scanCloudTrailAll(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, parents, err := scanCloudTrailOlderPhases(ctx, client, acct, region, st, scanID)
	t1, i1, ierr := scanCloudTrailInsightSelectors(ctx, client, acct, region, st, scanID, parents)
	t2, i2, cerr := scanCloudTrailEventConfigurations(ctx, client, acct, region, st, scanID, parents)
	return total + t1 + t2, inserted + i1 + i2, errors.Join(demoteTransient(st, "cloudtrail", acct, region, err), ierr, cerr)
}

// cloudTrailParents are the trails and event data stores the older phases
// listed this scan (see scanCloudTrailOlderPhases for when one can be
// unstored); each id is the parent's name.
type cloudTrailParents struct {
	// trails are the trails owned by the scanned account.
	trails []childParent
	// insightTrails are the trails whose HasInsightSelectors is not false.
	insightTrails   []childParent
	eventDataStores []childParent
	// insightEventDataStores are the event data stores that log management
	// events, the only events Insights can be enabled on for a store.
	insightEventDataStores []childParent
}

// cloudTrailLogsManagementEvents reports whether an advanced event selector
// selects eventCategory = Management.
func cloudTrailLogsManagementEvents(selectors []cloudtrailtypes.AdvancedEventSelector) bool {
	for _, s := range selectors {
		for _, f := range s.FieldSelectors {
			if sv(f.Field) == "eventCategory" && slices.Contains(f.Equals, "Management") {
				return true
			}
		}
	}
	return false
}

// scanCloudTrailOlderPhases runs the phases that predate the per-parent
// configuration phases sequentially, stopping at the first hard error, and
// returns the trails and event data stores listed before it. Trails are
// returned only once stored; event data stores are collected ahead of their
// page's upsert, so a failed page's stores can be returned unstored.
func scanCloudTrailOlderPhases(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents cloudTrailParents, err error) {
	// Phase 1: classic trails. AccessDenied here does not bar Phase 2 — Lake
	// event-data-store permissions are granted independently.
	out, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	switch {
	case err != nil && isAccessDenied(err):
		_ = skipIfAccessDenied(st, "cloudtrail:DescribeTrails", acct.ID, region, err)
	case err != nil:
		return 0, 0, parents, fmt.Errorf("cloudtrail:DescribeTrails: %w", err)
	default:
		// GetTrailStatus enriches each trail with logging status.
		type trailWithStatus struct {
			Trail  any `json:"Trail"`
			Status any `json:"Status"`
		}
		var (
			batch  []*store.Resource
			listed cloudTrailParents
		)
		for _, trail := range out.TrailList {
			arn := sv(trail.TrailARN)
			var statusData any
			if statusOut, sErr := client.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: &arn}); sErr == nil {
				// Through mustJSON so the nested output drops ResultMetadata.
				statusData = json.RawMessage(mustJSON(statusOut))
			}
			attrs := trailWithStatus{Trail: trail, Status: statusData}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudTrailTrail,
				NativeID:       arn,
				Name:           trail.Name,
				Region:         &region,
				AttributesJSON: mustJSON(attrs),
				DiscoveredBy:   scanID,
			}
			if tagsOut, tErr := client.ListTags(ctx, &cloudtrail.ListTagsInput{ResourceIdList: []string{arn}}); tErr == nil && len(tagsOut.ResourceTagList) > 0 {
				tags := tagsOut.ResourceTagList[0].TagsList
				j := mustJSON(tags)
				r.TagsJSON = &j
			}
			batch = append(batch, r)
			if name := sv(trail.Name); arn != "" && name != "" {
				p := childParent{id: name, arn: arn}
				// GetEventConfiguration takes a trail name only, which it
				// resolves in the caller's account; a trail owned by another
				// account (an organization trail seen from a delegated
				// administrator) would answer TrailNotFoundException, so it
				// gets no call.
				if parsed, aErr := awsarn.Parse(arn); aErr == nil && parsed.AccountID == acct.ID {
					listed.trails = append(listed.trails, p)
				}
				// HasInsightSelectors false is answered by
				// InsightNotEnabledException; skip the call.
				if trail.HasInsightSelectors == nil || *trail.HasInsightSelectors {
					listed.insightTrails = append(listed.insightTrails, p)
				}
			}
		}
		if len(batch) > 0 {
			n, uErr := st.UpsertResources(batch)
			if uErr != nil {
				return 0, 0, parents, fmt.Errorf("upsert CloudTrail trails: %w", uErr)
			}
			total += len(batch)
			inserted += n
			parents = listed
		}
	}

	// Phase 2: CloudTrail Lake event data stores. List returns sparse entries;
	// GetEventDataStore supplies KmsKeyId + FederationRoleArn needed by resolvers.
	// Parents are collected ahead of their page's upsert; when that page fails,
	// RecordHierarchyBatch reports its missing parents as a store warning.
	var edsMu sync.Mutex
	p := cloudtrail.NewListEventDataStoresPaginator(client, &cloudtrail.ListEventDataStoresInput{})
	t2, n2, err := pageScanConcurrent(ctx, "cloudtrail:ListEventDataStores", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*cloudtrail.ListEventDataStoresOutput, error) { return p.NextPage(c) },
		func(o *cloudtrail.ListEventDataStoresOutput) []string {
			out := make([]string, 0, len(o.EventDataStores))
			for _, e := range o.EventDataStores {
				if a := sv(e.EventDataStoreArn); a != "" {
					out = append(out, a)
				}
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			detail, gErr := client.GetEventDataStore(gctx, &cloudtrail.GetEventDataStoreInput{EventDataStore: &arn})
			if gErr != nil {
				if isAccessDenied(gErr) {
					return nil, nil
				}
				return nil, fmt.Errorf("cloudtrail:GetEventDataStore %s: %w", arn, gErr)
			}
			status := string(detail.Status)
			edsARN := sv(detail.EventDataStoreArn)
			if edsARN != "" {
				p := childParent{id: sv(detail.Name), arn: edsARN}
				edsMu.Lock()
				parents.eventDataStores = append(parents.eventDataStores, p)
				if cloudTrailLogsManagementEvents(detail.AdvancedEventSelectors) {
					parents.insightEventDataStores = append(parents.insightEventDataStores, p)
				}
				edsMu.Unlock()
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudTrailEventDataStore,
				NativeID:       edsARN,
				Name:           detail.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(detail),
				DiscoveredBy:   scanID,
			}, nil
		}, 0)
	total += t2
	inserted += n2
	if err != nil {
		return total, inserted, parents, err
	}

	t3, n3, eErr := scanCloudTrailExtended(ctx, client, acct, region, st, scanID)
	total += t3
	inserted += n3
	return total, inserted, parents, eErr
}
