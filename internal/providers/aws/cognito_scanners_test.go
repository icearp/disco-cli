package aws

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidentitytypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentity/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/icearp/disco-cli/store"
)

// warningsUnder returns the messages of the warnings reported under op.
func warningsUnder(warns []store.ScanWarning, op string) []string {
	var msgs []string
	for _, w := range warns {
		if w.Service == op {
			msgs = append(msgs, w.Message)
		}
	}
	return msgs
}

// assertOneWarningNaming fails unless exactly one warning was reported under
// op and its message names key.
func assertOneWarningNaming(t *testing.T, warns []store.ScanWarning, op, key string) {
	t.Helper()
	msgs := warningsUnder(warns, op)
	if len(msgs) != 1 || !strings.Contains(msgs[0], key) {
		t.Errorf("%s warnings = %q; want one naming %s", op, msgs, key)
	}
}

func scanCogPools(ctx context.Context, f *cogFake, st *store.Store) (cognitoStoredParents, int, int, error) {
	return scanCognitoUserPoolsAndClients(ctx, f.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// assertListedPoolRow fails unless the pool row was built from its
// ListUserPools element: its attributes are that element, with no tags.
func assertListedPoolRow(t *testing.T, row store.Resource, listed cognitoidptypes.UserPoolDescriptionType) {
	t.Helper()
	var got cognitoidptypes.UserPoolDescriptionType
	if err := json.Unmarshal([]byte(row.AttributesJSON), &got); err != nil {
		t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
	}
	if !reflect.DeepEqual(got, listed) || row.TagsJSON != nil || sv(row.Name) != sv(listed.Name) {
		t.Errorf("row = name %q attrs %s tags %v; want the ListUserPools element %+v, no tags", sv(row.Name), row.AttributesJSON, row.TagsJSON, listed)
	}
}

func TestScanCognitoUserPools_StoresDescribedPoolsWithTags(t *testing.T) {
	st := newTestStore(t)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_tagged"), Name: sdkaws.String("listed-name")}}
	f.morePools = [][]cognitoidptypes.UserPoolDescriptionType{{{Id: sdkaws.String("us-east-1_untagged")}}}
	f.poolTags = map[string]map[string]string{"us-east-1_tagged": {"env": "prod", "team": "id"}}

	stored, total, inserted, err := scanCogPools(context.Background(), f, st)
	if err != nil || total != 2 || inserted != 2 {
		t.Fatalf("scan = %d, %d, %v; want 2, 2, nil", total, inserted, err)
	}
	if got := stored.poolIDs(); !reflect.DeepEqual(got, []string{"us-east-1_tagged", "us-east-1_untagged"}) {
		t.Errorf("stored pool IDs = %v; want both pools", got)
	}
	rows := rowsByNativeID(t, st, TypeCognitoUserPool)
	for _, id := range []string{"us-east-1_tagged", "us-east-1_untagged"} {
		row, ok := rows[cogTestPoolARN(testRegion, id)]
		if !ok {
			t.Fatalf("no row %s; got %v", cogTestPoolARN(testRegion, id), rows)
		}
		if sv(row.Name) != id {
			t.Errorf("%s Name = %q; want the described name %q", id, sv(row.Name), id)
		}
		var attrs cognitoidptypes.UserPoolType
		if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
		}
		if want := cogDescribedPool(id, f.poolTags[id]); !reflect.DeepEqual(&attrs, want) {
			t.Errorf("%s attrs = %s; want the DescribeUserPool body %+v", id, row.AttributesJSON, want)
		}
	}
	assertTagsMap(t, rows[cogTestPoolARN(testRegion, "us-east-1_tagged")].TagsJSON, map[string]string{"env": "prod", "team": "id"})
	if tags := rows[cogTestPoolARN(testRegion, "us-east-1_untagged")].TagsJSON; tags != nil {
		t.Errorf("untagged pool TagsJSON = %q; want nil", *tags)
	}
}

func TestScanCognitoUserPools_DescribeFailures(t *testing.T) {
	const pool = "us-east-1_failing"
	listed := cognitoidptypes.UserPoolDescriptionType{Id: sdkaws.String(pool), Name: sdkaws.String("listed-name")}
	for _, tc := range []struct {
		name     string
		code     string
		wantRow  bool
		wantWarn bool
		wantErr  func(error) bool
	}{
		{"denied", "AccessDeniedException", true, true, func(err error) bool { return err == nil }},
		{"not authorized", "NotAuthorizedException", true, true, func(err error) bool { return err == nil }},
		{"transient", "TooManyRequestsException", true, false, isTransientNetworkError},
		{"other", "InvalidParameterException", true, false, func(err error) bool { return isAPIErrorCode(err, "InvalidParameterException") }},
		{"gone", "ResourceNotFoundException", false, false, func(err error) bool { return err == nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newCogFake()
			f.pools = []cognitoidptypes.UserPoolDescriptionType{listed, {Id: sdkaws.String("us-east-1_ok")}}
			f.clients[pool] = []string{"c1"}
			f.errs["DescribeUserPool "+pool] = bcmPCAPIErr(tc.code, "x")

			stored, total, inserted, err := scanCogPools(context.Background(), f, st)
			if !tc.wantErr(err) {
				t.Errorf("err = %v; want %s handled as %s", err, tc.code, tc.name)
			}
			wantTotal := 1
			if tc.wantRow {
				wantTotal = 3 // the failing pool, its client and the other pool
			}
			if total != wantTotal || inserted != wantTotal {
				t.Errorf("counts = %d, %d; want %d, %d", total, inserted, wantTotal, wantTotal)
			}
			rows := rowsByNativeID(t, st, TypeCognitoUserPool)
			if _, ok := rows[cogTestPoolARN(testRegion, "us-east-1_ok")]; !ok {
				t.Errorf("the other pool was not stored")
			}
			row, ok := rows[cogTestPoolARN(testRegion, pool)]
			if ok != tc.wantRow {
				t.Fatalf("failing pool stored = %v; want %v", ok, tc.wantRow)
			}
			_, clientStored := rowsByNativeID(t, st, TypeCognitoAppClient)[cogTestClientARN(pool, "c1")]
			if clientStored != tc.wantRow {
				t.Errorf("client stored = %v; want %v", clientStored, tc.wantRow)
			}
			if inStored := slices.ContainsFunc(stored.pools, func(p childParent) bool { return p.id == pool }); inStored != tc.wantRow {
				t.Errorf("pool in stored parents = %v; want %v", inStored, tc.wantRow)
			}
			if tc.wantRow {
				assertListedPoolRow(t, row, listed)
			}
			if tc.wantWarn {
				assertOneWarningNaming(t, *warns, "cognito-idp:DescribeUserPool", pool)
			} else if msgs := warningsUnder(*warns, "cognito-idp:DescribeUserPool"); len(msgs) != 0 {
				t.Errorf("DescribeUserPool warnings = %q; want none", msgs)
			}
		})
	}
}

// TestScanCognitoUserPools_DeniedCodesShareOneWarning pins that both denial
// codes DescribeUserPool models are tallied together.
func TestScanCognitoUserPools_DeniedCodesShareOneWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cognito-idp:DescribeUserPool")
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_a")}, {Id: sdkaws.String("us-east-1_b")}}
	f.errs["DescribeUserPool us-east-1_a"] = bcmPCAPIErr("AccessDeniedException", "x")
	f.errs["DescribeUserPool us-east-1_b"] = bcmPCAPIErr("NotAuthorizedException", "x")
	if _, total, _, err := scanCogPools(context.Background(), f, st); err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	if *warns != 1 {
		t.Errorf("DescribeUserPool warnings = %d; want 1", *warns)
	}
}

