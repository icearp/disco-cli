package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/fms"
	fmstypes "github.com/aws/aws-sdk-go-v2/service/fms/types"
	"github.com/icearp/disco-cli/store"
)

var fmsThrottled = apiErr("ThrottlingException", "slow down")

var fmsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: fms:List")

func fmsPolicyARN(id string) string {
	return fmsPolicyARNIn(testRegion, id)
}

func fmsPolicyARNIn(region, id string) string {
	return fmt.Sprintf("arn:aws:fms:%s:%s:policy/%s", region, testAccountID, id)
}

func fmsMember(uri string) fmstypes.Resource {
	return fmstypes.Resource{URI: sdkaws.String(uri), AccountId: sdkaws.String(testAccountID)}
}

// fmsPageCapped fails a call whose MaxResults is unset or above the model's
// cap, as the service would.
func fmsPageCapped(maxResults *int32) error {
	if maxResults == nil || *maxResults < 1 || *maxResults > fmsPageSize {
		return apiErr("InvalidInputException", "MaxResults out of range")
	}
	return nil
}

// fmsHandlers answers an FMS admin account with two policies, a summary with
// an ARN but no PolicyId, one with a PolicyId but no ARN (GetPolicy supplies
// it), and one with neither; and two resource sets plus one with no Id; each
// listing over two pages; set rs1 has three members over two pages
// (answered out of order), rs2 has none. The member calls go into calls.
func fmsHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	members := map[string][][]fmstypes.Resource{
		"rs1": {
			{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-b"), fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a")},
			{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-c")},
		},
		"rs2": {{}},
	}
	return map[string]func(in any) (any, error){
		"ListPolicies": func(in any) (any, error) {
			if err := fmsPageCapped(in.(*fms.ListPoliciesInput).MaxResults); err != nil {
				return nil, err
			}
			if in.(*fms.ListPoliciesInput).NextToken == nil {
				return &fms.ListPoliciesOutput{PolicyList: []fmstypes.PolicySummary{
					{PolicyId: sdkaws.String("p1"), PolicyArn: sdkaws.String(fmsPolicyARN("p1")), PolicyName: sdkaws.String("waf-all"), PolicyStatus: fmstypes.CustomerPolicyStatusActive},
					{PolicyId: sdkaws.String(""), PolicyArn: sdkaws.String(fmsPolicyARN("blank"))},
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
					{Id: sdkaws.String("rs1"), Name: sdkaws.String("prod-vpcs"), ResourceSetStatus: fmstypes.ResourceSetStatusActive},
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
			calls.add(id)
			pages, ok := members[id]
			if !ok {
				return nil, apiErr("ResourceNotFoundException", "no such set")
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
		"ListAppsLists":      func(any) (any, error) { return &fms.ListAppsListsOutput{}, nil },
		"ListProtocolsLists": func(any) (any, error) { return &fms.ListProtocolsListsOutput{}, nil },
	}
}

func fmsTestAccount(h map[string]func(in any) (any, error)) *account {
	return &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, fmsDenied)}
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

func TestScanFMS_StoresPolicyBodiesAndResourceSetMembers(t *testing.T) {
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var calls parentCalls
	total, inserted, err := scanFMS(context.Background(), fmsTestAccount(fmsHandlers(&calls)), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanFMS: %v", err)
	}
	if total != 7 || inserted != 7 {
		t.Errorf("total, inserted = %d, %d; want 7, 7 (4 policies, 2 resource sets, 1 channel)", total, inserted)
	}
	// fms:ListPolicies: one for the id-less summary stored from the listing,
	// one for the summary with neither id nor ARN; fms:ListResourceSets: one
	// for the Id-less set.
	if got, want := warns(), map[string]int{"fms:ListPolicies": 2, "fms:ListResourceSets": 1}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"rs1", "rs2"}) {
		t.Errorf("sets whose members were listed = %v; want [rs1 rs2]", got)
	}

	policies := rowsByNativeID(t, st, TypeFMSPolicy)
	if len(policies) != 4 {
		t.Fatalf("policy rows = %d; want 4 (only the summary with neither id nor ARN skipped)", len(policies))
	}
	if _, ok := policies[fmsPolicyARN("arnless")]; !ok {
		t.Errorf("policy rows %v; want the ARN-less summary under the ARN GetPolicy returned", slices.Collect(maps.Keys(policies)))
	}
	// The id-less summary is stored as listed: it has PolicyArn, which the
	// GetPolicy body lacks, and no ResourceSetIds.
	blank := fmsAttrKeys(t, policies[fmsPolicyARN("blank")])
	if _, arn := blank["PolicyArn"]; !arn {
		t.Errorf("id-less policy attributes %v; want its listed summary", blank)
	}
	if _, body := blank["ResourceSetIds"]; body {
		t.Errorf("id-less policy attributes %v; want no GetPolicy body", blank)
	}
	p1 := policies[fmsPolicyARN("p1")]
	var body fmstypes.Policy
	if err := json.Unmarshal([]byte(p1.AttributesJSON), &body); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if !reflect.DeepEqual(body.ResourceSetIds, []string{"rs1"}) || sdkaws.ToString(body.PolicyName) != "from-get-p1" {
		t.Errorf("policy attrs = %s; want the GetPolicy body", p1.AttributesJSON)
	}
	if sdkaws.ToString(p1.Name) != "waf-all" || sdkaws.ToString(p1.Status) != "ACTIVE" || sdkaws.ToString(p1.Region) != testRegion {
		t.Errorf("policy name, status, region = %q, %q, %q", sdkaws.ToString(p1.Name), sdkaws.ToString(p1.Status), sdkaws.ToString(p1.Region))
	}
	if p2 := policies[fmsPolicyARN("p2")]; p2.Status != nil {
		t.Errorf("p2 status = %q; want unset for an empty PolicyStatus", *p2.Status)
	}

	sets := rowsByNativeID(t, st, TypeFMSResourceSet)
	if len(sets) != 2 {
		t.Fatalf("resource set rows = %d; want 2 (the id-less summary skipped)", len(sets))
	}
	rs1 := sets[fmsResourceSetARN("aws", testRegion, testAccountID, "rs1")]
	want := []string{
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a",
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-b",
		"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-c",
	}
	if got := fmsStoredMembers(t, rs1); !reflect.DeepEqual(got, want) {
		t.Errorf("rs1 members = %v; want both pages, sorted: %v", got, want)
	}
	if sdkaws.ToString(rs1.Name) != "prod-vpcs" || sdkaws.ToString(rs1.Status) != "ACTIVE" {
		t.Errorf("rs1 name, status = %q, %q", sdkaws.ToString(rs1.Name), sdkaws.ToString(rs1.Status))
	}
	if got := fmsStoredMembers(t, sets[fmsResourceSetARN("aws", testRegion, testAccountID, "rs2")]); len(got) != 0 {
		t.Errorf("rs2 members = %v; want an empty list", got)
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

// A failed member read never stores the set with a list that looks complete:
// a set deleted after the listing is dropped silently, a denial or a transient
// failure keeps the set without a Resources key and warns once however many
// sets it hits, and any other error fails the phase while the other phases
// still store their rows.
func TestScanFMS_MemberReadFailure(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		fail      []string // set ids whose member read returns err
		wantErr   string   // error code the scan returns, "" for nil
		wantWarns int
		wantSets  []string
	}{
		{"set deleted after listing", apiErr("ResourceNotFoundException", "gone"), []string{"rs1"}, "", 0, []string{"rs2"}},
		{"denied", fmsDenied, []string{"rs1", "rs2"}, "", 1, []string{"rs1", "rs2"}},
		{"throttled", fmsThrottled, []string{"rs1"}, "", 1, []string{"rs1", "rs2"}},
		{"unmodelled failure", apiErr("InvalidOperationException", "no"), []string{"rs1"}, "InvalidOperationException", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "fms:ListResourceSetResources")
			var calls parentCalls
			h := fmsHandlers(&calls)
			list := h["ListResourceSetResources"]
			h["ListResourceSetResources"] = func(in any) (any, error) {
				if slices.Contains(tc.fail, sdkaws.ToString(in.(*fms.ListResourceSetResourcesInput).Identifier)) {
					return nil, tc.err
				}
				return list(in)
			}
			_, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && !isAPIErrorCode(err, tc.wantErr) {
				t.Fatalf("err = %v; want code %q", err, tc.wantErr)
			}
			if tc.wantErr != "" && classifyServiceError(err) != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError", classifyServiceError(err))
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			var got []string
			sets := rowsByNativeID(t, st, TypeFMSResourceSet)
			for _, id := range []string{"rs1", "rs2"} {
				r, ok := sets[fmsResourceSetARN("aws", testRegion, testAccountID, id)]
				if !ok {
					continue
				}
				got = append(got, id)
				keys := fmsAttrKeys(t, r)
				if _, has := keys["Resources"]; has == slices.Contains(tc.fail, id) {
					t.Errorf("%s has Resources key = %v; want it only when the member read succeeded", id, has)
				}
				if _, has := keys["ResourceSet"]; !has {
					t.Errorf("%s attributes %s lack the listed ResourceSet", id, r.AttributesJSON)
				}
			}
			if !reflect.DeepEqual(got, tc.wantSets) {
				t.Errorf("stored sets = %v; want %v", got, tc.wantSets)
			}
			if n := len(rowsByNativeID(t, st, TypeFMSNotificationChannel)); n != 1 {
				t.Errorf("notification channel rows = %d; want 1 from the later phase", n)
			}
		})
	}
}

