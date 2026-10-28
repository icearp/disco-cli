package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListDetectors serves d-t on its first page and d-b on its
// second. Each detector holds one item of every per-detector kind, named
// "<kind>-<suffix>" after the detector's suffix (f-t, ip-t, pd-t, tes-t, ...),
// and one member. The malware protection plans (mpp-t, mpp-b) and the custom
// detection rule associations (a-t, a-b) come one per page over two pages;
// one rule organization configuration (r-1, LIVE) is listed. Every resource
// whose id ends in "-t" has tags {"type": <its type>}: in its get's output for
// the detectors, filters and IP sets, from its tag read for the others.

var (
	gdBadRequest  = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
	gdNotFound    = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	gdMemberError = &smithy.GenericAPIError{Code: "BadRequestException", Message: "The request is rejected because member accounts cannot manage specified resources or properties."}
)

// gdDetARN is detector did's ARN, spelled out so the test pins the format.
func gdDetARN(did string) string {
	return "arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":detector/" + did
}

// gdSuffix is the suffix of a fixture id: "t" for d-t.
func gdSuffix(id string) string { return id[strings.LastIndex(id, "-")+1:] }

// gdTagged is the tags of the fixture resource id of type typ: {"type": typ}
// when id ends in "-t", else none.
func gdTagged(typ, id string) map[string]string {
	if strings.HasSuffix(id, "-t") {
		return map[string]string{"type": typ}
	}
	return nil
}

func gdMemberAccount(did string) string {
	return map[string]string{"t": "210000000001", "b": "210000000002", "g": "210000000003"}[gdSuffix(did)]
}

func gdDetectorOut(did string) *guardduty.GetDetectorOutput {
	return &guardduty.GetDetectorOutput{
		Status: gdtypes.DetectorStatusEnabled, FindingPublishingFrequency: gdtypes.FindingPublishingFrequencySixHours,
		ServiceRole: sdkaws.String("role-" + did), Tags: gdTagged(TypeGuardDutyDetector, did),
	}
}

func gdFilterOut(did string) *guardduty.GetFilterOutput {
	name := "f-" + gdSuffix(did)
	return &guardduty.GetFilterOutput{
		Name: &name, Action: gdtypes.FilterActionArchive, Rank: sdkaws.Int32(1),
		FindingCriteria: &gdtypes.FindingCriteria{}, Tags: gdTagged(TypeGuardDutyFilter, name),
	}
}

// gdIPSetOut is the get body of detector did's IP set, listed as id
// "ip-<suffix>": its display name differs from that id.
func gdIPSetOut(did string) *guardduty.GetIPSetOutput {
	name := "ipname-" + gdSuffix(did)
	return &guardduty.GetIPSetOutput{
		Name: &name, Format: gdtypes.IpSetFormatTxt, Location: sdkaws.String("s3://bucket/" + name),
		Status: gdtypes.IpSetStatusActive, Tags: gdTagged(TypeGuardDutyIPSet, name),
	}
}

func gdMemberOf(did string) gdtypes.Member {
	return gdtypes.Member{
		AccountId: sdkaws.String(gdMemberAccount(did)), DetectorId: sdkaws.String("member-" + did),
		MasterId: sdkaws.String(testAccountID), RelationshipStatus: sdkaws.String("Enabled"),
	}
}

func gdDestOf(did string) gdtypes.Destination {
	return gdtypes.Destination{
		DestinationId: sdkaws.String("pd-" + gdSuffix(did)), DestinationType: gdtypes.DestinationTypeS3,
		Status: gdtypes.PublishingStatusPublishing,
	}
}

func gdPlanOf(n string) gdtypes.MalwareProtectionPlanSummary {
	return gdtypes.MalwareProtectionPlanSummary{MalwareProtectionPlanId: sdkaws.String("mpp-" + n)}
}

// gdCalls records the stubbed calls: what each listing was sent (bacCalls)
// and the key each get or tag read was sent; errs and tags, keyed
// "<op> <key>", make a get or tag read fail or answer with tags.
type gdCalls struct {
	*bacCalls
	keys map[string][]string
}

func newGDCalls() *gdCalls { return &gdCalls{bacCalls: newBACCalls(), keys: map[string][]string{}} }

// keyed answers op with out(key), key(in) being what the call was sent,
// unless errs holds an error for it.
func (c *gdCalls) keyed(op string, key func(in any) string, out func(key string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		k := key(in)
		c.mu.Lock()
		c.ops[op]++
		c.keys[op] = append(c.keys[op], k)
		err := c.errs[op+" "+k]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return out(k), nil
	}
}

// sent is a sorted copy of the keys op was sent.
func (c *gdCalls) sent(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.keys[op])
	slices.Sort(got)
	return got
}

func gdDetectorOf(in any) string { return sv(bacField[*string](in, "DetectorId")) }

// twoPages answers op with first on the first page and second on the next.
func (c *gdCalls) twoPages(op string, first, second func(next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return first(sdkaws.String("p2"))
		}
		return second(nil)
	})
}

