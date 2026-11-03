package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/fms"
	fmstypes "github.com/aws/aws-sdk-go-v2/service/fms/types"
	"github.com/icearp/disco-cli/store"
)

var fmsThrottled = apiErr("ThrottlingException", "slow down")

var fmsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: fms:List")

var fmsGone = apiErr("ResourceNotFoundException", "gone")

var fmsAdminOnly = apiErr("AccessDeniedException", "This operation is only available to AWS Firewall Manager Administrators")

var fmsNoDefaultAdmin = apiErr("AccessDeniedException", "No default admin could be found")

// fmsTestTotal is the rows fmsHandlers' fixture stores: 4 policies, 2
// resource sets, the notification channel, 2 applications lists and 2
// protocols lists.
const fmsTestTotal = 11

func fmsPolicyARN(id string) string {
	return fmsPolicyARNIn(testRegion, id)
}

func fmsPolicyARNIn(region, id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:policy/%s", region, testAccountID, id)
}

func fmsSetARN(id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:resource-set/%s", testRegion, testAccountID, id)
}

func fmsAppsARN(id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:applications-list/%s", testRegion, testAccountID, id)
}

func fmsProtoARN(id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:protocols-list/%s", testRegion, testAccountID, id)
}

func fmsMember(uri string) fmstypes.Resource {
	return fmstypes.Resource{URI: sdkaws.String(uri), AccountId: sdkaws.String(testAccountID)}
}

// fmsPageCapped fails a call not sent the model's MaxResults cap of 100.
func fmsPageCapped(maxResults *int32) error {
	if maxResults == nil || *maxResults != 100 {
		return apiErr("InvalidInputException", "MaxResults is not 100")
	}
	return nil
}

// fmsFixtureTags are the tags fmsHandlers answers ListTagsForResource with,
// by ARN; every other ARN has none. The ARN-less policy's tags are keyed by
// the ARN built from its PolicyId.
func fmsFixtureTags() map[string]map[string]string {
	return map[string]map[string]string{
		fmsPolicyARN("p1"):      {"team": "net"},
		fmsPolicyARN("arnless"): {"origin": "built"},
		fmsSetARN("rs1"):        {"env": "prod"},
		fmsAppsARN("a1"):        {"kind": "apps"},
		fmsProtoARN("l1"):       {"kind": "proto"},
	}
}

// fmsFixtureTagARNs are the ARNs of every row fmsHandlers' fixture stores
// but the notification channel, sorted: the ARNs ListTagsForResource is sent.
func fmsFixtureTagARNs() []string {
	return slices.Sorted(slices.Values([]string{
		fmsPolicyARN("p1"), fmsPolicyARN("blank"), fmsPolicyARN("arnless"), fmsPolicyARN("p2"),
		fmsSetARN("rs1"), fmsSetARN("rs2"),
		fmsAppsARN("a1"), fmsAppsARN("a2"),
		fmsProtoARN("l1"), fmsProtoARN("l2"),
	}))
}

// The summaries fmsHandlers lists with every field set, so a row builder
// that drops a listed field fails the attribute comparisons.
var (
	fmsListedP1 = fmstypes.PolicySummary{
		PolicyId: sdkaws.String("p1"), PolicyArn: sdkaws.String(fmsPolicyARN("p1")), PolicyName: sdkaws.String("waf-all"),
		PolicyStatus: fmstypes.CustomerPolicyStatusActive, ResourceType: sdkaws.String("AWS::ElasticLoadBalancingV2::LoadBalancer"),
		SecurityServiceType: fmstypes.SecurityServiceTypeWafv2, RemediationEnabled: true, DeleteUnusedFMManagedResources: true,
	}
	// fmsListedBlank is listed with an empty PolicyId, so it is stored as
	// listed.
	fmsListedBlank = fmstypes.PolicySummary{
		PolicyId: sdkaws.String(""), PolicyArn: sdkaws.String(fmsPolicyARN("blank")), PolicyName: sdkaws.String("blank-id"),
		PolicyStatus: fmstypes.CustomerPolicyStatusOutOfAdminScope, ResourceType: sdkaws.String("AWS::EC2::VPC"),
		SecurityServiceType: fmstypes.SecurityServiceTypeSecurityGroupsContentAudit, RemediationEnabled: true, DeleteUnusedFMManagedResources: true,
	}
	fmsListedRS1 = fmstypes.ResourceSetSummary{
		Id: sdkaws.String("rs1"), Name: sdkaws.String("prod-vpcs"), Description: sdkaws.String("every prod VPC"),
		LastUpdateTime: sdkaws.Time(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)), ResourceSetStatus: fmstypes.ResourceSetStatusActive,
	}
	fmsListedA1 = fmstypes.AppsListDataSummary{
		ListArn: sdkaws.String(fmsAppsARN("a1")), ListId: sdkaws.String("a1"), ListName: sdkaws.String("apps-1"),
		AppsList: []fmstypes.App{{AppName: sdkaws.String("https"), Port: sdkaws.Int64(443), Protocol: sdkaws.String("tcp")}},
	}
	fmsListedL1 = fmstypes.ProtocolsListDataSummary{
		ListArn: sdkaws.String(fmsProtoARN("l1")), ListId: sdkaws.String("l1"), ListName: sdkaws.String("proto-1"),
		ProtocolsList: []string{"tcp", "udp"},
	}
)

// fmsAssertAttrs decodes r's attributes into a T and compares them with want.
func fmsAssertAttrs[T any](t *testing.T, r store.Resource, want T) {
	t.Helper()
	var got T
	if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
		t.Fatalf("decode %s: %v", r.NativeID, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s attributes = %s; want %+v", r.NativeID, r.AttributesJSON, want)
	}
}

// fmsCalls records what fmsHandlers was asked. tagErrs (by ARN) fails those
// tag reads; set it before the scan.
type fmsCalls struct {
	ops     parentCalls // every op called
	members parentCalls // set ids whose members were listed
	tagARNs parentCalls // ARNs sent to ListTagsForResource
	tagErrs map[string]error
}

