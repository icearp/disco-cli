package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture's two instances: the first holds every child, the second none
// unless inst2Set gives it ssoTagInst2Set. ssoTagOrphanStore is the Identity
// Store of the instance listed without an ARN (noARN), which holds one user.
const (
	ssoTagInst1       = "ssoins-1111"
	ssoTagInst2       = "ssoins-2222"
	ssoTagInst2Set    = "ps-inst2"
	ssoTagOrphanStore = "d-orphan"
)

var (
	ssoTagDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: sso:ListTagsForResource")
	ssoTagGone     = apiErr("ResourceNotFoundException", "gone")
	ssoTagBad      = apiErr("ValidationException", "bad")
	ssoTagThrottle = apiErr("ThrottlingException", "Rate exceeded")
	ssoTagConflict = apiErr("ConflictException", "conflict")
)

// ssoTagKind is one tagged type: its listing op, its ARN's resource kind,
// and the ids of its tagged and untagged rows, listed on pages 1 and 2.
type ssoTagKind struct {
	typ, listOp, kind, tagged, bare string
}

func ssoTagKinds() []ssoTagKind {
	return []ssoTagKind{
		{TypeSSOInstance, "ListInstances", "instance", ssoTagInst1, ssoTagInst2},
		{TypeSSOPermissionSet, "ListPermissionSets", "permissionSet", "ps-tagged", "ps-bare"},
		{TypeSSOApplication, "ListApplications", "application", "apl-tagged", "apl-bare"},
		{TypeSSOTrustedTokenIssuer, "ListTrustedTokenIssuers", "trustedTokenIssuer", "tti-tagged", "tti-bare"},
	}
}

func ssoTagKindOf(t *testing.T, typ string) ssoTagKind {
	t.Helper()
	for _, k := range ssoTagKinds() {
		if k.typ == typ {
			return k
		}
	}
	t.Fatalf("%s is no tagged SSO type", typ)
	return ssoTagKind{}
}

// ssoTagScan serves one scanSSOAdmin run: every listing in two pages, the
// tags of each tagged row, and the errors a case sets. It records each call
// as "<Op> <key>", the key being the parent or resource the call names.
type ssoTagScan struct {
	partition, region, owner string
	appAccount               string           // the account in application and issuer ARNs
	tagErr                   map[string]error // by ResourceArn
	describeErr              map[string]error // by PermissionSetArn
	describeEmpty            map[string]bool  // answered with no PermissionSet
	failAfterFirst           map[string]error // by listing op: page 2 fails
	repeatToken              map[string]bool  // by listing op: both pages return one token
	noInstances, noARN       bool             // ListInstances lists nothing; adds an instance without an ARN
	inst2Set                 bool             // the second instance has one permission set
	inst1SetsErr             error            // the first instance's ListPermissionSets page 1 fails
	opErr                    map[string]error // by op: every call fails

	mu    sync.Mutex
	calls []string
}

func newSSOTagScan() *ssoTagScan { return newSSOTagScanIn("aws", testRegion) }

func newSSOTagScanIn(partition, region string) *ssoTagScan {
	return &ssoTagScan{
		partition: partition, region: region, owner: testAccountID, appAccount: testAccountID,
		tagErr: map[string]error{}, describeErr: map[string]error{}, describeEmpty: map[string]bool{},
		failAfterFirst: map[string]error{}, repeatToken: map[string]bool{}, opErr: map[string]error{},
	}
}

func (s *ssoTagScan) arn(k ssoTagKind, id string) string {
	switch k.kind {
	case "instance":
		return "arn:" + s.partition + ":sso:::instance/" + id
	case "permissionSet":
		return "arn:" + s.partition + ":sso:::permissionSet/" + ssoTagInst1 + "/" + id
	default:
		return "arn:" + s.partition + ":sso::" + s.appAccount + ":" + k.kind + "/" + ssoTagInst1 + "/" + id
	}
}

// inst2SetARN is the ARN of the second instance's permission set (inst2Set).
func (s *ssoTagScan) inst2SetARN() string {
	return "arn:" + s.partition + ":sso:::permissionSet/" + ssoTagInst2 + "/" + ssoTagInst2Set
}

func (s *ssoTagScan) instanceARN(id string) string {
	return s.arn(ssoTagKind{kind: "instance"}, id)
}

func (s *ssoTagScan) record(op, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, op+" "+key)
}

func (s *ssoTagScan) called(op, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.calls, op+" "+key)
}

// ops is the ops called, in call order, each once.
func (s *ssoTagScan) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ops []string
	for _, c := range s.calls {
		op, _, _ := strings.Cut(c, " ")
		if !slices.Contains(ops, op) {
			ops = append(ops, op)
		}
	}
	return ops
}

// tagReads is every ListTagsForResource call, "<ResourceArn> <InstanceArn>",
// sorted.
func (s *ssoTagScan) tagReads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var got []string
	for _, c := range s.calls {
		if read, ok := strings.CutPrefix(c, "ListTagsForResource "); ok {
			got = append(got, read)
		}
	}
	slices.Sort(got)
	return got
}

// allTagReads is tagReads when every row is read: each row's ARN with its
// instance's, the first instance's twice (its tags come in two pages).
func (s *ssoTagScan) allTagReads() []string {
	var want []string
	for _, k := range ssoTagKinds() {
		for _, id := range []string{k.tagged, k.bare} {
			inst := s.instanceARN(ssoTagInst1)
			if id == ssoTagInst2 {
				inst = s.instanceARN(ssoTagInst2)
			}
			want = append(want, s.arn(k, id)+" "+inst)
		}
	}
	want = append(want, s.instanceARN(ssoTagInst1)+" "+s.instanceARN(ssoTagInst1))
	slices.Sort(want)
	return want
}

// wantTags is the tags of k's tagged row: the instance's come from two pages.
func ssoWantTags(k ssoTagKind) map[string]string {
	if k.typ == TypeSSOInstance {
		return map[string]string{"type": k.typ, "team": "idc"}
	}
	return map[string]string{"type": k.typ}
}