func TestScanCognitoUserPools_ListFailures(t *testing.T) {
	page0 := cognitoidptypes.UserPoolDescriptionType{Id: sdkaws.String("us-east-1_first")}
	for _, tc := range []struct {
		name      string
		failToken string
		code      string
		wantTotal int
		wantWarns int
		wantCode  string
	}{
		{"first page denied", "", "AccessDeniedException", 0, 1, ""},
		{"later page denied", "1", "AccessDeniedException", 1, 1, ""},
		{"later page fails", "1", "InvalidParameterException", 1, 0, "InvalidParameterException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newCogFake()
			f.pools = []cognitoidptypes.UserPoolDescriptionType{page0}
			f.morePools = [][]cognitoidptypes.UserPoolDescriptionType{{{Id: sdkaws.String("us-east-1_second")}}}
			f.errs["ListUserPools "+tc.failToken] = bcmPCAPIErr(tc.code, "x")

			_, total, inserted, err := scanCogPools(context.Background(), f, st)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if total != tc.wantTotal || inserted != tc.wantTotal {
				t.Errorf("counts = %d, %d; want %d", total, inserted, tc.wantTotal)
			}
			if got := len(warningsUnder(*warns, "cognito-idp:ListUserPools")); got != tc.wantWarns {
				t.Errorf("ListUserPools warnings = %d; want %d", got, tc.wantWarns)
			}
		})
	}
}