// gdHandlers answers every op the scan calls with the fixture.
func gdHandlers(c *gdCalls) map[string]func(any) (any, error) {
	perDetector := func(op string, out func(did string) any) func(any) (any, error) {
		return c.answer(op, func(in any) any { return out(gdDetectorOf(in)) })
	}
	return map[string]func(any) (any, error){
		"ListDetectors": c.twoPages("ListDetectors",
			func(next *string) any {
				return &guardduty.ListDetectorsOutput{DetectorIds: []string{"d-t"}, NextToken: next}
			},
			func(next *string) any {
				return &guardduty.ListDetectorsOutput{DetectorIds: []string{"d-b"}, NextToken: next}
			}),
		"GetDetector": c.keyed("GetDetector", gdDetectorOf, func(did string) any { return gdDetectorOut(did) }),
		"ListFilters": perDetector("ListFilters", func(did string) any {
			return &guardduty.ListFiltersOutput{FilterNames: []string{"f-" + gdSuffix(did)}}
		}),
		"GetFilter": c.keyed("GetFilter", func(in any) string {
			return gdDetectorOf(in) + "/" + sv(in.(*guardduty.GetFilterInput).FilterName)
		}, func(k string) any {
			did, _, _ := strings.Cut(k, "/")
			return gdFilterOut(did)
		}),
		"ListIPSets": perDetector("ListIPSets", func(did string) any {
			return &guardduty.ListIPSetsOutput{IpSetIds: []string{"ip-" + gdSuffix(did)}}
		}),
		"GetIPSet": c.keyed("GetIPSet", func(in any) string {
			return gdDetectorOf(in) + "/" + sv(in.(*guardduty.GetIPSetInput).IpSetId)
		}, func(k string) any {
			did, _, _ := strings.Cut(k, "/")
			return gdIPSetOut(did)
		}),
		"ListMembers": perDetector("ListMembers", func(did string) any {
			return &guardduty.ListMembersOutput{Members: []gdtypes.Member{gdMemberOf(did)}}
		}),
		"ListMalwareProtectionPlans": c.twoPages("ListMalwareProtectionPlans",
			func(next *string) any {
				return &guardduty.ListMalwareProtectionPlansOutput{MalwareProtectionPlans: []gdtypes.MalwareProtectionPlanSummary{gdPlanOf("t")}, NextToken: next}
			},
			func(next *string) any {
				return &guardduty.ListMalwareProtectionPlansOutput{MalwareProtectionPlans: []gdtypes.MalwareProtectionPlanSummary{gdPlanOf("b")}, NextToken: next}
			}),
		"GetMalwareProtectionPlan": c.keyed("GetMalwareProtectionPlan", func(in any) string {
			return sv(in.(*guardduty.GetMalwareProtectionPlanInput).MalwareProtectionPlanId)
		}, func(id string) any {
			return &guardduty.GetMalwareProtectionPlanOutput{Tags: gdTagged(TypeGuardDutyMalwareProtectionPlan, id)}
		}),
		"ListPublishingDestinations": perDetector("ListPublishingDestinations", func(did string) any {
			return &guardduty.ListPublishingDestinationsOutput{Destinations: []gdtypes.Destination{gdDestOf(did)}}
		}),
		"ListThreatEntitySets": perDetector("ListThreatEntitySets", func(did string) any {
			return &guardduty.ListThreatEntitySetsOutput{ThreatEntitySetIds: []string{"tes-" + gdSuffix(did)}}
		}),
		"ListThreatIntelSets": perDetector("ListThreatIntelSets", func(did string) any {
			return &guardduty.ListThreatIntelSetsOutput{ThreatIntelSetIds: []string{"tis-" + gdSuffix(did)}}
		}),
		"ListTrustedEntitySets": perDetector("ListTrustedEntitySets", func(did string) any {
			return &guardduty.ListTrustedEntitySetsOutput{TrustedEntitySetIds: []string{"trs-" + gdSuffix(did)}}
		}),
		"GetThreatEntitySet": c.keyed("GetThreatEntitySet", func(in any) string {
			return gdDetectorOf(in) + "/" + sv(in.(*guardduty.GetThreatEntitySetInput).ThreatEntitySetId)
		}, func(k string) any {
			_, id, _ := strings.Cut(k, "/")
			return &guardduty.GetThreatEntitySetOutput{Tags: gdTagged(TypeGuardDutyThreatEntitySet, id)}
		}),
		"GetThreatIntelSet": c.keyed("GetThreatIntelSet", func(in any) string {
			return gdDetectorOf(in) + "/" + sv(in.(*guardduty.GetThreatIntelSetInput).ThreatIntelSetId)
		}, func(k string) any {
			_, id, _ := strings.Cut(k, "/")
			return &guardduty.GetThreatIntelSetOutput{Tags: gdTagged(TypeGuardDutyThreatIntelSet, id)}
		}),
		"GetTrustedEntitySet": c.keyed("GetTrustedEntitySet", func(in any) string {
			return gdDetectorOf(in) + "/" + sv(in.(*guardduty.GetTrustedEntitySetInput).TrustedEntitySetId)
		}, func(k string) any {
			_, id, _ := strings.Cut(k, "/")
			return &guardduty.GetTrustedEntitySetOutput{Tags: gdTagged(TypeGuardDutyTrustedEntitySet, id)}
		}),
		"ListTagsForResource": c.keyed("ListTagsForResource", func(in any) string {
			return sv(in.(*guardduty.ListTagsForResourceInput).ResourceArn)
		}, func(arn string) any {
			return &guardduty.ListTagsForResourceOutput{Tags: c.tags["ListTagsForResource "+arn]}
		}),
		"DescribeOrganizationConfiguration": c.answer("DescribeOrganizationConfiguration", func(any) any {
			return &guardduty.DescribeOrganizationConfigurationOutput{AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll}
		}),
		"ListCustomDetectionRuleAssociations": c.twoPages("ListCustomDetectionRuleAssociations",
			func(next *string) any {
				return &guardduty.ListCustomDetectionRuleAssociationsOutput{RuleAssociations: []gdtypes.AssociationSummary{gdTestAssoc("r-1", "a-t")}, NextToken: next}
			},
			func(next *string) any {
				return &guardduty.ListCustomDetectionRuleAssociationsOutput{RuleAssociations: []gdtypes.AssociationSummary{gdTestAssoc("r-1", "a-b")}, NextToken: next}
			}),
		"GetCustomDetectionRuleAssociation": c.keyed("GetCustomDetectionRuleAssociation", func(in any) string {
			g := in.(*guardduty.GetCustomDetectionRuleAssociationInput)
			return sv(g.RuleId) + "/" + sv(g.AssociationId)
		}, func(k string) any {
			return &guardduty.GetCustomDetectionRuleAssociationOutput{Tags: gdTagged(TypeGuardDutyCustomDetectionRuleAssociation, k)}
		}),
		"ListCustomDetectionRuleOrgConfigurations": c.answer("ListCustomDetectionRuleOrgConfigurations", func(any) any {
			return &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{Configurations: []gdtypes.DetectionRuleOrgConfigurationSummary{gdTestOrgConfig("r-1", gdtypes.AssociationModeLive)}}
		}),
	}
}

// gdWant is a row the fixture stores.
type gdWant struct {
	typ, arn, name string
	status         *string
	created        bool
	element        any
	tags           map[string]string
}