// ssoTagListPage serves page token of op's two pages, or the failure a case
// set for op.
func ssoTagListPage[T any](s *ssoTagScan, op string, token *string, pages [][]T) ([]T, *string, error) {
	if s.repeatToken[op] {
		i, again := 0, "again"
		if token != nil {
			i = 1
		}
		return pages[i], &again, nil
	}
	if err := s.failAfterFirst[op]; err != nil {
		return stubPage(pages[:1], token, err)
	}
	return stubPage(pages, token, nil)
}

// ssoTagInputErr rejects a listing call that does not ask for full pages or
// names an instance other than want ("" for a call that names none).
func ssoTagInputErr(op string, maxResults *int32, instanceARN *string, want string) error {
	if maxResults == nil || *maxResults != 100 {
		return apiErr("ValidationException", fmt.Sprintf("%s MaxResults=%v, want 100", op, maxResults))
	}
	if sv(instanceARN) != want {
		return apiErr("ValidationException", fmt.Sprintf("%s InstanceArn=%q, want %q", op, sv(instanceARN), want))
	}
	return nil
}

func (s *ssoTagScan) handlers() map[string]func(any) (any, error) {
	inst1, inst2 := s.instanceARN(ssoTagInst1), s.instanceARN(ssoTagInst2)
	kinds := ssoTagKinds()
	ps, app, tti := kinds[1], kinds[2], kinds[3]
	// underInst1 lists pages for the first instance and nothing for the second.
	underInst1 := func(op string, in *string) bool {
		s.record(op, sv(in))
		return sv(in) == inst1
	}
	empty := func(op string, key func(any) string, out func() any) func(any) (any, error) {
		return func(in any) (any, error) {
			s.record(op, key(in))
			return out(), nil
		}
	}
	appKey := func(in any) string { return sv(bacField[*string](in, "ApplicationArn")) }
	psKey := func(in any) string { return sv(bacField[*string](in, "PermissionSetArn")) }
	instKey := func(in any) string { return sv(bacField[*string](in, "InstanceArn")) }
	return map[string]func(any) (any, error){
		"ListInstances": func(in any) (any, error) {
			req := in.(*ssoadmin.ListInstancesInput)
			s.record("ListInstances", sv(req.NextToken))
			if err := ssoTagInputErr("ListInstances", req.MaxResults, nil, ""); err != nil {
				return nil, err
			}
			if s.noInstances {
				return &ssoadmin.ListInstancesOutput{}, nil
			}
			instance := func(arn *string, name string) ssotypes.InstanceMetadata {
				return ssotypes.InstanceMetadata{
					InstanceArn: arn, Name: sdkaws.String(name), OwnerAccountId: sdkaws.String(s.owner),
					Status: ssotypes.InstanceStatusActive, CreatedDate: &ssoAdded,
				}
			}
			first := []ssotypes.InstanceMetadata{instance(&inst1, "name-"+ssoTagInst1)}
			if s.noARN {
				orphan := instance(nil, "orphan")
				orphan.IdentityStoreId = sdkaws.String(ssoTagOrphanStore)
				first = append(first, orphan)
			}
			items, next, err := ssoTagListPage(s, "ListInstances", req.NextToken, [][]ssotypes.InstanceMetadata{first, {instance(&inst2, "name-"+ssoTagInst2)}})
			return &ssoadmin.ListInstancesOutput{Instances: items, NextToken: next}, err
		},
		"ListPermissionSets": func(in any) (any, error) {
			req := in.(*ssoadmin.ListPermissionSetsInput)
			if !underInst1("ListPermissionSets", req.InstanceArn) {
				if s.inst2Set && sv(req.InstanceArn) == inst2 {
					return &ssoadmin.ListPermissionSetsOutput{PermissionSets: []string{s.inst2SetARN()}}, nil
				}
				return &ssoadmin.ListPermissionSetsOutput{}, nil
			}
			if err := ssoTagInputErr("ListPermissionSets", req.MaxResults, req.InstanceArn, inst1); err != nil {
				return nil, err
			}
			if s.inst1SetsErr != nil && req.NextToken == nil {
				return nil, s.inst1SetsErr
			}
			items, next, err := ssoTagListPage(s, "ListPermissionSets", req.NextToken, [][]string{{s.arn(ps, ps.tagged)}, {s.arn(ps, ps.bare)}})
			return &ssoadmin.ListPermissionSetsOutput{PermissionSets: items, NextToken: next}, err
		},
		"DescribePermissionSet": func(in any) (any, error) {
			req := in.(*ssoadmin.DescribePermissionSetInput)
			arn := sv(req.PermissionSetArn)
			s.record("DescribePermissionSet", arn)
			want := inst1
			if arn == s.inst2SetARN() {
				want = inst2
			}
			if sv(req.InstanceArn) != want {
				return nil, apiErr("ValidationException", "DescribePermissionSet InstanceArn="+sv(req.InstanceArn))
			}
			if err := s.describeErr[arn]; err != nil {
				return nil, err
			}
			if s.describeEmpty[arn] {
				return &ssoadmin.DescribePermissionSetOutput{}, nil
			}
			name := "name-" + arn[strings.LastIndexByte(arn, '/')+1:]
			return &ssoadmin.DescribePermissionSetOutput{PermissionSet: &ssotypes.PermissionSet{
				PermissionSetArn: &arn, Name: &name, CreatedDate: &ssoAdded,
			}}, nil
		},
		"ListApplications": func(in any) (any, error) {
			req := in.(*ssoadmin.ListApplicationsInput)
			if !underInst1("ListApplications", req.InstanceArn) {
				return &ssoadmin.ListApplicationsOutput{}, nil
			}
			if err := ssoTagInputErr("ListApplications", req.MaxResults, req.InstanceArn, inst1); err != nil {
				return nil, err
			}
			element := func(id string) ssotypes.Application {
				return ssotypes.Application{
					ApplicationArn: sdkaws.String(s.arn(app, id)), Name: sdkaws.String("name-" + id), InstanceArn: &inst1,
					ApplicationAccount: sdkaws.String(s.appAccount),
					Status:             ssotypes.ApplicationStatusEnabled, CreatedDate: &ssoAdded,
				}
			}
			items, next, err := ssoTagListPage(s, "ListApplications", req.NextToken, [][]ssotypes.Application{{element(app.tagged)}, {element(app.bare)}})
			return &ssoadmin.ListApplicationsOutput{Applications: items, NextToken: next}, err
		},
		"ListTrustedTokenIssuers": func(in any) (any, error) {
			req := in.(*ssoadmin.ListTrustedTokenIssuersInput)
			if !underInst1("ListTrustedTokenIssuers", req.InstanceArn) {
				return &ssoadmin.ListTrustedTokenIssuersOutput{}, nil
			}
			if err := ssoTagInputErr("ListTrustedTokenIssuers", req.MaxResults, req.InstanceArn, inst1); err != nil {
				return nil, err
			}
			element := func(id string) ssotypes.TrustedTokenIssuerMetadata {
				return ssotypes.TrustedTokenIssuerMetadata{
					TrustedTokenIssuerArn: sdkaws.String(s.arn(tti, id)), Name: sdkaws.String("name-" + id),
					TrustedTokenIssuerType: ssotypes.TrustedTokenIssuerTypeOidcJwt,
				}
			}
			items, next, err := ssoTagListPage(s, "ListTrustedTokenIssuers", req.NextToken, [][]ssotypes.TrustedTokenIssuerMetadata{{element(tti.tagged)}, {element(tti.bare)}})
			return &ssoadmin.ListTrustedTokenIssuersOutput{TrustedTokenIssuers: items, NextToken: next}, err
		},
		"ListTagsForResource": func(in any) (any, error) {
			req := in.(*ssoadmin.ListTagsForResourceInput)
			arn := sv(req.ResourceArn)
			s.record("ListTagsForResource", arn+" "+sv(req.InstanceArn))
			if err := s.tagErr[arn]; err != nil {
				return nil, err
			}
			var pages [][]ssotypes.Tag
			for _, k := range ssoTagKinds() {
				if arn == s.arn(k, k.tagged) {
					pages = [][]ssotypes.Tag{{{Key: sdkaws.String("type"), Value: sdkaws.String(k.typ)}}}
				}
			}
			if arn == inst1 {
				pages = append(pages, []ssotypes.Tag{{Key: sdkaws.String("team"), Value: sdkaws.String("idc")}})
			}
			items, next, err := stubPage(pages, req.NextToken, nil)
			return &ssoadmin.ListTagsForResourceOutput{Tags: items, NextToken: next}, err
		},
		"ListAccountsForProvisionedPermissionSet": empty("ListAccountsForProvisionedPermissionSet", psKey, func() any { return &ssoadmin.ListAccountsForProvisionedPermissionSetOutput{} }),
		"ListApplicationProviders":                empty("ListApplicationProviders", func(any) string { return "" }, func() any { return &ssoadmin.ListApplicationProvidersOutput{} }),
		"ListApplicationAssignments":              empty("ListApplicationAssignments", appKey, func() any { return &ssoadmin.ListApplicationAssignmentsOutput{} }),
		"DescribeInstanceAccessControlAttributeConfiguration": empty("DescribeInstanceAccessControlAttributeConfiguration", instKey, func() any {
			return &ssoadmin.DescribeInstanceAccessControlAttributeConfigurationOutput{}
		}),
		"ListRegions":                          empty("ListRegions", instKey, func() any { return &ssoadmin.ListRegionsOutput{} }),
		"ListApplicationAccessScopes":          empty("ListApplicationAccessScopes", appKey, func() any { return &ssoadmin.ListApplicationAccessScopesOutput{} }),
		"ListApplicationAuthenticationMethods": empty("ListApplicationAuthenticationMethods", appKey, func() any { return &ssoadmin.ListApplicationAuthenticationMethodsOutput{} }),
		"ListApplicationGrants":                empty("ListApplicationGrants", appKey, func() any { return &ssoadmin.ListApplicationGrantsOutput{} }),
		"ListManagedPoliciesInPermissionSet":   empty("ListManagedPoliciesInPermissionSet", psKey, func() any { return &ssoadmin.ListManagedPoliciesInPermissionSetOutput{} }),
		"ListCustomerManagedPolicyReferencesInPermissionSet": empty("ListCustomerManagedPolicyReferencesInPermissionSet", psKey, func() any {
			return &ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{}
		}),
		"ListUsers": func(in any) (any, error) {
			store := sv(in.(*identitystore.ListUsersInput).IdentityStoreId)
			s.record("ListUsers", store)
			if store != ssoTagOrphanStore {
				return nil, apiErr("ValidationException", "ListUsers IdentityStoreId="+store)
			}
			return &identitystore.ListUsersOutput{Users: []istypes.User{{UserId: sdkaws.String("u-1"), UserName: sdkaws.String("alice")}}}, nil
		},
		"ListGroups":           func(any) (any, error) { return &identitystore.ListGroupsOutput{}, nil },
		"ListGroupMemberships": func(any) (any, error) { return &identitystore.ListGroupMembershipsOutput{}, nil },
	}
}