func TestScanCognitoUserPools_EmptyIDWarns(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Name: sdkaws.String("pool-without-id")}, {Id: sdkaws.String("us-east-1_ok")}}
	if _, total, inserted, err := scanCogPools(context.Background(), f, st); err != nil || total != 1 || inserted != 1 {
		t.Fatalf("scan = %d, %d, %v; want 1, 1, nil", total, inserted, err)
	}
	assertOneWarningNaming(t, *warns, "cognito-idp:ListUserPools", "pool-without-id")
}

// TestScanCognitoUserPools_StoreWriteFailureStops pins that a failed write
// returns the store error, no parents and no warning claiming rows.
func TestScanCognitoUserPools_StoreWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	refuseStoreWrites(t, st, TypeCognitoUserPool)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_denied")}, {Id: sdkaws.String("us-east-1_slow")}, {Id: sdkaws.String("us-east-1_empty")}}
	f.errs["DescribeUserPool us-east-1_denied"] = bcmPCAPIErr("AccessDeniedException", "x")
	f.errs["DescribeUserPool us-east-1_slow"] = bcmPCAPIErr("TooManyRequestsException", "x")
	f.bodiless = "us-east-1_empty"
	stored, total, inserted, err := scanCogPools(context.Background(), f, st)
	if !errors.Is(err, store.ErrStoreWrite) || isTransientNetworkError(err) {
		t.Errorf("err = %v; want only the store write failure", err)
	}
	if total != 0 || inserted != 0 || len(stored.pools) != 0 {
		t.Errorf("scan = %d, %d, %v; want 0, 0, no parents", total, inserted, stored.pools)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v; want none", *warns)
	}
}

// TestScanCognitoUserPools_ContextStop pins that once ctx ends no further pool
// is read and the stop is returned.
func TestScanCognitoUserPools_ContextStop(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_a")}}
	f.afterList = cancel
	_, total, _, err := scanCogPools(ctx, f, st)
	if !errors.Is(err, context.Canceled) || total != 0 {
		t.Errorf("scan = %d, %v; want 0 and the cancellation", total, err)
	}
}

// idPoolFake answers the Cognito identity-pool ops; an op it has no handler
// for is denied. Its maps are read-only once a scan starts.
type idPoolFake struct {
	pages     [][]cognitoidentitytypes.IdentityPoolShortDescription
	tags      map[string]map[string]string
	errs      map[string]error // "<Op> <pool ID or page token>"
	afterList func()
}

func newIDPoolFake(pages ...[]cognitoidentitytypes.IdentityPoolShortDescription) *idPoolFake {
	return &idPoolFake{pages: pages, tags: map[string]map[string]string{}, errs: map[string]error{}}
}