// A member listing whose NextToken repeats stopped incomplete, so the set is
// not stored with the partial list.
func TestScanFMS_MemberRepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := fmsHandlers(&calls)
	h["ListResourceSetResources"] = func(any) (any, error) {
		return &fms.ListResourceSetResourcesOutput{Items: []fmstypes.Resource{fmsMember("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-a")}, NextToken: sdkaws.String("same")}, nil
	}
	_, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, TypeFMSResourceSet)); n != 0 {
		t.Errorf("resource set rows = %d; want 0", n)
	}
}

// A resource-set listing whose NextToken repeats stopped incomplete, so it
// fails rather than store a partial set list.
func TestScanFMS_ResourceSetRepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := fmsHandlers(&calls)
	h["ListResourceSets"] = func(any) (any, error) {
		return &fms.ListResourceSetsOutput{ResourceSets: []fmstypes.ResourceSetSummary{{Id: sdkaws.String("rs1")}}, NextToken: sdkaws.String("same")}, nil
	}
	_, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, TypeFMSResourceSet)); n != 0 {
		t.Errorf("resource set rows = %d; want 0", n)
	}
}

// GetPolicy: a policy deleted after the listing is dropped silently, a denial
// or a transient failure keeps each policy with its listed summary as
// attributes and warns once, and
// a response without a body or an unmodelled error fails the phase.
func TestScanFMS_GetPolicyFailure(t *testing.T) {
	unmodelled := apiErr("InvalidOperationException", "no")
	cases := []struct {
		name         string
		answer       func() (any, error)
		fail         []string // policy ids whose GetPolicy returns answer
		wantErr      error
		wantWarns    int
		wantPolicies []string
	}{
		{"policy deleted after listing", func() (any, error) { return nil, apiErr("ResourceNotFoundException", "gone") }, []string{"p2"}, nil, 0, []string{"p1"}},
		{"denied", func() (any, error) { return nil, fmsDenied }, []string{"p1", "p2"}, nil, 1, []string{"p1", "p2"}},
		{"throttled", func() (any, error) { return nil, fmsThrottled }, []string{"p2"}, nil, 1, []string{"p1", "p2"}},
		{"no policy body", func() (any, error) { return &fms.GetPolicyOutput{}, nil }, []string{"p2"}, errFMSPolicyMissing, 0, nil},
		{"unmodelled failure", func() (any, error) { return nil, unmodelled }, []string{"p2"}, unmodelled, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "fms:GetPolicy")
			var calls parentCalls
			h := fmsHandlers(&calls)
			get := h["GetPolicy"]
			h["GetPolicy"] = func(in any) (any, error) {
				if slices.Contains(tc.fail, sdkaws.ToString(in.(*fms.GetPolicyInput).PolicyId)) {
					return tc.answer()
				}
				return get(in)
			}
			_, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v; want %v", err, tc.wantErr)
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
	var calls parentCalls
	h := fmsHandlers(&calls)
	h["ListPolicies"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListResourceSetResources"] = func(any) (any, error) { return nil, apiErr("InvalidOperationException", "no") }

	total, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidOperationException") {
		t.Errorf("err = %v; want the resource-set phase's InvalidOperationException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 (the notification channel)", total)
	}
}

// A summary without PolicyArn whose GetPolicy cannot supply one is not stored,
// with one warning; the other policies are stored.
func TestScanFMS_ARNlessPolicyWithoutARNFromGetPolicy(t *testing.T) {
	cases := []struct {
		name   string
		answer func(in any) (any, error)
	}{
		{"GetPolicy denied", func(any) (any, error) { return nil, fmsDenied }},
		{"GetPolicy without PolicyArn", func(in any) (any, error) {
			return &fms.GetPolicyOutput{Policy: &fmstypes.Policy{PolicyId: in.(*fms.GetPolicyInput).PolicyId}}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			var calls parentCalls
			h := fmsHandlers(&calls)
			// Leave out the summary with neither id nor ARN, so the only
			// unnamed policy is the one GetPolicy could not name.
			list := h["ListPolicies"]
			h["ListPolicies"] = func(in any) (any, error) {
				out, err := list(in)
				if err != nil {
					return nil, err
				}
				page := out.(*fms.ListPoliciesOutput)
				page.PolicyList = slices.DeleteFunc(page.PolicyList, func(p fmstypes.PolicySummary) bool {
					return sdkaws.ToString(p.PolicyName) == "nameless"
				})
				return page, nil
			}
			get := h["GetPolicy"]
			h["GetPolicy"] = func(in any) (any, error) {
				if sdkaws.ToString(in.(*fms.GetPolicyInput).PolicyId) == "arnless" {
					return tc.answer(in)
				}
				return get(in)
			}
			if _, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID); err != nil {
				t.Fatalf("scanFMS: %v", err)
			}
			policies := rowsByNativeID(t, st, TypeFMSPolicy)
			if _, ok := policies[""]; ok || len(policies) != 3 {
				t.Errorf("policy rows %v; want p1, p2 and blank only", slices.Collect(maps.Keys(policies)))
			}
			if got := warns()["fms:ListPolicies"]; got != 2 {
				t.Errorf("fms:ListPolicies warnings = %d; want 2 (the id-less summary, and the summary GetPolicy could not name)", got)
			}
		})
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
			var calls parentCalls
			h := fmsHandlers(&calls)
			h[tc.op] = func(in any) (any, error) {
				if k := tc.key(in); k == "p1" || k == "rs1" {
					return nil, fmsThrottled
				}
				return nil, apiErr("InvalidOperationException", "no")
			}
			_, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InvalidOperationException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v (outcome %v); want the hard InvalidOperationException as outcomeError", err, classifyServiceError(err))
			}
		})
	}
}