func (s *ssoTagScan) scan(st *store.Store) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = s.partition
	h := s.handlers()
	for op, err := range s.opErr {
		h[op] = func(any) (any, error) {
			s.record(op, "")
			return nil, err
		}
	}
	acct.cfg = opStubConfig(h, ssoTagDenied)
	return scanSSOAdmin(context.Background(), acct, s.region, st, testScanID)
}

// ssoTagRowCount is every row the fixture stores: two of each tagged type.
const ssoTagRowCount = 2 * 4

// Every instance, permission set, application and trusted token issuer
// stored carries its tags, read with ListTagsForResource on its ARN and its
// instance's ARN; a resource without tags has nil tags. Each listing sends
// MaxResults 100 (the handlers reject anything else). A rescan inserts
// nothing new.
func TestScanSSOAdmin_TagsEveryTaggableType(t *testing.T) {
	t.Parallel()
	s := newSSOTagScan()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, inserted, err := s.scan(st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ssoTagRowCount || inserted != ssoTagRowCount {
		t.Errorf("total=%d inserted=%d; want %d each", total, inserted, ssoTagRowCount)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if got, want := s.tagReads(), s.allTagReads(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource calls = %v\nwant %v", got, want)
	}
	for _, k := range ssoTagKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		assertTagsMap(t, rows[s.arn(k, k.tagged)].TagsJSON, ssoWantTags(k))
		if got := rows[s.arn(k, k.bare)].TagsJSON; got != nil {
			t.Errorf("%s untagged TagsJSON = %s; want nil", k.typ, *got)
		}
		for _, id := range []string{k.tagged, k.bare} {
			r := rows[s.arn(k, id)]
			if sv(r.Name) != "name-"+id || r.Status != nil || r.CreatedAt != nil || sv(r.Region) != testRegion {
				t.Errorf("%s %s Name=%q Status=%v CreatedAt=%v Region=%q; want name-%s, no status, no created-at, %s",
					k.typ, id, sv(r.Name), r.Status, r.CreatedAt, sv(r.Region), id, testRegion)
			}
		}
	}
	assertSSOAttr(t, rowsByNativeID(t, st, TypeSSOInstance)[s.instanceARN(ssoTagInst1)], "OwnerAccountId", testAccountID)
	assertSSOAttr(t, rowsByNativeID(t, st, TypeSSOApplication)[s.arn(ssoTagKindOf(t, TypeSSOApplication), "apl-tagged")], "ApplicationArn",
		s.arn(ssoTagKindOf(t, TypeSSOApplication), "apl-tagged"))
	assertSSOAttr(t, rowsByNativeID(t, st, TypeSSOTrustedTokenIssuer)[s.arn(ssoTagKindOf(t, TypeSSOTrustedTokenIssuer), "tti-bare")], "InstanceArn",
		s.instanceARN(ssoTagInst1))
	psRow := rowsByNativeID(t, st, TypeSSOPermissionSet)[s.arn(ssoTagKindOf(t, TypeSSOPermissionSet), "ps-tagged")]
	var described ssoadmin.DescribePermissionSetOutput
	if err := json.Unmarshal([]byte(psRow.AttributesJSON), &described); err != nil || described.PermissionSet == nil || sv(described.PermissionSet.Name) != "name-ps-tagged" {
		t.Errorf("permission set attrs = %s (%v); want the DescribePermissionSet body", psRow.AttributesJSON, err)
	}

	total, inserted, err = s.scan(st)
	if err != nil || total != ssoTagRowCount || inserted != 0 {
		t.Errorf("rescan total=%d inserted=%d err=%v; want %d, 0, nil", total, inserted, err, ssoTagRowCount)
	}
}

func assertSSOAttr(t *testing.T, r store.Resource, key, want string) {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %q: %v", r.NativeID, r.AttributesJSON, err)
	}
	if got, _ := attrs[key].(string); got != want {
		t.Errorf("%s attrs[%s] = %q; want %q", r.NativeID, key, got, want)
	}
}

