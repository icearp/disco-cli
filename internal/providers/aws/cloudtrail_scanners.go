package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
// Every phase runs whatever the others returned: trails, event data stores
// and the extended phases are permissioned independently, and the
// insight-selector and event-configuration phases run over the parents the
// earlier phases stored. Errors join through phaseErrs, which demotes the
// transient ones to warnings only beside a hard error.
func scanCloudTrailAll(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var errs phaseErrs
	total, inserted, parents := scanCloudTrailOlderPhases(ctx, client, acct, region, st, scanID, &errs)
	t1, i1, ierr := scanCloudTrailInsightSelectors(ctx, client, acct, region, st, scanID, parents)
	errs.add("cloudtrail:GetInsightSelectors", ierr)
	t2, i2, cerr := scanCloudTrailEventConfigurations(ctx, client, acct, region, st, scanID, parents)
	errs.add("cloudtrail:GetEventConfiguration", cerr)
	return total + t1 + t2, inserted + i1 + i2, errs.join(st, acct, region)
}

// cloudTrailParents are the trails and event data stores the older phases
// stored this scan; each id is the parent's name.
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
// configuration phases, each whatever the others returned, adds their errors
// to errs, and returns the trails and event data stores stored as parents.
func scanCloudTrailOlderPhases(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, errs *phaseErrs) (total, inserted int, parents cloudTrailParents) {
	total, inserted, parents, err := scanCloudTrailTrails(ctx, client, acct, region, st, scanID)
	errs.add("cloudtrail:DescribeTrails", err)
	t, i, err := scanCloudTrailEventDataStores(ctx, client, acct, region, st, scanID, &parents)
	total, inserted = total+t, inserted+i
	errs.add("cloudtrail:ListEventDataStores", err)
	t, i, err = scanCloudTrailExtended(ctx, client, acct, region, st, scanID, parents.eventDataStores)
	errs.add("cloudtrail", err)
	return total + t, inserted + i, parents
}

// scanCloudTrailEventDataStores stores the CloudTrail Lake event data stores
// and adds the stored ones to parents. ListEventDataStores returns sparse
// entries; GetEventDataStore supplies KmsKeyId + FederationRoleArn needed by
// resolvers. A denied listing keeps the pages read before it and warns. A
// denied or transient GetEventDataStore keeps the store as its list element,
// with one warning per cause; a store it no longer finds
// (EventDataStoreNotFoundException, in the op's model) was deleted since it
// was listed and is not stored. Any other error fails the phase after the
// other stores are read and stored.
func scanCloudTrailEventDataStores(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, parents *cloudTrailParents) (int, int, error) {
	var (
		listed  []cloudtrailtypes.EventDataStore
		arnless skipTally
		listErr error
	)
	p := cloudtrail.NewListEventDataStoresPaginator(client, &cloudtrail.ListEventDataStoresInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "cloudtrail:ListEventDataStores", acct.ID, region, fmt.Errorf(
					"listing truncated, event data stores on this and later pages not stored: %w", err))
				break
			}
			listErr = fmt.Errorf("cloudtrail:ListEventDataStores: %w", err)
			break
		}
		for _, e := range page.EventDataStores {
			if sv(e.EventDataStoreArn) == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(e.Name)), nil)
				continue
			}
			listed = append(listed, e)
		}
	}
	warnNotStored(st, "cloudtrail:ListEventDataStores", acct, region, &arnless, "event data stores have no EventDataStoreArn")

	byARN := make(map[string]cloudtrailtypes.EventDataStore, len(listed))
	arns := make([]string, 0, len(listed))
	for _, e := range listed {
		byARN[sv(e.EventDataStoreArn)] = e
		arns = append(arns, sv(e.EventDataStoreArn))
	}
	var (
		denied    skipTally
		transient transientTally
		mu        sync.Mutex
		failed    = map[string]error{}
		insight   = map[string]bool{}
	)
	// A hard error is collected rather than returned to describeFanOut, which
	// would cancel the other stores' reads and discard the rows read so far.
	// describe therefore never returns an error, and neither does describeFanOut.
	rows, _ := describeFanOut(ctx, st, acct, region, "cloudtrail:GetEventDataStore", arns, neverSkip,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			detail, gErr := client.GetEventDataStore(ctx, &cloudtrail.GetEventDataStoreInput{EventDataStore: &arn})
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailEventDataStore, Region: &region, DiscoveredBy: scanID,
			}
			switch {
			case gErr == nil:
				status := string(detail.Status)
				// Keyed by the listed ARN the read asked for, which the body
				// repeats, so a body without one cannot store an empty NativeID.
				r.NativeID, r.Name, r.Status, r.AttributesJSON = arn, detail.Name, &status, mustJSON(detail)
				mu.Lock()
				insight[r.NativeID] = cloudTrailLogsManagementEvents(detail.AdvancedEventSelectors)
				mu.Unlock()
				return r, nil
			case isAPIErrorCode(gErr, "EventDataStoreNotFoundException"):
				return nil, nil
			case transient.drop(arn, gErr):
			case isAccessDenied(gErr):
				denied.skipped(arn, gErr)
			default:
				mu.Lock()
				failed[arn] = gErr
				mu.Unlock()
				return nil, nil
			}
			// Kept as its ListEventDataStores element, verbatim: every field the
			// list no longer returns marshals as null, and Status as "", so
			// nothing reads as a false value. Without the selectors the store
			// is no Insights parent.
			e := byARN[arn]
			r.NativeID, r.Name, r.AttributesJSON = arn, e.Name, mustJSON(e)
			return r, nil
		})
	var errs phaseErrs
	errs.add("cloudtrail:ListEventDataStores", listErr)
	for _, arn := range slices.Sorted(maps.Keys(failed)) {
		errs.add("cloudtrail:GetEventDataStore", fmt.Errorf("cloudtrail:GetEventDataStore %s: %w", arn, failed[arn]))
	}
	total, inserted, uerr := upsertBatch(st, rows, "cloudtrail event-data-stores")
	if uerr != nil {
		return 0, 0, errors.Join(errs.join(st, acct, region), uerr)
	}
	for _, r := range rows {
		p := childParent{id: sv(r.Name), arn: r.NativeID}
		parents.eventDataStores = append(parents.eventDataStores, p)
		if insight[r.NativeID] {
			parents.insightEventDataStores = append(parents.insightEventDataStores, p)
		}
	}
	const kept = "%d event data store(s) stored as their ListEventDataStores element, without the store body, first %s: %w"
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, "cloudtrail:GetEventDataStore", acct.ID, region, fmt.Errorf(kept, denied.count, denied.first, denied.err))
	}
	if transient.count > 0 {
		_ = skipIfTransient(st, "cloudtrail:GetEventDataStore", acct.ID, region, fmt.Errorf(kept, transient.count, transient.first, transient.err))
	}
	return total, inserted, errs.join(st, acct, region)
}

