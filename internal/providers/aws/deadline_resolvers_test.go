package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/store"
)

// dlMemberAttrs is the stored attrs of a member of typ, marshalled from the
// SDK member struct of that type.
func dlMemberAttrs(typ, identityStoreID, principalID string, pt dltypes.DeadlinePrincipalType) string {
	ids, pid := sdkaws.String(identityStoreID), sdkaws.String(principalID)
	farm := sdkaws.String("farm-a")
	switch typ {
	case TypeDeadlineFleetMember:
		return mustJSON(dltypes.FleetMember{FarmId: farm, FleetId: sdkaws.String("fleet-a"), IdentityStoreId: ids, PrincipalId: pid, PrincipalType: pt, MembershipLevel: dltypes.MembershipLevelViewer})
	case TypeDeadlineQueueMember:
		return mustJSON(dltypes.QueueMember{FarmId: farm, QueueId: sdkaws.String("queue-a"), IdentityStoreId: ids, PrincipalId: pid, PrincipalType: pt, MembershipLevel: dltypes.MembershipLevelViewer})
	}
	return mustJSON(dltypes.FarmMember{FarmId: farm, IdentityStoreId: ids, PrincipalId: pid, PrincipalType: pt, MembershipLevel: dltypes.MembershipLevelViewer})
}

// Each member links to the user or group it names in its own identity store,
// and to nothing else: not to the same id in another store, nor to the other
// principal kind sharing the id.
func TestResolveDeadlineMemberPrincipals_LinksPrincipal(t *testing.T) {
	user, group := dltypes.DeadlinePrincipalTypeUser, dltypes.DeadlinePrincipalTypeGroup
	for _, typ := range []string{TypeDeadlineFarmMember, TypeDeadlineFleetMember, TypeDeadlineQueueMember} {
		for _, pt := range []dltypes.DeadlinePrincipalType{user, group} {
			t.Run(typ+"/"+string(pt), func(t *testing.T) {
				st := newTestStore(t)
				acct := newTestAccount(testAccountID)
				userID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, dlTestStore, "p-1"), "", "{}")
				groupID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroup, identityStoreGroupNativeID(acct.ID, dlTestStore, "p-1"), "", "{}")
				upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, "d-0000000000", "p-1"), "", "{}")
				upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroup, identityStoreGroupNativeID(acct.ID, "d-0000000000", "p-1"), "", "{}")
				member := upsertTestResource(t, st, "aws", acct.ID, typ, dlTestFarmA+"/member/"+string(pt)+"/p-1", testRegion, dlMemberAttrs(typ, dlTestStore, "p-1", pt))

				if err := resolveDeadlineMemberPrincipals(acct, st); err != nil {
					t.Fatalf("resolveDeadlineMemberPrincipals: %v", err)
				}
				want := userID
				if pt == group {
					want = groupID
				}
				rels, err := st.RelationshipsFrom(member)
				if err != nil {
					t.Fatalf("RelationshipsFrom: %v", err)
				}
				if len(rels) != 1 {
					t.Fatalf("edges = %v, want exactly one", rels)
				}
				assertRelationship(t, rels, member, want, store.RelUses)
			})
		}
	}
}

// A member without attributes, or whose principal is not stored, links to
// nothing.
func TestResolveDeadlineMemberPrincipals_NoAttrsOrUnstoredPrincipal(t *testing.T) {
	for _, typ := range []string{TypeDeadlineFarmMember, TypeDeadlineFleetMember, TypeDeadlineQueueMember} {
		t.Run(typ, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, dlTestStore, "p-1"), "", "{}")
			bare := upsertTestResource(t, st, "aws", acct.ID, typ, dlTestFarmA+"/member/USER/p-1", testRegion, "{}")
			unstored := upsertTestResource(t, st, "aws", acct.ID, typ, dlTestFarmA+"/member/USER/p-2", testRegion, dlMemberAttrs(typ, dlTestStore, "p-2", dltypes.DeadlinePrincipalTypeUser))

			if err := resolveDeadlineMemberPrincipals(acct, st); err != nil {
				t.Fatalf("resolveDeadlineMemberPrincipals: %v", err)
			}
			assertNoEdgesFrom(t, st, bare)
			assertNoEdgesFrom(t, st, unstored)
		})
	}
}

// A member whose attributes do not decode is reported and costs no other
// member its edge.
func TestResolveDeadlineMemberPrincipals_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, dlTestStore, "p-1"), "", "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFarmMember, dlTestFarmA+"/member/USER/p-0", testRegion, `{"PrincipalId":7}`)
	good := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueueMember, dlTestQueueA+"/member/USER/p-1", testRegion, dlMemberAttrs(TypeDeadlineQueueMember, dlTestStore, "p-1", dltypes.DeadlinePrincipalTypeUser))

	var typeErr *json.UnmarshalTypeError
	if err := resolveDeadlineMemberPrincipals(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	rels, _ := st.RelationshipsFrom(good)
	assertRelationship(t, rels, good, userID, store.RelUses)
}

func TestDeadlineFarmARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:deadline:us-east-1:123:farm/farm-1/queue/q-1", "arn:aws:deadline:us-east-1:123:farm/farm-1"},
		{"arn:aws:deadline:us-east-1:123:farm/farm-1/queue/q-1/fleet/f-1/association", "arn:aws:deadline:us-east-1:123:farm/farm-1"},
		{"arn:aws:deadline:us-east-1:123:farm/farm-1", ""},
	}
	for _, c := range cases {
		if got := deadlineFarmARNFromChild(c.in); got != c.want {
			t.Errorf("deadlineFarmARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveDeadlineFarmChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	farmID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFarm, farmARN, testRegion, "{}")
	fleetARN := farmARN + "/fleet/fleet-1"
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFleet, fleetARN, testRegion, "{}")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")

	if err := resolveDeadlineFarmChildren(acct, st); err != nil {
		t.Fatalf("resolveDeadlineFarmChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fleetID)
	assertRelationship(t, rels, fleetID, farmID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(queueID)
	assertRelationship(t, rels, queueID, farmID, store.RelAttachedTo)
}

func TestResolveDeadlineVolumeFleet(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/farm-1", testRegion, acct.ID)
	fleetARN := farmARN + "/fleet/fleet-1"
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFleet, fleetARN, testRegion, "{}")
	volARN := farmARN + "/volume/vol-1"
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineVolume, volARN, testRegion, `{"FleetId":"fleet-1"}`)

	if err := resolveDeadlineVolumeFleet(acct, st); err != nil {
		t.Fatalf("resolveDeadlineVolumeFleet: %v", err)
	}
	rels, _ := st.RelationshipsFrom(volID)
	assertRelationship(t, rels, volID, fleetID, store.RelAttachedTo)
}

func TestResolveDeadlineVolumeFleet_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/farm-1", testRegion, acct.ID)
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineVolume, farmARN+"/volume/vol-1", testRegion, "{}")
	if err := resolveDeadlineVolumeFleet(acct, st); err != nil {
		t.Fatalf("resolveDeadlineVolumeFleet: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(volID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveDeadlineQueueEnvParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")
	envARN := queueARN + "/queue-environment/env-1"
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueueEnvironment, envARN, testRegion, "{}")
	if err := resolveDeadlineQueueEnvParent(acct, st); err != nil {
		t.Fatalf("resolveDeadlineQueueEnvParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, queueID, store.RelAttachedTo)
}

func TestResolveDeadlineQueueFleetAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")
	fleetARN := farmARN + "/fleet/f-1"
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFleet, fleetARN, testRegion, "{}")
	assocARN := farmARN + "/queue/q-1/fleet/f-1/association"
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueueFleetAssociation, assocARN, testRegion, "{}")
	if err := resolveDeadlineQueueFleetAssoc(acct, st); err != nil {
		t.Fatalf("resolveDeadlineQueueFleetAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, queueID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, fleetID, store.RelAttachedTo)
}

func TestResolveDeadlineMeteredProductParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	leARN := fmt.Sprintf("arn:aws:deadline:%s:%s:license-endpoint/le-1", testRegion, acct.ID)
	leID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineLicenseEndpoint, leARN, testRegion, "{}")
	mpARN := leARN + "/metered-product/prod-1"
	mpID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineMeteredProduct, mpARN, testRegion, "{}")
	if err := resolveDeadlineMeteredProductParent(acct, st); err != nil {
		t.Fatalf("resolveDeadlineMeteredProductParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mpID)
	assertRelationship(t, rels, mpID, leID, store.RelAttachedTo)
}

func TestResolveDeadlineFarmKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	fARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/farm-1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFarm, fARN, testRegion,
		fmt.Sprintf(`{"KmsKeyArn":"%s"}`, keyARN))
	if err := resolveDeadlineFarmKMS(acct, st); err != nil {
		t.Fatalf("resolveDeadlineFarmKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, keyID, store.RelUses)
}

func TestResolveDeadlineLicenseEndpointVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	leARN := fmt.Sprintf("arn:aws:deadline:%s:%s:license-endpoint/le-1", testRegion, acct.ID)
	leID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineLicenseEndpoint, leARN, testRegion,
		`{"VpcId":"vpc-1"}`)
	if err := resolveDeadlineLicenseEndpointVPC(acct, st); err != nil {
		t.Fatalf("resolveDeadlineLicenseEndpointVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(leID)
	assertRelationship(t, rels, leID, vpcID, store.RelAttachedTo)
}

func TestResolveDeadlineMonitorRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dl-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	instARN := "arn:aws:sso:::instance/ssoins-1"
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, instARN, "", "{}")
	appARN := fmt.Sprintf("arn:aws:sso::%s:application/ssoins-1/apl-1", acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOApplication, appARN, "", "{}")
	mARN := fmt.Sprintf("arn:aws:deadline:%s:%s:monitor/m-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RoleArn":"%s","IdentityCenterInstanceArn":"%s","IdentityCenterApplicationArn":"%s"}`, roleARN, instARN, appARN)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineMonitor, mARN, testRegion, attrs)
	if err := resolveDeadlineMonitorRefs(acct, st); err != nil {
		t.Fatalf("resolveDeadlineMonitorRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, roleID, store.RelUses)
	assertRelationship(t, rels, mID, instID, store.RelAttachedTo)
	assertRelationship(t, rels, mID, appID, store.RelAttachedTo)
}