// Denied tag reads keep their rows untagged and are one warning for the
// scan, although they span three listings.
func TestScanSSOAdmin_TagDenialsWarnOnce(t *testing.T) {
	t.Parallel()
	s := newSSOTagScan()
	denied := []string{
		s.arn(ssoTagKindOf(t, TypeSSOPermissionSet), "ps-tagged"),
		s.arn(ssoTagKindOf(t, TypeSSOApplication), "apl-tagged"),
		s.arn(ssoTagKindOf(t, TypeSSOTrustedTokenIssuer), "tti-bare"),
	}
	for _, arn := range denied {
		s.tagErr[arn] = ssoTagDenied
	}
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, _, err := s.scan(st)
	if err != nil || total != ssoTagRowCount {
		t.Fatalf("total=%d err=%v; want %d, nil", total, err, ssoTagRowCount)
	}
	w := warns()
	if len(w) != 1 || w[0].Service != "ssoadmin:ListTagsForResource" || w[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("warnings = %v; want one under ssoadmin:ListTagsForResource, Scope %s/%s", w, testAccountID, testRegion)
	}
	for _, k := range ssoTagKinds() {
		for _, id := range []string{k.tagged, k.bare} {
			arn := s.arn(k, id)
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Errorf("%s not stored; want it kept", arn)
			}
			if slices.Contains(denied, arn) && r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", arn, *r.TagsJSON)
			}
		}
	}
}