// fmsHandlers answers an FMS admin account with two policies, a summary with
// an ARN but no PolicyId, one with a PolicyId but no ARN, and one with
// neither; two resource sets plus one with no Id; two applications lists
// and two protocols lists; each listing over two pages, and every listing
// sent the MaxResults cap. Set rs1 has three members over two pages
// (answered out of order), rs2 has none. Tags are fmsFixtureTags'.
func fmsHandlers(calls *fmsCalls) map[string]func(in any) (any, error) {
	members := map[string][][]fmstypes.Resource{
		"rs1": {
			{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-b"), fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a")},
			{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-c")},
		},
		"rs2": {{}},
	}
	tags := fmsFixtureTags()
	h := map[string]func(in any) (any, error){
		"ListPolicies": func(in any) (any, error) {
			if err := fmsPageCapped(in.(*fms.ListPoliciesInput).MaxResults); err != nil {
				return nil, err
			}
			if in.(*fms.ListPoliciesInput).NextToken == nil {
				return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{
					fmsListedP1,
					fmsListedBlank,
					{PolicyId: sdkaws.String("arnless")},
					{PolicyName: sdkaws.String("nameless")},
				}, NextToken: sdkaws.String("p-2")}, nil
			}
			return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{
				{PolicyId: sdkaws.String("p2"), PolicyArn: sdkaws.String(fmsPolicyARN("p2")), PolicyName: sdkaws.String("sg-audit")},
			}}, nil
		},
		"GetPolicy": func(in any) (any, error) {
			id := sdkaws.ToString(in.(*fms.GetPolicyInput).PolicyId)
			return &fms.GetPolicyOutput{PolicyArn: sdkaws.String(fmsPolicyARN(id)), Policy: &fmstypes.Policy{
				PolicyId: sdkaws.String(id), PolicyName: sdkaws.String("from-get-" + id), ResourceSetIds: []string{"rs1"},
			}}, nil
		},
		"ListResourceSets": func(in any) (any, error) {
			if err := fmsPageCapped(in.(*fms.ListResourceSetsInput).MaxResults); err != nil {
				return nil, err
			}
			if in.(*fms.ListResourceSetsInput).NextToken == nil {
				return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{
					fmsListedRS1,
					{Id: sdkaws.String("")},
				}, NextToken: sdkaws.String("rs-2")}, nil
			}
			return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{{Id: sdkaws.String("rs2"), Name: sdkaws.String("empty")}}}, nil
		},
		"ListResourceSetResources": func(in any) (any, error) {
			req := in.(*fms.ListResourceSetResourcesInput)
			if err := fmsPageCapped(req.MaxResults); err != nil {
				return nil, err
			}
			id := sdkaws.ToString(req.Identifier)
			calls.members.add(id)
			pages, ok := members[id]
			if !ok {
				return nil, fmsGone
			}
			page := 0
			if req.NextToken != nil {
				page = 1
			}
			out := &fms.ListResourceSetResourcesOutput{Items: pages[page]}
			if page+1 < len(pages) {
				out.NextToken = sdkaws.String("m-2")
			}
			return out, nil
		},
		"GetNotificationChannel": func(any) (any, error) {
			return &fms.GetNotificationChannelOutput{SnsTopicArn: sdkaws.String("arn:aws:sns:us-east-1:123456789012:fms")}, nil
		},
		"ListAppsLists": func(in any) (any, error) {
			req := in.(*fms.ListAppsListsInput)
			if err := fmsPageCapped(req.MaxResults); err != nil {
				return nil, err
			}
			if req.DefaultLists {
				return nil, apiErr("InvalidInputException", "the scan asked for the default lists")
			}
			if req.NextToken == nil {
				return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{fmsListedA1}, NextToken: sdkaws.String("a-2")}, nil
			}
			return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: sdkaws.String(fmsAppsARN("a2"))}}}, nil
		},
		"ListProtocolsLists": func(in any) (any, error) {
			req := in.(*fms.ListProtocolsListsInput)
			if err := fmsPageCapped(req.MaxResults); err != nil {
				return nil, err
			}
			if req.DefaultLists {
				return nil, apiErr("InvalidInputException", "the scan asked for the default lists")
			}
			if req.NextToken == nil {
				return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{fmsListedL1}, NextToken: sdkaws.String("l-2")}, nil
			}
			return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: sdkaws.String(fmsProtoARN("l2"))}}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*fms.ListTagsForResourceInput).ResourceArn)
			calls.tagARNs.add(arn)
			if err := calls.tagErrs[arn]; err != nil {
				return nil, err
			}
			var list []fmstypes.Tag
			for k, v := range tags[arn] {
				list = append(list, fmstypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
			}
			return &fms.ListTagsForResourceOutput{TagList: list}, nil
		},
	}
	return h
}

// fmsScan runs scanFMS in testRegion against h, recording every op called
// in calls.ops.
func fmsScan(ctx context.Context, st *store.Store, calls *fmsCalls, h map[string]func(in any) (any, error)) (int, int, error) {
	recorded := make(map[string]func(in any) (any, error), len(h))
	for op, answer := range h {
		recorded[op] = func(in any) (any, error) {
			calls.ops.add(op)
			return answer(in)
		}
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(recorded, fmsDenied)}
	return scanFMS(ctx, acct, testRegion, st, testScanID)
}