// A disabled answer after an earlier phase failed hard or stored rows is a
// warning: returning the sentinel would report the service disabled, hiding
// the hard error or the stored rows' counts.
func TestScanFMS_DisabledAfterEarlierPhase(t *testing.T) {
	notOnboarded := func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "No default admin could be found")
	}
	cases := []struct {
		name         string
		listPolicies func(any) (any, error) // nil keeps the fixture's
		wantCode     string                 // "" for a nil error
		wantTotal    int
	}{
		{"after a hard error", func(any) (any, error) { return nil, apiErr("InternalErrorException", "boom") }, "InternalErrorException", 1},
		{"after stored rows", nil, "", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "fms:ListResourceSets")
			var calls parentCalls
			h := fmsHandlers(&calls)
			if tc.listPolicies != nil {
				h["ListPolicies"] = tc.listPolicies
			}
			h["ListResourceSets"] = notOnboarded
			total, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
			if errors.Is(err, errServiceDisabled) {
				t.Fatalf("err = %v; want no disabled sentinel", err)
			}
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && (!isAPIErrorCode(err, tc.wantCode) || classifyServiceError(err) != outcomeError) {
				t.Errorf("err = %v; want code %q as outcomeError", err, tc.wantCode)
			}
			if *warns != 1 {
				t.Errorf("fms:ListResourceSets warnings = %d; want 1 for the disabled answer", *warns)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d (later phases still run)", total, tc.wantTotal)
			}
		})
	}
}