// A resource the tag read finds deleted is dropped silently, and nothing is
// listed under it: an instance's children, an application's assignments and
// child listings, a permission set's policies.
func TestScanSSOAdmin_TagNotFoundDropsRow(t *testing.T) {
	t.Parallel()
	for _, k := range ssoTagKinds() {
		t.Run(k.typ, func(t *testing.T) {
			s := newSSOTagScan()
			gone := s.arn(k, k.tagged)
			s.tagErr[gone] = ssoTagGone
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, _, err := s.scan(st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[gone]; ok {
				t.Errorf("%s stored; want it dropped", gone)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[s.arn(k, k.bare)]; !ok {
				t.Errorf("%s not stored; want it kept", s.arn(k, k.bare))
			}
			wantTotal := ssoTagRowCount - 1
			var under []string // the calls that must not be made for gone
			switch k.typ {
			case TypeSSOInstance:
				wantTotal = 1 // the second instance, which has no children
				under = []string{"ListPermissionSets", "ListApplications", "ListTrustedTokenIssuers", "ListRegions", "DescribeInstanceAccessControlAttributeConfiguration"}
			case TypeSSOApplication:
				under = []string{"ListApplicationAssignments", "ListApplicationAccessScopes", "ListApplicationAuthenticationMethods", "ListApplicationGrants"}
			case TypeSSOPermissionSet:
				under = []string{"ListManagedPoliciesInPermissionSet", "ListCustomerManagedPolicyReferencesInPermissionSet"}
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
			for _, op := range under {
				if s.called(op, gone) {
					t.Errorf("%s called for %s; want nothing listed under a dropped row", op, gone)
				}
			}
		})
	}
}

// A tag read's other failure keeps its row untagged; a lone transient one
// leaves the scan partial, beside a hard error it is demoted to one warning.
func TestScanSSOAdmin_TagReadErrors(t *testing.T) {
	t.Parallel()
	app, tti := ssoTagKindOf(t, TypeSSOApplication), ssoTagKindOf(t, TypeSSOTrustedTokenIssuer)
	for _, tc := range []struct {
		name     string
		set      func(s *ssoTagScan)
		outcome  serviceOutcome
		code     string
		warnings int
	}{
		{"other error", func(s *ssoTagScan) { s.tagErr[s.arn(app, app.tagged)] = ssoTagBad }, outcomeError, "ValidationException", 0},
		{"transient", func(s *ssoTagScan) { s.tagErr[s.arn(app, app.tagged)] = ssoTagThrottle }, outcomeTransient, "ThrottlingException", 0},
		{"transient beside a hard tag error", func(s *ssoTagScan) {
			s.tagErr[s.arn(app, app.tagged)] = ssoTagThrottle
			s.tagErr[s.arn(tti, tti.tagged)] = ssoTagBad
		}, outcomeError, "ValidationException", 1},
		{"transient beside a failed listing", func(s *ssoTagScan) {
			s.tagErr[s.arn(app, app.tagged)] = ssoTagThrottle
			s.failAfterFirst["ListTrustedTokenIssuers"] = ssoTagConflict
		}, outcomeError, "ConflictException", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSSOTagScan()
			tc.set(s)
			st := newTestStore(t)
			warns := countOpWarnings(st, "ssoadmin:ListTagsForResource")

			_, _, err := s.scan(st)
			assertGlueOutcome(t, err, tc.outcome)
			assertAPICode(t, err, tc.code)
			if *warns != tc.warnings {
				t.Errorf("ssoadmin:ListTagsForResource warnings = %d; want %d", *warns, tc.warnings)
			}
			r, ok := rowsByNativeID(t, st, TypeSSOApplication)[s.arn(app, app.tagged)]
			if !ok || r.TagsJSON != nil {
				t.Errorf("application stored=%t TagsJSON=%v; want it kept untagged", ok, r.TagsJSON)
			}
		})
	}
}

// Under an instance another account owns (a delegated administrator's
// view), a denied or not-found tag read of the instance or a permission set,
// whose ARNs name no account, keeps the row untagged and silent.
func TestScanSSOAdmin_TagsOwnedElsewhere(t *testing.T) {
	t.Parallel()
	owned := []ssoTagKind{ssoTagKindOf(t, TypeSSOInstance), ssoTagKindOf(t, TypeSSOPermissionSet)}
	for _, c := range []struct {
		name    string
		readErr error
	}{{"denied", ssoTagDenied}, {"not found", ssoTagGone}} {
		t.Run(c.name, func(t *testing.T) {
			s := newSSOTagScan()
			s.owner = "999999999999"
			for _, k := range owned {
				s.tagErr[s.arn(k, k.tagged)] = c.readErr
				s.tagErr[s.arn(k, k.bare)] = c.readErr
			}
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, _, err := s.scan(st)
			if err != nil || total != ssoTagRowCount {
				t.Fatalf("total=%d err=%v; want %d, nil", total, err, ssoTagRowCount)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, k := range owned {
				rows := rowsByNativeID(t, st, k.typ)
				for _, id := range []string{k.tagged, k.bare} {
					if r, ok := rows[s.arn(k, id)]; !ok || r.TagsJSON != nil {
						t.Errorf("%s stored=%t TagsJSON=%v; want it kept untagged", s.arn(k, id), ok, r.TagsJSON)
					}
				}
			}
		})
	}
}

// An application or trusted token issuer belongs to the account its ARN
// names, whoever owns its instance. Named elsewhere (a member account's
// application in an organization instance), a denied or not-found tag read
// keeps it untagged and silent, and its children are still listed. Named
// this account's, under an instance another account owns, a denial is the
// scan's one warning and a not-found drops the row, while the instance's and
// permission sets' own failures stay silent.
func TestScanSSOAdmin_ApplicationAndIssuerTagsJudgedByTheirARN(t *testing.T) {
	t.Parallel()
	app, tti := ssoTagKindOf(t, TypeSSOApplication), ssoTagKindOf(t, TypeSSOTrustedTokenIssuer)
	for _, c := range []struct {
		name               string
		owner, appAccount  string
		readErr            error
		warnings, wantRows int // ssoadmin:ListTagsForResource warnings, the only ones; total
		appsKept           bool
	}{
		{"named elsewhere, denied", testAccountID, "222222222222", ssoTagDenied, 0, ssoTagRowCount, true},
		{"named elsewhere, not found", testAccountID, "222222222222", ssoTagGone, 0, ssoTagRowCount, true},
		{"named here, instance elsewhere, denied", "999999999999", testAccountID, ssoTagDenied, 1, ssoTagRowCount, true},
		{"named here, instance elsewhere, not found", "999999999999", testAccountID, ssoTagGone, 0, ssoTagRowCount - 4, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSSOTagScan()
			s.owner, s.appAccount = c.owner, c.appAccount
			failing := []ssoTagKind{app, tti}
			if c.owner != testAccountID {
				failing = ssoTagKinds() // the instance's and permission sets' reads fail too, silently
			}
			for _, k := range failing {
				s.tagErr[s.arn(k, k.tagged)] = c.readErr
				s.tagErr[s.arn(k, k.bare)] = c.readErr
			}
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, _, err := s.scan(st)
			if err != nil || total != c.wantRows {
				t.Fatalf("total=%d err=%v; want %d, nil", total, err, c.wantRows)
			}
			if w := warns(); len(w) != c.warnings || (c.warnings == 1 && w[0].Service != "ssoadmin:ListTagsForResource") {
				t.Errorf("warnings = %v; want %d, under ssoadmin:ListTagsForResource", w, c.warnings)
			}
			for _, k := range []ssoTagKind{app, tti} {
				rows := rowsByNativeID(t, st, k.typ)
				for _, id := range []string{k.tagged, k.bare} {
					r, ok := rows[s.arn(k, id)]
					if ok != c.appsKept || r.TagsJSON != nil {
						t.Errorf("%s stored=%t TagsJSON=%v; want stored=%t, untagged", s.arn(k, id), ok, r.TagsJSON, c.appsKept)
					}
				}
			}
			for _, id := range []string{app.tagged, app.bare} {
				if listed := s.called("ListApplicationAssignments", s.arn(app, id)) && s.called("ListApplicationGrants", s.arn(app, id)); listed != c.appsKept {
					t.Errorf("children of %s listed=%t; want %t", s.arn(app, id), listed, c.appsKept)
				}
			}
		})
	}
}

// In GovCloud and China each row's listed ARN, and its instance's, reach the
// tag op unchanged.
func TestScanSSOAdmin_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			s := newSSOTagScanIn(c.partition, c.region)
			st := newTestStore(t)
			if _, _, err := s.scan(st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := s.tagReads(), s.allTagReads(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource calls = %v\nwant %v", got, want)
			}
			k := ssoTagKindOf(t, TypeSSOPermissionSet)
			assertTagsMap(t, rowsByNativeID(t, st, k.typ)["arn:"+c.partition+":sso:::permissionSet/"+ssoTagInst1+"/ps-tagged"].TagsJSON, ssoWantTags(k))
		})
	}
}

