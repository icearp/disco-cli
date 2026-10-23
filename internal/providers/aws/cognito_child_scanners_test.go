package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	cognitoidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

// cogFake answers the Cognito user-pool ops through opStubConfig; an op it has
// no handler for is denied. Handlers run on fan-out goroutines, so the maps are
// read-only once a scan starts and calls is locked.
type cogFake struct {
	calls parentCalls
	errs  map[string]error // "<Op> <key>"

	pools        []cognitoidptypes.UserPoolDescriptionType   // ListUserPools page 0
	morePools    [][]cognitoidptypes.UserPoolDescriptionType // pages 1..n
	poolTags     map[string]map[string]string                // pool ID → UserPoolTags
	afterList    func()                                      // runs as ListUserPools returns
	bodiless     string                                      // pool whose DescribeUserPool has no UserPool
	clients      map[string][]string                         // pool ID → client IDs
	confidential map[string]bool                             // client ID → has a secret
	replicas     map[string][][]cognitoidptypes.UserPoolReplicaType
	secrets      map[string][][]cognitoidptypes.ClientSecretDescriptorType // client ID → pages
	groupsErr    error
	// repeatToken makes the two child ops answer every page after the first
	// with no items and the token they were given.
	repeatToken bool
}

func newCogFake() *cogFake {
	return &cogFake{
		errs:         map[string]error{},
		clients:      map[string][]string{},
		confidential: map[string]bool{},
		replicas:     map[string][][]cognitoidptypes.UserPoolReplicaType{},
		secrets:      map[string][][]cognitoidptypes.ClientSecretDescriptorType{},
	}
}

// cogPage returns page token of pages (token "" is page 0) and the next token.
func cogPage[T any](pages [][]T, token string) ([]T, *string) {
	i := 0
	if token != "" {
		i, _ = strconv.Atoi(token)
	}
	if i >= len(pages) {
		return nil, nil
	}
	var next *string
	if i+1 < len(pages) {
		next = sdkaws.String(strconv.Itoa(i + 1))
	}
	return pages[i], next
}

func cogTestPoolARN(region, poolID string) string {
	return "arn:aws:cognito-idp:" + region + ":" + testAccountID + ":userpool/" + poolID
}

func cogTestClientARN(poolID, clientID string) string {
	return cogTestPoolARN(testRegion, poolID) + "/client/" + clientID
}