// An account FMS was never onboarded for reports the service disabled and
// stores nothing.
func TestScanFMS_NotEnabled(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := fmsHandlers(&calls)
	h["ListPolicies"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "No default admin could be found")
	}
	total, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
	if !errors.Is(err, errServiceDisabled) || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, errServiceDisabled", total, err)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("member listings after a disabled service = %v; want none", got)
	}
}

// fmsRegionalOps are the ops scanFMS sends in a region where the account is
// the Firewall Manager administrator.
var fmsRegionalOps = []string{
	"GetNotificationChannel", "GetPolicy", "ListAppsLists", "ListPolicies",
	"ListProtocolsLists", "ListResourceSetResources", "ListResourceSets",
}

const (
	fmsAdmin        = "admin"
	fmsMemberAcct   = "member"
	fmsNotOnboarded = "not-onboarded"
)

// fmsRegionalHandlers answers each region by its mode: an administrator
// region holds one policy using resource set rs1, one applications list, one
// protocols list and a notification channel, every ARN in that region; a
// member region answers the admin-only denial; a not-onboarded region answers
// that no default admin exists. Each call goes into seen as op@region.
func fmsRegionalHandlers(mode map[string]string, seen *parentCalls) map[string]func(context.Context, any) (any, error) {
	h := map[string]func(context.Context, any) (any, error){}
	for _, op := range fmsRegionalOps {
		h[op] = func(ctx context.Context, in any) (any, error) {
			region := awsmw.GetRegion(ctx)
			seen.add(op + "@" + region)
			switch mode[region] {
			case fmsMemberAcct:
				return nil, apiErr("AccessDeniedException", "This operation is only available to AWS Firewall Manager Administrators")
			case fmsNotOnboarded:
				return nil, apiErr("AccessDeniedException", "No default admin could be found")
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
// warns once; any other error fails the scan. The rows of the phases that run
// before it (policies, resource sets) are kept in every case; the fixture
// lists nothing for the phases after it.
func TestScanFMS_NotificationChannelErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string // "" for a nil error
	}{
		{"real deny", fmsDenied, 1, ""},
		{"no channel set", apiErr("ResourceNotFoundException", "no channel"), 0, ""},
		{"not onboarded", apiErr("AccessDeniedException", "No default admin could be found"), 0, ""},
		{"not the administrator", apiErr("AccessDeniedException", "This operation is only available to AWS Firewall Manager Administrators"), 0, ""},
		{"other error", apiErr("InternalErrorException", "boom"), 0, "InternalErrorException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := fmsWarningsByOp(st)
			var calls parentCalls
			h := fmsHandlers(&calls)
			h["GetNotificationChannel"] = func(any) (any, error) { return nil, tc.err }
			total, _, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := warns()["fms:GetNotificationChannel"]; got != tc.wantWarns {
				t.Errorf("fms:GetNotificationChannel warnings = %d; want %d", got, tc.wantWarns)
			}
			if total != 6 {
				t.Errorf("total = %d; want 6 (4 policies, 2 resource sets, no channel)", total)
			}
			if rows := rowsByNativeID(t, st, TypeFMSNotificationChannel); len(rows) != 0 {
				t.Errorf("channel rows = %v; want none", slices.Sorted(maps.Keys(rows)))
			}
		})
	}
}

// Applications and protocols lists are read over every page; a summary with
// no ListArn cannot be named, so it is not stored and is warned about once
// per op.
func TestScanFMS_ListsWithoutARN(t *testing.T) {
	appsARN := func(id string) string {
		return "arn:aws:fms:" + testRegion + ":" + testAccountID + ":applications-list/" + id
	}
	protoARN := func(id string) string {
		return "arn:aws:fms:" + testRegion + ":" + testAccountID + ":protocols-list/" + id
	}
	st := newTestStore(t)
	warns := fmsWarningsByOp(st)
	var calls parentCalls
	h := fmsHandlers(&calls)
	h["ListAppsLists"] = func(in any) (any, error) {
		if in.(*fms.ListAppsListsInput).NextToken == nil {
			return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{
				{ListArn: sdkaws.String(appsARN("a1")), ListName: sdkaws.String("apps-1")},
				{ListName: sdkaws.String("arnless-apps")},
			}, NextToken: sdkaws.String("a-2")}, nil
		}
		return &fms.ListAppsListsOutput{AppsLists: []fmstypes.AppsListDataSummary{{ListArn: sdkaws.String(appsARN("a2"))}}}, nil
	}
	h["ListProtocolsLists"] = func(in any) (any, error) {
		if in.(*fms.ListProtocolsListsInput).NextToken == nil {
			return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{
				{ListArn: sdkaws.String(protoARN("l1")), ListName: sdkaws.String("proto-1")},
				{ListName: sdkaws.String("arnless-proto")},
			}, NextToken: sdkaws.String("l-2")}, nil
		}
		return &fms.ListProtocolsListsOutput{ProtocolsLists: []fmstypes.ProtocolsListDataSummary{{ListArn: sdkaws.String(protoARN("l2"))}}}, nil
	}
	total, inserted, err := scanFMS(context.Background(), fmsTestAccount(h), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanFMS: %v", err)
	}
	if total != 11 || inserted != 11 {
		t.Errorf("total, inserted = %d, %d; want 11, 11 (7 from the fixture, 2 apps lists, 2 protocols lists)", total, inserted)
	}
	got := warns()
	for _, op := range []string{"fms:ListAppsLists", "fms:ListProtocolsLists"} {
		if got[op] != 1 {
			t.Errorf("%s warnings = %d; want 1 for the ARN-less summary", op, got[op])
		}
	}
	for typ, want := range map[string][]string{
		TypeFMSAppsList:      {appsARN("a1"), appsARN("a2")},
		TypeFMSProtocolsList: {protoARN("l1"), protoARN("l2")},
	} {
		rows := rowsByNativeID(t, st, typ)
		if ids := slices.Sorted(maps.Keys(rows)); !reflect.DeepEqual(ids, want) {
			t.Errorf("%s rows = %v; want %v (both pages, the ARN-less summary skipped)", typ, ids, want)
		}
	}
	if a1 := rowsByNativeID(t, st, TypeFMSAppsList)[appsARN("a1")]; sdkaws.ToString(a1.Name) != "apps-1" || sdkaws.ToString(a1.Region) != testRegion {
		t.Errorf("a1 name, region = %q, %q; want apps-1, %s", sdkaws.ToString(a1.Name), sdkaws.ToString(a1.Region), testRegion)
	}
}