// ssoListedPermissionSet is the attributes of a permission set stored as its
// listed element.
func ssoListedPermissionSet(arn string) map[string]any {
	return map[string]any{"PermissionSetArn": arn}
}

// A permission set whose DescribePermissionSet fails other than as not
// found is stored as its listed element, its tags still read and its
// policies still listed: a denial warns once, another error is returned. One
// found deleted is dropped with no tag read. One described without a
// PermissionSet is stored as its listed element too.
func TestScanSSOAdmin_PermissionSetDescribeFailures(t *testing.T) {
	t.Parallel()
	ps := ssoTagKindOf(t, TypeSSOPermissionSet)
	for _, tc := range []struct {
		name     string
		set      func(s *ssoTagScan, arn string)
		kept     bool
		attrs    func(arn string) map[string]any // the stored row's decoded attributes
		code     string                          // the scan's error code; "" for none
		warnings int                             // under sso-admin:DescribePermissionSet
	}{
		{"denied", func(s *ssoTagScan, arn string) { s.describeErr[arn] = ssoTagDenied }, true, ssoListedPermissionSet, "", 1},
		{"other error", func(s *ssoTagScan, arn string) { s.describeErr[arn] = ssoTagBad }, true, ssoListedPermissionSet, "ValidationException", 0},
		{"not found", func(s *ssoTagScan, arn string) { s.describeErr[arn] = ssoTagGone }, false, nil, "", 0},
		{"no permission set", func(s *ssoTagScan, arn string) { s.describeEmpty[arn] = true }, true, ssoListedPermissionSet, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSSOTagScan()
			arn := s.arn(ps, ps.tagged)
			tc.set(s, arn)
			st := newTestStore(t)
			warns := countOpWarnings(st, "sso-admin:DescribePermissionSet")

			_, _, err := s.scan(st)
			if tc.code == "" && err != nil {
				t.Fatalf("scan: %v", err)
			}
			if tc.code != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, tc.code)
			}
			if *warns != tc.warnings {
				t.Errorf("sso-admin:DescribePermissionSet warnings = %d; want %d", *warns, tc.warnings)
			}
			r, ok := rowsByNativeID(t, st, TypeSSOPermissionSet)[arn]
			if ok != tc.kept {
				t.Fatalf("%s stored=%t; want %t", arn, ok, tc.kept)
			}
			read := s.called("ListTagsForResource", arn+" "+s.instanceARN(ssoTagInst1))
			listed := s.called("ListManagedPoliciesInPermissionSet", arn)
			if read != tc.kept || listed != tc.kept {
				t.Errorf("tags read=%t policies listed=%t; want both %t", read, listed, tc.kept)
			}
			if !tc.kept {
				return
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs %q: %v", r.AttributesJSON, err)
			}
			if want := tc.attrs(arn); !reflect.DeepEqual(attrs, want) || r.Name != nil {
				t.Errorf("attrs=%v Name=%v; want %v and no name", attrs, r.Name, want)
			}
			assertTagsMap(t, r.TagsJSON, ssoWantTags(ps))
		})
	}
}

// A listing whose second page fails keeps its first page's row, and the
// rows listed under it; every other listing still runs. A repeated
// NextToken ends a listing as an error after storing what it read.
func TestScanSSOAdmin_ListingFailuresKeepEarlierPages(t *testing.T) {
	t.Parallel()
	for _, k := range ssoTagKinds() {
		t.Run(k.listOp, func(t *testing.T) {
			s := newSSOTagScan()
			s.failAfterFirst[k.listOp] = ssoTagConflict
			st := newTestStore(t)

			total, _, err := s.scan(st)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ConflictException")
			if total != ssoTagRowCount-1 {
				t.Errorf("total = %d; want %d", total, ssoTagRowCount-1)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[s.arn(k, k.tagged)]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v; want only the first page's", k.typ, rows)
			}
		})
	}
	t.Run("repeated token", func(t *testing.T) {
		s := newSSOTagScan()
		s.repeatToken["ListApplications"] = true
		st := newTestStore(t)

		total, _, err := s.scan(st)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if total != ssoTagRowCount {
			t.Errorf("total = %d; want %d: both pages read are stored", total, ssoTagRowCount)
		}
		app := ssoTagKindOf(t, TypeSSOApplication)
		if !s.called("ListApplicationAssignments", s.arn(app, app.bare)) {
			t.Error("the second page's application has no assignments listing; want it listed")
		}
	})
}