func (f *cogFake) client() *cognitoidp.Client {
	handlers := map[string]func(in any) (any, error){
		"ListUserPools": func(in any) (any, error) {
			i := in.(*cognitoidp.ListUserPoolsInput)
			if sdkaws.ToInt32(i.MaxResults) != 60 {
				return nil, bcmPCAPIErr("InvalidParameterException", "MaxResults must be the documented cap 60")
			}
			if err := f.errs["ListUserPools "+sv(i.NextToken)]; err != nil {
				return nil, err
			}
			if f.afterList != nil {
				defer f.afterList()
			}
			page, next := cogPage(append([][]cognitoidptypes.UserPoolDescriptionType{f.pools}, f.morePools...), sv(i.NextToken))
			return &cognitoidp.ListUserPoolsOutput{UserPools: page, NextToken: next}, nil
		},
		"DescribeUserPool": func(in any) (any, error) {
			id := sv(in.(*cognitoidp.DescribeUserPoolInput).UserPoolId)
			if err := f.errs["DescribeUserPool "+id]; err != nil {
				return nil, err
			}
			if id == f.bodiless {
				return &cognitoidp.DescribeUserPoolOutput{}, nil
			}
			return &cognitoidp.DescribeUserPoolOutput{UserPool: cogDescribedPool(id, f.poolTags[id])}, nil
		},
		"ListUserPoolClients": func(in any) (any, error) {
			poolID := sv(in.(*cognitoidp.ListUserPoolClientsInput).UserPoolId)
			if err := f.errs["ListUserPoolClients "+poolID]; err != nil {
				return nil, err
			}
			var out []cognitoidptypes.UserPoolClientDescription
			for _, c := range f.clients[poolID] {
				out = append(out, cognitoidptypes.UserPoolClientDescription{ClientId: sdkaws.String(c)})
			}
			return &cognitoidp.ListUserPoolClientsOutput{UserPoolClients: out}, nil
		},
		"DescribeUserPoolClient": func(in any) (any, error) {
			id := sv(in.(*cognitoidp.DescribeUserPoolClientInput).ClientId)
			c := &cognitoidptypes.UserPoolClientType{ClientId: &id, ClientName: &id}
			if f.confidential[id] {
				c.ClientSecret = sdkaws.String("not-a-real-secret")
			}
			return &cognitoidp.DescribeUserPoolClientOutput{UserPoolClient: c}, nil
		},
		"ListGroups": func(any) (any, error) {
			if f.groupsErr != nil {
				return nil, f.groupsErr
			}
			return &cognitoidp.ListGroupsOutput{}, nil
		},
		"ListUserPoolReplicas": func(in any) (any, error) {
			i := in.(*cognitoidp.ListUserPoolReplicasInput)
			key := sv(i.UserPoolId)
			f.calls.add("ListUserPoolReplicas " + key)
			if err := f.errs["ListUserPoolReplicas "+key]; err != nil {
				return nil, err
			}
			if f.repeatToken && i.NextToken != nil {
				return &cognitoidp.ListUserPoolReplicasOutput{NextToken: i.NextToken}, nil
			}
			page, next := cogPage(f.replicas[key], sv(i.NextToken))
			return &cognitoidp.ListUserPoolReplicasOutput{UserPoolReplicas: page, NextToken: next}, nil
		},
		"ListUserPoolClientSecrets": func(in any) (any, error) {
			i := in.(*cognitoidp.ListUserPoolClientSecretsInput)
			key := sv(i.UserPoolId) + "/" + sv(i.ClientId)
			f.calls.add("ListUserPoolClientSecrets " + key)
			if err := f.errs["ListUserPoolClientSecrets "+key]; err != nil {
				return nil, err
			}
			if f.repeatToken && i.NextToken != nil {
				return &cognitoidp.ListUserPoolClientSecretsOutput{NextToken: i.NextToken}, nil
			}
			page, next := cogPage(f.secrets[sv(i.ClientId)], sv(i.NextToken))
			return &cognitoidp.ListUserPoolClientSecretsOutput{ClientSecrets: page, NextToken: next}, nil
		},
	}
	return cognitoidp.NewFromConfig(opStubConfig(handlers, bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: cognito-idp:Op")))
}

// cogDescribedPool is the DescribeUserPool body the fake returns for id.
func cogDescribedPool(id string, tags map[string]string) *cognitoidptypes.UserPoolType {
	return &cognitoidptypes.UserPoolType{
		Id: sdkaws.String(id), Name: sdkaws.String(id), Arn: sdkaws.String(cogTestPoolARN(testRegion, id)),
		MfaConfiguration: cognitoidptypes.UserPoolMfaTypeOptional, UserPoolTags: tags,
	}
}

func cogIdentityClient() *cognitoidentity.Client {
	return cognitoidentity.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListIdentityPools": func(any) (any, error) { return &cognitoidentity.ListIdentityPoolsOutput{}, nil },
	}, bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: cognito-identity:Op")))
}

func cogReplica(region string, role cognitoidptypes.ReplicaRoleType, status cognitoidptypes.ReplicaStatusType) cognitoidptypes.UserPoolReplicaType {
	return cognitoidptypes.UserPoolReplicaType{
		RegionName: sdkaws.String(region), Role: role, Status: status,
		UserPoolArn: sdkaws.String(cogTestPoolARN(region, "pool")),
	}
}

func cogSecret(id string) cognitoidptypes.ClientSecretDescriptorType {
	return cognitoidptypes.ClientSecretDescriptorType{
		ClientSecretId:         sdkaws.String(id),
		ClientSecretCreateDate: sdkaws.Time(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)),
	}
}

// cogChildPhase drives one of the two child phases over parents keyed p1, p2, ….
type cogChildPhase struct {
	op         string
	typ        string
	parentType string
	// warnCodes are the codes, besides a denial and a throttle, that skip a
	// parent with one warning.
	warnCodes []string
	parentARN func(key string) string
	callKey   func(key string) string
	seedOK    func(f *cogFake, key string) (native string)
	run       func(f *cogFake, st *store.Store, keys []string) (int, int, error)
}

