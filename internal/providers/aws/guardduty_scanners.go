package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGuardDutyCustomDetectionRuleAssociation, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyCustomDetectionRuleOrgConfiguration, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyDetector, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyFilter, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyIPSet, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyMember, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyMalwareProtectionPlan, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyOrganizationConfiguration, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyPublishingDestination, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyThreatEntitySet, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyThreatIntelSet, Service: "guardduty"})
	registerType(restype.Descriptor{Type: TypeGuardDutyTrustedEntitySet, Service: "guardduty"})
	registerService(serviceEntry{
		name: "aws:guardduty",
		fn:   scanGuardDuty,
	})
}

// guarddutyAPI is the narrow set of GuardDuty operations the detector phases
// call.
type guarddutyAPI interface {
	ListDetectors(context.Context, *guardduty.ListDetectorsInput, ...func(*guardduty.Options)) (*guardduty.ListDetectorsOutput, error)
	GetDetector(context.Context, *guardduty.GetDetectorInput, ...func(*guardduty.Options)) (*guardduty.GetDetectorOutput, error)
	ListFilters(context.Context, *guardduty.ListFiltersInput, ...func(*guardduty.Options)) (*guardduty.ListFiltersOutput, error)
	GetFilter(context.Context, *guardduty.GetFilterInput, ...func(*guardduty.Options)) (*guardduty.GetFilterOutput, error)
	ListIPSets(context.Context, *guardduty.ListIPSetsInput, ...func(*guardduty.Options)) (*guardduty.ListIPSetsOutput, error)
	GetIPSet(context.Context, *guardduty.GetIPSetInput, ...func(*guardduty.Options)) (*guardduty.GetIPSetOutput, error)
	ListMembers(context.Context, *guardduty.ListMembersInput, ...func(*guardduty.Options)) (*guardduty.ListMembersOutput, error)
}

// gdPageCap is the model's MaxResults cap (1 to 50) of ListDetectors and of
// every per-detector listing.
const gdPageCap = 50