// cloudTrailTrailReads tallies, per optional per-trail read, the trails stored
// without that read's part.
type cloudTrailTrailReads struct{ status, tags cloudTrailReadTally }

// cloudTrailReadTally counts the trails a read left without its part: denied,
// failed transiently, or answered not-found for a trail another account owns.
type cloudTrailReadTally struct {
	denied      skipTally
	transient   transientTally
	foreignGone skipTally
}

// tally counts a kept trail whose read returned err (nil counts nothing).
func (t *cloudTrailReadTally) tally(arn string, err error) {
	switch {
	case err == nil:
	case t.transient.drop(arn, err):
	default:
		t.denied.skipped(arn, err)
	}
}

// warn reports the trails stored without what, once per cause; call it only
// after they are stored.
func (t *cloudTrailReadTally) warn(st *store.Store, op, what string, acct *account, region string) {
	const msg = "%d trail(s) stored without %s, first %s: %w"
	if t.denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf(msg, t.denied.count, what, t.denied.first, t.denied.err))
	}
	if t.transient.count > 0 {
		_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf(msg, t.transient.count, what, t.transient.first, t.transient.err))
	}
	if t.foreignGone.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d trail(s) owned by another account stored without %s, which the read answered not found; first %s: %v",
				t.foreignGone.count, what, t.foreignGone.first, t.foreignGone.err),
		})
	}
}

// cloudTrailReadFailure returns err wrapped with op and arn, or nil when err
// is nil, transient or a denial: those keep the trail without that read.
func cloudTrailReadFailure(op, arn string, err error) error {
	if err == nil || isTransientNetworkError(err) || isAccessDenied(err) {
		return nil
	}
	return fmt.Errorf("%s %s: %w", op, arn, err)
}