func cogChildPhases() []cogChildPhase {
	return []cogChildPhase{
		{
			op: "ListUserPoolReplicas", typ: TypeCognitoUserPoolReplica, parentType: TypeCognitoUserPool,
			warnCodes: []string{"OperationNotEnabledException"},
			parentARN: func(key string) string { return cogTestPoolARN(testRegion, key) },
			callKey:   func(key string) string { return key },
			seedOK: func(f *cogFake, key string) string {
				f.replicas[key] = [][]cognitoidptypes.UserPoolReplicaType{{
					cogReplica(testRegion, cognitoidptypes.ReplicaRoleTypePrimary, cognitoidptypes.ReplicaStatusTypeActive),
					cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive),
				}}
				return cogTestPoolARN(testRegion, key) + "/replica/us-west-2"
			},
			run: func(f *cogFake, st *store.Store, keys []string) (int, int, error) {
				var pools []childParent
				for _, k := range keys {
					pools = append(pools, childParent{id: k, arn: cogTestPoolARN(testRegion, k)})
				}
				return scanCognitoUserPoolReplicas(context.Background(), f.client(), newTestAccount(testAccountID), testRegion, st, testScanID, pools)
			},
		},
		{
			op: "ListUserPoolClientSecrets", typ: TypeCognitoUserPoolClientSecret, parentType: TypeCognitoAppClient,
			parentARN: func(key string) string { return cogTestClientARN("pool", key) },
			callKey:   func(key string) string { return "pool/" + key },
			seedOK: func(f *cogFake, key string) string {
				f.secrets[key] = [][]cognitoidptypes.ClientSecretDescriptorType{{cogSecret(key + "--1")}}
				return cogTestClientARN("pool", key) + "/secret/" + key + "--1"
			},
			run: func(f *cogFake, st *store.Store, keys []string) (int, int, error) {
				var clients []cognitoAppClientRef
				for _, k := range keys {
					clients = append(clients, cognitoAppClientRef{poolID: "pool", clientID: k, arn: cogTestClientARN("pool", k)})
				}
				return scanCognitoUserPoolClientSecrets(context.Background(), f.client(), newTestAccount(testAccountID), testRegion, st, testScanID, clients)
			},
		},
	}
}

func (ph cogChildPhase) seedParents(t *testing.T, st *store.Store, keys ...string) {
	t.Helper()
	for _, k := range keys {
		upsertTestResource(t, st, "aws", testAccountID, ph.parentType, ph.parentARN(k), testRegion, "{}")
	}
}

func TestScanCognitoUserPoolReplicas_PagesStoredUnderPrimary(t *testing.T) {
	st := newTestStore(t)
	ph := cogChildPhases()[0]
	ph.seedParents(t, st, "p1", "p2-gone")
	f := newCogFake()
	// The PRIMARY entry is on the second page, so the first page's replica is
	// kept only if every page is read before the primary check.
	f.replicas["p1"] = [][]cognitoidptypes.UserPoolReplicaType{
		{cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive)},
		{
			cogReplica(testRegion, cognitoidptypes.ReplicaRoleTypePrimary, cognitoidptypes.ReplicaStatusTypeActive),
			cogReplica("eu-west-1", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeCreating),
			{Role: cognitoidptypes.ReplicaRoleTypeSecondary},
		},
	}
	f.errs["ListUserPoolReplicas p2-gone"] = bcmPCAPIErr("ResourceNotFoundException", "gone")

	total, _, err := ph.run(f, st, []string{"p1", "p2-gone"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeCognitoUserPoolReplica)
	if len(rows) != 2 {
		t.Errorf("stored %d rows; want 2: %v", len(rows), rows)
	}
	primaryARN := cogTestPoolARN(testRegion, "p1")
	for _, want := range []struct{ region, status string }{{"us-west-2", "ACTIVE"}, {"eu-west-1", "CREATING"}} {
		native := primaryARN + "/replica/" + want.region
		r, ok := rows[native]
		if !ok {
			t.Errorf("row %s missing", native)
			continue
		}
		if sv(r.Name) != want.region || sv(r.Region) != testRegion || sv(r.Status) != want.status {
			t.Errorf("%s Name/Region/Status = %q/%q/%q; want %q/%q/%q", native, sv(r.Name), sv(r.Region), sv(r.Status), want.region, testRegion, want.status)
		}
		var attrs struct{ UserPoolArn, Role string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
		}
		if attrs.UserPoolArn != cogTestPoolARN(want.region, "pool") || attrs.Role != "SECONDARY" {
			t.Errorf("%s attrs = %+v", native, attrs)
		}
		assertContainsNative(t, st, primaryARN, native)
	}
	want := []string{"ListUserPoolReplicas p1", "ListUserPoolReplicas p2-gone"}
	if got := f.calls.distinct(); !slices.Equal(got, want) {
		t.Errorf("calls = %v; want %v", got, want)
	}
	if n := len(f.calls.ids); n != 3 {
		t.Errorf("%d calls; want 3 (two pages of p1, one of p2-gone)", n)
	}
}

