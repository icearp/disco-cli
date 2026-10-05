package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
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

			total, _, err := tc.run(context.Background(), stub, st, parents)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 4 {
				t.Errorf("total=%d, want 4 (two pages of a, key-less item skipped, plus b)", total)
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
				if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != key {
					t.Errorf("%s: type=%s region=%s name=%s, want %s %s %s", id, r.Type, sv(r.Region), sv(r.Name), tc.typ, testRegion, key)
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
		"ListInstances": one(&ssoadmin.ListInstancesOutput{Instances: []ssotypes.InstanceMetadata{{InstanceArn: sdkaws.String(ssoTestInstanceARN)}}}),
		"ListPermissionSets": {
			{Output: &ssoadmin.ListPermissionSetsOutput{}},
			{Output: &ssoadmin.ListPermissionSetsOutput{}},
		},
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
func TestScanSSOAdmin_NewPhasesWiredAndOrdered(t *testing.T) {
	type row struct{ typ, nativeID string }
	app := ssoTestAppARN("a")
	application := row{TypeSSOApplication, app}
	issuer := row{TypeSSOTrustedTokenIssuer, "arn:aws:sso::" + testAccountID + ":trustedTokenIssuer/ssoins-1111/tti-1"}
	scope := row{TypeSSOApplicationAccessScope, app + "/access-scope/sso:account:access"}
	method := row{TypeSSOApplicationAuthenticationMethod, app + "/authentication-method/IAM"}
	grant := row{TypeSSOApplicationGrant, app + "/grant/urn:ietf:params:oauth:grant-type:jwt-bearer"}
	region := row{TypeSSORegion, ssoTestInstanceARN + "/region/us-east-1"}
	all := []row{application, issuer, scope, method, grant, region}
	for _, tc := range []struct {
		name    string
		failOp  string
		want    []row
		wantErr bool
	}{
		{name: "all succeed", want: all},
		{name: "regions fail", failOp: "ListRegions", want: []row{application, issuer}, wantErr: true},
		{name: "access scopes fail", failOp: "ListApplicationAccessScopes", want: []row{application, issuer, region}, wantErr: true},
		{name: "grants fail", failOp: "ListApplicationGrants", want: []row{application, issuer, region, scope, method}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			responses := ssoTopLevelResponses()
			if tc.failOp != "" {
				responses[tc.failOp] = []stubCall{{Err: apiErr("InternalServerException", "bad")}}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: sdkaws.Config{
				Region:           testRegion,
				Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
				RetryMaxAttempts: 1,
				APIOptions:       []func(*smithymw.Stack) error{stubResponses(t, responses)},
			}}

			_, _, err := scanSSOAdmin(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantErr != isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("scanSSOAdmin err=%v, wantErr=%t", err, tc.wantErr)
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