func idPoolShort(id string) cognitoidentitytypes.IdentityPoolShortDescription {
	return cognitoidentitytypes.IdentityPoolShortDescription{IdentityPoolId: sdkaws.String(id), IdentityPoolName: sdkaws.String("listed-" + id)}
}

func idPoolRoleARN(id string) string { return "arn:aws:iam::" + testAccountID + ":role/auth-" + id }

func idPoolNativeID(id string) string {
	return "arn:aws:cognito-identity:" + testRegion + ":" + testAccountID + ":identitypool/" + id
}

// idPoolDescribed is the DescribeIdentityPool body the fake returns for id.
func idPoolDescribed(id string, tags map[string]string) cognitoidentity.DescribeIdentityPoolOutput {
	return cognitoidentity.DescribeIdentityPoolOutput{
		IdentityPoolId: sdkaws.String(id), IdentityPoolName: sdkaws.String(id),
		AllowUnauthenticatedIdentities: true, IdentityPoolTags: tags,
	}
}

func (f *idPoolFake) client() *cognitoidentity.Client {
	return cognitoidentity.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListIdentityPools": func(in any) (any, error) {
			i := in.(*cognitoidentity.ListIdentityPoolsInput)
			if sdkaws.ToInt32(i.MaxResults) != 60 {
				return nil, bcmPCAPIErr("InvalidParameterException", "MaxResults must be the documented cap 60")
			}
			if err := f.errs["ListIdentityPools "+sv(i.NextToken)]; err != nil {
				return nil, err
			}
			if f.afterList != nil {
				defer f.afterList()
			}
			page, next := cogPage(f.pages, sv(i.NextToken))
			return &cognitoidentity.ListIdentityPoolsOutput{IdentityPools: page, NextToken: next}, nil
		},
		"DescribeIdentityPool": func(in any) (any, error) {
			id := sv(in.(*cognitoidentity.DescribeIdentityPoolInput).IdentityPoolId)
			if err := f.errs["DescribeIdentityPool "+id]; err != nil {
				return nil, err
			}
			out := idPoolDescribed(id, f.tags[id])
			return &out, nil
		},
		"GetIdentityPoolRoles": func(in any) (any, error) {
			id := sv(in.(*cognitoidentity.GetIdentityPoolRolesInput).IdentityPoolId)
			if err := f.errs["GetIdentityPoolRoles "+id]; err != nil {
				return nil, err
			}
			return &cognitoidentity.GetIdentityPoolRolesOutput{IdentityPoolId: &id, Roles: map[string]string{"authenticated": idPoolRoleARN(id)}}, nil
		},
	}, bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: cognito-identity:Op")))
}