// An instance listed without an ARN is not stored, is warned about by name,
// and no SSO Admin listing runs under it; its Identity Store, which it
// names, is still listed.
func TestScanSSOAdmin_InstanceWithoutARNWarns(t *testing.T) {
	t.Parallel()
	s := newSSOTagScan()
	s.noARN = true
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, _, err := s.scan(st)
	if err != nil || total != ssoTagRowCount+1 {
		t.Fatalf("total=%d err=%v; want %d (the user included), nil", total, err, ssoTagRowCount+1)
	}
	if _, ok := rowsByNativeID(t, st, TypeIdentityStoreUser)[identityStoreUserNativeID(testAccountID, ssoTagOrphanStore, "u-1")]; !ok {
		t.Error("the Identity Store user under the instance without an ARN was not stored")
	}
	w := warns()
	if len(w) != 1 || w[0].Service != "sso-admin:ListInstances" {
		t.Fatalf("warnings = %v; want one under sso-admin:ListInstances", w)
	}
	if _, ok := rowsByNativeID(t, st, TypeSSOInstance)[""]; ok {
		t.Error("an instance stored with an empty NativeID")
	}
	if s.called("ListPermissionSets", "") || s.called("ListApplications", "") {
		t.Error("a listing ran under the instance without an ARN")
	}
}

// A region without an instance makes no call after ListInstances.
func TestScanSSOAdmin_NoInstancesListsNothingElse(t *testing.T) {
	t.Parallel()
	s := newSSOTagScan()
	s.noInstances = true
	st := newTestStore(t)

	total, inserted, err := s.scan(st)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d err=%v; want 0, 0, nil", total, inserted, err)
	}
	if got := s.ops(); !slices.Equal(got, []string{"ListInstances"}) {
		t.Errorf("ops called = %v; want only ListInstances", got)
	}
}

// A failed write stops the scan wherever it happens: no warning about the
// rows it lost (a denied tag read of that type among them), no later listing
// runs, and the earlier listings' rows stay stored.
func TestScanSSOAdmin_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ     string
		total   int      // the rows stored before the refused write
		notRun  []string // listings after it
		earlier string   // a type stored before it
	}{
		{TypeSSOPermissionSet, 2, []string{"ListAccountsForProvisionedPermissionSet", "ListApplications", "ListManagedPoliciesInPermissionSet"}, TypeSSOInstance},
		{TypeSSOApplication, 4, []string{"ListApplicationAssignments", "ListTrustedTokenIssuers", "ListRegions"}, TypeSSOPermissionSet},
		{TypeSSOTrustedTokenIssuer, 6, []string{"ListRegions", "ListApplicationAccessScopes", "ListManagedPoliciesInPermissionSet"}, TypeSSOApplication},
	} {
		t.Run(c.typ, func(t *testing.T) {
			s := newSSOTagScan()
			k := ssoTagKindOf(t, c.typ)
			s.tagErr[s.arn(k, k.tagged)] = ssoTagDenied
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)

			total, _, err := s.scan(st)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.notRun {
				if slices.Contains(s.ops(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.total {
				t.Errorf("total = %d; want %d", total, c.total)
			}
			if n := len(rowsByNativeID(t, st, c.earlier)); n != 2 {
				t.Errorf("%s rows = %d; want 2", c.earlier, n)
			}
		})
	}
}

// One instance's ListPermissionSets failing on its first page costs the
// other instance nothing: its permission set is stored, tag-read and
// policy-listed. A not-found first page (the instance deleted after
// ListInstances) is an empty listing; on a later page it is an error that
// keeps the first page. A denial, on either page, is one warning under the
// listing that keeps the pages read before it.
func TestScanSSOAdmin_PermissionSetListingPerInstance(t *testing.T) {
	t.Parallel()
	ps := ssoTagKindOf(t, TypeSSOPermissionSet)
	for _, c := range []struct {
		name      string
		set       func(s *ssoTagScan)
		code      string // the scan's error code; "" for none
		warnings  int    // under sso-admin:ListPermissionSets
		inst1Kept int    // the first instance's permission sets stored
	}{
		{"not found", func(s *ssoTagScan) { s.inst1SetsErr = ssoTagGone }, "", 0, 0},
		{"denied", func(s *ssoTagScan) { s.inst1SetsErr = ssoTagDenied }, "", 1, 0},
		{"fails", func(s *ssoTagScan) { s.inst1SetsErr = ssoTagConflict }, "ConflictException", 0, 0},
		{"not found on page 2", func(s *ssoTagScan) { s.failAfterFirst["ListPermissionSets"] = ssoTagGone }, "ResourceNotFoundException", 0, 1},
		{"denied on page 2", func(s *ssoTagScan) { s.failAfterFirst["ListPermissionSets"] = ssoTagDenied }, "", 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSSOTagScan()
			s.inst2Set = true
			c.set(s)
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, _, err := s.scan(st)
			if c.code == "" && err != nil {
				t.Fatalf("scan: %v", err)
			}
			if c.code != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			}
			if w := warns(); len(w) != c.warnings || (c.warnings == 1 && w[0].Service != "sso-admin:ListPermissionSets") {
				t.Errorf("warnings = %v; want %d, under sso-admin:ListPermissionSets", w, c.warnings)
			}
			if want := ssoTagRowCount - 2 + c.inst1Kept + 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, TypeSSOPermissionSet)
			if len(rows) != c.inst1Kept+1 {
				t.Errorf("permission sets = %v; want %d of the first instance and the second's", rows, c.inst1Kept)
			}
			if c.inst1Kept == 1 {
				if _, ok := rows[s.arn(ps, ps.tagged)]; !ok {
					t.Errorf("first page's %s not stored", s.arn(ps, ps.tagged))
				}
			}
			other := s.inst2SetARN()
			r, ok := rows[other]
			if !ok || sv(r.Name) != "name-"+ssoTagInst2Set {
				t.Fatalf("%s stored=%t Name=%q; want it stored, described", other, ok, sv(r.Name))
			}
			if !s.called("ListTagsForResource", other+" "+s.instanceARN(ssoTagInst2)) || !s.called("ListManagedPoliciesInPermissionSet", other) {
				t.Errorf("%s tag-read=%t policy-listed=%t; want both", other,
					s.called("ListTagsForResource", other+" "+s.instanceARN(ssoTagInst2)), s.called("ListManagedPoliciesInPermissionSet", other))
			}
		})
	}
}

