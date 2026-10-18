package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssodocument "github.com/aws/aws-sdk-go-v2/service/ssoadmin/document"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

var ssoAdded = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const ssoTestInstanceARN = "arn:aws:sso:::instance/ssoins-1111"

func ssoTestAppARN(id string) string {
	return "arn:aws:sso::" + testAccountID + ":application/ssoins-1111/apl-" + id
}

// stubSSOAdmin serves the per-parent list ops from page sets keyed by parent
// ARN. fail is keyed "<Op>/<parentArn>" and replaces that call with the error.
type stubSSOAdmin struct {
	ssoadminAPI
	mu      sync.Mutex
	calls   []string
	fail    map[string]error
	scopes  map[string][][]ssotypes.ScopeDetails
	methods map[string][][]ssotypes.AuthenticationMethodItem
	grants  map[string][][]ssotypes.GrantItem
	regions map[string][][]ssotypes.RegionMetadata
	managed map[string][][]ssotypes.AttachedManagedPolicy
	custom  map[string][][]ssotypes.CustomerManagedPolicyReference
}

// ssoPolicyListInputErr rejects a permission-set policy list call that does
// not name the permission set's instance or does not ask for full pages.
func ssoPolicyListInputErr(instanceARN *string, maxResults *int32) error {
	if sv(instanceARN) != ssoTestInstanceARN {
		return fmt.Errorf("InstanceArn=%q, want %q", sv(instanceARN), ssoTestInstanceARN)
	}
	if maxResults == nil || *maxResults != 100 {
		return fmt.Errorf("MaxResults=%v, want 100", maxResults)
	}
	return nil
}