func scanIDPools(ctx context.Context, f *idPoolFake, st *store.Store) ([]string, int, int, error) {
	return scanCognitoIdentityPools(ctx, f.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// idPoolStoredAttrs decodes a stored identity-pool row's attributes, Pool
// left raw for the caller to decode as the shape it expects.
func idPoolStoredAttrs(t *testing.T, row store.Resource) (pool json.RawMessage, roles map[string]string) {
	t.Helper()
	var attrs struct {
		Pool  json.RawMessage
		Roles map[string]string
	}
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
	}
	return attrs.Pool, attrs.Roles
}

func TestScanCognitoIdentityPools_StoresDescribedPoolsWithTags(t *testing.T) {
	st := newTestStore(t)
	f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:tagged")},
		[]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:untagged")})
	f.tags["us-east-1:tagged"] = map[string]string{"env": "prod"}

	ids, total, inserted, err := scanIDPools(context.Background(), f, st)
	if err != nil || total != 2 || inserted != 2 || !reflect.DeepEqual(ids, []string{"us-east-1:tagged", "us-east-1:untagged"}) {
		t.Fatalf("scan = %v, %d, %d, %v; want both pools, 2, 2, nil", ids, total, inserted, err)
	}
	rows := rowsByNativeID(t, st, TypeCognitoIdentityPool)
	for _, id := range ids {
		row, ok := rows[idPoolNativeID(id)]
		if !ok {
			t.Fatalf("no identity-pool row %s", idPoolNativeID(id))
		}
		if sv(row.Name) != id {
			t.Errorf("%s Name = %q; want the described name", id, sv(row.Name))
		}
		if strings.Contains(row.AttributesJSON, "ResultMetadata") {
			t.Errorf("attrs carry the SDK's ResultMetadata envelope: %s", row.AttributesJSON)
		}
		rawPool, roles := idPoolStoredAttrs(t, row)
		var pool cognitoidentity.DescribeIdentityPoolOutput
		if err := json.Unmarshal(rawPool, &pool); err != nil {
			t.Fatalf("Pool %s: %v", rawPool, err)
		}
		if want := idPoolDescribed(id, f.tags[id]); !reflect.DeepEqual(pool, want) {
			t.Errorf("%s Pool = %s; want the DescribeIdentityPool body %+v", id, rawPool, want)
		}
		if want := map[string]string{"authenticated": idPoolRoleARN(id)}; !reflect.DeepEqual(roles, want) {
			t.Errorf("%s Roles = %v; want %v", id, roles, want)
		}
	}
	assertTagsMap(t, rows[idPoolNativeID("us-east-1:tagged")].TagsJSON, map[string]string{"env": "prod"})
	if tags := rows[idPoolNativeID("us-east-1:untagged")].TagsJSON; tags != nil {
		t.Errorf("untagged pool TagsJSON = %q; want nil", *tags)
	}
}

func TestScanCognitoIdentityPools_ReadFailures(t *testing.T) {
	const pool = "us-east-1:failing"
	for _, tc := range []struct {
		name, op, code string
		wantRow        bool
		wantWarn       bool
		wantErr        func(error) bool
	}{
		{"describe denied", "DescribeIdentityPool", "AccessDeniedException", true, true, func(err error) bool { return err == nil }},
		{"describe not authorized", "DescribeIdentityPool", "NotAuthorizedException", true, true, func(err error) bool { return err == nil }},
		{"describe transient", "DescribeIdentityPool", "TooManyRequestsException", true, false, isTransientNetworkError},
		{"describe other", "DescribeIdentityPool", "InvalidParameterException", true, false, func(err error) bool { return isAPIErrorCode(err, "InvalidParameterException") }},
		{"describe gone", "DescribeIdentityPool", "ResourceNotFoundException", false, false, func(err error) bool { return err == nil }},
		{"roles denied", "GetIdentityPoolRoles", "NotAuthorizedException", true, true, func(err error) bool { return err == nil }},
		{"roles transient", "GetIdentityPoolRoles", "TooManyRequestsException", true, false, isTransientNetworkError},
		{"roles other", "GetIdentityPoolRoles", "ResourceConflictException", true, false, func(err error) bool { return isAPIErrorCode(err, "ResourceConflictException") }},
		{"roles gone", "GetIdentityPoolRoles", "ResourceNotFoundException", false, false, func(err error) bool { return err == nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort(pool), idPoolShort("us-east-1:ok")})
			f.tags[pool] = map[string]string{"env": "prod"}
			f.errs[tc.op+" "+pool] = bcmPCAPIErr(tc.code, "x")

			ids, total, inserted, err := scanIDPools(context.Background(), f, st)
			if !tc.wantErr(err) {
				t.Errorf("err = %v; want %s handled as %s", err, tc.code, tc.name)
			}
			wantIDs := []string{"us-east-1:ok"}
			if tc.wantRow {
				wantIDs = []string{pool, "us-east-1:ok"}
			}
			if !reflect.DeepEqual(ids, wantIDs) || total != len(wantIDs) || inserted != len(wantIDs) {
				t.Errorf("scan = %v, %d, %d; want %v and matching counts", ids, total, inserted, wantIDs)
			}
			row, ok := rowsByNativeID(t, st, TypeCognitoIdentityPool)[idPoolNativeID(pool)]
			if ok != tc.wantRow {
				t.Fatalf("failing pool stored = %v; want %v", ok, tc.wantRow)
			}
			op := "cognito-identity:" + tc.op
			if tc.wantWarn {
				assertOneWarningNaming(t, *warns, op, pool)
			} else if msgs := warningsUnder(*warns, op); len(msgs) != 0 {
				t.Errorf("%s warnings = %q; want none", op, msgs)
			}
			if !tc.wantRow {
				return
			}
			rawPool, roles := idPoolStoredAttrs(t, row)
			if tc.op == "DescribeIdentityPool" {
				var listed cognitoidentitytypes.IdentityPoolShortDescription
				if err := json.Unmarshal(rawPool, &listed); err != nil || !reflect.DeepEqual(listed, idPoolShort(pool)) {
					t.Errorf("Pool = %s; want the ListIdentityPools element", rawPool)
				}
				if row.TagsJSON != nil || sv(row.Name) != "listed-"+pool {
					t.Errorf("row name %q tags %v; want the listed name, no tags", sv(row.Name), row.TagsJSON)
				}
				if roles["authenticated"] != idPoolRoleARN(pool) {
					t.Errorf("Roles = %v; want the role map", roles)
				}
				return
			}
			if roles != nil {
				t.Errorf("Roles = %v; want none", roles)
			}
			assertTagsMap(t, row.TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

func TestScanCognitoIdentityPools_ListFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failToken string
		code      string
		wantTotal int
		wantWarns int
		wantCode  string
	}{
		{"first page denied", "", "NotAuthorizedException", 0, 1, ""},
		{"later page denied", "1", "AccessDeniedException", 1, 1, ""},
		{"later page fails", "1", "InvalidParameterException", 1, 0, "InvalidParameterException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:first")},
				[]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:second")})
			f.errs["ListIdentityPools "+tc.failToken] = bcmPCAPIErr(tc.code, "x")

			_, total, inserted, err := scanIDPools(context.Background(), f, st)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if total != tc.wantTotal || inserted != tc.wantTotal {
				t.Errorf("counts = %d, %d; want %d", total, inserted, tc.wantTotal)
			}
			if got := len(warningsUnder(*warns, "cognito-identity:ListIdentityPools")); got != tc.wantWarns {
				t.Errorf("ListIdentityPools warnings = %d; want %d", got, tc.wantWarns)
			}
		})
	}
}