// fmsStoredMembers decodes a stored resource set's Resources, failing on a
// row whose Resources is absent or null rather than a list.
func fmsStoredMembers(t *testing.T, r store.Resource) []string {
	t.Helper()
	var raw struct {
		Resources json.RawMessage `json:"Resources"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
		t.Fatalf("decode %s: %v", r.NativeID, err)
	}
	var members []fmstypes.Resource
	if err := json.Unmarshal(raw.Resources, &members); err != nil || members == nil {
		t.Fatalf("%s Resources = %s; want a list", r.NativeID, raw.Resources)
	}
	uris := []string{}
	for _, m := range members {
		uris = append(uris, sdkaws.ToString(m.URI))
	}
	return uris
}

// fmsWarningsByOp counts st's warnings per op.
func fmsWarningsByOp(st *store.Store) func() map[string]int {
	var (
		mu sync.Mutex
		n  = map[string]int{}
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		n[w.Service]++
		mu.Unlock()
	}
	return func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(n)
	}
}

// fmsAttrKeys returns the top-level attribute keys of a stored row.
func fmsAttrKeys(t *testing.T, r store.Resource) map[string]json.RawMessage {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &keys); err != nil {
		t.Fatalf("decode %s: %v", r.NativeID, err)
	}
	return keys
}

// fmsStoredIDs returns the sorted NativeIDs of typ's stored rows.
func fmsStoredIDs(t *testing.T, st *store.Store, typ string) []string {
	t.Helper()
	return slices.Sorted(maps.Keys(rowsByNativeID(t, st, typ)))
}

// fmsAssertRow checks a stored row's name, status, region and CreatedAt.
func fmsAssertRow(t *testing.T, r store.Resource, name, status string) {
	t.Helper()
	if got := sdkaws.ToString(r.Name); got != name {
		t.Errorf("%s Name = %q; want %q", r.NativeID, got, name)
	}
	if got := sdkaws.ToString(r.Status); got != status {
		t.Errorf("%s Status = %q; want %q", r.NativeID, got, status)
	}
	if got := sdkaws.ToString(r.Region); got != testRegion {
		t.Errorf("%s Region = %q; want %q", r.NativeID, got, testRegion)
	}
	if r.CreatedAt != nil {
		t.Errorf("%s CreatedAt = %q; want unset", r.NativeID, *r.CreatedAt)
	}
}

// Every listing stores both its pages. A policy's attributes are its
// GetPolicy body, a summary without PolicyId keeps its summary, and a
// summary without PolicyArn is stored under the ARN built from its id; a
// resource set's attributes carry its members. Every row but the
// notification channel has its tags read with its ARN, exactly once, and
// stores them (nil for none).
func TestScanFMS_StoresEveryRowWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var calls fmsCalls
	total, inserted, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
	if err != nil {
		t.Fatalf("scanFMS: %v", err)
	}
	if total != fmsTestTotal || inserted != fmsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, fmsTestTotal, fmsTestTotal)
	}
	// fms:ListPolicies: one for the id-less summary stored from the listing,
	// one for the summary with neither id nor ARN; fms:ListResourceSets: one
	// for the Id-less set.
	if got, want := warns(), map[string]int{"fms:ListPolicies": 2, "fms:ListResourceSets": 1}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
	if got := calls.members.distinct(); !reflect.DeepEqual(got, []string{"rs1", "rs2"}) {
		t.Errorf("sets whose members were listed = %v; want [rs1 rs2]", got)
	}
	if got, want := calls.tagARNs.ids, fmsFixtureTagARNs(); !slices.Equal(slices.Sorted(slices.Values(got)), want) {
		t.Errorf("ListTagsForResource ARNs = %v; want each of %v once", got, want)
	}

	policies := rowsByNativeID(t, st, TypeFMSPolicy)
	if got, want := fmsStoredIDs(t, st, TypeFMSPolicy), slices.Sorted(slices.Values([]string{fmsPolicyARN("p1"), fmsPolicyARN("blank"), fmsPolicyARN("arnless"), fmsPolicyARN("p2")})); !slices.Equal(got, want) {
		t.Fatalf("policy rows = %v; want %v (only the summary with neither id nor ARN skipped)", got, want)
	}
	// The id-less summary is stored as listed, every field kept.
	fmsAssertAttrs(t, policies[fmsPolicyARN("blank")], fmsListedBlank)
	for id, wantName := range map[string]string{"p1": "from-get-p1", "arnless": "from-get-arnless"} {
		var body fmstypes.Policy
		if err := json.Unmarshal([]byte(policies[fmsPolicyARN(id)].AttributesJSON), &body); err != nil {
			t.Fatalf("decode policy %s: %v", id, err)
		}
		if !reflect.DeepEqual(body.ResourceSetIds, []string{"rs1"}) || sdkaws.ToString(body.PolicyName) != wantName {
			t.Errorf("policy %s attrs = %s; want the GetPolicy body", id, policies[fmsPolicyARN(id)].AttributesJSON)
		}
	}
	fmsAssertRow(t, policies[fmsPolicyARN("p1")], "waf-all", "ACTIVE")
	fmsAssertRow(t, policies[fmsPolicyARN("p2")], "sg-audit", "")
	assertTagsMap(t, policies[fmsPolicyARN("p1")].TagsJSON, map[string]string{"team": "net"})
	assertTagsMap(t, policies[fmsPolicyARN("arnless")].TagsJSON, map[string]string{"origin": "built"})

	sets := rowsByNativeID(t, st, TypeFMSResourceSet)
	if got := fmsStoredIDs(t, st, TypeFMSResourceSet); !slices.Equal(got, []string{fmsSetARN("rs1"), fmsSetARN("rs2")}) {
		t.Fatalf("resource set rows = %v; want rs1, rs2 (the id-less summary skipped)", got)
	}
	rs1 := sets[fmsSetARN("rs1")]
	want := []string{
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a",
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-b",
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-c",
	}
	if got := fmsStoredMembers(t, rs1); !reflect.DeepEqual(got, want) {
		t.Errorf("rs1 members = %v; want both pages, sorted: %v", got, want)
	}
	var rs1Attrs fmsResourceSetAttrs
	if err := json.Unmarshal([]byte(rs1.AttributesJSON), &rs1Attrs); err != nil {
		t.Fatalf("decode rs1: %v", err)
	}
	if !reflect.DeepEqual(rs1Attrs.ResourceSet, fmsListedRS1) {
		t.Errorf("rs1 ResourceSet = %+v; want the listed summary %+v", rs1Attrs.ResourceSet, fmsListedRS1)
	}
	fmsAssertRow(t, rs1, "prod-vpcs", "ACTIVE")
	assertTagsMap(t, rs1.TagsJSON, map[string]string{"env": "prod"})
	if got := fmsStoredMembers(t, sets[fmsSetARN("rs2")]); len(got) != 0 {
		t.Errorf("rs2 members = %v; want an empty list", got)
	}

	apps := rowsByNativeID(t, st, TypeFMSAppsList)
	if got := fmsStoredIDs(t, st, TypeFMSAppsList); !slices.Equal(got, []string{fmsAppsARN("a1"), fmsAppsARN("a2")}) {
		t.Fatalf("applications list rows = %v; want both pages", got)
	}
	fmsAssertRow(t, apps[fmsAppsARN("a1")], "apps-1", "")
	fmsAssertAttrs(t, apps[fmsAppsARN("a1")], fmsListedA1)
	assertTagsMap(t, apps[fmsAppsARN("a1")].TagsJSON, map[string]string{"kind": "apps"})

	protos := rowsByNativeID(t, st, TypeFMSProtocolsList)
	if got := fmsStoredIDs(t, st, TypeFMSProtocolsList); !slices.Equal(got, []string{fmsProtoARN("l1"), fmsProtoARN("l2")}) {
		t.Fatalf("protocols list rows = %v; want both pages", got)
	}
	fmsAssertRow(t, protos[fmsProtoARN("l1")], "proto-1", "")
	fmsAssertAttrs(t, protos[fmsProtoARN("l1")], fmsListedL1)
	assertTagsMap(t, protos[fmsProtoARN("l1")].TagsJSON, map[string]string{"kind": "proto"})

	for typ, untagged := range map[string]string{
		TypeFMSPolicy:        fmsPolicyARN("p2"),
		TypeFMSResourceSet:   fmsSetARN("rs2"),
		TypeFMSAppsList:      fmsAppsARN("a2"),
		TypeFMSProtocolsList: fmsProtoARN("l2"),
	} {
		if r := rowsByNativeID(t, st, typ)[untagged]; r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil for no tags", untagged, *r.TagsJSON)
		}
	}
	if got := policies[fmsPolicyARN("blank")].TagsJSON; got != nil {
		t.Errorf("blank TagsJSON = %q; want nil for no tags", *got)
	}
}

// A rescan of unchanged resources inserts nothing.
func TestScanFMS_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	var calls fmsCalls
	if _, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != fmsTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, fmsTestTotal)
	}
}

// A region with nothing listed stores nothing, reads no tags and warns
// nothing.
func TestScanFMS_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var calls fmsCalls
	h := fmsHandlers(&calls)
	h["ListPolicies"] = func(any) (any, error) { return &fms.ListPoliciesOutput{}, nil }
	h["ListResourceSets"] = func(any) (any, error) { return &fms.ListResourceSetsOutput{}, nil }
	h["GetNotificationChannel"] = func(any) (any, error) { return nil, fmsGone }
	h["ListAppsLists"] = func(any) (any, error) { return &fms.ListAppsListsOutput{}, nil }
	h["ListProtocolsLists"] = func(any) (any, error) { return &fms.ListProtocolsListsOutput{}, nil }
	total, inserted, err := fmsScan(context.Background(), st, &calls, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if got := warns(); len(got) != 0 {
		t.Errorf("warnings by op = %v; want none", got)
	}
}

// A failed member read never stores the set with a list that looks complete:
// a set deleted after the listing is dropped silently; a denial keeps the
// set without a Resources key and warns once however many sets it hits; a
// throttle keeps it and marks the scan partial; any other error keeps it and
// fails the scan, while the later phases still store their rows.
func TestScanFMS_MemberReadFailure(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		fail      []string       // set ids whose member read returns err
		outcome   serviceOutcome // -1: no error
		wantWarns int
		wantSets  []string
	}{
		{"set deleted after listing", fmsGone, []string{"rs1"}, -1, 0, []string{"rs2"}},
		{"denied", fmsDenied, []string{"rs1", "rs2"}, -1, 1, []string{"rs1", "rs2"}},
		{"throttled", fmsThrottled, []string{"rs1"}, outcomeTransient, 0, []string{"rs1", "rs2"}},
		{"unmodelled failure", apiErr("InvalidOperationException", "no"), []string{"rs1"}, outcomeError, 0, []string{"rs1", "rs2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "fms:ListResourceSetResources")
			var calls fmsCalls
			h := fmsHandlers(&calls)
			list := h["ListResourceSetResources"]
			h["ListResourceSetResources"] = func(in any) (any, error) {
				if slices.Contains(tc.fail, sdkaws.ToString(in.(*fms.ListResourceSetResourcesInput).Identifier)) {
					return nil, tc.err
				}
				return list(in)
			}
			_, _, err := fmsScan(context.Background(), st, &calls, h)
			if tc.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, tc.outcome)
				assertAPICode(t, err, apiCode(tc.err))
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			listedSets := map[string]fmstypes.ResourceSetSummary{
				"rs1": fmsListedRS1,
				"rs2": {Id: sdkaws.String("rs2"), Name: sdkaws.String("empty")},
			}
			var got []string
			sets := rowsByNativeID(t, st, TypeFMSResourceSet)
			for _, id := range []string{"rs1", "rs2"} {
				r, ok := sets[fmsSetARN(id)]
				if !ok {
					continue
				}
				got = append(got, id)
				if slices.Contains(tc.fail, id) {
					// The whole listed summary, with no Resources key.
					fmsAssertAttrs(t, r, fmsResourceSetAttrs{ResourceSet: listedSets[id]})
					continue
				}
				keys := fmsAttrKeys(t, r)
				if _, has := keys["Resources"]; !has {
					t.Errorf("%s attributes %s lack Resources; want them after a successful member read", id, r.AttributesJSON)
				}
				if _, has := keys["ResourceSet"]; !has {
					t.Errorf("%s attributes %s lack the listed ResourceSet", id, r.AttributesJSON)
				}
			}
			if !reflect.DeepEqual(got, tc.wantSets) {
				t.Errorf("stored sets = %v; want %v", got, tc.wantSets)
			}
			if n := len(rowsByNativeID(t, st, TypeFMSProtocolsList)); n != 2 {
				t.Errorf("protocols list rows = %d; want 2 from the last phase", n)
			}
		})
	}
}

// A member listing whose NextToken repeats stopped incomplete, so the set is
// stored without its partial member list, and the scan fails.
func TestScanFMS_MemberRepeatedTokenKeepsSetWithoutMembers(t *testing.T) {
	st := newTestStore(t)
	var calls fmsCalls
	h := fmsHandlers(&calls)
	h["ListResourceSetResources"] = func(any) (any, error) {
		return &fms.ListResourceSetResourcesOutput{Items: []fmstypes.Resource{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a")}, NextToken: sdkaws.String("same")}, nil
	}
	_, _, err := fmsScan(context.Background(), st, &calls, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	sets := rowsByNativeID(t, st, TypeFMSResourceSet)
	if len(sets) != 2 {
		t.Fatalf("resource set rows = %d; want 2", len(sets))
	}
	for _, r := range sets {
		if _, has := fmsAttrKeys(t, r)["Resources"]; has {
			t.Errorf("%s attributes %s; want no Resources", r.NativeID, r.AttributesJSON)
		}
	}
}

// fmsListings are the paged listings of scanFMS, each with its first page's
// stored row and the type it stores.
var fmsListings = []struct {
	op, typ, firstRow string
	page              func(token *string) any
}{
	{"ListPolicies", TypeFMSPolicy, fmsPolicyARN("p1"), func(token *string) any {
		return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{{PolicyId: sdkaws.String("p1"), PolicyArn: sdkaws.String(fmsPolicyARN("p1"))}}, NextToken: token}
	}},
	{"ListResourceSets", TypeFMSResourceSet, fmsSetARN("rs1"), func(token *string) any {
		return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{{Id: sdkaws.String("rs1")}}, NextToken: token}
	}},
	{"ListAppsLists", TypeFMSAppsList, fmsAppsARN("a1"), func(token *string) any {
		return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: sdkaws.String(fmsAppsARN("a1"))}}, NextToken: token}
	}},
	{"ListProtocolsLists", TypeFMSProtocolsList, fmsProtoARN("l1"), func(token *string) any {
		return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: sdkaws.String(fmsProtoARN("l1"))}}, NextToken: token}
	}},
}

// A repeated NextToken ends each listing as an error, keeping the rows of
// its page; the later phases still run.
func TestScanFMS_RepeatedTokenKeepsPages(t *testing.T) {
	for _, l := range fmsListings {
		t.Run(l.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls fmsCalls
			h := fmsHandlers(&calls)
			h[l.op] = func(any) (any, error) { return l.page(sdkaws.String("same")), nil }
			_, _, err := fmsScan(context.Background(), st, &calls, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if got := fmsStoredIDs(t, st, l.typ); !slices.Equal(got, []string{l.firstRow}) {
				t.Errorf("%s rows = %v; want the page's %s", l.typ, got, l.firstRow)
			}
			if l.op != "ListProtocolsLists" && len(rowsByNativeID(t, st, TypeFMSProtocolsList)) != 2 {
				t.Errorf("protocols lists not stored; want the last phase to run")
			}
		})
	}
}

// A listing whose second page fails keeps its first page, and the later
// phases still run. Any error but a denial fails the scan; the admin-only
// denial after a page was read is warned about as a denial, under the op.
func TestScanFMS_LaterPageFailureKeepsPages(t *testing.T) {
	for _, l := range fmsListings {
		for _, tc := range []struct {
			name   string
			err    error
			denial bool
		}{
			{"hard error", apiErr("InternalErrorException", "boom"), false},
			{"admin-only denial", fmsAdminOnly, true},
		} {
			t.Run(l.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := fmsWarningsByOp(st)
				var calls fmsCalls
				h := fmsHandlers(&calls)
				h[l.op] = func(in any) (any, error) {
					if bacField[*string](in, "NextToken") != nil {
						return nil, tc.err
					}
					return l.page(sdkaws.String("next")), nil
				}
				_, _, err := fmsScan(context.Background(), st, &calls, h)
				wantWarns := map[string]int{}
				if tc.denial {
					wantWarns["fms:"+l.op] = 1
					if err != nil {
						t.Errorf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, "InternalErrorException")
				}
				// The fixture's own warnings, for the listings not replaced.
				if l.op != "ListPolicies" {
					wantWarns["fms:ListPolicies"] += 2
				}
				if l.op != "ListResourceSets" {
					wantWarns["fms:ListResourceSets"]++
				}
				if got := warns(); !maps.Equal(got, wantWarns) {
					t.Errorf("warnings by op = %v; want %v", got, wantWarns)
				}
				if got := fmsStoredIDs(t, st, l.typ); !slices.Equal(got, []string{l.firstRow}) {
					t.Errorf("%s rows = %v; want the first page's %s", l.typ, got, l.firstRow)
				}
				if l.op != "ListProtocolsLists" && len(rowsByNativeID(t, st, TypeFMSProtocolsList)) != 2 {
					t.Errorf("protocols lists not stored; want the last phase to run")
				}
			})
		}
	}
}

// Each listing writes a page before it reads the next: the first page's row
// is stored when the second page is requested.
func TestScanFMS_WritesEachPageBeforeTheNext(t *testing.T) {
	st := newTestStore(t)
	var calls fmsCalls
	h := fmsHandlers(&calls)
	lookups := map[string]error{} // per listing: GetResource of its page-1 row, when page 2 was requested
	for _, l := range fmsListings {
		list := h[l.op]
		h[l.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, lookups[l.op] = st.GetResource(store.ResourceID("aws", testAccountID, l.firstRow))
			}
			return list(in)
		}
	}
	if _, _, err := fmsScan(context.Background(), st, &calls, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, l := range fmsListings {
		err, asked := lookups[l.op]
		if !asked {
			t.Errorf("%s: page 2 never requested", l.op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", l.op, l.firstRow, err)
		}
	}
}

// GetPolicy: a policy deleted after the listing is dropped silently; a
// denial keeps each policy with its listed summary as attributes and warns
// once; a throttle keeps it and marks the scan partial; a response without
// a body or an unmodelled error keeps it and fails the scan.
func TestScanFMS_GetPolicyFailure(t *testing.T) {
	unmodelled := apiErr("InvalidOperationException", "no")
	cases := []struct {
		name         string
		answer       func() (any, error)
		fail         []string       // policy ids whose GetPolicy returns answer
		outcome      serviceOutcome // -1: no error
		wantErr      error
		wantWarns    int
		wantPolicies []string
	}{
		{"policy deleted after listing", func() (any, error) { return nil, fmsGone }, []string{"p2"}, -1, nil, 0, []string{"p1"}},
		{"denied", func() (any, error) { return nil, fmsDenied }, []string{"p1", "p2"}, -1, nil, 1, []string{"p1", "p2"}},
		{"throttled", func() (any, error) { return nil, fmsThrottled }, []string{"p2"}, outcomeTransient, fmsThrottled, 0, []string{"p1", "p2"}},
		{"no policy body", func() (any, error) { return &fms.GetPolicyOutput{}, nil }, []string{"p2"}, outcomeError, errFMSPolicyMissing, 0, []string{"p1", "p2"}},
		{"unmodelled failure", func() (any, error) { return nil, unmodelled }, []string{"p2"}, outcomeError, unmodelled, 0, []string{"p1", "p2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "fms:GetPolicy")
			var calls fmsCalls
			h := fmsHandlers(&calls)
			get := h["GetPolicy"]
			h["GetPolicy"] = func(in any) (any, error) {
				if slices.Contains(tc.fail, sdkaws.ToString(in.(*fms.GetPolicyInput).PolicyId)) {
					return tc.answer()
				}
				return get(in)
			}
			_, _, err := fmsScan(context.Background(), st, &calls, h)
			if tc.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, tc.outcome)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			var got []string
			policies := rowsByNativeID(t, st, TypeFMSPolicy)
			for _, id := range []string{"p1", "p2"} {
				r, ok := policies[fmsPolicyARN(id)]
				if !ok {
					continue
				}
				got = append(got, id)
				// The summary carries PolicyArn and no ResourceSetIds; the
				// GetPolicy body is the other way round.
				keys := fmsAttrKeys(t, r)
				_, summary := keys["PolicyArn"]
				_, body := keys["ResourceSetIds"]
				if wantSummary := slices.Contains(tc.fail, id); summary != wantSummary || body == wantSummary {
					t.Errorf("%s attributes %s; want the summary only when GetPolicy failed", id, r.AttributesJSON)
				}
				if id == "p1" && slices.Contains(tc.fail, id) {
					fmsAssertAttrs(t, r, fmsListedP1)
				}
			}
			if !reflect.DeepEqual(got, tc.wantPolicies) {
				t.Errorf("stored policies = %v; want %v", got, tc.wantPolicies)
			}
			if n := len(rowsByNativeID(t, st, TypeFMSResourceSet)); n != 2 {
				t.Errorf("resource set rows = %d; want 2 from the later phase", n)
			}
		})
	}
}

// A transient policy-phase error is demoted to a warning, so the resource-set
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanFMS_TransientDoesNotMaskMemberError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "fms:ListPolicies")
	var calls fmsCalls
	h := fmsHandlers(&calls)
	h["ListPolicies"] = func(any) (any, error) { return nil, fmsThrottled }
	h["ListResourceSetResources"] = func(any) (any, error) { return nil, apiErr("InvalidOperationException", "no") }

	total, _, err := fmsScan(context.Background(), st, &calls, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidOperationException")
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if want := fmsTestTotal - 4; total != want {
		t.Errorf("total = %d; want %d (every row but the policies)", total, want)
	}
}

// A transient failure on one key does not soften a hard error on another:
// the phase fails and the scan's outcome is an error.
func TestScanFMS_TransientKeyWithHardKeyFails(t *testing.T) {
	cases := []struct {
		name, op string
		key      func(in any) string
	}{
		{"policies", "GetPolicy", func(in any) string { return sdkaws.ToString(in.(*fms.GetPolicyInput).PolicyId) }},
		{"resource sets", "ListResourceSetResources", func(in any) string {
			return sdkaws.ToString(in.(*fms.ListResourceSetResourcesInput).Identifier)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls fmsCalls
			h := fmsHandlers(&calls)
			h[tc.op] = func(in any) (any, error) {
				if k := tc.key(in); k == "p1" || k == "rs1" {
					return nil, fmsThrottled
				}
				return nil, apiErr("InvalidOperationException", "no")
			}
			_, _, err := fmsScan(context.Background(), st, &calls, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidOperationException")
		})
	}
}

// A not-onboarded answer after rows were stored or a call failed, in an
// earlier phase or on an earlier page of the same listing, is a warning
// under the op that met it: returning the sentinel would report the
// service disabled, hiding the hard error or the stored rows' counts. The
// later phases still run. A rescan stores the same rows again without
// inserting any, and still warns rather than reports the service disabled.
func TestScanFMS_DisabledAfterEarlierPhase(t *testing.T) {
	notOnboarded := func(any) (any, error) { return nil, fmsNoDefaultAdmin }
	cases := []struct {
		name      string
		override  map[string]func(any) (any, error)
		rescan    bool // scan with the fixture first, so every row is already stored
		warnOp    string
		wantCode  string // "" for a nil error
		wantTotal int
	}{
		{
			"after a hard error",
			map[string]func(any) (any, error){
				"ListPolicies":     func(any) (any, error) { return nil, apiErr("InternalErrorException", "boom") },
				"ListResourceSets": notOnboarded,
			},
			false, "fms:ListResourceSets", "InternalErrorException", fmsTestTotal - 4 - 2,
		},
		{"after stored rows", map[string]func(any) (any, error){"ListResourceSets": notOnboarded}, false, "fms:ListResourceSets", "", fmsTestTotal - 2},
		{"after rows stored again on a rescan", map[string]func(any) (any, error){"ListResourceSets": notOnboarded}, true, "fms:ListResourceSets", "", fmsTestTotal - 2},
		{
			"on a later page",
			map[string]func(any) (any, error){"ListPolicies": func(in any) (any, error) {
				if in.(*fms.ListPoliciesInput).NextToken != nil {
					return nil, fmsNoDefaultAdmin
				}
				return fmsListings[0].page(sdkaws.String("next")), nil
			}},
			false, "fms:ListPolicies", "", fmsTestTotal - 4 + 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls fmsCalls
			if tc.rescan {
				if _, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls)); err != nil {
					t.Fatalf("first scan: %v", err)
				}
			}
			warns := countOpWarnings(st, tc.warnOp)
			h := fmsHandlers(&calls)
			maps.Copy(h, tc.override)
			total, _, err := fmsScan(context.Background(), st, &calls, h)
			if errors.Is(err, errServiceDisabled) {
				t.Fatalf("err = %v; want no disabled sentinel", err)
			}
			if tc.wantCode == "" && err != nil {
				t.Errorf("err = %v; want nil", err)
			}
			if tc.wantCode != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, tc.wantCode)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d; want 1 for the disabled answer", tc.warnOp, *warns)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d (later phases still run)", total, tc.wantTotal)
			}
		})
	}
}

// An account FMS was never onboarded for reports the service disabled,
// stores nothing and makes no later call, whether ListPolicies says so on
// its first page or on a later page after an empty one.
func TestScanFMS_NotEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		list func(in any) (any, error)
	}{
		{"first page", func(any) (any, error) { return nil, fmsNoDefaultAdmin }},
		{"after an empty first page", func(in any) (any, error) {
			if in.(*fms.ListPoliciesInput).NextToken != nil {
				return nil, fmsNoDefaultAdmin
			}
			return &fms.ListPoliciesOutput{NextToken: sdkaws.String("next")}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			var calls fmsCalls
			h := fmsHandlers(&calls)
			h["ListPolicies"] = tc.list
			total, _, err := fmsScan(context.Background(), st, &calls, h)
			if !errors.Is(err, errServiceDisabled) || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, errServiceDisabled", total, err)
			}
			if got := calls.ops.distinct(); !slices.Equal(got, []string{"ListPolicies"}) {
				t.Errorf("calls after a disabled service = %v; want only ListPolicies", got)
			}
			if got := warns(); len(got) != 0 {
				t.Errorf("warnings by op = %v; want none", got)
			}
		})
	}
}

// Tag reads that fail. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanFMS_TagReadFailures(t *testing.T) {
	rows := map[string]string{
		TypeFMSPolicy:        fmsPolicyARN("p1"),
		TypeFMSResourceSet:   fmsSetARN("rs1"),
		TypeFMSAppsList:      fmsAppsARN("a1"),
		TypeFMSProtocolsList: fmsProtoARN("l1"),
	}
	for _, typ := range slices.Sorted(maps.Keys(rows)) {
		arn := rows[typ]
		for _, c := range []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			wantTotal int
			wantKept  bool
			warn      bool
		}{
			{"not found", fmsGone, -1, fmsTestTotal - 1, false, false},
			{"denied", fmsDenied, -1, fmsTestTotal, true, true},
			{"transient", fmsThrottled, outcomeTransient, fmsTestTotal, true, false},
			{"other error", apiErr("InvalidInputException", "bad arn"), outcomeError, fmsTestTotal, true, false},
		} {
			t.Run(typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := fmsCalls{tagErrs: map[string]error{arn: c.err}}
				total, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, apiCode(c.err))
				}
				if total != c.wantTotal {
					t.Errorf("total = %d; want %d", total, c.wantTotal)
				}
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				var tagWarns []store.ScanWarning
				for _, w := range warns() {
					if w.Service == "fms:ListTagsForResource" {
						tagWarns = append(tagWarns, w)
					}
				}
				wantWarns := 0
				if c.warn {
					wantWarns = 1
				}
				if len(tagWarns) != wantWarns {
					t.Fatalf("fms:ListTagsForResource warnings = %v; want %v", tagWarns, c.warn)
				}
				for _, w := range tagWarns {
					if w.Scope != testAccountID+"/"+testRegion {
						t.Errorf("warning Scope %q; want %s/%s", w.Scope, testAccountID, testRegion)
					}
				}
			})
		}
	}
}

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanFMS_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "fms:ListTagsForResource")
	calls := fmsCalls{tagErrs: map[string]error{
		fmsPolicyARN("p2"): fmsDenied, fmsSetARN("rs2"): fmsDenied, fmsAppsARN("a2"): fmsDenied, fmsProtoARN("l2"): fmsDenied,
	}}
	total, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != fmsTestTotal {
		t.Errorf("total = %d; want %d", total, fmsTestTotal)
	}
	if *warns != 1 {
		t.Errorf("fms:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanFMS_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "fms:ListTagsForResource")
	calls := fmsCalls{tagErrs: map[string]error{fmsPolicyARN("p1"): fmsThrottled, fmsProtoARN("l2"): apiErr("InvalidInputException", "bad arn")}}
	_, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("fms:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan before any later listing, and no warning
// names a row the write lost: the failed phase's listing warnings and a
// denied tag read of its rows are not sent. Warnings about the rows earlier
// phases stored stand: the fixture's two fms:ListPolicies warnings, its
// fms:ListResourceSets warning, and the denied tag read of the stored p1.
func TestScanFMS_FailedWriteStopsScan(t *testing.T) {
	afterPolicies := map[string]int{"fms:ListPolicies": 2, "fms:ListTagsForResource": 1}
	afterSets := map[string]int{"fms:ListPolicies": 2, "fms:ListResourceSets": 1, "fms:ListTagsForResource": 1}
	for _, c := range []struct {
		typ        string
		laterLists []string // listings that must not be called
		wantTotal  int
		wantWarns  map[string]int
	}{
		{TypeFMSPolicy, []string{"ListResourceSets", "GetNotificationChannel", "ListAppsLists", "ListProtocolsLists"}, 0, map[string]int{}},
		{TypeFMSResourceSet, []string{"GetNotificationChannel", "ListAppsLists", "ListProtocolsLists"}, 4, afterPolicies},
		{TypeFMSAppsList, []string{"ListProtocolsLists"}, 4 + 2 + 1, afterSets},
		{TypeFMSProtocolsList, nil, 4 + 2 + 1 + 2, afterSets},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			refuseStoreWrites(t, st, c.typ)
			calls := fmsCalls{tagErrs: map[string]error{fmsPolicyARN("p1"): fmsDenied}}
			total, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if got := warns(); !maps.Equal(got, c.wantWarns) {
				t.Errorf("warnings by op = %v; want %v", got, c.wantWarns)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.ops.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// A write that fails on the policies' second page sends none of the
// listing's warnings, though the first page, with the summary listed
// without PolicyId, was stored.
func TestScanFMS_FailedLaterPageWriteSendsNoWarning(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_p2 BEFORE INSERT ON resources
		WHEN NEW.native_id = '` + fmsPolicyARN("p2") + `'
		BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	var calls fmsCalls
	total, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (the first page's policies)", total)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged, and a resource
// set's ARN and the ARN of a policy listed without PolicyArn are built in
// the account's partition.
func TestScanFMS_PartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arn := func(resource string) string { return partitionARN(pf.partition, "fms", pf.region, resource) }
			var calls fmsCalls
			h := fmsHandlers(&calls)
			h["ListPolicies"] = func(any) (any, error) {
				return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{
					{PolicyId: sdkaws.String("p1"), PolicyArn: sdkaws.String(arn("policy/p1"))},
					{PolicyId: sdkaws.String("p9")},
				}}, nil
			}
			h["ListResourceSets"] = func(any) (any, error) {
				return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{{Id: sdkaws.String("rs1")}}}, nil
			}
			h["ListAppsLists"] = func(any) (any, error) {
				return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: sdkaws.String(arn("applications-list/a1"))}}}, nil
			}
			h["ListProtocolsLists"] = func(any) (any, error) {
				return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: sdkaws.String(arn("protocols-list/l1"))}}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, fmsDenied)
			if _, _, err := scanFMS(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := slices.Sorted(slices.Values([]string{
				arn("policy/p1"), arn("policy/p9"), arn("resource-set/rs1"), arn("applications-list/a1"), arn("protocols-list/l1"),
			}))
			if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, ids := range map[string][]string{
				TypeFMSPolicy:        {arn("policy/p1"), arn("policy/p9")},
				TypeFMSResourceSet:   {arn("resource-set/rs1")},
				TypeFMSAppsList:      {arn("applications-list/a1")},
				TypeFMSProtocolsList: {arn("protocols-list/l1")},
			} {
				if got := fmsStoredIDs(t, st, typ); !slices.Equal(got, ids) {
					t.Errorf("%s rows = %v; want %v", typ, got, ids)
				}
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanFMS_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	var calls fmsCalls
	h := fmsHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListResourceSets"]
	h["ListResourceSets"] = func(in any) (any, error) {
		if in.(*fms.ListResourceSetsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, _, err := fmsScan(ctx, st, &calls, h)
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
	for _, op := range []string{"GetNotificationChannel", "ListAppsLists", "ListProtocolsLists"} {
		if slices.Contains(calls.ops.distinct(), op) {
			t.Errorf("%s called after ctx ended", op)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanFMS_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "fms.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		var calls fmsCalls
		h := fmsHandlers(&calls)
		h["ListAppsLists"] = func(any) (any, error) { return nil, nx }
		total, _, err := fmsScan(context.Background(), st, &calls, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := fmsTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := fmsCalls{tagErrs: map[string]error{fmsProtoARN("l2"): nx}}
		_, _, err := fmsScan(context.Background(), st, &calls, fmsHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		_, _, err := scanFMS(context.Background(), &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(nil, nx)}, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// fmsRegionalOps are the ops scanFMS sends in a region where the account is
// the Firewall Manager administrator.
var fmsRegionalOps = []string{
	"GetNotificationChannel", "GetPolicy", "ListAppsLists", "ListPolicies",
	"ListProtocolsLists", "ListResourceSetResources", "ListResourceSets", "ListTagsForResource",
}

const (
	fmsAdmin        = "admin"
	fmsMemberAcct   = "member"
	fmsNotOnboarded = "not-onboarded"
)

// fmsRegionalHandlers answers each region by its mode: an administrator
// region holds one policy using resource set rs1, one applications list, one
// protocols list and a notification channel, every ARN in that region, none
// tagged; a member region answers the admin-only denial; a not-onboarded
// region answers that no default admin exists. Each call goes into seen as
// op@region.
func fmsRegionalHandlers(mode map[string]string, seen *parentCalls) map[string]func(context.Context, any) (any, error) {
	h := map[string]func(context.Context, any) (any, error){}
	for _, op := range fmsRegionalOps {
		h[op] = func(ctx context.Context, in any) (any, error) {
			region := awsmw.GetRegion(ctx)
			seen.add(op + "@" + region)
			switch mode[region] {
			case fmsMemberAcct:
				return nil, fmsAdminOnly
			case fmsNotOnboarded:
				return nil, fmsNoDefaultAdmin
			}
			arn := func(kind, id string) *string {
				return sdkaws.String(fmt.Sprintf("arn:aws:fms:%s:%s:%s/%s", region, testAccountID, kind, id))
			}
			switch op {
			case "ListPolicies":
				return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{{PolicyId: sdkaws.String("p1"), PolicyArn: arn("policy", "p1")}}}, nil
			case "GetPolicy":
				return &fms.GetPolicyOutput{PolicyArn: arn("policy", "p1"), Policy: &fmstypes.Policy{PolicyId: sdkaws.String("p1"), ResourceSetIds: []string{"rs1"}}}, nil
			case "ListResourceSets":
				return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{{Id: sdkaws.String("rs1")}}}, nil
			case "ListResourceSetResources":
				return &fms.ListResourceSetResourcesOutput{}, nil
			case "GetNotificationChannel":
				return &fms.GetNotificationChannelOutput{SnsTopicArn: sdkaws.String("arn:aws:sns:" + region + ":" + testAccountID + ":fms")}, nil
			case "ListAppsLists":
				return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: arn("applications-list", "a1")}}}, nil
			case "ListTagsForResource":
				return &fms.ListTagsForResourceOutput{}, nil
			default:
				return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: arn("protocols-list", "l1")}}}, nil
			}
		}
	}
	return h
}

func fmsServiceEntry(t *testing.T) serviceEntry {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:fms" })
	if idx < 0 {
		t.Fatal("aws:fms not registered")
	}
	return registeredServices[idx]
}

// Firewall Manager objects are regional: each dispatched region's calls go to
// that region, its rows carry it, and the same ids in two regions (rs1, the
// notification channel singleton) are two rows, each policy using its own
// region's resource set.
func TestScanFMS_RegionalRowsPerRegion(t *testing.T) {
	entry := fmsServiceEntry(t)
	if entry.global {
		t.Fatal("aws:fms is global; want regional (one scan per region)")
	}
	regions := []string{"eu-west-1", "ap-southeast-2"}
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var seen parentCalls
	h := fmsRegionalHandlers(map[string]string{regions[0]: fmsAdmin, regions[1]: fmsAdmin}, &seen)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(h, fmsDenied)}
	for _, region := range regions {
		total, inserted, err := entry.fn(context.Background(), acct, region, st, testScanID)
		if err != nil || total != 5 || inserted != 5 {
			t.Fatalf("fn(%s) = %d, %d, %v; want 5, 5, nil (policy, resource set, channel, apps list, protocols list)", region, total, inserted, err)
		}
	}
	var wantCalls []string
	for _, op := range fmsRegionalOps {
		for _, region := range regions {
			wantCalls = append(wantCalls, op+"@"+region)
		}
	}
	slices.Sort(wantCalls)
	gotCalls := seen.distinct()
	slices.Sort(gotCalls)
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Errorf("calls = %v; want %v", gotCalls, wantCalls)
	}
	if got := warns(); len(got) != 0 {
		t.Errorf("warnings by op = %v; want none", got)
	}

	for typ, nativeID := range map[string]func(region string) string{
		TypeFMSPolicy:              func(r string) string { return fmsPolicyARNIn(r, "p1") },
		TypeFMSResourceSet:         func(r string) string { return fmsResourceSetARN("aws", r, testAccountID, "rs1") },
		TypeFMSNotificationChannel: func(r string) string { return "arn:aws:fms:" + r + ":" + testAccountID + ":notification-channel" },
		TypeFMSAppsList:            func(r string) string { return "arn:aws:fms:" + r + ":" + testAccountID + ":applications-list/a1" },
		TypeFMSProtocolsList:       func(r string) string { return "arn:aws:fms:" + r + ":" + testAccountID + ":protocols-list/l1" },
	} {
		rows := rowsByNativeID(t, st, typ)
		if len(rows) != len(regions) {
			t.Errorf("%s rows = %v; want one per region", typ, slices.Sorted(maps.Keys(rows)))
		}
		for _, region := range regions {
			r, ok := rows[nativeID(region)]
			if !ok {
				t.Errorf("%s: no row %s", typ, nativeID(region))
				continue
			}
			if got := sdkaws.ToString(r.Region); got != region {
				t.Errorf("%s %s region = %q; want %q", typ, r.NativeID, got, region)
			}
		}
	}

	if err := resolveFMSPolicyResourceSets(acct, st); err != nil {
		t.Fatalf("resolveFMSPolicyResourceSets: %v", err)
	}
	for _, region := range regions {
		set := store.ResourceID("aws", testAccountID, fmsResourceSetARN("aws", region, testAccountID, "rs1"))
		want := []string{store.ResourceID("aws", testAccountID, fmsPolicyARNIn(region, "p1"))}
		if got := edgeSources(t, st, set, store.RelUses); !reflect.DeepEqual(got, want) {
			t.Errorf("uses edges into %s rs1 from %v; want %v", region, got, want)
		}
	}
}

// Whether the account administers Firewall Manager is answered per region: a
// member region stores nothing and warns nothing, and a not-onboarded region
// reports the service disabled even after another region stored rows.
func TestScanFMS_NonAdminPerRegion(t *testing.T) {
	const adminRegion = "eu-west-1"
	cases := []struct {
		mode         string
		wantDisabled bool
		wantCalls    []string // ops sent in the non-admin region
	}{
		{fmsMemberAcct, false, []string{"GetNotificationChannel", "ListAppsLists", "ListPolicies", "ListProtocolsLists", "ListResourceSets"}},
		{fmsNotOnboarded, true, []string{"ListPolicies"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			const region = "ap-southeast-2"
			entry := fmsServiceEntry(t)
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			var seen parentCalls
			h := fmsRegionalHandlers(map[string]string{adminRegion: fmsAdmin, region: tc.mode}, &seen)
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(h, fmsDenied)}
			if total, _, err := entry.fn(context.Background(), acct, adminRegion, st, testScanID); err != nil || total != 5 {
				t.Fatalf("fn(%s) = %d, %v; want 5, nil", adminRegion, total, err)
			}
			total, inserted, err := entry.fn(context.Background(), acct, region, st, testScanID)
			if errors.Is(err, errServiceDisabled) != tc.wantDisabled || !tc.wantDisabled && err != nil {
				t.Errorf("fn(%s) err = %v; want disabled sentinel %v", region, err, tc.wantDisabled)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("fn(%s) total, inserted = %d, %d; want 0, 0", region, total, inserted)
			}
			if got := warns(); len(got) != 0 {
				t.Errorf("warnings by op = %v; want none", got)
			}
			var got []string
			for _, c := range seen.distinct() {
				if op, ok := strings.CutSuffix(c, "@"+region); ok {
					got = append(got, op)
				}
			}
			slices.Sort(got)
			if !reflect.DeepEqual(got, tc.wantCalls) {
				t.Errorf("ops sent in %s = %v; want %v", region, got, tc.wantCalls)
			}
			for _, typ := range []string{TypeFMSPolicy, TypeFMSResourceSet, TypeFMSNotificationChannel, TypeFMSAppsList, TypeFMSProtocolsList} {
				for _, r := range rowsByNativeID(t, st, typ) {
					if sdkaws.ToString(r.Region) != adminRegion {
						t.Errorf("%s row %s in region %q; want only %s rows", typ, r.NativeID, sdkaws.ToString(r.Region), adminRegion)
					}
				}
			}
		})
	}
}

// GetNotificationChannel's expected states (no channel set, FMS not
// onboarded, not the administrator) store nothing silently; a real IAM denial
// warns once; any other error fails the scan. The other phases' rows are kept
// in every case.
func TestScanFMS_NotificationChannelErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string // "" for a nil error
	}{
		{"real deny", fmsDenied, 1, ""},
		{"no channel set", fmsGone, 0, ""},
		{"not onboarded", fmsNoDefaultAdmin, 0, ""},
		{"not the administrator", fmsAdminOnly, 0, ""},
		{"other error", apiErr("InternalErrorException", "boom"), 0, "InternalErrorException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			var calls fmsCalls
			h := fmsHandlers(&calls)
			h["GetNotificationChannel"] = func(any) (any, error) { return nil, tc.err }
			total, _, err := fmsScan(context.Background(), st, &calls, h)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := warns()["fms:GetNotificationChannel"]; got != tc.wantWarns {
				t.Errorf("fms:GetNotificationChannel warnings = %d; want %d", got, tc.wantWarns)
			}
			if total != fmsTestTotal-1 {
				t.Errorf("total = %d; want %d (no channel)", total, fmsTestTotal-1)
			}
			if rows := rowsByNativeID(t, st, TypeFMSNotificationChannel); len(rows) != 0 {
				t.Errorf("channel rows = %v; want none", slices.Sorted(maps.Keys(rows)))
			}
		})
	}
}

// A listed applications or protocols list with no ListArn cannot be named,
// so it is not stored and is warned about once per op.
func TestScanFMS_ListsWithoutARN(t *testing.T) {
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var calls fmsCalls
	h := fmsHandlers(&calls)
	h["ListAppsLists"] = func(in any) (any, error) {
		if in.(*fms.ListAppsListsInput).NextToken == nil {
			return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{
				{ListArn: sdkaws.String(fmsAppsARN("a1")), ListName: sdkaws.String("apps-1")},
				{ListName: sdkaws.String("arnless-apps")},
			}, NextToken: sdkaws.String("a-2")}, nil
		}
		return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: sdkaws.String(fmsAppsARN("a2"))}}}, nil
	}
	h["ListProtocolsLists"] = func(in any) (any, error) {
		if in.(*fms.ListProtocolsListsInput).NextToken == nil {
			return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{
				{ListArn: sdkaws.String(fmsProtoARN("l1")), ListName: sdkaws.String("proto-1")},
				{ListName: sdkaws.String("arnless-proto")},
			}, NextToken: sdkaws.String("l-2")}, nil
		}
		return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: sdkaws.String(fmsProtoARN("l2"))}}}, nil
	}
	total, inserted, err := fmsScan(context.Background(), st, &calls, h)
	if err != nil {
		t.Fatalf("scanFMS: %v", err)
	}
	if total != fmsTestTotal || inserted != fmsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, fmsTestTotal, fmsTestTotal)
	}
	got := warns()
	for _, op := range []string{"fms:ListAppsLists", "fms:ListProtocolsLists"} {
		if got[op] != 1 {
			t.Errorf("%s warnings = %d; want 1 for the ARN-less summary", op, got[op])
		}
	}
	for typ, want := range map[string][]string{
		TypeFMSAppsList:      {fmsAppsARN("a1"), fmsAppsARN("a2")},
		TypeFMSProtocolsList: {fmsProtoARN("l1"), fmsProtoARN("l2")},
	} {
		if ids := fmsStoredIDs(t, st, typ); !reflect.DeepEqual(ids, want) {
			t.Errorf("%s rows = %v; want %v (both pages, the ARN-less summary skipped)", typ, ids, want)
		}
	}
}