// gdRows is every row the fixture stores. A tag-read row's tags are those
// newGDCalls sets up.
func gdRows() []gdWant {
	enabled, active := sdkaws.String("ENABLED"), sdkaws.String("ACTIVE")
	var rows []gdWant
	for _, s := range []string{"t", "b"} {
		did := "d-" + s
		d := gdDetARN(did)
		member := gdMemberOf(did)
		plan := gdPlanOf(s)
		assoc := gdTestAssoc("r-1", "a-"+s)
		idSet := func(typ, kind, idKey, prefix string) gdWant {
			id := prefix + s
			return gdWant{typ, d + "/" + kind + "/" + id, id, nil, false, map[string]string{"DetectorId": did, idKey: id}, gdTagged(typ, id)}
		}
		rows = append(rows,
			gdWant{TypeGuardDutyDetector, d, "", enabled, false, gdDetectorOut(did), gdTagged(TypeGuardDutyDetector, did)},
			gdWant{TypeGuardDutyFilter, d + "/filter/f-" + s, "f-" + s, nil, false, gdFilterOut(did), gdTagged(TypeGuardDutyFilter, "f-"+s)},
			gdWant{TypeGuardDutyIPSet, d + "/ipset/ip-" + s, "ipname-" + s, nil, false, gdIPSetOut(did), gdTagged(TypeGuardDutyIPSet, "ipname-"+s)},
			gdWant{TypeGuardDutyMember, d + "/member/" + sv(member.AccountId), sv(member.AccountId), nil, false, member, nil},
			gdWant{TypeGuardDutyPublishingDestination, d + "/publishingdestination/pd-" + s, "pd-" + s, nil, false, gdDestOf(did), gdTagged(TypeGuardDutyPublishingDestination, "pd-"+s)},
			idSet(TypeGuardDutyThreatEntitySet, "threatentityset", "ThreatEntitySetId", "tes-"),
			idSet(TypeGuardDutyThreatIntelSet, "threatintelset", "ThreatIntelSetId", "tis-"),
			idSet(TypeGuardDutyTrustedEntitySet, "trustedentityset", "TrustedEntitySetId", "trs-"),
			gdWant{
				TypeGuardDutyOrganizationConfiguration, d + "/organization-configuration", "organization-configuration", nil, false,
				&guardduty.DescribeOrganizationConfigurationOutput{AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll}, nil,
			},
			gdWant{
				TypeGuardDutyMalwareProtectionPlan, "arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":malware-protection-plan/mpp-" + s, "mpp-" + s, nil, false,
				plan, gdTagged(TypeGuardDutyMalwareProtectionPlan, "mpp-"+s),
			},
			gdWant{TypeGuardDutyCustomDetectionRuleAssociation, gdTestAssocARN("r-1", "a-"+s), "a-" + s, nil, true, assoc, gdTagged(TypeGuardDutyCustomDetectionRuleAssociation, "a-"+s)},
		)
	}
	return append(rows, gdWant{
		TypeGuardDutyCustomDetectionRuleOrgConfiguration,
		"arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":detection-rule/custom/r-1/org-configuration/LIVE", "r-1", active, true,
		gdTestOrgConfig("r-1", gdtypes.AssociationModeLive), nil,
	})
}

// newGDFixtureCalls is gdCalls with ListTagsForResource answering the
// fixture's publishing destination tags, the only type whose tags it reads.
func newGDFixtureCalls() *gdCalls {
	c := newGDCalls()
	for _, r := range gdRows() {
		if r.typ == TypeGuardDutyPublishingDestination && r.tags != nil {
			c.tags["ListTagsForResource "+r.arn] = r.tags
		}
	}
	return c
}

// gdScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func gdScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "unstubbed op"))
	return scanGuardDuty(ctx, acct, testRegion, st, testScanID)
}

func gdRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type is stored under its ARN (the Service Reference's format for the
// built ones) with the tags its get or tag read returns, nil for a resource
// without tags. Exactly the ARNs, plan ids and association keys of the
// tag-read rows are sent to their tag reads; members have no tags read.
// Every page of every listing is read with the model's page cap. Each row
// keeps the base scan's Name, Status, CreatedAt and verbatim element, and the
// detector's filters, IP sets, members and organization configuration are
// recorded under it.
func TestScanGuardDuty_StoresEveryRowWithItsTags(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGDFixtureCalls()

	total, inserted, err := gdScan(context.Background(), st, gdHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := gdRows()
	if total != 23 || inserted != 23 || len(rows) != 23 {
		t.Errorf("total, inserted = %d, %d; want 23 (fixture rows %d)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, want := range rows {
		r, ok := gdRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if want.typ == TypeGuardDutyPublishingDestination {
			wantARNs = append(wantARNs, want.arn)
		}
		if want.tags != nil {
			assertTagsMap(t, r.TagsJSON, want.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || (r.Name == nil) != (want.name == "") {
			t.Errorf("%s %s Name = %q (nil %t); want %q", want.typ, want.arn, sv(r.Name), r.Name == nil, want.name)
		}
		if (r.Status == nil) != (want.status == nil) || sv(r.Status) != sv(want.status) {
			t.Errorf("%s %s Status = %v; want %v", want.typ, want.arn, r.Status, want.status)
		}
		if wantCreated := map[bool]string{true: "2026-09-02T03:04:05Z"}[want.created]; sv(r.CreatedAt) != wantCreated {
			t.Errorf("%s %s CreatedAt = %v; want %q", want.typ, want.arn, r.CreatedAt, wantCreated)
		}
		if sv(r.Region) != testRegion || r.ManagedByProvider || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, managed, attributes = %v, %v, %s; want %s, false, %s", want.typ, want.arn, r.Region, r.ManagedByProvider, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	slices.Sort(wantARNs)
	if got := calls.sent("ListTagsForResource"); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}
	for op, want := range map[string][]string{
		"GetMalwareProtectionPlan": {"mpp-b", "mpp-t"},
		"GetThreatEntitySet":       {"d-b/tes-b", "d-t/tes-t"},
		"GetThreatIntelSet":        {"d-b/tis-b", "d-t/tis-t"},
		"GetTrustedEntitySet":      {"d-b/trs-b", "d-t/trs-t"},
	} {
		if got := calls.sent(op); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v; want %v", op, got, want)
		}
	}
	if got, want := calls.sent("GetCustomDetectionRuleAssociation"), []string{"r-1/a-b", "r-1/a-t"}; !slices.Equal(got, want) {
		t.Errorf("GetCustomDetectionRuleAssociation keys = %v; want %v", got, want)
	}
	for op, want := range map[string][]int32{
		"ListDetectors": {50, 50}, "ListFilters": {50, 50}, "ListIPSets": {50, 50}, "ListMembers": {50, 50},
		"ListPublishingDestinations": {50, 50}, "ListThreatEntitySets": {50, 50}, "ListThreatIntelSets": {50, 50},
		"ListTrustedEntitySets": {50, 50}, "ListCustomDetectionRuleAssociations": {100, 100},
		"ListCustomDetectionRuleOrgConfigurations": {100},
	} {
		if got := calls.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	if n := calls.count("ListMalwareProtectionPlans"); n != 2 {
		t.Errorf("ListMalwareProtectionPlans calls = %d; want 2", n)
	}
	for _, s := range []string{"t", "b"} {
		d := gdDetARN("d-" + s)
		assertQSContains(t, st, d, d+"/filter/f-"+s, d+"/ipset/ip-"+s, d+"/member/"+gdMemberAccount("d-"+s), d+"/organization-configuration")
	}
}

// An aws-us-gov account scanned in us-gov-west-1 builds its ARNs in that
// partition and sends them to the tag op unchanged.
func TestScanGuardDuty_GovCloudARNsReachTagOp(t *testing.T) {
	const govRegion = "us-gov-west-1"
	d := "arn:aws-us-gov:guardduty:" + govRegion + ":" + testAccountID + ":detector/d-g"
	st := newTestStore(t)
	calls := newGDCalls()
	calls.tags["ListTagsForResource "+d+"/publishingdestination/pd-g"] = map[string]string{"env": "gov"}
	h := gdHandlers(calls)
	h["ListDetectors"] = calls.answer("ListDetectors", func(any) any { return &guardduty.ListDetectorsOutput{DetectorIds: []string{"d-g"}} })
	h["ListMalwareProtectionPlans"] = calls.answer("ListMalwareProtectionPlans", func(any) any { return &guardduty.ListMalwareProtectionPlansOutput{} })
	h["ListCustomDetectionRuleAssociations"] = calls.answer("ListCustomDetectionRuleAssociations", func(any) any {
		return &guardduty.ListCustomDetectionRuleAssociationsOutput{}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))

	if _, _, err := scanGuardDuty(context.Background(), acct, govRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{d + "/publishingdestination/pd-g"}
	if got := calls.sent("ListTagsForResource"); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	r, ok := gdRowOf(t, st, TypeGuardDutyPublishingDestination, d+"/publishingdestination/pd-g")
	if !ok {
		t.Fatalf("gov publishing destination not stored")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"env": "gov"})
	if sv(r.Region) != govRegion {
		t.Errorf("Region = %v; want %s", r.Region, govRegion)
	}
	if _, ok := gdRowOf(t, st, TypeGuardDutyDetector, d); !ok {
		t.Errorf("gov detector %s not stored", d)
	}
	assertQSContains(t, st, d, d+"/filter/f-g", d+"/ipset/ip-g", d+"/member/"+gdMemberAccount("d-g"), d+"/organization-configuration")
}

// A region without detectors or plans stores nothing, reads no tags, makes
// no custom detection rule call and reports nothing.
func TestScanGuardDuty_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGDCalls()
	h := gdHandlers(calls)
	h["ListDetectors"] = calls.answer("ListDetectors", func(any) any { return &guardduty.ListDetectorsOutput{} })
	h["ListMalwareProtectionPlans"] = calls.answer("ListMalwareProtectionPlans", func(any) any { return &guardduty.ListMalwareProtectionPlansOutput{} })

	total, inserted, err := gdScan(context.Background(), st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.keys) != 0 || calls.count("ListCustomDetectionRuleAssociations") != 0 {
		t.Errorf("warnings, keyed calls, association listings = %d, %v, %d; want 0, none, 0", *warns, calls.keys, calls.count("ListCustomDetectionRuleAssociations"))
	}
}

// A detector whose per-detector listings return empty pages stores only
// itself and reads no tags.
func TestScanGuardDuty_EmptyDetectorListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGDCalls()
	h := gdHandlers(calls)
	h["ListDetectors"] = calls.answer("ListDetectors", func(any) any { return &guardduty.ListDetectorsOutput{DetectorIds: []string{"d-b"}} })
	for op, out := range map[string]any{
		"ListFilters": &guardduty.ListFiltersOutput{}, "ListIPSets": &guardduty.ListIPSetsOutput{}, "ListMembers": &guardduty.ListMembersOutput{},
		"ListMalwareProtectionPlans": &guardduty.ListMalwareProtectionPlansOutput{}, "ListPublishingDestinations": &guardduty.ListPublishingDestinationsOutput{},
		"ListThreatEntitySets": &guardduty.ListThreatEntitySetsOutput{}, "ListThreatIntelSets": &guardduty.ListThreatIntelSetsOutput{},
		"ListTrustedEntitySets": &guardduty.ListTrustedEntitySetsOutput{}, "ListCustomDetectionRuleAssociations": &guardduty.ListCustomDetectionRuleAssociationsOutput{},
		"ListCustomDetectionRuleOrgConfigurations": &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{},
	} {
		h[op] = calls.answer(op, func(any) any { return out })
	}

	total, _, err := gdScan(context.Background(), st, h)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2 (detector, organization configuration), nil", total, err)
	}
	if *warns != 0 || len(calls.sent("ListTagsForResource")) != 0 || len(calls.sent("GetFilter")) != 0 {
		t.Errorf("warnings, tag reads, filter gets = %d, %v, %v; want 0, none, none", *warns, calls.sent("ListTagsForResource"), calls.sent("GetFilter"))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGuardDuty_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newGDFixtureCalls()
	if _, _, err := gdScan(context.Background(), st, gdHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := gdScan(context.Background(), st, gdHandlers(calls))
	if err != nil || total != 23 || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want 23, 0, nil", total, inserted, err)
	}
}

// A failed tag read keeps the row without tags unless its op models the
// resource as gone: a denial warns once under the tag op, scoped to the
// account and region; a throttle after retries is a transient error; any
// other code is a hard one. The plan and association gets'
// ResourceNotFoundException drops the row; ListTagsForResource and the set
// gets model no not-found, so that code is a hard error and the row is kept.
func TestScanGuardDuty_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		op, key, typ, arn string
		notFoundDrops     bool
	}{
		{"ListTagsForResource", gdDetARN("d-t") + "/publishingdestination/pd-t", TypeGuardDutyPublishingDestination, gdDetARN("d-t") + "/publishingdestination/pd-t", false},
		{"GetThreatEntitySet", "d-t/tes-t", TypeGuardDutyThreatEntitySet, gdDetARN("d-t") + "/threatentityset/tes-t", false},
		{"GetThreatIntelSet", "d-b/tis-b", TypeGuardDutyThreatIntelSet, gdDetARN("d-b") + "/threatintelset/tis-b", false},
		{"GetTrustedEntitySet", "d-t/trs-t", TypeGuardDutyTrustedEntitySet, gdDetARN("d-t") + "/trustedentityset/trs-t", false},
		{"GetMalwareProtectionPlan", "mpp-t", TypeGuardDutyMalwareProtectionPlan, "arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":malware-protection-plan/mpp-t", true},
		{"GetCustomDetectionRuleAssociation", "r-1/a-t", TypeGuardDutyCustomDetectionRuleAssociation, gdTestAssocARN("r-1", "a-t"), true},
	} {
		t.Run(target.op, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantWarns int
			}{
				{"denied", apiErr("AccessDeniedException", "denied"), 0, "", 1},
				{"not found", gdNotFound, outcomeError, "ResourceNotFoundException", 0},
				{"other error", gdBadRequest, outcomeError, "BadRequestException", 0},
				{"transient", crThrottled, outcomeTransient, "", 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					dropped := c.err == gdNotFound && target.notFoundDrops
					st := newTestStore(t)
					warnings := bacWarnings(st, "guardduty:"+target.op)
					calls := newGDFixtureCalls()
					calls.errs[target.op+" "+target.key] = c.err

					total, _, err := gdScan(context.Background(), st, gdHandlers(calls))
					switch {
					case dropped || c.outcome == 0:
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					default:
						assertGlueOutcome(t, err, c.outcome)
						if c.code != "" {
							assertAPICode(t, err, c.code)
						}
					}
					r, ok := gdRowOf(t, st, target.typ, target.arn)
					if ok == dropped {
						t.Fatalf("row stored = %v; want %v", ok, !dropped)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 23
					if dropped {
						want = 22
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("warnings = %v; want %d", *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads of several detectors' rows are one warning for the phase.
func TestScanGuardDuty_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, gdTagsOp)
	calls := newGDFixtureCalls()
	denied := []string{gdDetARN("d-t") + "/publishingdestination/pd-t", gdDetARN("d-b") + "/publishingdestination/pd-b"}
	for _, arn := range denied {
		calls.errs["ListTagsForResource "+arn] = apiErr("AccessDeniedException", "denied")
	}

	if _, _, err := gdScan(context.Background(), st, gdHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", gdTagsOp, *warns)
	}
	for _, arn := range denied {
		if r, ok := gdRowOf(t, st, TypeGuardDutyPublishingDestination, arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of another tag read is demoted
// to one warning, so the hard error decides the outcome.
func TestScanGuardDuty_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, gdTagsOp)
	calls := newGDFixtureCalls()
	calls.errs["ListTagsForResource "+gdDetARN("d-t")+"/publishingdestination/pd-t"] = crThrottled
	calls.errs["GetMalwareProtectionPlan mpp-b"] = gdBadRequest

	_, _, err := gdScan(context.Background(), st, gdHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", gdTagsOp, *warns)
	}
}

// A throttled get beside a hard failure of another get of the same op is
// demoted to one warning under the op, so the hard error decides the
// outcome.
func TestScanGuardDuty_GetThrottleDemotedBesideHardError(t *testing.T) {
	for _, c := range []struct{ op, throttled, failed string }{
		{"GetDetector", "d-t", "d-b"},
		{"GetFilter", "d-t/f-t", "d-b/f-b"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "guardduty:"+c.op)
			calls := newGDFixtureCalls()
			calls.errs[c.op+" "+c.throttled] = crThrottled
			calls.errs[c.op+" "+c.failed] = gdBadRequest

			_, _, err := gdScan(context.Background(), st, gdHandlers(calls))
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
			if *warns != 1 {
				t.Errorf("guardduty:%s warnings = %d; want 1", c.op, *warns)
			}
		})
	}
}

// A ctx that ended before any detector's listing started is the phase's
// error, although no id was listed and so no get ran.
func TestStoreGDDetectorSets_EndedCtxIsThePhaseError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var p phaseTotals
	failed := storeGDDetectorSets(ctx, st, acct, testRegion, testScanID, []childParent{{id: "d-t", arn: gdDetARN("d-t")}}, gdDetectorSet{
		listOp: "guardduty:ListFilters", getOp: "guardduty:GetFilter", what: "filter(s)",
		typ: TypeGuardDutyFilter, kind: "filter", idKey: "FilterName",
		page: func(context.Context, string, *string) ([]string, *string, error) {
			t.Error("listing called after ctx ended")
			return nil, nil, nil
		},
		get: func(context.Context, string, string, *store.Resource) (any, error) {
			t.Error("get called after ctx ended")
			return nil, nil
		},
	}, &p)
	if failed {
		t.Fatal("storeWriteFailed = true; want false")
	}
	if _, _, err := p.result(st, acct, testRegion); !errors.Is(err, context.Canceled) {
		t.Errorf("phase err = %v; want context.Canceled", err)
	}
}

// A failed get of a detector, filter or IP set keeps its row as the listed
// element: the NativeID the get's row has, the listed id as the Name (none
// for a detector, whose get row has none either), the ids as the attributes,
// no status and no tags. The gets model no not-found code, so no failure
// drops a row. A denial warns once under the get's op; a throttle after
// retries is a transient error and any other code a hard one. The sibling
// whose get answered keeps its body. A detector kept so still exists, so its
// filters, IP sets, members and organization configuration are listed and
// recorded under it. A kept filter or IP set is recorded under its detector
// beside the detector's other children.
func TestScanGuardDuty_GetFailuresKeepTheListedRow(t *testing.T) {
	for _, target := range []struct {
		op, key, typ, arn, name string
		element                 map[string]string
		sibling                 string
		siblingBody             any
	}{
		{
			"GetDetector", "d-b", TypeGuardDutyDetector, gdDetARN("d-b"), "",
			map[string]string{"DetectorId": "d-b"},
			gdDetARN("d-t"), gdDetectorOut("d-t"),
		},
		{
			"GetFilter", "d-t/f-t", TypeGuardDutyFilter, gdDetARN("d-t") + "/filter/f-t", "f-t",
			map[string]string{"DetectorId": "d-t", "FilterName": "f-t"},
			gdDetARN("d-b") + "/filter/f-b", gdFilterOut("d-b"),
		},
		{
			"GetIPSet", "d-b/ip-b", TypeGuardDutyIPSet, gdDetARN("d-b") + "/ipset/ip-b", "ip-b",
			map[string]string{"DetectorId": "d-b", "IpSetId": "ip-b"},
			gdDetARN("d-t") + "/ipset/ip-t", gdIPSetOut("d-t"),
		},
	} {
		t.Run(target.op, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantWarns int
			}{
				{"denied", apiErr("AccessDeniedException", "denied"), 0, "", 1},
				{"transient", crThrottled, outcomeTransient, "ThrottlingException", 0},
				{"other error", gdBadRequest, outcomeError, "BadRequestException", 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, "guardduty:"+target.op)
					calls := newGDFixtureCalls()
					calls.errs[target.op+" "+target.key] = c.err

					total, _, err := gdScan(context.Background(), st, gdHandlers(calls))
					if c.outcome == 0 {
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					} else {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.code)
					}
					if total != 23 || countStoredRows(t, st) != 23 {
						t.Errorf("total = %d, stored = %d; want 23", total, countStoredRows(t, st))
					}
					r, ok := gdRowOf(t, st, target.typ, target.arn)
					if !ok {
						t.Fatalf("%s %s not stored; want it kept as its listed element", target.typ, target.arn)
					}
					if sv(r.Name) != target.name || (r.Name == nil) != (target.name == "") {
						t.Errorf("Name = %q (nil %t); want %q", sv(r.Name), r.Name == nil, target.name)
					}
					if r.Status != nil || r.TagsJSON != nil {
						t.Errorf("Status, TagsJSON = %v, %v; want nil, nil", r.Status, r.TagsJSON)
					}
					if want := mustJSON(target.element); r.AttributesJSON != want {
						t.Errorf("AttributesJSON = %s; want %s", r.AttributesJSON, want)
					}
					if s, ok := gdRowOf(t, st, target.typ, target.sibling); !ok || s.AttributesJSON != mustJSON(target.siblingBody) {
						t.Errorf("sibling %s stored %v, attributes %s; want its get's body", target.sibling, ok, s.AttributesJSON)
					}
					did := target.element["DetectorId"]
					d := gdDetARN(did)
					assertQSContains(t, st, d, d+"/filter/f-"+gdSuffix(did), d+"/ipset/ip-"+gdSuffix(did), d+"/member/"+gdMemberAccount(did), d+"/organization-configuration")
					if len(*warnings) != c.wantWarns {
						t.Errorf("%s warnings = %v; want %d", target.op, *warnings, c.wantWarns)
					}
				})
			}
		})
	}
}