func TestScanCognitoIdentityPools_EmptyIDWarns(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{
		{IdentityPoolName: sdkaws.String("pool-without-id")}, idPoolShort("us-east-1:ok"),
	})
	ids, total, inserted, err := scanIDPools(context.Background(), f, st)
	if err != nil || total != 1 || inserted != 1 || !reflect.DeepEqual(ids, []string{"us-east-1:ok"}) {
		t.Fatalf("scan = %v, %d, %d, %v; want [us-east-1:ok], 1, 1, nil", ids, total, inserted, err)
	}
	assertOneWarningNaming(t, *warns, "cognito-identity:ListIdentityPools", "pool-without-id")
}

func TestScanCognitoIdentityPools_StoreWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	refuseStoreWrites(t, st, TypeCognitoIdentityPool)
	f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:denied"), idPoolShort("us-east-1:slow")})
	f.errs["DescribeIdentityPool us-east-1:denied"] = bcmPCAPIErr("AccessDeniedException", "x")
	f.errs["GetIdentityPoolRoles us-east-1:slow"] = bcmPCAPIErr("TooManyRequestsException", "x")
	ids, total, inserted, err := scanIDPools(context.Background(), f, st)
	if !errors.Is(err, store.ErrStoreWrite) || isTransientNetworkError(err) {
		t.Errorf("err = %v; want only the store write failure", err)
	}
	if total != 0 || inserted != 0 || ids != nil {
		t.Errorf("scan = %v, %d, %d; want no ids, 0, 0", ids, total, inserted)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v; want none", *warns)
	}
}

func TestScanCognitoIdentityPools_ContextStop(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:a")})
	f.afterList = cancel
	ids, total, _, err := scanIDPools(ctx, f, st)
	if !errors.Is(err, context.Canceled) || total != 0 || len(ids) != 0 {
		t.Errorf("scan = %v, %d, %v; want nothing and the cancellation", ids, total, err)
	}
}

