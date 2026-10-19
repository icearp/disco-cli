package aws

import (
	"errors"
	"testing"

	workmailtypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/icearp/disco-cli/store"
)

const wmFnARN = "arn:aws:lambda:us-east-1:123456789012:function:availability"

func TestResolveWorkMailAvailabilityLambda(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      workmailtypes.AvailabilityConfiguration
		wantEdge bool
	}{
		{"UnqualifiedARN", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN)}}, true},
		{"QualifiedARN", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN + ":live")}}, true},
		{"UnscannedFunction", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN + "-other")}}, false},
		{"EwsProvider", workmailtypes.AvailabilityConfiguration{EwsProvider: &workmailtypes.RedactedEwsAvailabilityProvider{EwsEndpoint: ptrStr("https://ews.example.com")}}, false},
		{"NoAttrs", workmailtypes.AvailabilityConfiguration{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailAvailabilityConfiguration,
				wmOrgA+"/availabilityconfiguration/example.com", testRegion, mustJSON(tc.cfg))
			fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, wmFnARN, testRegion, "{}")

			if err := resolveWorkMailAvailabilityLambda(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(cfgID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if tc.wantEdge {
				assertRelationship(t, rels, cfgID, fnID, store.RelUses)
				if len(rels) != 1 {
					t.Errorf("edges = %v, want only the lambda edge", rels)
				}
			} else if len(rels) != 0 {
				t.Errorf("edges = %v, want none", rels)
			}
		})
	}
}

func TestResolveWorkMailMailboxPermissionGrantees(t *testing.T) {
	const (
		mailbox = wmOrgA + "/user/u1"
		userB   = wmOrgB + "/user/u-2"
	)
	for _, tc := range []struct {
		name     string
		perm     workmailtypes.Permission
		wantEdge string // NativeID of the grantee the edge must reach; "" for none
	}{
		{"User", workmailtypes.Permission{GranteeId: ptrStr("u-2"), GranteeType: workmailtypes.MemberTypeUser}, wmOrgA + "/user/u-2"},
		{"Group", workmailtypes.Permission{GranteeId: ptrStr("g-1"), GranteeType: workmailtypes.MemberTypeGroup}, wmOrgA + "/group/g-1"},
		// A USER grant naming a stored group's id does not reach that group;
		// the User case's single edge shows m-b's u-2 is not reached either.
		{"UserTypeDoesNotMatchGroup", workmailtypes.Permission{GranteeId: ptrStr("g-1"), GranteeType: workmailtypes.MemberTypeUser}, ""},
		{"UnscannedGrantee", workmailtypes.Permission{GranteeId: ptrStr("u-9"), GranteeType: workmailtypes.MemberTypeUser}, ""},
		{"NoAttrs", workmailtypes.Permission{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			permID := upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailMailboxPermission,
				mailbox+"/mailboxpermission/"+sv(tc.perm.GranteeId), testRegion, mustJSON(tc.perm))
			upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailUser, wmOrgA+"/user/u-2", testRegion, "{}")
			upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailUser, userB, testRegion, "{}")
			upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailGroup, wmOrgA+"/group/g-1", testRegion, "{}")

			if err := resolveWorkMailMailboxPermissionGrantees(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(permID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if tc.wantEdge == "" {
				if len(rels) != 0 {
					t.Errorf("edges = %v, want none", rels)
				}
				return
			}
			assertRelationship(t, rels, permID, store.ResourceID("aws", acct.ID, tc.wantEdge), store.RelUses)
			if len(rels) != 1 {
				t.Errorf("edges = %v, want only the grantee edge", rels)
			}
		})
	}
}

// One permission whose attributes or NativeID do not decode is reported and
// costs no other permission its edge. The rows are named so that the bad one
// is listed between two good ones, so neither stopping at it nor skipping
// past the rest goes unseen.
func TestResolveWorkMailMailboxPermissionGrantees_DecodeErrors(t *testing.T) {
	grant := mustJSON(workmailtypes.Permission{GranteeId: ptrStr("u-2"), GranteeType: workmailtypes.MemberTypeUser})
	for _, tc := range []struct {
		name, nativeID, attrs string
		wantNativeIDErr       bool
	}{
		{"BadAttrs", wmOrgA + "/user/u1/mailboxpermission/x", "{not json", false},
		{"BadNativeID", "not-a-workmail-id", grant, true},
		{"EmptyOrgID", "arn:aws:workmail:us-east-1:123456789012:organization//user/u1/mailboxpermission/u-2", grant, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResourceNamed(t, st, TypeWorkMailMailboxPermission, tc.nativeID, testRegion, tc.attrs, "b-bad")
			goodIDs := []string{
				upsertTestResourceNamed(t, st, TypeWorkMailMailboxPermission, wmOrgA+"/user/u1/mailboxpermission/u-2", testRegion, grant, "a-good"),
				upsertTestResourceNamed(t, st, TypeWorkMailMailboxPermission, wmOrgA+"/user/u3/mailboxpermission/u-2", testRegion, grant, "c-good"),
			}
			userID := upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailUser, wmOrgA+"/user/u-2", testRegion, "{}")

			err := resolveWorkMailMailboxPermissionGrantees(acct, st)
			if err == nil {
				t.Error("resolve: err = nil, want the decode error")
			}
			if tc.wantNativeIDErr != errors.Is(err, errWorkMailChildNativeID) {
				t.Errorf("err = %v; want errWorkMailChildNativeID: %v", err, tc.wantNativeIDErr)
			}
			for _, goodID := range goodIDs {
				rels, rerr := st.RelationshipsFrom(goodID)
				if rerr != nil {
					t.Fatalf("RelationshipsFrom: %v", rerr)
				}
				assertRelationship(t, rels, goodID, userID, store.RelUses)
			}
		})
	}
}