func (s *stubSSOAdmin) ListManagedPoliciesInPermissionSet(_ context.Context, in *ssoadmin.ListManagedPoliciesInPermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListManagedPoliciesInPermissionSetOutput, error) {
	if err := ssoPolicyListInputErr(in.InstanceArn, in.MaxResults); err != nil {
		return nil, err
	}
	items, next, err := ssoServe(s, "ListManagedPoliciesInPermissionSet/"+sv(in.PermissionSetArn), s.managed[sv(in.PermissionSetArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListManagedPoliciesInPermissionSetOutput{AttachedManagedPolicies: items, NextToken: next}, nil
}

func (s *stubSSOAdmin) ListCustomerManagedPolicyReferencesInPermissionSet(_ context.Context, in *ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput, error) {
	if err := ssoPolicyListInputErr(in.InstanceArn, in.MaxResults); err != nil {
		return nil, err
	}
	items, next, err := ssoServe(s, "ListCustomerManagedPolicyReferencesInPermissionSet/"+sv(in.PermissionSetArn), s.custom[sv(in.PermissionSetArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{CustomerManagedPolicyReferences: items, NextToken: next}, nil
}

func ssoTestPermissionSetARN(id string) string {
	return "arn:aws:sso:::permissionSet/ssoins-1111/ps-" + id
}

func ssoManagedPolicy(path, name string) ssotypes.AttachedManagedPolicy {
	return ssotypes.AttachedManagedPolicy{Arn: sdkaws.String("arn:aws:iam::aws:policy" + path + name), Name: sdkaws.String(name)}
}

// ssoPolicyInstances maps every permission set to the test instance.
func ssoPolicyInstances(sets []childParent) map[string]string {
	m := make(map[string]string, len(sets))
	for _, ps := range sets {
		m[ps.id] = ssoTestInstanceARN
	}
	return m
}

func ssoServe[T any](s *stubSSOAdmin, key string, pages [][]T, token *string) ([]T, *string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, key)
	err := s.fail[key]
	s.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return stubPage(pages, token, nil)
}

func (s *stubSSOAdmin) ListApplicationAccessScopes(_ context.Context, in *ssoadmin.ListApplicationAccessScopesInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationAccessScopesOutput, error) {
	items, next, err := ssoServe(s, "ListApplicationAccessScopes/"+sv(in.ApplicationArn), s.scopes[sv(in.ApplicationArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListApplicationAccessScopesOutput{Scopes: items, NextToken: next}, nil
}

func (s *stubSSOAdmin) ListApplicationAuthenticationMethods(_ context.Context, in *ssoadmin.ListApplicationAuthenticationMethodsInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationAuthenticationMethodsOutput, error) {
	items, next, err := ssoServe(s, "ListApplicationAuthenticationMethods/"+sv(in.ApplicationArn), s.methods[sv(in.ApplicationArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListApplicationAuthenticationMethodsOutput{AuthenticationMethods: items, NextToken: next}, nil
}

func (s *stubSSOAdmin) ListApplicationGrants(_ context.Context, in *ssoadmin.ListApplicationGrantsInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationGrantsOutput, error) {
	items, next, err := ssoServe(s, "ListApplicationGrants/"+sv(in.ApplicationArn), s.grants[sv(in.ApplicationArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListApplicationGrantsOutput{Grants: items, NextToken: next}, nil
}

func (s *stubSSOAdmin) ListRegions(_ context.Context, in *ssoadmin.ListRegionsInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListRegionsOutput, error) {
	items, next, err := ssoServe(s, "ListRegions/"+sv(in.InstanceArn), s.regions[sv(in.InstanceArn)], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &ssoadmin.ListRegionsOutput{Regions: items, NextToken: next}, nil
}

func ssoScope(scope string) ssotypes.ScopeDetails {
	return ssotypes.ScopeDetails{Scope: sdkaws.String(scope), AuthorizedTargets: []string{"arn:aws:sso::" + testAccountID + ":application/ssoins-1111/apl-target"}}
}

func ssoIAMMethod() ssotypes.AuthenticationMethodItem {
	return ssotypes.AuthenticationMethodItem{
		AuthenticationMethodType: ssotypes.AuthenticationMethodTypeIam,
		AuthenticationMethod: &ssotypes.AuthenticationMethodMemberIam{Value: ssotypes.IamAuthenticationMethod{
			ActorPolicy: ssodocument.NewLazyDocument(map[string]any{"Version": "2012-10-17"}),
		}},
	}
}

func ssoGrant(t ssotypes.GrantType) ssotypes.GrantItem {
	g := ssotypes.GrantItem{GrantType: t}
	switch t {
	case ssotypes.GrantTypeJwtBearer:
		g.Grant = &ssotypes.GrantMemberJwtBearer{Value: ssotypes.JwtBearerGrant{AuthorizedTokenIssuers: []ssotypes.AuthorizedTokenIssuer{{
			TrustedTokenIssuerArn: sdkaws.String("arn:aws:sso::" + testAccountID + ":trustedTokenIssuer/ssoins-1111/tti-1"),
			AuthorizedAudiences:   []string{"aud-1"},
		}}}}
	case ssotypes.GrantTypeAuthorizationCode:
		g.Grant = &ssotypes.GrantMemberAuthorizationCode{Value: ssotypes.AuthorizationCodeGrant{RedirectUris: []string{"https://app.example.com/cb"}}}
	default:
		g.Grant = &ssotypes.GrantMemberRefreshToken{}
	}
	return g
}

func ssoRegion(name string, primary bool) ssotypes.RegionMetadata {
	return ssotypes.RegionMetadata{RegionName: sdkaws.String(name), IsPrimaryRegion: primary, Status: ssotypes.RegionStatusActive, AddedDate: &ssoAdded}
}

// ssoChildCase is one per-parent child scanner. Its fill seeds parent a with
// two pages (items a1 and a2 plus one key-less item, then a3) and parent b
// with item b1.
type ssoChildCase struct {
	name, op, typ, kind string
	parentType          string
	parentARN           func(id string) string
	fill                func(s *stubSSOAdmin, a, b string)
	run                 func(context.Context, *stubSSOAdmin, *store.Store, []childParent) (int, int, error)
	keys                [4]string // NativeID suffix keys of a1, a2, a3, b1
	names               [4]string // Names of a1, a2, a3, b1; zero = the keys
	wantStatus          string    // Status of a1; "" = unset
	wantCreated         bool
	attrKey, attrWant   string // an attribute the fixture sets on a1
	skipCode            string // error code that skips one parent silently
	propagateCode       string // modeled code the other cases skip; here it must propagate
}

func ssoChildCases() []ssoChildCase {
	acct := newTestAccount(testAccountID)
	return []ssoChildCase{
		{
			name: "access scopes", op: "ListApplicationAccessScopes", typ: TypeSSOApplicationAccessScope, kind: "access-scope",
			parentType: TypeSSOApplication, parentARN: ssoTestAppARN,
			fill: func(s *stubSSOAdmin, a, b string) {
				s.scopes = map[string][][]ssotypes.ScopeDetails{
					a: {{ssoScope("sso:account:access"), ssoScope("redshift:connect"), {AuthorizedTargets: []string{"x"}}}, {ssoScope("s3:access:read")}},
					b: {{ssoScope("sso:account:access")}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSOApplicationAccessScopes(ctx, s, acct, testRegion, st, testScanID, ps)
			},
			keys:    [4]string{"sso:account:access", "redshift:connect", "s3:access:read", "sso:account:access"},
			attrKey: "Scope", attrWant: "sso:account:access",
			skipCode: "ResourceNotFoundException", propagateCode: "ValidationException",
		},
		{
			name: "authentication methods", op: "ListApplicationAuthenticationMethods", typ: TypeSSOApplicationAuthenticationMethod, kind: "authentication-method",
			parentType: TypeSSOApplication, parentARN: ssoTestAppARN,
			fill: func(s *stubSSOAdmin, a, b string) {
				other := ssotypes.AuthenticationMethodItem{AuthenticationMethodType: "OTHER"}
				third := ssotypes.AuthenticationMethodItem{AuthenticationMethodType: "THIRD"}
				s.methods = map[string][][]ssotypes.AuthenticationMethodItem{
					a: {{ssoIAMMethod(), other, {}}, {third}},
					b: {{ssoIAMMethod()}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSOApplicationAuthenticationMethods(ctx, s, acct, testRegion, st, testScanID, ps)
			},
			keys:    [4]string{"IAM", "OTHER", "THIRD", "IAM"},
			attrKey: "AuthenticationMethodType", attrWant: "IAM",
			skipCode: "ResourceNotFoundException", propagateCode: "ValidationException",
		},
		{
			name: "grants", op: "ListApplicationGrants", typ: TypeSSOApplicationGrant, kind: "grant",
			parentType: TypeSSOApplication, parentARN: ssoTestAppARN,
			fill: func(s *stubSSOAdmin, a, b string) {
				s.grants = map[string][][]ssotypes.GrantItem{
					a: {{ssoGrant(ssotypes.GrantTypeJwtBearer), ssoGrant(ssotypes.GrantTypeAuthorizationCode), {}}, {ssoGrant(ssotypes.GrantTypeRefreshToken)}},
					b: {{ssoGrant(ssotypes.GrantTypeJwtBearer)}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSOApplicationGrants(ctx, s, acct, testRegion, st, testScanID, ps)
			},
			keys:    [4]string{"urn:ietf:params:oauth:grant-type:jwt-bearer", "authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:jwt-bearer"},
			attrKey: "GrantType", attrWant: "urn:ietf:params:oauth:grant-type:jwt-bearer",
			skipCode: "ResourceNotFoundException", propagateCode: "ValidationException",
		},
		{
			name: "regions", op: "ListRegions", typ: TypeSSORegion, kind: "region",
			parentType: TypeSSOInstance, parentARN: func(id string) string { return "arn:aws:sso:::instance/ssoins-" + id },
			fill: func(s *stubSSOAdmin, a, b string) {
				s.regions = map[string][][]ssotypes.RegionMetadata{
					a: {{ssoRegion("us-east-1", true), ssoRegion("eu-west-1", false), {Status: ssotypes.RegionStatusAdding}}, {ssoRegion("ap-south-1", false)}},
					b: {{ssoRegion("us-west-2", true)}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSORegions(ctx, s, acct, testRegion, st, testScanID, ps)
			},
			keys:       [4]string{"us-east-1", "eu-west-1", "ap-south-1", "us-west-2"},
			wantStatus: "ACTIVE", wantCreated: true, attrKey: "RegionName", attrWant: "us-east-1",
			skipCode: "ValidationException", propagateCode: "ResourceNotFoundException",
		},
		{
			name: "managed policies", op: "ListManagedPoliciesInPermissionSet", typ: TypeSSOManagedPolicy, kind: "managed-policy",
			parentType: TypeSSOPermissionSet, parentARN: ssoTestPermissionSetARN,
			fill: func(s *stubSSOAdmin, a, b string) {
				s.managed = map[string][][]ssotypes.AttachedManagedPolicy{
					a: {{ssoManagedPolicy("/", "AdministratorAccess"), ssoManagedPolicy("/job-function/", "ViewOnlyAccess"), {Name: sdkaws.String("no-arn")}}, {ssoManagedPolicy("/", "ReadOnlyAccess")}},
					b: {{ssoManagedPolicy("/", "AdministratorAccess")}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSOManagedPolicies(ctx, s, acct, testRegion, st, testScanID, ps, ssoPolicyInstances(ps))
			},
			keys: [4]string{
				"arn:aws:iam::aws:policy/AdministratorAccess", "arn:aws:iam::aws:policy/job-function/ViewOnlyAccess",
				"arn:aws:iam::aws:policy/ReadOnlyAccess", "arn:aws:iam::aws:policy/AdministratorAccess",
			},
			names:   [4]string{"AdministratorAccess", "ViewOnlyAccess", "ReadOnlyAccess", "AdministratorAccess"},
			attrKey: "Arn", attrWant: "arn:aws:iam::aws:policy/AdministratorAccess",
			skipCode: "ResourceNotFoundException", propagateCode: "ValidationException",
		},
		{
			name: "customer managed policy references", op: "ListCustomerManagedPolicyReferencesInPermissionSet",
			typ: TypeSSOCustomerManagedPolicyReference, kind: "customer-managed-policy",
			parentType: TypeSSOPermissionSet, parentARN: ssoTestPermissionSetARN,
			fill: func(s *stubSSOAdmin, a, b string) {
				ref := func(path *string, name string) ssotypes.CustomerManagedPolicyReference {
					return ssotypes.CustomerManagedPolicyReference{Path: path, Name: sdkaws.String(name)}
				}
				s.custom = map[string][][]ssotypes.CustomerManagedPolicyReference{
					a: {{ref(sdkaws.String("/"), "PolicyA"), ref(sdkaws.String("/team/"), "PolicyB"), {Path: sdkaws.String("/")}}, {ref(nil, "PolicyC")}},
					b: {{ref(sdkaws.String("/"), "PolicyA")}},
				}
			},
			run: func(ctx context.Context, s *stubSSOAdmin, st *store.Store, ps []childParent) (int, int, error) {
				return scanSSOCustomerManagedPolicyReferences(ctx, s, acct, testRegion, st, testScanID, ps, ssoPolicyInstances(ps))
			},
			keys:    [4]string{"PolicyA", "team/PolicyB", "PolicyC", "PolicyA"},
			names:   [4]string{"PolicyA", "PolicyB", "PolicyC", "PolicyA"},
			attrKey: "Path", attrWant: "/",
			skipCode: "ResourceNotFoundException", propagateCode: "ValidationException",
		},
	}
}

// ssoChildFixture stores parents a and b and seeds the stub through tc.fill.
func ssoChildFixture(t *testing.T, st *store.Store, tc ssoChildCase) (*stubSSOAdmin, []childParent) {
	t.Helper()
	var parents []childParent
	for _, id := range []string{"a", "b"} {
		arn := tc.parentARN(id)
		upsertTestResource(t, st, "aws", testAccountID, tc.parentType, arn, testRegion, "{}")
		parents = append(parents, childParent{id: arn, arn: arn})
	}
	stub := &stubSSOAdmin{}
	tc.fill(stub, parents[0].id, parents[1].id)
	return stub, parents
}

func TestSSOChildScanners_PagesEveryParent(t *testing.T) {
	for _, tc := range ssoChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			stub, parents := ssoChildFixture(t, st, tc)

			total, inserted, err := tc.run(context.Background(), stub, st, parents)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 4 || inserted != 4 {
				t.Errorf("total=%d inserted=%d, want 4 each (two pages of a, key-less item skipped, plus b)", total, inserted)
			}
			rows := lmvRows(t, st, tc.typ)
			if len(rows) != 4 {
				t.Fatalf("stored %d rows, want 4: %v", len(rows), rows)
			}
			native := func(p childParent, key string) string { return p.arn + "/" + tc.kind + "/" + key }
			wantParent := []childParent{parents[0], parents[0], parents[0], parents[1]}
			for i, key := range tc.keys {
				id := native(wantParent[i], key)
				r, ok := rows[id]
				if !ok {
					t.Errorf("missing row %s", id)
					continue
				}
				wantName := cmp.Or(tc.names[i], key)
				if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != wantName {
					t.Errorf("%s: type=%s region=%s name=%s, want %s %s %s", id, r.Type, sv(r.Region), sv(r.Name), tc.typ, testRegion, wantName)
				}
			}
			a1 := rows[native(parents[0], tc.keys[0])]
			if sv(a1.Status) != tc.wantStatus {
				t.Errorf("status=%q, want %q", sv(a1.Status), tc.wantStatus)
			}
			if tc.wantCreated && sv(a1.CreatedAt) != ssoAdded.Format(time.RFC3339) {
				t.Errorf("createdAt=%q, want %q", sv(a1.CreatedAt), ssoAdded.Format(time.RFC3339))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(a1.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			if got := attrs[tc.attrKey]; got != tc.attrWant {
				t.Errorf("attrs[%s]=%v, want %q", tc.attrKey, got, tc.attrWant)
			}

			rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, parents[0].arn), store.RelContains)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, store.ResourceID("aws", testAccountID, parents[0].arn), store.ResourceID("aws", testAccountID, native(parents[0], tc.keys[2])), store.RelContains)
			for _, r := range rels {
				if r.ToID == store.ResourceID("aws", testAccountID, native(parents[1], tc.keys[3])) {
					t.Errorf("parent a contains parent b's child")
				}
			}
		})
	}
}

func TestSSOChildScanners_Empty(t *testing.T) {
	for _, tc := range ssoChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			_, parents := ssoChildFixture(t, st, tc)
			total, _, err := tc.run(context.Background(), &stubSSOAdmin{}, st, parents)
			if err != nil || total != 0 {
				t.Fatalf("total=%d err=%v, want 0/nil", total, err)
			}
			if rows := lmvRows(t, st, tc.typ); len(rows) != 0 {
				t.Errorf("stored %d rows, want 0", len(rows))
			}
		})
	}
}

func TestSSOChildScanners_PerParentErrors(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: sso:List")
	for _, tc := range ssoChildCases() {
		for _, ec := range []struct {
			name         string
			failA, failB error
			wantRows     int
			wantWarnings int
			wantErr      string // API error code expected back; "" = nil
		}{
			{name: "denied on every parent warns once", failA: deny, failB: deny, wantWarnings: 1},
			{name: "denied on one parent keeps the other", failA: deny, wantRows: 1, wantWarnings: 1},
			{name: "other error", failA: apiErr("InternalServerException", "bad"), wantErr: "InternalServerException"},
		} {
			t.Run(tc.name+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := 0
				st.OnWarn = func(store.ScanWarning) { warnings++ }
				stub, parents := ssoChildFixture(t, st, tc)
				stub.fail = map[string]error{}
				if ec.failA != nil {
					stub.fail[tc.op+"/"+parents[0].id] = ec.failA
				}
				if ec.failB != nil {
					stub.fail[tc.op+"/"+parents[1].id] = ec.failB
				}

				_, _, err := tc.run(context.Background(), stub, st, parents)
				if ec.wantErr != "" {
					if !isAPIErrorCode(err, ec.wantErr) {
						t.Fatalf("err=%v, want %s propagated", err, ec.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				if warnings != ec.wantWarnings {
					t.Errorf("warnings=%d, want %d", warnings, ec.wantWarnings)
				}
				if rows := lmvRows(t, st, tc.typ); len(rows) != ec.wantRows {
					t.Errorf("stored %d rows, want %d", len(rows), ec.wantRows)
				}
				if len(stub.calls) < len(parents) {
					t.Errorf("calls=%v, want every parent tried", stub.calls)
				}
			})
		}
	}
}

// An application deleted between ListApplications and the child call is
// skipped, as is an instance that cannot hold Regions (not an organization
// instance); the other parent's rows are kept. The code skipped by the other
// family is not tolerated here.
func TestSSOChildScanners_SkippedParent(t *testing.T) {
	for _, tc := range ssoChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub, parents := ssoChildFixture(t, st, tc)
			stub.fail = map[string]error{tc.op + "/" + parents[0].id: apiErr(tc.skipCode, "skip")}

			if _, _, err := tc.run(context.Background(), stub, st, parents); err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if warnings != 0 {
				t.Errorf("warnings=%d, want 0", warnings)
			}
			if rows := lmvRows(t, st, tc.typ); len(rows) != 1 {
				t.Errorf("stored %d rows, want parent b's 1", len(rows))
			}

			stub.fail = map[string]error{tc.op + "/" + parents[0].id: apiErr(tc.propagateCode, "bad")}
			if _, _, err := tc.run(context.Background(), stub, newTestStore(t), parents); !isAPIErrorCode(err, tc.propagateCode) {
				t.Errorf("err=%v, want %s propagated", err, tc.propagateCode)
			}
		})
	}
}

// encoding/json renders a Smithy document as {}, so the IAM actor policy must
// reach the stored attributes through the document's own marshaler.
func TestScanSSOApplicationAuthenticationMethods_KeepsActorPolicy(t *testing.T) {
	st := newTestStore(t)
	app := ssoTestAppARN("a")
	upsertTestResource(t, st, "aws", testAccountID, TypeSSOApplication, app, testRegion, "{}")
	stub := &stubSSOAdmin{methods: map[string][][]ssotypes.AuthenticationMethodItem{app: {{ssoIAMMethod()}}}}

	if _, _, err := scanSSOApplicationAuthenticationMethods(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []childParent{{id: app, arn: app}}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := lmvRows(t, st, TypeSSOApplicationAuthenticationMethod)[app+"/authentication-method/IAM"]
	if !ok {
		t.Fatal("IAM authentication method not stored")
	}
	var attrs struct {
		AuthenticationMethod struct {
			Value struct{ ActorPolicy map[string]any }
		}
		AuthenticationMethodType string
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if got := attrs.AuthenticationMethod.Value.ActorPolicy["Version"]; got != "2012-10-17" || attrs.AuthenticationMethodType != "IAM" {
		t.Errorf("attrs=%s, want ActorPolicy.Version 2012-10-17 and type IAM", r.AttributesJSON)
	}
}

func ssoTopLevelResponses() map[string][]stubCall {
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	app := ssoTestAppARN("a")
	return map[string][]stubCall{
		"ListInstances": one(&ssoadmin.ListInstancesOutput{Instances: []ssotypes.InstanceMetadata{{
			InstanceArn: sdkaws.String(ssoTestInstanceARN), IdentityStoreId: sdkaws.String(testIdentityID), OwnerAccountId: sdkaws.String(testAccountID),
		}}}),
		"ListUsers": one(&identitystore.ListUsersOutput{Users: []istypes.User{{UserId: sdkaws.String("u-1"), UserName: sdkaws.String("alice")}}}),
		"ListGroups": { // once for the groups, once for the memberships walk
			{Output: &identitystore.ListGroupsOutput{Groups: []istypes.Group{{GroupId: sdkaws.String("g-1")}}}},
			{Output: &identitystore.ListGroupsOutput{Groups: []istypes.Group{{GroupId: sdkaws.String("g-1")}}}},
		},
		"ListGroupMemberships": one(&identitystore.ListGroupMembershipsOutput{}),
		"ListPermissionSets": {
			{Output: &ssoadmin.ListPermissionSetsOutput{PermissionSets: []string{ssoTestPermissionSetARN("a")}}},
			{Output: &ssoadmin.ListPermissionSetsOutput{PermissionSets: []string{ssoTestPermissionSetARN("a")}}},
		},
		"DescribePermissionSet": one(&ssoadmin.DescribePermissionSetOutput{PermissionSet: &ssotypes.PermissionSet{
			PermissionSetArn: sdkaws.String(ssoTestPermissionSetARN("a")), Name: sdkaws.String("Admins"),
		}}),
		"ListAccountsForProvisionedPermissionSet": one(&ssoadmin.ListAccountsForProvisionedPermissionSetOutput{AccountIds: []string{testTargetAcct}}),
		"ListAccountAssignments": one(&ssoadmin.ListAccountAssignmentsOutput{AccountAssignments: []ssotypes.AccountAssignment{{
			AccountId: sdkaws.String(testTargetAcct), PermissionSetArn: sdkaws.String(ssoTestPermissionSetARN("a")),
			PrincipalType: ssotypes.PrincipalTypeUser, PrincipalId: sdkaws.String("u-1"),
		}}}),
		"ListManagedPoliciesInPermissionSet": one(&ssoadmin.ListManagedPoliciesInPermissionSetOutput{
			AttachedManagedPolicies: []ssotypes.AttachedManagedPolicy{ssoManagedPolicy("/", "AdministratorAccess")},
		}),
		"ListCustomerManagedPolicyReferencesInPermissionSet": one(&ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{
			CustomerManagedPolicyReferences: []ssotypes.CustomerManagedPolicyReference{{Name: sdkaws.String("PolicyA"), Path: sdkaws.String("/")}},
		}),
		"ListApplicationProviders": one(&ssoadmin.ListApplicationProvidersOutput{}),
		"ListApplications": one(&ssoadmin.ListApplicationsOutput{Applications: []ssotypes.Application{{
			ApplicationArn: sdkaws.String(app), Name: sdkaws.String("app-a"),
		}}}),
		"ListApplicationAssignments":                          one(&ssoadmin.ListApplicationAssignmentsOutput{}),
		"DescribeInstanceAccessControlAttributeConfiguration": one(&ssoadmin.DescribeInstanceAccessControlAttributeConfigurationOutput{}),
		"ListTrustedTokenIssuers": one(&ssoadmin.ListTrustedTokenIssuersOutput{TrustedTokenIssuers: []ssotypes.TrustedTokenIssuerMetadata{{
			TrustedTokenIssuerArn: sdkaws.String("arn:aws:sso::" + testAccountID + ":trustedTokenIssuer/ssoins-1111/tti-1"),
		}}}),
		"ListApplicationAccessScopes":          one(&ssoadmin.ListApplicationAccessScopesOutput{Scopes: []ssotypes.ScopeDetails{ssoScope("sso:account:access")}}),
		"ListApplicationAuthenticationMethods": one(&ssoadmin.ListApplicationAuthenticationMethodsOutput{AuthenticationMethods: []ssotypes.AuthenticationMethodItem{ssoIAMMethod()}}),
		"ListApplicationGrants":                one(&ssoadmin.ListApplicationGrantsOutput{Grants: []ssotypes.GrantItem{ssoGrant(ssotypes.GrantTypeJwtBearer)}}),
		"ListRegions":                          one(&ssoadmin.ListRegionsOutput{Regions: []ssotypes.RegionMetadata{ssoRegion("us-east-1", true)}}),
	}
}

// Drives scanSSOAdmin against real SDK clients. Regions and the application
// children are listed for the instance and applications the scan just stored,
// after every established phase, so a failure in them keeps the established
// rows; Regions run first, so an application-phase failure keeps them too.
// The permission-set policy lists run last, for the permission set stored this
// scan; each keeps its rows when the other fails, and a throttle on one cannot
// turn the other's hard error into a transient one.
func TestScanSSOAdmin_NewPhasesWiredAndOrdered(t *testing.T) {
	type row struct{ typ, nativeID string }
	app := ssoTestAppARN("a")
	application := row{TypeSSOApplication, app}
	issuer := row{TypeSSOTrustedTokenIssuer, "arn:aws:sso::" + testAccountID + ":trustedTokenIssuer/ssoins-1111/tti-1"}
	scope := row{TypeSSOApplicationAccessScope, app + "/access-scope/sso:account:access"}
	method := row{TypeSSOApplicationAuthenticationMethod, app + "/authentication-method/IAM"}
	grant := row{TypeSSOApplicationGrant, app + "/grant/urn:ietf:params:oauth:grant-type:jwt-bearer"}
	region := row{TypeSSORegion, ssoTestInstanceARN + "/region/us-east-1"}
	ps := ssoTestPermissionSetARN("a")
	permissionSet := row{TypeSSOPermissionSet, ps}
	managed := row{TypeSSOManagedPolicy, ps + "/managed-policy/arn:aws:iam::aws:policy/AdministratorAccess"}
	custom := row{TypeSSOCustomerManagedPolicyReference, ps + "/customer-managed-policy/PolicyA"}
	// A hard error: classifyServiceError reads it as outcomeError, not transient.
	const failCode = "ConflictException"
	assignment := row{TypeSSOAccountAssignment, ssoAssignmentNativeID(ps, testTargetAcct, "USER", "u-1")}
	user := row{TypeIdentityStoreUser, identityStoreUserNativeID(testAccountID, testIdentityID, "u-1")}
	group := row{TypeIdentityStoreGroup, identityStoreGroupNativeID(testAccountID, testIdentityID, "g-1")}
	head := []row{permissionSet, assignment, user, group}
	appRows := []row{application, issuer, scope, method, grant, region}
	established := slices.Concat(head, appRows)
	all := append(slices.Clone(established), managed, custom)
	policies := []row{managed, custom}
	without := func(rows []row, drop row) []row {
		return slices.DeleteFunc(slices.Clone(rows), func(r row) bool { return r == drop })
	}
	for _, tc := range []struct {
		name      string
		failOp    string
		throttle  string // op answered with a throttle; it is demoted to one warning
		want      []row
		wantErr   bool
		warnings  int
		wantCount int // total and inserted, the instance row included; 0 = unchecked
	}{
		{name: "all succeed", want: all, wantCount: 13},
		{
			name: "regions fail", failOp: "ListRegions", want: slices.Concat(head, []row{application, issuer}, policies), wantErr: true,
			wantCount: 9, // the application and issuer listed before the failure still count
		},
		{name: "access scopes fail", failOp: "ListApplicationAccessScopes", want: slices.Concat(head, []row{application, issuer, region}, policies), wantErr: true},
		{name: "grants fail", failOp: "ListApplicationGrants", want: slices.Concat(head, []row{application, issuer, region, scope, method}, policies), wantErr: true},
		{name: "application providers fail", failOp: "ListApplicationProviders", want: slices.Concat(head, policies), wantErr: true, wantCount: 7},
		{
			name: "permission sets fail", failOp: "DescribePermissionSet", want: slices.Concat([]row{assignment, user, group}, appRows), wantErr: true,
			wantCount: 10, // nothing stored for policies to list; every other phase still runs
		},
		{name: "account assignments fail", failOp: "ListAccountsForProvisionedPermissionSet", want: without(all, assignment), wantErr: true, wantCount: 12},
		{
			name: "users fail", failOp: "ListUsers", want: without(without(all, user), group), wantErr: true,
			wantCount: 11, // the phase stops before groups; phases 5 and 6 still run
		},
		{
			name: "users throttled, managed fails", throttle: "ListUsers", failOp: "ListManagedPoliciesInPermissionSet",
			want: without(without(without(all, user), group), managed), wantErr: true, warnings: 1, wantCount: 10,
		},
		{
			name: "application providers throttled, assignments fail", throttle: "ListApplicationProviders", failOp: "ListAccountsForProvisionedPermissionSet",
			want: []row{permissionSet, user, group, managed, custom}, wantErr: true, warnings: 1, wantCount: 6,
		},
		{
			name: "assignments throttled, managed fails", throttle: "ListAccountsForProvisionedPermissionSet", failOp: "ListManagedPoliciesInPermissionSet",
			want: append(without(established, assignment), custom), wantErr: true, warnings: 1, wantCount: 11,
		},
		{name: "managed policies fail", failOp: "ListManagedPoliciesInPermissionSet", want: append(slices.Clone(established), custom), wantErr: true},
		{name: "customer policy references fail", failOp: "ListCustomerManagedPolicyReferencesInPermissionSet", want: append(slices.Clone(established), managed), wantErr: true},
		{
			name: "managed throttled, customer fails", throttle: "ListManagedPoliciesInPermissionSet", failOp: "ListCustomerManagedPolicyReferencesInPermissionSet",
			want: established, wantErr: true, warnings: 1,
		},
		{
			name: "customer throttled, managed fails", throttle: "ListCustomerManagedPolicyReferencesInPermissionSet", failOp: "ListManagedPoliciesInPermissionSet",
			want: established, wantErr: true, warnings: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			responses := ssoTopLevelResponses()
			if tc.failOp != "" {
				responses[tc.failOp] = []stubCall{{Err: apiErr(failCode, "bad")}}
			}
			if tc.throttle != "" {
				responses[tc.throttle] = []stubCall{{Err: apiErr("ThrottlingException", "slow down")}}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: sdkaws.Config{
				Region:           testRegion,
				Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
				RetryMaxAttempts: 1,
				APIOptions:       []func(*smithymw.Stack) error{stubResponses(t, responses)},
			}}

			total, inserted, err := scanSSOAdmin(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantCount != 0 && (total != tc.wantCount || inserted != tc.wantCount) {
				t.Errorf("total=%d inserted=%d, want %d each", total, inserted, tc.wantCount)
			}
			if tc.wantErr != isAPIErrorCode(err, failCode) {
				t.Fatalf("scanSSOAdmin err=%v, wantErr=%t", err, tc.wantErr)
			}
			if tc.wantErr && classifyServiceError(err) != outcomeError {
				t.Errorf("classifyServiceError(%v)=%v, want outcomeError", err, classifyServiceError(err))
			}
			if warnings != tc.warnings {
				t.Errorf("warnings=%d, want %d", warnings, tc.warnings)
			}
			for _, r := range all {
				_, stored := lmvRows(t, st, r.typ)[r.nativeID]
				if want := slices.Contains(tc.want, r); stored != want {
					t.Errorf("%s %s stored=%t, want %t", r.typ, r.nativeID, stored, want)
				}
			}
		})
	}
}

// The policy lists run only for permission sets the scan stored: not for ones
// whose DescribePermissionSet was denied (one warning for both), nor for one
// described without an ARN or without a permission set (one not-stored
// warning), nor for one deleted after the listing (silent). The account
// assignments fan-out treats a denied or deleted triple the same way.
func TestScanSSOAdmin_PolicyListsOnlyForStoredPermissionSets(t *testing.T) {
	stored, denied, denied2 := ssoTestPermissionSetARN("a"), ssoTestPermissionSetARN("b"), ssoTestPermissionSetARN("f")
	noARN, noSet, gone := ssoTestPermissionSetARN("c"), ssoTestPermissionSetARN("d"), ssoTestPermissionSetARN("e")
	const okAcct, deniedAcct, deniedAcct2, goneAcct = "222222222201", "222222222202", "222222222203", "222222222204"
	notFound := apiErr("ResourceNotFoundException", "gone")
	var (
		mu    sync.Mutex
		calls []string
	)
	record := func(op string, ps *string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, op+"/"+sv(ps))
	}
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: sso:Describe")
	handlers := map[string]func(any) (any, error){
		"ListInstances": func(any) (any, error) {
			return &ssoadmin.ListInstancesOutput{Instances: []ssotypes.InstanceMetadata{{InstanceArn: sdkaws.String(ssoTestInstanceARN)}}}, nil
		},
		"ListPermissionSets": func(any) (any, error) {
			return &ssoadmin.ListPermissionSetsOutput{PermissionSets: []string{stored, denied, noARN, noSet, gone, denied2}}, nil
		},
		"DescribePermissionSet": func(in any) (any, error) {
			switch arn := sv(in.(*ssoadmin.DescribePermissionSetInput).PermissionSetArn); arn {
			case denied, denied2:
				return nil, deny
			case gone:
				return nil, notFound
			case noARN:
				return &ssoadmin.DescribePermissionSetOutput{PermissionSet: &ssotypes.PermissionSet{Name: sdkaws.String("no-arn")}}, nil
			case noSet:
				return &ssoadmin.DescribePermissionSetOutput{}, nil
			default:
				return &ssoadmin.DescribePermissionSetOutput{PermissionSet: &ssotypes.PermissionSet{PermissionSetArn: sdkaws.String(arn)}}, nil
			}
		},
		"ListAccountsForProvisionedPermissionSet": func(in any) (any, error) {
			if sv(in.(*ssoadmin.ListAccountsForProvisionedPermissionSetInput).PermissionSetArn) != stored {
				return &ssoadmin.ListAccountsForProvisionedPermissionSetOutput{}, nil
			}
			return &ssoadmin.ListAccountsForProvisionedPermissionSetOutput{AccountIds: []string{okAcct, deniedAcct, deniedAcct2, goneAcct}}, nil
		},
		"ListAccountAssignments": func(in any) (any, error) {
			switch id := sv(in.(*ssoadmin.ListAccountAssignmentsInput).AccountId); id {
			case deniedAcct, deniedAcct2:
				return nil, deny
			case goneAcct:
				return nil, notFound
			default:
				return &ssoadmin.ListAccountAssignmentsOutput{AccountAssignments: []ssotypes.AccountAssignment{{
					AccountId: sdkaws.String(id), PermissionSetArn: sdkaws.String(stored),
					PrincipalType: ssotypes.PrincipalTypeUser, PrincipalId: sdkaws.String("u-1"),
				}}}, nil
			}
		},
		"ListManagedPoliciesInPermissionSet": func(in any) (any, error) {
			ps := in.(*ssoadmin.ListManagedPoliciesInPermissionSetInput).PermissionSetArn
			record("managed", ps)
			return &ssoadmin.ListManagedPoliciesInPermissionSetOutput{AttachedManagedPolicies: []ssotypes.AttachedManagedPolicy{ssoManagedPolicy("/", "AdministratorAccess")}}, nil
		},
		"ListCustomerManagedPolicyReferencesInPermissionSet": func(in any) (any, error) {
			ps := in.(*ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput).PermissionSetArn
			record("custom", ps)
			return &ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{}, nil
		},
	}
	st := newTestStore(t)
	type tally struct{ denied, other int }
	warnings := map[string]*tally{"sso-admin:DescribePermissionSet": {}, "sso-admin:ListAccountAssignments": {}}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock() // warnings can come from fan-out goroutines
		defer mu.Unlock()
		c, ok := warnings[w.Service]
		if !ok {
			return
		}
		if strings.Contains(w.Message, "AccessDeniedException") {
			c.denied++
		} else {
			c.other++
		}
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(handlers, deny)}

	if _, _, err := scanSSOAdmin(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanSSOAdmin: %v", err)
	}
	if got := *warnings["sso-admin:DescribePermissionSet"]; got != (tally{denied: 1, other: 1}) {
		t.Errorf("DescribePermissionSet warnings=%+v, want one denial and one not-stored warning", got)
	}
	if got := *warnings["sso-admin:ListAccountAssignments"]; got != (tally{denied: 1}) {
		t.Errorf("ListAccountAssignments warnings=%+v, want one denial", got)
	}
	assignments := lmvRows(t, st, TypeSSOAccountAssignment)
	if _, ok := assignments[ssoAssignmentNativeID(stored, okAcct, "USER", "u-1")]; !ok || len(assignments) != 1 {
		t.Errorf("assignment rows=%v, want only account %s's", assignments, okAcct)
	}
	if sets := lmvRows(t, st, TypeSSOPermissionSet); len(sets) != 1 {
		t.Errorf("permission-set rows=%v, want only %s", sets, stored)
	} else if _, ok := sets[stored]; !ok {
		t.Errorf("permission-set rows=%v, want %s", sets, stored)
	}
	slices.Sort(calls)
	if want := []string{"custom/" + stored, "managed/" + stored}; !slices.Equal(calls, want) {
		t.Errorf("policy calls=%v, want %v", calls, want)
	}
	rows := lmvRows(t, st, TypeSSOManagedPolicy)
	if _, ok := rows[stored+"/managed-policy/arn:aws:iam::aws:policy/AdministratorAccess"]; !ok || len(rows) != 1 {
		t.Errorf("managed-policy rows=%v, want only the stored permission set's", rows)
	}
}

// A DescribePermissionSet answer with no PermissionSet stores nothing and is
// reported in the not-stored warning, like one without an ARN.
func TestScanSSOPermissionSets_NoPermissionSetWarns(t *testing.T) {
	ps := ssoTestPermissionSetARN("d")
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: sso:List")
	client := ssoadmin.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListPermissionSets":    func(any) (any, error) { return &ssoadmin.ListPermissionSetsOutput{PermissionSets: []string{ps}}, nil },
		"DescribePermissionSet": func(any) (any, error) { return &ssoadmin.DescribePermissionSetOutput{}, nil },
	}, deny))
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	instances := []ssotypes.InstanceMetadata{{InstanceArn: sdkaws.String(ssoTestInstanceARN)}}

	sets, total, _, err := scanSSOPermissionSets(context.Background(), client, newTestAccount(testAccountID), testRegion, instances, st, testScanID)
	if err != nil || total != 0 || len(sets) != 0 {
		t.Fatalf("sets=%v total=%d err=%v, want none stored and nil", sets, total, err)
	}
	if len(warnings) != 1 || warnings[0].Service != "sso-admin:DescribePermissionSet" {
		t.Errorf("warnings=%v, want one sso-admin:DescribePermissionSet not-stored warning", warnings)
	}
}

// With more keys than the fan-out limit, a throttle on the first call must
// reach the scan as that throttle (demoted to one warning), not as the
// context.Canceled the cancelled siblings see.
func TestScanSSOAdmin_FanOutThrottleDemoted(t *testing.T) {
	const keys = 30 // above fanoutMed (DescribePermissionSet) and fanoutHigh (ListAccountAssignments)
	sets := make([]string, keys)
	accounts := make([]string, keys)
	for i := range keys {
		sets[i] = ssoTestPermissionSetARN(fmt.Sprintf("%02d", i))
		accounts[i] = fmt.Sprintf("2222222222%02d", i)
	}
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	base := func() map[string]func(any) (any, error) {
		return map[string]func(any) (any, error){
			"ListInstances": empty(&ssoadmin.ListInstancesOutput{Instances: []ssotypes.InstanceMetadata{{InstanceArn: sdkaws.String(ssoTestInstanceARN)}}}),
			"ListPermissionSets": func(any) (any, error) {
				return &ssoadmin.ListPermissionSetsOutput{PermissionSets: sets[:1]}, nil
			},
			"DescribePermissionSet": func(in any) (any, error) {
				arn := in.(*ssoadmin.DescribePermissionSetInput).PermissionSetArn
				return &ssoadmin.DescribePermissionSetOutput{PermissionSet: &ssotypes.PermissionSet{PermissionSetArn: arn}}, nil
			},
			"ListAccountsForProvisionedPermissionSet":             empty(&ssoadmin.ListAccountsForProvisionedPermissionSetOutput{}),
			"ListAccountAssignments":                              func(any) (any, error) { return &ssoadmin.ListAccountAssignmentsOutput{}, nil }, // fresh per call: the SDK writes ResultMetadata into it
			"ListApplicationProviders":                            empty(&ssoadmin.ListApplicationProvidersOutput{}),
			"ListApplications":                                    empty(&ssoadmin.ListApplicationsOutput{}),
			"DescribeInstanceAccessControlAttributeConfiguration": empty(&ssoadmin.DescribeInstanceAccessControlAttributeConfigurationOutput{}),
			"ListTrustedTokenIssuers":                             empty(&ssoadmin.ListTrustedTokenIssuersOutput{}),
			"ListRegions":                                         empty(&ssoadmin.ListRegionsOutput{}),
			"ListManagedPoliciesInPermissionSet":                  func(any) (any, error) { return &ssoadmin.ListManagedPoliciesInPermissionSetOutput{}, nil },
			"ListCustomerManagedPolicyReferencesInPermissionSet": func(any) (any, error) {
				return &ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{}, nil
			},
		}
	}
	// firstFails answers op's first call with an error of code and the rest with ok.
	firstFails := func(code string, ok func(any) (any, error)) func(any) (any, error) {
		var once sync.Once
		return func(in any) (any, error) {
			fail := false
			once.Do(func() { fail = true })
			if fail {
				return nil, apiErr(code, "first call")
			}
			return ok(in)
		}
	}
	type setup func(h map[string]func(any) (any, error), code string)
	describe := func(h map[string]func(any) (any, error), code string) {
		h["ListPermissionSets"] = empty(&ssoadmin.ListPermissionSetsOutput{PermissionSets: sets})
		h["DescribePermissionSet"] = firstFails(code, h["DescribePermissionSet"])
	}
	assignments := func(h map[string]func(any) (any, error), code string) {
		h["ListAccountsForProvisionedPermissionSet"] = empty(&ssoadmin.ListAccountsForProvisionedPermissionSetOutput{AccountIds: accounts})
		h["ListAccountAssignments"] = firstFails(code, h["ListAccountAssignments"])
	}
	for _, tc := range []struct {
		name  string
		setup setup
		code  string
	}{
		{name: "DescribePermissionSet throttled", setup: describe, code: "ThrottlingException"},
		{name: "ListAccountAssignments throttled", setup: assignments, code: "ThrottlingException"},
		{name: "DescribePermissionSet hard error", setup: describe, code: "ConflictException"},
		{name: "ListAccountAssignments hard error", setup: assignments, code: "ConflictException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := base()
			tc.setup(h, tc.code)
			st := newTestStore(t)
			var (
				mu       sync.Mutex
				warnings []store.ScanWarning
			)
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				defer mu.Unlock()
				warnings = append(warnings, w)
			}
			deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: sso:List")
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, deny)}

			_, _, err := scanSSOAdmin(context.Background(), acct, testRegion, st, testScanID)
			if tc.code == "ConflictException" {
				if !isAPIErrorCode(err, tc.code) || classifyServiceError(err) != outcomeError || len(warnings) != 0 {
					t.Errorf("err=%v warnings=%v, want the %s itself (outcomeError) and no warning", err, warnings, tc.code)
				}
				return
			}
			if err != nil {
				t.Errorf("err=%v (outcome %v), want the throttle demoted to a warning", err, classifyServiceError(err))
			}
			if len(warnings) != 1 || warnings[0].Service != "sso-admin" || !strings.Contains(warnings[0].Message, tc.code) {
				t.Errorf("warnings=%v, want one sso-admin warning carrying %s", warnings, tc.code)
			}
		})
	}
}