// scanCloudTrailTrails stores the classic trails, each with its logging
// status and tags, and returns them as parents. A trail listed without an ARN
// is not stored and is warned about once. A trail that vanished after
// DescribeTrails (see cloudTrailTrailRow) is not stored. Any other read error
// is returned after the other trails are read and stored.
func scanCloudTrailTrails(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents cloudTrailParents, err error) {
	out, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	switch {
	case err != nil && isAccessDenied(err):
		return 0, 0, parents, skipIfAccessDenied(st, "cloudtrail:DescribeTrails", acct.ID, region, err)
	case err != nil:
		return 0, 0, parents, fmt.Errorf("cloudtrail:DescribeTrails: %w", err)
	}
	var (
		batch   []*store.Resource
		listed  cloudTrailParents
		reads   cloudTrailTrailReads
		arnless skipTally
		failed  phaseErrs
	)
	for _, trail := range out.TrailList {
		if sv(trail.TrailARN) == "" {
			arnless.skipped(fmt.Sprintf("%q", sv(trail.Name)), nil)
			continue
		}
		r, owner, readErr := cloudTrailTrailRow(ctx, client, acct, region, scanID, trail, &reads)
		failed.add("cloudtrail", readErr)
		if r == nil {
			continue
		}
		batch = append(batch, r)
		if name := sv(trail.Name); name != "" {
			p := childParent{id: name, arn: r.NativeID}
			// GetEventConfiguration takes a trail name only, which it
			// resolves in the caller's account; a trail owned by another
			// account (an organization trail seen from a delegated
			// administrator) would answer TrailNotFoundException, so it
			// gets no call.
			if owner == acct.ID {
				listed.trails = append(listed.trails, p)
			}
			// HasInsightSelectors false is answered by
			// InsightNotEnabledException; skip the call.
			if trail.HasInsightSelectors == nil || *trail.HasInsightSelectors {
				listed.insightTrails = append(listed.insightTrails, p)
			}
		}
	}
	warnNotStored(st, "cloudtrail:DescribeTrails", acct, region, &arnless, "trails have no TrailARN")
	if len(batch) > 0 {
		n, uErr := st.UpsertResources(batch)
		if uErr != nil {
			return 0, 0, parents, errors.Join(failed.join(st, acct, region), fmt.Errorf("upsert CloudTrail trails: %w", uErr))
		}
		total, inserted, parents = len(batch), n, listed
		reads.status.warn(st, "cloudtrail:GetTrailStatus", "Status", acct, region)
		reads.tags.warn(st, "cloudtrail:ListTags", "tags", acct, region)
	}
	return total, inserted, parents, failed.join(st, acct, region)
}

// cloudTrailTrailRow builds one trail's row from its DescribeTrails element,
// GetTrailStatus and ListTags, and returns the account its ARN names ("" when
// the ARN does not parse). A denied or transient read keeps the row without
// that part and is tallied in reads. A not-found (TrailNotFoundException from
// GetTrailStatus, ResourceNotFoundException from ListTags, both in their ops'
// models) on a trail the scanned account owns means it vanished since it was
// listed: no row is returned. A trail another account owns (an organization
// trail seen from a member account) can answer not-found while it exists, as
// GetEventConfiguration does, so it keeps its row without that part and is
// tallied in reads. Any other read error returns no row.
func cloudTrailTrailRow(ctx context.Context, client cloudtrailAPI, acct *account, region, scanID string, trail cloudtrailtypes.Trail, reads *cloudTrailTrailReads) (r *store.Resource, owner string, err error) {
	arn := sv(trail.TrailARN)
	if parsed, aErr := awsarn.Parse(arn); aErr == nil {
		owner = parsed.AccountID
	}
	foreign := owner != "" && owner != acct.ID

	statusOut, statusErr := client.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: &arn})
	haveStatus := statusErr == nil
	statusGone := isAPIErrorCode(statusErr, "TrailNotFoundException")
	if statusGone && !foreign {
		return nil, owner, nil
	}
	if err := cloudTrailReadFailure("cloudtrail:GetTrailStatus", arn, statusErr); err != nil && !statusGone {
		return nil, owner, err
	}
	tagsOut, tagsErr := client.ListTags(ctx, &cloudtrail.ListTagsInput{ResourceIdList: []string{arn}})
	haveTags := tagsErr == nil
	tagsGone := isAPIErrorCode(tagsErr, "ResourceNotFoundException")
	if tagsGone && !foreign {
		return nil, owner, nil
	}
	if err := cloudTrailReadFailure("cloudtrail:ListTags", arn, tagsErr); err != nil && !tagsGone {
		return nil, owner, err
	}
	if statusGone {
		reads.status.foreignGone.skipped(arn, statusErr)
	} else {
		reads.status.tally(arn, statusErr)
	}
	if tagsGone {
		reads.tags.foreignGone.skipped(arn, tagsErr)
	} else {
		reads.tags.tally(arn, tagsErr)
	}

	attrs := struct {
		Trail  any `json:"Trail"`
		Status any `json:"Status"`
	}{Trail: trail}
	if haveStatus {
		// Through mustJSON so the nested output drops ResultMetadata.
		attrs.Status = json.RawMessage(mustJSON(statusOut))
	}
	r = &store.Resource{
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
	if haveTags && len(tagsOut.ResourceTagList) > 0 {
		j := mustJSON(tagsOut.ResourceTagList[0].TagsList)
		r.TagsJSON = &j
	}
	return r, owner, nil
}