// scanGuardDuty discovers the GuardDuty resources of one region: the
// detectors, each detector's filters, members, IP sets, publishing
// destinations and threat and trusted entity and intel sets, the account's
// malware protection plans, each detector's organization configuration, and
// the account's custom detection rule associations and the delegated
// administrator's organization configurations of those rules. Every listing
// runs as its own phase, one at a time: a phase's error does not stop the
// later ones; a failed write and ctx's end do; a region whose ListDetectors
// finds no endpoint runs no other phase (runHostPhases). The per-detector
// listings read every detector this scan listed, each of which it stored.
//
// Filters, IP sets and detectors carry their tags in the get that completes
// their row; one whose get fails is stored untagged. Publishing destinations
// have theirs read per row with ListTagsForResource (gdTags); the entity and
// intel sets (gdSetTags), malware protection plans (gdPlanTags) and custom
// detection rule associations (gdAssocTags) with their own get. Members are
// other accounts' detectors and have no tags read.
func scanGuardDuty(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := guardduty.NewFromConfig(acct.cfg, func(o *guardduty.Options) { o.Region = region })
	var (
		detectors  []childParent
		orgRefused bool
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "guardduty", 0, []hostPhase{
		{op: "guardduty:ListDetectors", run: func(p *phaseTotals) (failed bool) {
			detectors, failed = scanGDDetectors(ctx, client, acct, region, st, scanID, p)
			return failed
		}},
		{op: "guardduty:ListFilters", run: func(p *phaseTotals) bool {
			return scanGDFilters(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListMembers", run: func(p *phaseTotals) bool {
			return scanGDMembers(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListIPSets", run: func(p *phaseTotals) bool {
			return scanGDIPSets(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListMalwareProtectionPlans", run: func(p *phaseTotals) bool {
			return scanGDMalwareProtectionPlans(ctx, client, acct, region, st, scanID, p)
		}},
		{op: "guardduty:ListPublishingDestinations", run: func(p *phaseTotals) bool {
			return scanGDPublishingDestinations(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListThreatEntitySets", run: func(p *phaseTotals) bool {
			return scanGDThreatEntitySets(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListThreatIntelSets", run: func(p *phaseTotals) bool {
			return scanGDThreatIntelSets(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: "guardduty:ListTrustedEntitySets", run: func(p *phaseTotals) bool {
			return scanGDTrustedEntitySets(ctx, client, acct, region, st, scanID, detectors, p)
		}},
		{op: gdOrgConfigOp, run: func(p *phaseTotals) bool {
			t, i, refused, err := scanGDOrganizationConfigurations(ctx, client, acct, region, st, scanID, childParentIDs(detectors))
			orgRefused = refused
			return p.addPhase(gdOrgConfigOp, t, i, err)
		}},
		// The custom-detection-rule ops take no detector, but a region without
		// one has GuardDuty off. Rule associations and organization
		// configurations in such a region are not listed; whether they can
		// exist there is unverified.
		{op: gdListCustomRuleAssocsOp, run: func(p *phaseTotals) bool {
			if len(detectors) == 0 {
				return false
			}
			return scanGDCustomRuleAssociations(ctx, client, acct, region, st, scanID, p)
		}},
		// A region that refused the account its organization configuration is
		// not the delegated administrator's, which the organization-level list
		// needs as well.
		{op: gdListCustomRuleOrgOp, run: func(p *phaseTotals) bool {
			if len(detectors) == 0 || orgRefused {
				return false
			}
			t, i, err := scanGDCustomRuleOrgConfigurations(ctx, client, acct, region, st, scanID)
			return p.addPhase(gdListCustomRuleOrgOp, t, i, err)
		}},
	})
	return total, inserted, err
}

func guardDutyDetectorARN(partition, region, accountID, detectorID string) string {
	return arnFor(partition, "guardduty", region, accountID, "detector/"+detectorID)
}

// gdDetectorChildARN is the ARN of a detector's child of kind: the Service
// Reference's ARN format for filter, ipset, publishingdestination,
// threatentityset, threatintelset and trustedentityset. A member has no ARN
// of its own; its NativeID takes the same shape with kind "member".
func gdDetectorChildARN(partition, region, accountID, detectorID, kind, id string) string {
	return guardDutyDetectorARN(partition, region, accountID, detectorID) + "/" + kind + "/" + id
}

// scanGDDetectors lists the region's detectors and stores each one,
// completed by GetDetector (readRowDetails): the get's body as the
// attributes, its Status and its Tags. It returns the detectors listed, all
// of which the child listings read: every listed detector is stored.
//
// ListDetectors returns ids only, so a detector whose get fails is stored as
// its listed element: the same NativeID, the DetectorId as its attributes,
// no status and no tags. It was listed, so its children are listed under
// it. GetDetector models no not-found code, so no detector is dropped. A
// denied get warns once, after the write succeeded; any other error is
// returned once the rows are stored, a transient one apart so that it is
// demoted beside a hard error. It reports whether the write failed, after
// which the scanner must stop.
func scanGDDetectors(ctx context.Context, client guarddutyAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (detectors []childParent, storeWriteFailed bool) {
	const (
		listOp = "guardduty:ListDetectors"
		getOp  = "guardduty:GetDetector"
	)
	ids, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
		out, err := client.ListDetectors(ctx, &guardduty.ListDetectorsInput{NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
		if err != nil {
			return nil, nil, err
		}
		return out.DetectorIds, out.NextToken, nil
	})
	var (
		skips notStoredSkips
		rows  []*store.Resource
	)
	// Read only while the gets run, each of which completes its own row.
	idOf := map[*store.Resource]string{}
	for _, id := range ids {
		if id == "" {
			skips.skipped("detector(s) with an empty id", "(a detector with no id)")
			continue
		}
		d := childParent{id: id, arn: guardDutyDetectorARN(acct.partition(), region, acct.ID, id)}
		detectors = append(detectors, d)
		r := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeGuardDutyDetector, NativeID: d.arn, Region: &region,
			AttributesJSON: mustJSON(map[string]string{"DetectorId": id}), DiscoveredBy: scanID,
		}
		rows = append(rows, r)
		idOf[r] = id
	}
	rows, getWarn, hard, transient := readRowDetails(ctx, st, acct, region, getOp, "detector(s)", nil, rows, func(ctx context.Context, r *store.Resource) (any, error) {
		desc, err := client.GetDetector(ctx, &guardduty.GetDetectorInput{DetectorId: sdkaws.String(idOf[r])})
		if err != nil {
			return nil, err
		}
		status := string(desc.Status)
		r.Status, r.TagsJSON = &status, mapTagsJSON(desc.Tags)
		return desc, nil
	})
	total, inserted, err := storeReadRows(st, acct, region, listOp, rows, listErr, func() {
		skips.warn(st, listOp, acct, region)
		getWarn()
	})
	if (rowTagErrs{hard, transient}).addPhase(p, getOp, listOp, total, inserted, err) {
		return nil, true
	}
	return detectors, false
}

// gdDetectorSet is a kind of detector child that GuardDuty lists by id only
// and returns in full from a get (filters, IP sets): its ops; what names its
// items in messages; typ, kind and idKey build the row of one listed id
// (gdIDSetRow); page is one detector's listing page; get completes a listed
// id's row r: it returns the body stored as the attributes and sets r's
// other fields the body carries. isDenied reports a get's denial; nil means
// isAccessDenied.
type gdDetectorSet struct {
	listOp, getOp, what, typ, kind, idKey string
	page                                  func(ctx context.Context, detector string, token *string) ([]string, *string, error)
	get                                   func(ctx context.Context, detector, id string, r *store.Resource) (any, error)
	isDenied                              func(error) bool
}

// storeGDDetectorSets lists s's ids for each detector, the detectors read
// concurrently, completes each id's row with its get
// (readRowDetailsDeniedBy) and stores the rows under their detector
// (RecordHierarchyBatch). A detector's failed listing keeps the ids read
// before it and costs no other detector its rows: a denial warns once and any
// other error is returned after the rows are stored (the listings model no
// not-found code). An id whose get fails is stored as its listed element,
// under its detector; the gets model no not-found code, so none is dropped. A
// get s.isDenied reports warns once the rows are stored, and the others'
// errors are returned, a transient one apart. It reports whether the write
// failed, after which the scanner must stop.
func storeGDDetectorSets(ctx context.Context, st *store.Store, acct *account, region, scanID string, detectors []childParent, s gdDetectorSet, p *phaseTotals) (storeWriteFailed bool) {
	reads, stopped := readEachKey(ctx, detectors, func(ctx context.Context, d childParent) parentItems[string] {
		return readParentPages(ctx, d.id, s.page)
	})
	type key struct {
		detector childParent
		id       string
	}
	// Read only while the gets run, each of which completes its own row.
	keys := map[*store.Resource]key{}
	out := parentOutcomes{op: s.listOp, parentOf: map[*store.Resource]string{}}
	for i, read := range reads {
		d := detectors[i]
		addParentRows(&out, d.id, read, func(did, id string) (*store.Resource, string, string) {
			r, why, skipKey := gdIDSetRow(acct, region, scanID, s.typ, s.what, s.kind, s.idKey, did, id)
			if r != nil {
				keys[r] = key{d, id}
			}
			return r, why, skipKey
		})
	}
	out.stopped(stopped)
	var (
		getWarn         func()
		hard, transient error
	)
	out.rows, getWarn, hard, transient = readRowDetailsDeniedBy(ctx, st, acct, region, s.getOp, s.what, nil, orAccessDenied(s.isDenied), out.rows, func(ctx context.Context, r *store.Resource) (any, error) {
		k := keys[r]
		return s.get(ctx, k.detector.id, k.id, r)
	})
	for _, r := range out.rows {
		out.parentOf[r] = store.ResourceID("aws", acct.ID, keys[r].detector.arn)
	}
	total, inserted, err := out.store(st, acct, region)
	if !errors.Is(err, store.ErrStoreWrite) {
		getWarn()
	}
	return rowTagErrs{hard, transient}.addPhase(p, s.getOp, s.listOp, total, inserted, err)
}

// scanGDFilters stores each detector's filters, completed by GetFilter: its
// body as the attributes, its Name and its Tags.
func scanGDFilters(ctx context.Context, client guarddutyAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeGDDetectorSets(ctx, st, acct, region, scanID, detectors, gdDetectorSet{
		listOp: "guardduty:ListFilters", getOp: "guardduty:GetFilter", what: "filter(s)",
		typ: TypeGuardDutyFilter, kind: "filter", idKey: "FilterName",
		page: func(ctx context.Context, did string, token *string) ([]string, *string, error) {
			out, err := client.ListFilters(ctx, &guardduty.ListFiltersInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FilterNames, out.NextToken, nil
		},
		get: func(ctx context.Context, did, name string, r *store.Resource) (any, error) {
			f, err := client.GetFilter(ctx, &guardduty.GetFilterInput{DetectorId: &did, FilterName: &name})
			if err != nil {
				return nil, err
			}
			r.Name, r.TagsJSON = f.Name, mapTagsJSON(f.Tags)
			return f, nil
		},
	}, p)
}

// scanGDIPSets stores each detector's IP sets, completed by GetIPSet: its
// body as the attributes, its Name and its Tags. One stored as its listed
// element is named by its id.
//
// A member account's ListIPSets returns its administrator's IP sets (the
// SDK doc: "If you use this operation from a member account, the IPSets
// returned are the IPSets from the associated administrator account").
// Whether GetIPSet refuses a member those sets was not observed live; a get
// refused as member restricted (isGuardDutyMemberRestricted) is a denial,
// so the set is kept as its listed element and warned about once, not
// returned as an error.
func scanGDIPSets(ctx context.Context, client guarddutyAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeGDDetectorSets(ctx, st, acct, region, scanID, detectors, gdDetectorSet{
		listOp: "guardduty:ListIPSets", getOp: "guardduty:GetIPSet", what: "IP set(s)",
		typ: TypeGuardDutyIPSet, kind: "ipset", idKey: "IpSetId",
		isDenied: func(err error) bool { return isAccessDenied(err) || isGuardDutyMemberRestricted(err) },
		page: func(ctx context.Context, did string, token *string) ([]string, *string, error) {
			out, err := client.ListIPSets(ctx, &guardduty.ListIPSetsInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IpSetIds, out.NextToken, nil
		},
		get: func(ctx context.Context, did, id string, r *store.Resource) (any, error) {
			s, err := client.GetIPSet(ctx, &guardduty.GetIPSetInput{DetectorId: &did, IpSetId: &id})
			if err != nil {
				return nil, err
			}
			r.Name, r.TagsJSON = s.Name, mapTagsJSON(s.Tags)
			return s, nil
		},
	}, p)
}

// scanGDMembers stores each detector's member accounts under it.
// Only an administrator's detector lists members; other detectors get empty
// pages. A member is another account's detector, so no tags are read. The
// resolver links each member to its organization account.
func scanGDMembers(ctx context.Context, client guarddutyAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	const op = "guardduty:ListMembers"
	total, inserted, err := storeChildrenEachParent(ctx, st, acct, region, op, nil, detectors, nil, nil,
		func(ctx context.Context, d childParent, token *string) ([]gdtypes.Member, *string, error) {
			out, err := client.ListMembers(ctx, &guardduty.ListMembersInput{DetectorId: &d.id, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Members, out.NextToken, nil
		},
		func(d childParent, m gdtypes.Member) (*store.Resource, string, string) {
			if sv(m.AccountId) == "" {
				return nil, "member(s) with an empty AccountId", "one of detector " + d.id
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyMember, NativeID: gdDetectorChildARN(acct.partition(), region, acct.ID, d.id, "member", *m.AccountId),
				Name: m.AccountId, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}, "", ""
		})
	return p.addPhase(op, total, inserted, err)
}
