package aws

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/accountaccess"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accountaccess/types"
	"github.com/icearp/disco-cli/store"
)

// aaAppAttrs renders an application whose identity source names instanceARN
// and ssoAppARN the way scanAccountAccessApplications stores it.
func aaAppAttrs(instanceARN, ssoAppARN string) string {
	ic := aatypes.IdentityCenterDetails{InstanceArn: &instanceARN}
	if ssoAppARN != "" {
		ic.ApplicationArn = &ssoAppARN
	}
	return mustJSON(flattenAccountAccessApplication(&accountaccess.GetApplicationOutput{
		Status:         aatypes.StatusActive,
		IdentitySource: &aatypes.IdentitySourceDetailsMemberIdentityCenter{Value: ic},
	}))
}

// aaEntAttrs renders an entitlement granting roleARN the way
// listAccountAccessEntitlements stores it.
func aaEntAttrs(roleARN string) string {
	e := aaEntitlement("ent-1", &aatypes.IdentityCenterPrincipalMemberUserId{Value: aaUserID})
	e.Entitlement.(*aatypes.EntitlementSummaryMemberPrincipalRole).Value.RoleArn = &roleARN
	return mustJSON(flattenAccountAccessEntitlement(e))
}

// An application uses the scanned Identity Center instance and application
// its identity source names. An application whose refs are not scanned, or
// that has no attributes, has no edge.
func TestResolveAccountAccessApplicationSSO_Edges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, aaInstanceARN, "", "{}")
	ssoAppID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOApplication, aaSSOAppARN, "", "{}")
	both := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessApplication, aaAppA, aaRegion, aaAppAttrs(aaInstanceARN, aaSSOAppARN))
	instOnly := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessApplication, aaAppB, aaRegion, aaAppAttrs(aaInstanceARN, ""))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessApplication, aaAppA+"x", aaRegion,
		aaAppAttrs("arn:aws:sso:::instance/ssoins-9999888877776666", ""))
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessApplication, aaAppA+"y", aaRegion, "{}")

	if err := resolveAccountAccessApplicationSSO(acct, st); err != nil {
		t.Fatalf("resolveAccountAccessApplicationSSO: %v", err)
	}
	if got := outEdges(t, st, both); len(got) != 2 || got[instID] != store.RelUses || got[ssoAppID] != store.RelUses {
		t.Errorf("app-a edges = %v, want uses → %s and %s", got, instID, ssoAppID)
	}
	if got := outEdges(t, st, instOnly); len(got) != 1 || got[instID] != store.RelUses {
		t.Errorf("app-b edges = %v, want uses → %s only", got, instID)
	}
	for name, id := range map[string]string{"unscanned instance": unscanned, "no attrs": noAttrs} {
		if got := outEdges(t, st, id); len(got) != 0 {
			t.Errorf("%s application edges = %v, want none", name, got)
		}
	}
}

// An entitlement uses the role it grants when that role is scanned in the
// same account; a role in another account gets no edge.
func TestResolveAccountAccessEntitlementRole_Edges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, aaRoleARN, "", "{}")
	otherRoleARN := "arn:aws:iam::210987654321:role/Admin"
	upsertTestResource(t, st, "aws", "210987654321", TypeIAMRole, otherRoleARN, "", "{}")
	granted := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessEntitlement, aaEntNativeID(aaAppA, "ent-1"), aaRegion, aaEntAttrs(aaRoleARN))
	otherAcct := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessEntitlement, aaEntNativeID(aaAppA, "ent-2"), aaRegion,
		aaEntAttrs(otherRoleARN))
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypeAccountAccessEntitlement, aaEntNativeID(aaAppA, "ent-3"), aaRegion, "{}")

	if err := resolveAccountAccessEntitlementRole(acct, st); err != nil {
		t.Fatalf("resolveAccountAccessEntitlementRole: %v", err)
	}
	if got := outEdges(t, st, granted); len(got) != 1 || got[roleID] != store.RelUses {
		t.Errorf("granted entitlement edges = %v, want uses → %s", got, roleID)
	}
	for name, id := range map[string]string{"other account": otherAcct, "no attrs": noAttrs} {
		if got := outEdges(t, st, id); len(got) != 0 {
			t.Errorf("%s entitlement edges = %v, want none", name, got)
		}
	}
}

// A row whose attributes do not decode is reported, and costs the other rows
// none of their edges.
func TestResolveAccountAccess_DecodeError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(*account, *store.Store) error
		typ     string
		target  string
		tgtARN  string
		good    string
		goodID  string
		badID   string
	}{
		{
			name: "application", resolve: resolveAccountAccessApplicationSSO, typ: TypeAccountAccessApplication,
			target: TypeSSOInstance, tgtARN: aaInstanceARN, good: aaAppAttrs(aaInstanceARN, ""), goodID: aaAppA, badID: aaAppB,
		},
		{
			name: "entitlement", resolve: resolveAccountAccessEntitlementRole, typ: TypeAccountAccessEntitlement,
			target: TypeIAMRole, tgtARN: aaRoleARN, good: aaEntAttrs(aaRoleARN),
			goodID: aaEntNativeID(aaAppA, "ent-1"), badID: aaEntNativeID(aaAppA, "ent-2"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			targetID := upsertTestResource(t, st, "aws", acct.ID, tc.target, tc.tgtARN, "", "{}")
			upsertTestResource(t, st, "aws", acct.ID, tc.typ, tc.badID, aaRegion, "[]")
			good := upsertTestResource(t, st, "aws", acct.ID, tc.typ, tc.goodID, aaRegion, tc.good)
			// ListResources orders by name before root_id, and an unnamed row
			// sorts first, so this named row is read after the bad one: the
			// test fails if a decode error stops the loop.
			later := upsertTestResourceNamed(t, st, tc.typ, tc.badID+"-later", aaRegion, tc.good, "later")

			err := tc.resolve(acct, st)
			var ute *json.UnmarshalTypeError
			if !errors.As(err, &ute) {
				t.Fatalf("err = %v, want the JSON decode error", err)
			}
			for _, id := range []string{good, later} {
				if got := outEdges(t, st, id); len(got) != 1 || got[targetID] != store.RelUses {
					t.Errorf("decodable row %s edges = %v, want uses → %s", id, got, targetID)
				}
			}
		})
	}
}
