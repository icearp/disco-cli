package aws

import (
	"context"
	"encoding/json"
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
func scanCloudTrail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total int, err error) {
	cfg := acct.cfg
	cfg.Region = region
	client := cloudtrail.NewFromConfig(cfg)
	return scanCloudTrailAll(ctx, client, acct, region, st, scanID)
}

// scanCloudTrailAll holds the testable scan body.
//
// Its phases run one at a time, each whatever the earlier ones returned
// (runHostPhases): trails, event data stores, channels and dashboards are
// permissioned independently; the resource policies are read for the Lake
// items those phases stored, and the insight-selector and event-configuration
// phases run over the trails and event data stores stored. A failed write or
// ctx's end stops the later phases. Every error is kept under the op that
// failed.
//
// Trails, event data stores, channels and dashboards have their tags read
// with ListTags (newCloudTrailTags); its denials are one warning for the
// whole scan.
func scanCloudTrailAll(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := newCloudTrailTags(client, acct.ID)
	var (
		parents                    cloudTrailParents
		channelArns, dashboardArns []string
	)
	total, err := runHostPhases(ctx, st, acct, region, "cloudtrail", 0, []hostPhase{
		{op: "cloudtrail:DescribeTrails", run: func(p *phaseTotals) (failed bool) {
			parents, failed = scanCloudTrailTrails(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "cloudtrail:ListEventDataStores", run: func(p *phaseTotals) bool {
			return scanCloudTrailEventDataStores(ctx, client, acct, region, st, scanID, tags, &parents, p)
		}},
		{op: ctListChannelsOp, run: func(p *phaseTotals) (failed bool) {
			channelArns, failed = scanCTChannels(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: ctListDashboardsOp, run: func(p *phaseTotals) (failed bool) {
			dashboardArns, failed = scanCTDashboards(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "cloudtrail:GetResourcePolicy", func() (int, error) {
			targets := slices.Concat(channelArns, dashboardArns)
			for _, eds := range parents.eventDataStores {
				targets = append(targets, eds.arn)
			}
			return scanCTResourcePolicies(ctx, client, targets, acct, region, st, scanID)
		}),
		countedPhase(0, cloudTrailInsightSelectorsOp, func() (int, error) {
			return scanCloudTrailInsightSelectors(ctx, client, acct, region, st, scanID, parents)
		}),
		countedPhase(0, cloudTrailEventConfigurationOp, func() (int, error) {
			return scanCloudTrailEventConfigurations(ctx, client, acct, region, st, scanID, parents)
		}),
	})
	tags.rows.warnDenials(st, acct, region)
	return total, err
}

const (
	ctListTagsOp = "cloudtrail:ListTags"
	// ctTagBatch is ListTags' cap on ResourceIdList: "The list has a limit
	// of 20 ARNs."
	ctTagBatch = 20
	// ctLakePageCap is the MaxResults maximum the model sets for
	// ListEventDataStores, ListChannels and ListDashboards alike.
	ctLakePageCap = 1000
)

// cloudTrailTags reads the tags of one scan's trails, event data stores,
// channels and dashboards: bulk makes the ListTags calls, and every phase
// reads its rows through rows, which shares one denial tally for the scan.
// peek sees what rows will read for a row without taking it.
type cloudTrailTags struct {
	bulk *bulkRowTags
	rows rowTagReader
	peek rowTagReader
}

// newCloudTrailTags reads tags with ListTags, sent up to 20 row NativeIDs
// (the ARNs the listings returned) a call. The op's model takes trail, event
// data store, dashboard and channel ARNs in one list, and the Service
// Reference lists all four resources for it. ResourceNotFoundException and
// EventDataStoreNotFoundException, the op's modeled not-found errors, drop a
// resource deleted after it was listed. The model also lists refusals of one
// resource (InactiveEventDataStoreException, the ARN-invalid codes) that fail
// the whole call, so a failed call is made again per ARN (splittingFailures).
// A resource another account owns (an organization trail seen from a
// delegated administrator) that the read denies or does not find is kept
// untagged without a warning (untaggedIfOtherAccount). The op's docs do not
// say whether an untagged resource is left out of ResourceTagList, so a
// resource the response leaves out is kept untagged and its read fails
// (errTagsNotDescribed) rather than being read as having no tags.
func newCloudTrailTags(client cloudtrailAPI, acctID string) cloudTrailTags {
	bulk := newBulkRowTags(ctListTagsOp, ctTagBatch, []string{"ResourceNotFoundException", "EventDataStoreNotFoundException"},
		func(r *store.Resource) string { return r.NativeID },
		func(ctx context.Context, arns []string) (map[string]*string, error) {
			return listCloudTrailTags(ctx, client, arns)
		}).splittingFailures()
	return cloudTrailTags{
		bulk: bulk,
		rows: bulk.bulkTagReader().untaggedIfOtherAccount(acctID).sharingDenials(),
		peek: bulk.bulkTagPeeker().untaggedIfOtherAccount(acctID),
	}
}

// listCloudTrailTags makes one ListTags read of arns and returns each
// resource its response describes, with its tags (nil for none). The model
// pages the op, though it documents NextToken as reserved for future use, so
// every page is read; a repeated token fails the read.
func listCloudTrailTags(ctx context.Context, client cloudtrailAPI, arns []string) (map[string]*string, error) {
	got := make(map[string]*string, len(arns))
	var token *string
	for {
		out, err := client.ListTags(ctx, &cloudtrail.ListTagsInput{ResourceIdList: arns, NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, rt := range out.ResourceTagList {
			got[sv(rt.ResourceId)] = kvTagsJSON(rt.TagsList, func(t cloudtrailtypes.Tag) (*string, *string) { return t.Key, t.Value })
		}
		done, err := lastPage(token, out.NextToken)
		if err != nil {
			return nil, err
		}
		if done {
			return got, nil
		}
		token = out.NextToken
	}
}

// readCloudTrailRowTags reads the tags of rows, keyed by NativeID, through
// t (readRowTags), leaving the read errors in errs, and returns the rows to
// store and the warning to send once they are stored.
func (t cloudTrailTags) readCloudTrailRowTags(ctx context.Context, st *store.Store, acct *account, region string, rows []*store.Resource, errs *rowTagErrs) ([]*store.Resource, func()) {
	arns := make([]string, len(rows))
	for i, r := range rows {
		arns[i] = r.NativeID
	}
	t.bulk.fetchBulkTags(ctx, arns)
	return t.rows.hook(ctx, st, acct, region, errs)(rows)
}

// fetchCloudTrailTagsFound reads the tags of rows, keyed by NativeID, and
// returns the rows whose read did not find the resource gone: those that a
// t.rows hook on them then keeps. Another read of the rows returned, made
// before that hook, thus reports no resource the tag read then drops.
func (t cloudTrailTags) fetchCloudTrailTagsFound(ctx context.Context, rows []*store.Resource) []*store.Resource {
	arns := make([]string, len(rows))
	for i, r := range rows {
		arns[i] = r.NativeID
	}
	t.bulk.fetchBulkTags(ctx, arns)
	found := make([]*store.Resource, 0, len(rows))
	for _, r := range rows {
		// readRowTags drops a row exactly when its read fails with a
		// notFound code.
		if _, err := t.peek.read(ctx, r); !isAPIErrorCode(err, t.peek.notFound...) {
			found = append(found, r)
		}
	}
	return found
}

// cloudTrailParents are the trails and event data stores the trail and
// event-data-store phases stored this scan; each id is the parent's name.
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

// scanCloudTrailEventDataStores stores the CloudTrail Lake event data stores,
// each with its tags, adds its counts and errors to p and the stored stores
// to parents, and reports whether the write failed. ListEventDataStores
// returns sparse entries; GetEventDataStore supplies KmsKeyId +
// FederationRoleArn needed by resolvers. A failed listing keeps the pages read
// before it (a denied one warns once they are stored). The tags are read
// first and their failures counted last: a store ListTags no longer finds is
// dropped before its GetEventDataStore read, and a tag read failure counts
// only for a store that read keeps, so neither read reports a store the other
// drops. Each other store is read with readRowDetails: a store
// GetEventDataStore no longer finds (EventDataStoreNotFoundException, in the
// op's model) was deleted since it was listed and is not stored; any other
// failure, and ctx's end, keeps the store as its ListEventDataStores element,
// verbatim: every field the list does not return marshals as null, and Status
// stays unset, so nothing reads as a false value. Such a store is no Insights
// parent, since its selectors are unknown, so a transient failure is
// returned, leaving the scan partial, rather than only warned about.
func scanCloudTrailEventDataStores(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, tags cloudTrailTags, parents *cloudTrailParents, p *phaseTotals) (storeWriteFailed bool) {
	const (
		op    = "cloudtrail:ListEventDataStores"
		getOp = "cloudtrail:GetEventDataStore"
	)
	var (
		rows    []*store.Resource
		arnless skipTally
	)
	listErr := ctListPages(func(token *string) (*string, error) {
		out, err := client.ListEventDataStores(ctx, &cloudtrail.ListEventDataStoresInput{NextToken: token, MaxResults: aws.Int32(ctLakePageCap)})
		if err != nil {
			return nil, err
		}
		for _, e := range out.EventDataStores {
			if sv(e.EventDataStoreArn) == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(e.Name)), nil)
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailEventDataStore, Region: &region, DiscoveredBy: scanID,
				// Keyed by the listed ARN the read asks for, which the body
				// repeats, so a body without one cannot store an empty NativeID.
				NativeID: sv(e.EventDataStoreArn), Name: e.Name, AttributesJSON: mustJSON(e),
			})
		}
		return out.NextToken, nil
	})
	warnNotStored(st, op, acct, region, &arnless, "event data stores have no EventDataStoreArn")

	var (
		mu      sync.Mutex
		insight = map[string]bool{}
	)
	rows = tags.fetchCloudTrailTagsFound(ctx, rows)
	rows, getWarn, getHard, getTransient := readRowDetails(ctx, st, acct, region, getOp, "event data store(s)", []string{"EventDataStoreNotFoundException"}, rows,
		func(ctx context.Context, r *store.Resource) (any, error) {
			detail, err := client.GetEventDataStore(ctx, &cloudtrail.GetEventDataStoreInput{EventDataStore: &r.NativeID})
			if err != nil {
				return nil, err
			}
			status := string(detail.Status)
			r.Name, r.Status = detail.Name, &status
			mu.Lock()
			defer mu.Unlock()
			insight[r.NativeID] = cloudTrailLogsManagementEvents(detail.AdvancedEventSelectors)
			return detail, nil
		})
	reads := detailedRows{getOp: getOp, getErrs: rowTagErrs{hard: getHard, transient: getTransient}, tags: tags.rows}
	kept, tagWarn := tags.rows.hook(ctx, st, acct, region, &reads.tagErrs)(rows)
	total, err := storeReadRows(st, acct, region, op, kept, listErr, func() {
		getWarn()
		tagWarn()
	})
	if reads.addPhase(p, op, total, err) {
		return true
	}
	for _, r := range kept {
		parent := childParent{id: sv(r.Name), arn: r.NativeID}
		parents.eventDataStores = append(parents.eventDataStores, parent)
		if insight[r.NativeID] {
			parents.insightEventDataStores = append(parents.insightEventDataStores, parent)
		}
	}
	return false
}

// cloudTrailReadTally counts the trails a read left without its part: denied,
// failed transiently, or answered not-found for a trail another account owns.
type cloudTrailReadTally struct {
	denied      skipTally
	transient   transientTally
	foreignGone skipTally
}

// addTrailReads adds every trail o tallied to t, keeping t's first of each
// cause unless t has none.
func (t *cloudTrailReadTally) addTrailReads(o *cloudTrailReadTally) {
	t.denied.addAll(&o.denied)
	t.transient.addAll(&o.transient.skipTally)
	t.foreignGone.addAll(&o.foreignGone)
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

// cloudTrailARNAccount returns the account an ARN names, "" when it does not
// parse.
func cloudTrailARNAccount(arn string) string {
	parsed, err := awsarn.Parse(arn)
	if err != nil {
		return ""
	}
	return parsed.AccountID
}

// scanCloudTrailTrails stores the classic trails, each with its logging
// status (cloudTrailTrailRow) and tags, adds its counts and errors to p,
// returns the stored trails as parents, and reports whether the write failed.
// A trail listed without an ARN is not stored and is warned about once. A
// trail's GetTrailStatus failure is reported only once its tag read has kept
// it, so a trail ListTags then finds gone is named in no warning or error.
func scanCloudTrailTrails(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string, tags cloudTrailTags, p *phaseTotals) (parents cloudTrailParents, storeWriteFailed bool) {
	const op = "cloudtrail:DescribeTrails"
	out, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	switch {
	case err != nil && isAccessDenied(err):
		return parents, p.addPhase(op, 0, skipIfAccessDenied(st, op, acct.ID, region, err))
	case err != nil:
		return parents, p.addPhase(op, 0, fmt.Errorf("%s: %w", op, err))
	}
	// listedTrail is a trail stored unless its tag read drops it, with what
	// its GetTrailStatus read left out.
	type listedTrail struct {
		trail     cloudtrailtypes.Trail
		status    cloudTrailReadTally
		statusErr error
	}
	var (
		rows    []*store.Resource
		listed  = map[string]*listedTrail{}
		arnless skipTally
	)
	for _, trail := range out.TrailList {
		if sv(trail.TrailARN) == "" {
			arnless.skipped(fmt.Sprintf("%q", sv(trail.Name)), nil)
			continue
		}
		l := &listedTrail{trail: trail}
		var r *store.Resource
		r, l.statusErr = cloudTrailTrailRow(ctx, client, acct, region, scanID, trail, &l.status)
		if r != nil {
			rows = append(rows, r)
			listed[r.NativeID] = l
		}
	}
	warnNotStored(st, op, acct, region, &arnless, "trails have no TrailARN")

	var tagErrs rowTagErrs
	kept, tagWarn := tags.readCloudTrailRowTags(ctx, st, acct, region, rows, &tagErrs)
	var status cloudTrailReadTally
	for _, r := range kept {
		l := listed[r.NativeID]
		status.addTrailReads(&l.status)
		p.errs.add("cloudtrail:GetTrailStatus", l.statusErr)
	}
	total, err := storeReadRows(st, acct, region, op, kept, nil, func() {
		status.warn(st, "cloudtrail:GetTrailStatus", "Status", acct, region)
		tagWarn()
	})
	if tagErrs.addPhase(p, ctListTagsOp, op, total, err) {
		return parents, true
	}
	for _, r := range kept {
		trail := listed[r.NativeID].trail
		name := sv(trail.Name)
		if name == "" {
			continue
		}
		parent := childParent{id: name, arn: r.NativeID}
		// GetEventConfiguration takes a trail name only, which it resolves
		// in the caller's account; a trail owned by another account (an
		// organization trail seen from a delegated administrator) would
		// answer TrailNotFoundException, so it gets no call.
		if cloudTrailARNAccount(r.NativeID) == acct.ID {
			parents.trails = append(parents.trails, parent)
		}
		// HasInsightSelectors false is answered by
		// InsightNotEnabledException; skip the call.
		if trail.HasInsightSelectors == nil || *trail.HasInsightSelectors {
			parents.insightTrails = append(parents.insightTrails, parent)
		}
	}
	return parents, false
}

// cloudTrailTrailRow builds one trail's row from its DescribeTrails element
// and GetTrailStatus. A denied or transient read keeps the row without its
// Status and is tallied in status. TrailNotFoundException (in the op's model)
// on a trail the scanned account owns means it vanished since it was listed:
// no row is returned. A trail another account owns (an organization trail
// seen from a delegated administrator) can answer not-found while it exists,
// as GetEventConfiguration does, so it keeps its row without Status and is
// tallied. Any other error, an NXDOMAIN among them (lateNXDOMAIN), keeps the
// row without Status and is returned.
func cloudTrailTrailRow(ctx context.Context, client cloudtrailAPI, acct *account, region, scanID string, trail cloudtrailtypes.Trail, status *cloudTrailReadTally) (*store.Resource, error) {
	arn := sv(trail.TrailARN)
	owner := cloudTrailARNAccount(arn)
	foreign := owner != "" && owner != acct.ID

	attrs := struct {
		Trail  any `json:"Trail"`
		Status any `json:"Status"`
	}{Trail: trail}
	var failure error
	out, err := client.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: &arn})
	// DescribeTrails reached this host, so an NXDOMAIN now is a failure of
	// this scan, not a region gap or a transient read.
	err = lateNXDOMAIN("cloudtrail:GetTrailStatus", err)
	switch gone := isAPIErrorCode(err, "TrailNotFoundException"); {
	case err == nil:
		// Through mustJSON so the nested output drops ResultMetadata.
		attrs.Status = json.RawMessage(mustJSON(out))
	case gone && !foreign:
		return nil, nil
	case gone:
		status.foreignGone.skipped(arn, err)
	case status.transient.drop(arn, err):
	case isAccessDenied(err):
		status.denied.skipped(arn, err)
	default:
		failure = fmt.Errorf("cloudtrail:GetTrailStatus %s: %w", arn, err)
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeCloudTrailTrail,
		NativeID:       arn,
		Name:           trail.Name,
		Region:         &region,
		AttributesJSON: mustJSON(attrs),
		DiscoveredBy:   scanID,
	}, failure
}