// TestScanCognitoAll_IdentityPoolsRunAfterPoolPhaseFailure pins that a hard
// user-pool phase error neither skips the identity-pool phase nor is lost.
func TestScanCognitoAll_IdentityPoolsRunAfterPoolPhaseFailure(t *testing.T) {
	st := newTestStore(t)
	f := newCogFake()
	f.errs["ListUserPools "] = bcmPCAPIErr("InvalidParameterException", "bad")
	idf := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:a")})
	total, inserted, err := scanCognitoAll(context.Background(), f.client(), idf.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
	// The identity pool and the role attachment the extended phase reads for it.
	if !isAPIErrorCode(err, "InvalidParameterException") || total != 2 || inserted != 2 {
		t.Errorf("scan = %d, %d, %v; want 2, 2 and the ListUserPools InvalidParameterException", total, inserted, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeCognitoIdentityPool)[idPoolNativeID("us-east-1:a")]; !ok {
		t.Errorf("identity pool not stored")
	}
}

// TestScanCognitoAll_StoreWriteFailureStopsLaterPhases pins that a failed
// write in either early phase stops the scan with the store error.
func TestScanCognitoAll_StoreWriteFailureStopsLaterPhases(t *testing.T) {
	for _, refused := range []string{TypeCognitoUserPool, TypeCognitoIdentityPool} {
		t.Run(refused, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, refused)
			f := cogTopLevelFake()
			idf := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:a")})
			_, _, err := scanCognitoAll(context.Background(), f.client(), idf.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v; want the store write failure", err)
			}
			if calls := f.calls.distinct(); len(calls) != 0 {
				t.Errorf("child calls = %v; want none after the failed write", calls)
			}
			_, idStored := rowsByNativeID(t, st, TypeCognitoIdentityPool)[idPoolNativeID("us-east-1:a")]
			if idStored {
				t.Errorf("identity pool stored after the failed write")
			}
		})
	}
}

// TestScanCognitoUserPools_ClientListingErrorKeepsPool pins that a pool whose
// ListUserPoolClients fails is still stored and the error is returned.
func TestScanCognitoUserPools_ClientListingErrorKeepsPool(t *testing.T) {
	st := newTestStore(t)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_failing")}, {Id: sdkaws.String("us-east-1_ok")}}
	f.clients["us-east-1_ok"] = []string{"c1"}
	f.errs["ListUserPoolClients us-east-1_failing"] = bcmPCAPIErr("InvalidParameterException", "x")

	stored, total, inserted, err := scanCogPools(context.Background(), f, st)
	if !isAPIErrorCode(err, "InvalidParameterException") {
		t.Errorf("err = %v; want the ListUserPoolClients InvalidParameterException", err)
	}
	if total != 3 || inserted != 3 || len(stored.pools) != 2 {
		t.Errorf("scan = %d, %d, %d stored pools; want 3, 3 (both pools and c1), 2", total, inserted, len(stored.pools))
	}
	if _, ok := rowsByNativeID(t, st, TypeCognitoUserPool)[cogTestPoolARN(testRegion, "us-east-1_failing")]; !ok {
		t.Errorf("the pool whose client listing failed was not stored")
	}
}

// TestScanCognitoUserPools_TransientDemotedBesideHardError pins that a
// transient DescribeUserPool tally is warned under its op, not buried in a
// hard error of the same phase.
func TestScanCognitoUserPools_TransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_slow")}, {Id: sdkaws.String("us-east-1_bad")}}
	f.errs["DescribeUserPool us-east-1_slow"] = bcmPCAPIErr("TooManyRequestsException", "x")
	f.errs["DescribeUserPool us-east-1_bad"] = bcmPCAPIErr("InvalidParameterException", "x")

	_, total, inserted, err := scanCogPools(context.Background(), f, st)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "InvalidParameterException") || isAPIErrorCode(err, "TooManyRequestsException") {
		t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with only the InvalidParameterException", got, err, outcomeError)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("counts = %d, %d; want 2, 2", total, inserted)
	}
	assertOneWarningNaming(t, *warns, "cognito-idp:DescribeUserPool", "us-east-1_slow")
}