// gdListOps is every listing the scan makes, but the organization
// configuration's, whose own tests pin it.
var gdListOps = []string{
	"ListDetectors", "ListFilters", "ListMembers", "ListIPSets", "ListMalwareProtectionPlans", "ListPublishingDestinations",
	"ListThreatEntitySets", "ListThreatIntelSets", "ListTrustedEntitySets", "ListCustomDetectionRuleAssociations", "ListCustomDetectionRuleOrgConfigurations",
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied ListDetectors leaves the per-detector and custom detection
// rule listings nothing to read.
func TestScanGuardDuty_DeniedListingWarnsUnderItsOp(t *testing.T) {
	// 23 rows, less the denied listing's: 2 for most, 1 for the rule
	// organization configurations, all but the 2 plans for the detectors.
	lost := map[string]int{"ListDetectors": 21, "ListCustomDetectionRuleOrgConfigurations": 1}
	for _, op := range gdListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "guardduty:"+op)
			h := gdHandlers(newGDFixtureCalls())
			h[op] = func(any) (any, error) { return nil, apiErr("AccessDeniedException", "denied") }

			total, _, err := gdScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (under the op: %d); want 1 under the op", *warns, *opWarns)
			}
			want := 23 - 2
			if n, ok := lost[op]; ok {
				want = 23 - n
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, one detector's
// failed listing keeps the other detector's rows, and no failure stops the
// phases after it.
func TestScanGuardDuty_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Run("ListDetectors page 2", func(t *testing.T) {
		st := newTestStore(t)
		h := gdHandlers(newGDFixtureCalls())
		h["ListDetectors"] = failOnCall(2, gdBadRequest, h["ListDetectors"])
		total, _, err := gdScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		// d-t's 9 rows, the plans, the associations and the rule
		// organization configuration.
		if total != 14 {
			t.Errorf("total = %d; want 14", total)
		}
		if _, ok := gdRowOf(t, st, TypeGuardDutyTrustedEntitySet, gdDetARN("d-t")+"/trustedentityset/trs-t"); !ok {
			t.Error("trusted entity set of d-t, from page 1, missing")
		}
	})
	t.Run("ListMalwareProtectionPlans page 2", func(t *testing.T) {
		st := newTestStore(t)
		h := gdHandlers(newGDFixtureCalls())
		h["ListMalwareProtectionPlans"] = failOnCall(2, gdBadRequest, h["ListMalwareProtectionPlans"])
		total, _, err := gdScan(context.Background(), st, h)
		assertAPICode(t, err, "BadRequestException")
		if total != 22 {
			t.Errorf("total = %d; want 22", total)
		}
		if _, ok := gdRowOf(t, st, TypeGuardDutyMalwareProtectionPlan, "arn:aws:guardduty:"+testRegion+":"+testAccountID+":malware-protection-plan/mpp-t"); !ok {
			t.Error("plan of page 1 missing")
		}
	})
	for _, op := range []string{"ListFilters", "ListIPSets", "ListMembers", "ListPublishingDestinations", "ListThreatEntitySets", "ListThreatIntelSets", "ListTrustedEntitySets"} {
		t.Run(op+" of one detector", func(t *testing.T) {
			st := newTestStore(t)
			calls := newGDFixtureCalls()
			h := gdHandlers(calls)
			next := h[op]
			h[op] = func(in any) (any, error) {
				if gdDetectorOf(in) == "d-t" {
					return nil, gdBadRequest
				}
				return next(in)
			}
			total, _, err := gdScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
			if total != 22 {
				t.Errorf("total = %d; want 22", total)
			}
			if calls.count("ListCustomDetectionRuleOrgConfigurations") != 1 {
				t.Error("the last phase did not run")
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanGuardDuty_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct{ op, typ, arn string }{
		{"ListDetectors", TypeGuardDutyDetector, gdDetARN("d-b")},
		{"ListMalwareProtectionPlans", TypeGuardDutyMalwareProtectionPlan, "arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":malware-protection-plan/mpp-b"},
		{"ListPublishingDestinations", TypeGuardDutyPublishingDestination, gdDetARN("d-b") + "/publishingdestination/pd-b"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := gdHandlers(newGDFixtureCalls())
			next := h[c.op]
			h[c.op] = func(in any) (any, error) {
				out, err := next(in)
				switch o := out.(type) {
				case *guardduty.ListDetectorsOutput:
					o.NextToken = sdkaws.String("same")
				case *guardduty.ListMalwareProtectionPlansOutput:
					o.NextToken = sdkaws.String("same")
				case *guardduty.ListPublishingDestinationsOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			_, _, err := gdScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := gdRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost, a denied tag read or get among them.
func TestScanGuardDuty_FailedWriteStopsWithoutWarning(t *testing.T) {
	for _, c := range []struct{ typ, deny, next string }{
		{TypeGuardDutyDetector, "GetDetector d-b", "ListFilters"},
		{TypeGuardDutyFilter, "GetFilter d-b/f-b", "ListMembers"},
		{TypeGuardDutyPublishingDestination, "ListTagsForResource " + gdDetARN("d-t") + "/publishingdestination/pd-t", "ListThreatEntitySets"},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newGDFixtureCalls()
			calls.errs[c.deny] = apiErr("AccessDeniedException", "denied")

			_, _, err := gdScan(context.Background(), st, gdHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if n := calls.count(c.next); n != 0 {
				t.Errorf("%s called %d times after the failed write", c.next, n)
			}
		})
	}
}

// Items listed without the id their row needs are not stored or read and are
// warned about once per listing, counted, under a key that is never empty.
// Each listing answers two such items; a per-detector listing answers them
// for each of the two detectors.
func TestScanGuardDuty_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct {
		op, key string
		count   int
		out     any
	}{
		{"ListDetectors", "(a detector with no id)", 2, &guardduty.ListDetectorsOutput{DetectorIds: []string{"", ""}}},
		{"ListFilters", "one of detector d-", 4, &guardduty.ListFiltersOutput{FilterNames: []string{"", ""}}},
		{"ListMembers", "one of detector d-", 4, &guardduty.ListMembersOutput{Members: []gdtypes.Member{{}, {}}}},
		{"ListMalwareProtectionPlans", "(a malware protection plan with no id)", 2, &guardduty.ListMalwareProtectionPlansOutput{MalwareProtectionPlans: []gdtypes.MalwareProtectionPlanSummary{{}, {}}}},
		{"ListPublishingDestinations", "one of detector d-", 4, &guardduty.ListPublishingDestinationsOutput{Destinations: []gdtypes.Destination{{}, {}}}},
		{"ListThreatIntelSets", "one of detector d-", 4, &guardduty.ListThreatIntelSetsOutput{ThreatIntelSetIds: []string{"", ""}}},
		{"ListCustomDetectionRuleAssociations", "a-x", 2, &guardduty.ListCustomDetectionRuleAssociationsOutput{RuleAssociations: []gdtypes.AssociationSummary{{AssociationId: sdkaws.String("a-x")}, {AssociationId: sdkaws.String("a-x")}}}},
		{"ListCustomDetectionRuleOrgConfigurations", "(a configuration with no rule id)", 2, &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{Configurations: []gdtypes.DetectionRuleOrgConfigurationSummary{{}, {}}}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "guardduty:"+c.op)
			calls := newGDFixtureCalls()
			h := gdHandlers(calls)
			h[c.op] = func(any) (any, error) { return c.out, nil }

			if _, _, err := gdScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one naming %q", *warnings, c.key)
			}
			var n int
			if _, err := fmt.Sscan((*warnings)[0].Message, &n); err != nil || n != c.count {
				t.Errorf("warning %q counts %d (%v); want %d", (*warnings)[0].Message, n, err, c.count)
			}
		})
	}
}

// A member account is refused the entity-set listings, its expected state:
// those detectors are skipped without a warning or an error. The threat
// intel set and publishing destination listings have no such gate, so the
// same answer there is an error.
func TestScanGuardDuty_MemberRestrictedEntitySets(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	h := gdHandlers(newGDFixtureCalls())
	for _, op := range []string{"ListThreatEntitySets", "ListTrustedEntitySets"} {
		h[op] = func(any) (any, error) { return nil, gdMemberError }
	}
	total, _, err := gdScan(context.Background(), st, h)
	if err != nil || total != 19 || *warns != 0 {
		t.Errorf("scan = %d, %v, warnings %d; want 19, nil, 0", total, err, *warns)
	}

	for _, op := range []string{"ListThreatIntelSets", "ListPublishingDestinations"} {
		t.Run(op, func(t *testing.T) {
			h := gdHandlers(newGDFixtureCalls())
			h[op] = func(any) (any, error) { return nil, gdMemberError }
			_, _, err := gdScan(context.Background(), newTestStore(t), h)
			assertAPICode(t, err, "BadRequestException")
		})
	}
}

// In a member account the sets listed are the administrator's: a set get
// refused as member restricted keeps the row, untagged, without a warning or
// an error.
func TestScanGuardDuty_MemberRestrictedSetGetsKeepTheRow(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGDFixtureCalls()
	sets := []struct{ key, typ, arn string }{
		{"GetThreatEntitySet d-t/tes-t", TypeGuardDutyThreatEntitySet, gdDetARN("d-t") + "/threatentityset/tes-t"},
		{"GetThreatIntelSet d-t/tis-t", TypeGuardDutyThreatIntelSet, gdDetARN("d-t") + "/threatintelset/tis-t"},
		{"GetTrustedEntitySet d-t/trs-t", TypeGuardDutyTrustedEntitySet, gdDetARN("d-t") + "/trustedentityset/trs-t"},
	}
	for _, set := range sets {
		calls.errs[set.key] = gdMemberError
	}

	total, _, err := gdScan(context.Background(), st, gdHandlers(calls))
	if err != nil || total != 23 || *warns != 0 {
		t.Fatalf("scan = %d, %v, warnings %d; want 23, nil, 0", total, err, *warns)
	}
	for _, set := range sets {
		if r, ok := gdRowOf(t, st, set.typ, set.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", set.arn, ok, r.TagsJSON)
		}
	}
}

// In a member account ListIPSets returns the administrator's IP sets: a
// GetIPSet refused as member restricted keeps the set as its listed element
// and warns once under the op, without an error. GetFilter has no such
// refusal, so the same answer there is a hard error, the filter still kept.
func TestScanGuardDuty_MemberRestrictedGetIPSetKeepsTheRow(t *testing.T) {
	for _, c := range []struct {
		op, key, typ, arn string
		element           map[string]string
		wantErr           bool
	}{
		{"GetIPSet", "d-b/ip-b", TypeGuardDutyIPSet, gdDetARN("d-b") + "/ipset/ip-b", map[string]string{"DetectorId": "d-b", "IpSetId": "ip-b"}, false},
		{"GetFilter", "d-b/f-b", TypeGuardDutyFilter, gdDetARN("d-b") + "/filter/f-b", map[string]string{"DetectorId": "d-b", "FilterName": "f-b"}, true},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "guardduty:"+c.op)
			calls := newGDFixtureCalls()
			calls.errs[c.op+" "+c.key] = gdMemberError

			total, _, err := gdScan(context.Background(), st, gdHandlers(calls))
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "BadRequestException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 23 {
				t.Errorf("total = %d; want 23", total)
			}
			r, ok := gdRowOf(t, st, c.typ, c.arn)
			if !ok || r.TagsJSON != nil || r.AttributesJSON != mustJSON(c.element) {
				t.Errorf("%s stored %v, TagsJSON %v, attributes %s; want it kept as its listed element %s", c.arn, ok, r.TagsJSON, r.AttributesJSON, mustJSON(c.element))
			}
			if want := map[bool]int{false: 1, true: 0}[c.wantErr]; len(*warnings) != want {
				t.Errorf("%s warnings = %v; want %d", c.op, *warnings, want)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// although the gets in flight saw it too. The filters whose gets it cut
// short are stored as their listed element.
func TestScanGuardDuty_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newGDFixtureCalls()
	h := gdHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["GetFilter"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := gdScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListMembers"); n != 0 {
		t.Errorf("ListMembers called %d times after ctx ended", n)
	}
	for _, s := range []string{"t", "b"} {
		arn, want := gdDetARN("d-"+s)+"/filter/f-"+s, mustJSON(map[string]string{"DetectorId": "d-" + s, "FilterName": "f-" + s})
		if r, ok := gdRowOf(t, st, TypeGuardDutyFilter, arn); !ok || r.AttributesJSON != want {
			t.Errorf("%s stored %v, attributes %s; want %s", arn, ok, r.AttributesJSON, want)
		}
	}
}

// A region without GuardDuty (NXDOMAIN on ListDetectors, nothing stored) is
// unavailable and no other call is made. An NXDOMAIN from a get after the
// listing resolved is a failure of this scan, not a region gap; the
// detector is kept as its listed element, and its children under it.
func TestScanGuardDuty_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "guardduty.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("no endpoint", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGDFixtureCalls()
		h := gdHandlers(calls)
		h["ListDetectors"] = func(any) (any, error) { return nil, nx }
		total, _, err := gdScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListMalwareProtectionPlans") != 0 {
			t.Errorf("total, ListMalwareProtectionPlans calls = %d, %d; want 0, 0", total, calls.count("ListMalwareProtectionPlans"))
		}
	})
	t.Run("get after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGDCalls()
		h := gdHandlers(calls)
		h["ListDetectors"] = calls.answer("ListDetectors", func(any) any { return &guardduty.ListDetectorsOutput{DetectorIds: []string{"d-t"}} })
		h["GetDetector"] = func(any) (any, error) { return nil, nx }
		for op, out := range map[string]any{
			"ListMalwareProtectionPlans": &guardduty.ListMalwareProtectionPlansOutput{}, "ListPublishingDestinations": &guardduty.ListPublishingDestinationsOutput{},
			"ListThreatEntitySets": &guardduty.ListThreatEntitySetsOutput{}, "ListThreatIntelSets": &guardduty.ListThreatIntelSetsOutput{},
			"ListTrustedEntitySets": &guardduty.ListTrustedEntitySetsOutput{}, "ListCustomDetectionRuleAssociations": &guardduty.ListCustomDetectionRuleAssociationsOutput{},
			"ListCustomDetectionRuleOrgConfigurations": &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{},
		} {
			h[op] = calls.answer(op, func(any) any { return out })
		}
		h["DescribeOrganizationConfiguration"] = func(any) (any, error) { return nil, gdBadRequest }
		total, _, err := gdScan(context.Background(), st, h)
		if total != 4 {
			t.Errorf("total = %d; want 4: the detector kept as its listed element, its filter, IP set and member", total)
		}
		assertGlueOutcome(t, err, outcomeError)
		if isDNSNotFound(err) {
			t.Errorf("err = %v; want the get's NXDOMAIN flattened", err)
		}
	})
}