// TestScanCognitoUserPoolReplicas_ListingNotFromPrimaryNotStored pins that a
// secondary pool's listing (its region's scan sees the same replica set) and
// a listing with no PRIMARY entry store nothing.
func TestScanCognitoUserPoolReplicas_ListingNotFromPrimaryNotStored(t *testing.T) {
	cases := map[string][]cognitoidptypes.UserPoolReplicaType{
		"secondary-listing": {
			cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypePrimary, cognitoidptypes.ReplicaStatusTypeActive),
			cogReplica(testRegion, cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive),
		},
		"no-primary": {cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive)},
		"empty":      {},
	}
	for name, listing := range cases {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			ph := cogChildPhases()[0]
			ph.seedParents(t, st, "p1")
			f := newCogFake()
			f.replicas["p1"] = [][]cognitoidptypes.UserPoolReplicaType{listing}
			total, _, err := ph.run(f, st, []string{"p1"})
			if err != nil || total != 0 {
				t.Errorf("scan = (%d, %v); want (0, nil)", total, err)
			}
			if rows := rowsByNativeID(t, st, TypeCognitoUserPoolReplica); len(rows) != 0 {
				t.Errorf("stored %v; want none", rows)
			}
		})
	}
}

func TestScanCognitoUserPoolClientSecrets_PagesStoredUnderClient(t *testing.T) {
	st := newTestStore(t)
	ph := cogChildPhases()[1]
	ph.seedParents(t, st, "c1", "c2-gone")
	f := newCogFake()
	f.secrets["c1"] = [][]cognitoidptypes.ClientSecretDescriptorType{{cogSecret("c1--1")}, {cogSecret("c1--2"), {}}}
	f.errs["ListUserPoolClientSecrets pool/c2-gone"] = bcmPCAPIErr("ResourceNotFoundException", "gone")

	total, _, err := ph.run(f, st, []string{"c1", "c2-gone"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeCognitoUserPoolClientSecret)
	if len(rows) != 2 {
		t.Errorf("stored %d rows; want 2: %v", len(rows), rows)
	}
	clientARN := cogTestClientARN("pool", "c1")
	for _, id := range []string{"c1--1", "c1--2"} {
		native := clientARN + "/secret/" + id
		r, ok := rows[native]
		if !ok {
			t.Errorf("row %s missing", native)
			continue
		}
		if sv(r.Name) != id || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" {
			t.Errorf("%s Name/Region/CreatedAt = %q/%q/%q", native, sv(r.Name), sv(r.Region), sv(r.CreatedAt))
		}
		var attrs struct {
			ClientSecretID string `json:"ClientSecretId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.ClientSecretID != id {
			t.Errorf("%s attrs = %s (%v); want ClientSecretId %s", native, r.AttributesJSON, err, id)
		}
		assertContainsNative(t, st, clientARN, native)
	}
	want := []string{"ListUserPoolClientSecrets pool/c1", "ListUserPoolClientSecrets pool/c2-gone"}
	if got := f.calls.distinct(); !slices.Equal(got, want) {
		t.Errorf("calls = %v; want %v", got, want)
	}
}

func TestScanCognitoChildren_Empty(t *testing.T) {
	for _, ph := range cogChildPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			ph.seedParents(t, st, "p1")
			f := newCogFake()
			total, _, err := ph.run(f, st, []string{"p1"})
			if err != nil || total != 0 {
				t.Errorf("scan = (%d, %v); want (0, nil)", total, err)
			}
			if rows := rowsByNativeID(t, st, ph.typ); len(rows) != 0 {
				t.Errorf("stored %v; want none", rows)
			}
			if got, want := f.calls.distinct(), []string{ph.op + " " + ph.callKey("p1")}; !slices.Equal(got, want) {
				t.Errorf("calls = %v; want %v", got, want)
			}
		})
	}
}

// TestScanCognitoChildren_RepeatedTokenFails pins that a listing answering
// with the token it was given ends after that page with errRepeatedNextToken
// instead of looping or reading as complete, and that every parent's rows read
// before the repeat are stored.
func TestScanCognitoChildren_RepeatedTokenFails(t *testing.T) {
	for _, ph := range cogChildPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			keys := []string{"p1", "p2"}
			ph.seedParents(t, st, keys...)
			f := newCogFake()
			f.repeatToken = true
			var want []string
			for _, k := range keys {
				want = append(want, ph.seedOK(f, k))
				f.replicas[k] = append(f.replicas[k], nil)
				f.secrets[k] = append(f.secrets[k], nil)
			}
			if _, _, err := ph.run(f, st, keys); !errors.Is(err, errRepeatedNextToken) {
				t.Errorf("err = %v; want errRepeatedNextToken", err)
			}
			if n := len(f.calls.ids); n != 4 {
				t.Errorf("%d calls; want 4 (per parent the first page, then the repeated token)", n)
			}
			rows := rowsByNativeID(t, st, ph.typ)
			for _, native := range want {
				if _, ok := rows[native]; !ok {
					t.Errorf("row %s not stored; stored %v", native, rows)
				}
			}
		})
	}
}

// TestScanCognitoChildren_ParentErrors fails two of three parents with each
// error class: skipped ones keep the third parent's row, warned ones warn once
// per op however many parents fail, and any other error is returned.
func TestScanCognitoChildren_ParentErrors(t *testing.T) {
	type errCase struct {
		name         string
		err          error
		wantWarnings int
		wantErrCode  string
	}
	common := []errCase{
		{name: "access-denied", err: bcmPCAPIErr("NotAuthorizedException", "User: x is not authorized to perform: cognito-idp:List"), wantWarnings: 1},
		{name: "transient", err: bcmPCAPIErr("TooManyRequestsException", "slow down"), wantWarnings: 1},
		{name: "parent-gone", err: bcmPCAPIErr("ResourceNotFoundException", "gone"), wantWarnings: 0},
		{name: "op-not-deployed", err: bcmPCAPIErr("UnknownOperationException", "unknown operation"), wantWarnings: 0},
		// The NotFoundException form; the AccessDenied form is already silenced by
		// skipIfAccessDenied.
		{name: "service-not-available", err: bcmPCAPIErr("NotFoundException", "Unable to determine service/operation name to be authorized"), wantWarnings: 0},
		{name: "other", err: bcmPCAPIErr("InvalidParameterException", "bad"), wantErrCode: "InvalidParameterException"},
	}
	for _, ph := range cogChildPhases() {
		cases := slices.Clone(common)
		for _, code := range ph.warnCodes {
			cases = append(cases, errCase{name: code, err: bcmPCAPIErr(code, "not enabled"), wantWarnings: 1})
		}
		for _, tc := range cases {
			t.Run(ph.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := countOpWarnings(st, "cognito-idp:"+ph.op)
				ph.seedParents(t, st, "p1", "p2", "p3")
				f := newCogFake()
				f.errs[ph.op+" "+ph.callKey("p1")] = tc.err
				f.errs[ph.op+" "+ph.callKey("p2")] = tc.err
				native := ph.seedOK(f, "p3")

				_, _, err := ph.run(f, st, []string{"p1", "p2", "p3"})
				if tc.wantErrCode != "" {
					if !isAPIErrorCode(err, tc.wantErrCode) {
						t.Errorf("err = %v; want %s", err, tc.wantErrCode)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warnings != tc.wantWarnings {
					t.Errorf("warnings = %d; want %d", *warnings, tc.wantWarnings)
				}
				rows := rowsByNativeID(t, st, ph.typ)
				if _, ok := rows[native]; !ok || len(rows) != 1 {
					t.Errorf("rows = %v; want only p3's child %s", rows, native)
				}
			})
		}
	}
}

// cogP1 is a pool created in the scan region.
const cogP1 = testRegion + "_p1"

// cogTopLevelFake has a replicated pool cogP1 with a confidential client c1
// and a public client c2, a pool p2 without replicas, and a pool whose ID
// names another region although ListUserPools reports ReplicaRegions for it
// (a secondary replica's pool).
func cogTopLevelFake() *cogFake {
	f := newCogFake()
	f.pools = []cognitoidptypes.UserPoolDescriptionType{
		{Id: sdkaws.String(cogP1), ReplicaRegions: []string{"us-west-2"}},
		{Id: sdkaws.String("p2")},
		{Id: sdkaws.String("us-west-2_p3"), ReplicaRegions: []string{testRegion}},
	}
	f.clients[cogP1] = []string{"c1", "c2"}
	f.confidential["c1"] = true
	f.replicas[cogP1] = [][]cognitoidptypes.UserPoolReplicaType{{
		cogReplica(testRegion, cognitoidptypes.ReplicaRoleTypePrimary, cognitoidptypes.ReplicaStatusTypeActive),
		cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive),
	}}
	f.secrets["c1"] = [][]cognitoidptypes.ClientSecretDescriptorType{{cogSecret("c1--1")}}
	return f
}

var (
	cogTopReplicaNative = cogTestPoolARN(testRegion, cogP1) + "/replica/us-west-2"
	cogTopSecretNative  = cogTestClientARN(cogP1, "c1") + "/secret/c1--1"
)

func runCognitoAll(f *cogFake, st *store.Store) (int, int, error) {
	return scanCognitoAll(context.Background(), f.client(), cogIdentityClient(), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// cogChildRowsStored reports whether the top-level fake's replica and secret
// rows were stored.
func cogChildRowsStored(t *testing.T, st *store.Store) (replica, secret bool) {
	t.Helper()
	_, replica = rowsByNativeID(t, st, TypeCognitoUserPoolReplica)[cogTopReplicaNative]
	_, secret = rowsByNativeID(t, st, TypeCognitoUserPoolClientSecret)[cogTopSecretNative]
	return replica, secret
}

func TestScanCognitoAll_StoresChildrenOfListedParents(t *testing.T) {
	st := newTestStore(t)
	f := cogTopLevelFake()
	total, _, err := runCognitoAll(f, st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7 (3 pools, 2 clients, 1 replica, 1 secret)", total)
	}
	if replica, secret := cogChildRowsStored(t, st); !replica || !secret {
		t.Errorf("replica stored = %v, secret stored = %v; want both", replica, secret)
	}
	assertContainsNative(t, st, cogTestPoolARN(testRegion, cogP1), cogTopReplicaNative)
	assertContainsNative(t, st, cogTestClientARN(cogP1, "c1"), cogTopSecretNative)
	// p2 lists no ReplicaRegions, us-west-2_p3 was not created in this region
	// and c2 has no secret, so none of them is asked.
	c1 := rowsByNativeID(t, st, TypeCognitoAppClient)[cogTestClientARN(cogP1, "c1")]
	var c1Attrs struct {
		ClientID                 string `json:"ClientId"`
		ClientName, ClientSecret string
	}
	if err := json.Unmarshal([]byte(c1.AttributesJSON), &c1Attrs); err != nil {
		t.Fatalf("c1 attrs %q: %v", c1.AttributesJSON, err)
	}
	if c1Attrs.ClientSecret != redact.Placeholder || c1Attrs.ClientID != "c1" || c1Attrs.ClientName != "c1" {
		t.Errorf("stored c1 attrs = %+v; want ClientSecret redacted, ClientId and ClientName readable", c1Attrs)
	}
	want := []string{"ListUserPoolClientSecrets " + cogP1 + "/c1", "ListUserPoolReplicas " + cogP1}
	if got := f.calls.distinct(); !slices.Equal(got, want) {
		t.Errorf("child calls = %v; want %v", got, want)
	}
}

// TestScanCognitoAll_ChildPhasesRunAfterOlderPhaseFailure pins that a hard
// error in an older phase that runs after the pool phase neither skips the
// child phases nor is lost.
func TestScanCognitoAll_ChildPhasesRunAfterOlderPhaseFailure(t *testing.T) {
	st := newTestStore(t)
	f := cogTopLevelFake()
	f.groupsErr = bcmPCAPIErr("InvalidParameterException", "bad")
	total, _, err := runCognitoAll(f, st)
	if !isAPIErrorCode(err, "InvalidParameterException") {
		t.Errorf("err = %v; want the ListGroups InvalidParameterException", err)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7", total)
	}
	if replica, secret := cogChildRowsStored(t, st); !replica || !secret {
		t.Errorf("replica stored = %v, secret stored = %v; want both", replica, secret)
	}
}

// TestScanCognitoAll_ChildPhaseErrorKeepsOtherPhases fails each child op in
// turn: its error is returned and every other phase still stores its rows.
func TestScanCognitoAll_ChildPhaseErrorKeepsOtherPhases(t *testing.T) {
	for _, failing := range []string{"ListUserPoolReplicas " + cogP1, "ListUserPoolClientSecrets " + cogP1 + "/c1"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			f := cogTopLevelFake()
			f.errs[failing] = bcmPCAPIErr("InvalidParameterException", "bad")
			total, _, err := runCognitoAll(f, st)
			if !isAPIErrorCode(err, "InvalidParameterException") {
				t.Errorf("err = %v; want the %s InvalidParameterException", err, failing)
			}
			if total != 6 {
				t.Errorf("total = %d; want 6", total)
			}
			replica, secret := cogChildRowsStored(t, st)
			if wantReplica := failing != "ListUserPoolReplicas "+cogP1; replica != wantReplica || secret == wantReplica {
				t.Errorf("replica stored = %v, secret stored = %v", replica, secret)
			}
			if pools := rowsByNativeID(t, st, TypeCognitoUserPool); len(pools) != 3 {
				t.Errorf("stored %d pools; want 3", len(pools))
			}
		})
	}
}

// TestScanCognitoAll_OlderTransientDoesNotHideChildHardError pins that an
// older phase's transient error is demoted to a warning, so the dispatcher
// sees a child phase's hard error rather than a transient join.
func TestScanCognitoAll_OlderTransientDoesNotHideChildHardError(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "cognito-idp")
	f := cogTopLevelFake()
	f.groupsErr = bcmPCAPIErr("TooManyRequestsException", "slow down")
	f.errs["ListUserPoolReplicas "+cogP1] = bcmPCAPIErr("InvalidParameterException", "bad")
	_, _, err := runCognitoAll(f, st)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "InvalidParameterException") {
		t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with the replica InvalidParameterException", got, err, outcomeError)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// TestScanCognitoAll_PoolDescribeFailureKeepsChildren pins that one pool's
// failed DescribeUserPool is returned without costing the other pools, their
// children or the pool's own row.
func TestScanCognitoAll_PoolDescribeFailureKeepsChildren(t *testing.T) {
	st := newTestStore(t)
	f := cogTopLevelFake()
	f.errs["DescribeUserPool p2"] = bcmPCAPIErr("InvalidParameterException", "bad")
	_, _, err := runCognitoAll(f, st)
	if !isAPIErrorCode(err, "InvalidParameterException") {
		t.Errorf("err = %v; want the DescribeUserPool InvalidParameterException", err)
	}
	if pools := rowsByNativeID(t, st, TypeCognitoUserPool); len(pools) != 3 {
		t.Errorf("stored %d pools; want 3", len(pools))
	}
	if replica, secret := cogChildRowsStored(t, st); !replica || !secret {
		t.Errorf("replica stored = %v, secret stored = %v; want both", replica, secret)
	}
}