func TestScanCognitoIdentityPools_TransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newIDPoolFake([]cognitoidentitytypes.IdentityPoolShortDescription{idPoolShort("us-east-1:slow"), idPoolShort("us-east-1:bad")})
	f.errs["DescribeIdentityPool us-east-1:slow"] = bcmPCAPIErr("TooManyRequestsException", "x")
	f.errs["GetIdentityPoolRoles us-east-1:bad"] = bcmPCAPIErr("ResourceConflictException", "x")

	_, total, inserted, err := scanIDPools(context.Background(), f, st)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ResourceConflictException") || isAPIErrorCode(err, "TooManyRequestsException") {
		t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with only the ResourceConflictException", got, err, outcomeError)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("counts = %d, %d; want 2, 2", total, inserted)
	}
	assertOneWarningNaming(t, *warns, "cognito-identity:DescribeIdentityPool", "us-east-1:slow")
}

// TestScanCognitoUserPools_NoBodyKeptAndWarned pins that a DescribeUserPool
// answer without a UserPool keeps the listed element as the row and warns once.
func TestScanCognitoUserPools_NoBodyKeptAndWarned(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	listed := cognitoidptypes.UserPoolDescriptionType{Id: sdkaws.String("us-east-1_empty"), Name: sdkaws.String("listed-name")}
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{listed, {Id: sdkaws.String("us-east-1_ok")}}
	f.bodiless = "us-east-1_empty"

	_, total, inserted, err := scanCogPools(context.Background(), f, st)
	if err != nil || total != 2 || inserted != 2 {
		t.Fatalf("scan = %d, %d, %v; want 2, 2, nil", total, inserted, err)
	}
	row, ok := rowsByNativeID(t, st, TypeCognitoUserPool)[cogTestPoolARN(testRegion, "us-east-1_empty")]
	if !ok {
		t.Fatalf("the pool without a DescribeUserPool body was not stored")
	}
	assertListedPoolRow(t, row, listed)
	assertOneWarningNaming(t, *warns, "cognito-idp:DescribeUserPool", "us-east-1_empty")
	for _, w := range *warns {
		if want := testAccountID + "/" + testRegion; w.Service == "cognito-idp:DescribeUserPool" && w.Scope != want {
			t.Errorf("warning Scope = %q; want %q", w.Scope, want)
		}
	}
}

// TestScanCognitoUserPools_ClientTransientWarnedUnderItsOp pins that a
// transient ListUserPoolClients error beside a hard error is warned under
// cognito-idp:ListUserPoolClients, not under the pool listing.
func TestScanCognitoUserPools_ClientTransientWarnedUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{{Id: sdkaws.String("us-east-1_slow")}, {Id: sdkaws.String("us-east-1_bad")}}
	f.errs["ListUserPoolClients us-east-1_slow"] = bcmPCAPIErr("TooManyRequestsException", "x")
	f.errs["DescribeUserPool us-east-1_bad"] = bcmPCAPIErr("InvalidParameterException", "x")

	_, total, inserted, err := scanCogPools(context.Background(), f, st)
	if got := classifyServiceError(err); got != outcomeError || isAPIErrorCode(err, "TooManyRequestsException") {
		t.Errorf("outcome = %d, err = %v; want outcomeError (%d) without the throttle", got, err, outcomeError)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("counts = %d, %d; want 2, 2", total, inserted)
	}
	assertOneWarningNaming(t, *warns, "cognito-idp:ListUserPoolClients", "us-east-1_slow")
	if msgs := warningsUnder(*warns, "cognito-idp:ListUserPools"); len(msgs) != 0 {
		t.Errorf("ListUserPools warnings = %q; want none", msgs)
	}
}