// A denial met under each of the two instances is one warning for the scan:
// the describes of a permission set in each, kept as their listed elements,
// and the permission set listings of both, each instance storing none.
func TestScanSSOAdmin_PermissionSetDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	ps := ssoTagKindOf(t, TypeSSOPermissionSet)
	for _, c := range []struct {
		name, op string
		set      func(s *ssoTagScan)
		sets     int // permission sets stored
	}{
		{"describe", "sso-admin:DescribePermissionSet", func(s *ssoTagScan) {
			s.describeErr[s.arn(ps, ps.tagged)] = ssoTagDenied
			s.describeErr[s.inst2SetARN()] = ssoTagDenied
		}, 3},
		{"listing", "sso-admin:ListPermissionSets", func(s *ssoTagScan) { s.opErr["ListPermissionSets"] = ssoTagDenied }, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSSOTagScan()
			s.inst2Set = true
			c.set(s)
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			if _, _, err := s.scan(st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if w := warns(); len(w) != 1 || w[0].Service != c.op || w[0].Scope != testAccountID+"/"+testRegion {
				t.Fatalf("warnings = %v; want one under %s, Scope %s/%s", w, c.op, testAccountID, testRegion)
			}
			rows := rowsByNativeID(t, st, TypeSSOPermissionSet)
			if len(rows) != c.sets {
				t.Fatalf("permission sets = %v; want %d", rows, c.sets)
			}
			if c.sets == 0 {
				listed := 0
				for _, call := range s.calls {
					if call == "ListPermissionSets " {
						listed++
					}
				}
				if listed != 2 {
					t.Errorf("ListPermissionSets calls = %d; want 2, one per instance", listed)
				}
				return
			}
			for _, arn := range []string{s.arn(ps, ps.tagged), s.inst2SetARN()} {
				if r, ok := rows[arn]; !ok || r.Name != nil {
					t.Errorf("%s stored=%t; want it stored as its listed element, no name", arn, ok)
				}
			}
		})
	}
}

// ssoNXDOMAIN is the error an endpoint that does not resolve gives.
var ssoNXDOMAIN = &net.DNSError{Err: "no such host", Name: "sso.example", IsNotFound: true}

// ListInstances failing with NXDOMAIN, nothing stored, is a region without
// SSO Admin: reported unavailable, nothing else called. Any later NXDOMAIN
// is a failure of this scan that keeps the stored rows' counts: one from SSO
// Admin after its rows were stored, and one from the Identity Store, called
// only for an instance ListInstances listed, whether or not an SSO row was
// stored.
func TestScanSSOAdmin_NXDOMAIN(t *testing.T) {
	t.Parallel()
	t.Run("region without SSO Admin", func(t *testing.T) {
		s := newSSOTagScan()
		s.opErr["ListInstances"] = ssoNXDOMAIN
		st := newTestStore(t)

		total, _, err := s.scan(st)
		if !isDNSNotFound(err) || classifyServiceError(err) != outcomeUnavailable || total != 0 {
			t.Fatalf("total=%d err=%v (%v); want 0 and an NXDOMAIN read as unavailable", total, err, classifyServiceError(err))
		}
		if got := s.ops(); !slices.Equal(got, []string{"ListInstances"}) {
			t.Errorf("ops called = %v; want only ListInstances", got)
		}
	})
	for _, c := range []struct {
		name  string
		set   func(s *ssoTagScan)
		total int
		kept  []string // types with both rows stored
	}{
		{
			"late SSO Admin", func(s *ssoTagScan) { s.opErr["ListApplications"] = ssoNXDOMAIN }, ssoTagRowCount - 2,
			[]string{TypeSSOInstance, TypeSSOPermissionSet, TypeSSOTrustedTokenIssuer},
		},
		{"Identity Store after SSO rows", func(s *ssoTagScan) {
			s.noARN = true
			s.opErr["ListUsers"] = ssoNXDOMAIN
		}, ssoTagRowCount, []string{TypeSSOInstance, TypeSSOPermissionSet, TypeSSOApplication, TypeSSOTrustedTokenIssuer}},
		{"Identity Store with no SSO row stored", func(s *ssoTagScan) {
			s.noARN = true
			s.opErr["ListUsers"] = ssoNXDOMAIN
			s.tagErr[s.instanceARN(ssoTagInst1)] = ssoTagGone
			s.tagErr[s.instanceARN(ssoTagInst2)] = ssoTagGone
		}, 0, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSSOTagScan()
			c.set(s)
			st := newTestStore(t)

			total, _, err := s.scan(st)
			if err == nil || isDNSNotFound(err) || classifyServiceError(err) != outcomeError {
				t.Fatalf("err = %v (%v); want a hard error, not an NXDOMAIN", err, classifyServiceError(err))
			}
			if total != c.total {
				t.Errorf("total = %d; want %d", total, c.total)
			}
			for _, typ := range c.kept {
				if n := len(rowsByNativeID(t, st, typ)); n != 2 {
					t.Errorf("%s rows = %d; want 2", typ, n)
				}
			}
			if len(rowsByNativeID(t, st, TypeIdentityStoreUser)) != 0 {
				t.Error("an Identity Store user stored; want none")
			}
		})
	}
}
