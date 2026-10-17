package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	cloud9types "github.com/aws/aws-sdk-go-v2/service/cloud9/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveCloud9EnvOwnerUser(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	envARN := fmt.Sprintf("arn:aws:cloud9:%s:%s:environment:env1", testRegion, acct.ID)
	userARN := fmt.Sprintf("arn:aws:iam::%s:user/alice", acct.ID)
	attrs := fmt.Sprintf(`{"OwnerArn":%q}`, userARN)

	eID := upsertTestResource(t, st, "aws", acct.ID, TypeCloud9EnvironmentEC2, envARN, testRegion, attrs)
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, testRegion, "{}")

	if err := resolveCloud9EnvOwner(acct, st); err != nil {
		t.Fatalf("resolveCloud9EnvOwner: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, uID, store.RelAttachedTo)
}

// cloud9MembershipRow stores a membership of env1 for userARN, its attributes
// marshalled from the SDK element.
func cloud9MembershipRow(t *testing.T, st *store.Store, userARN string) string {
	t.Helper()
	attrs, err := json.Marshal(cloud9types.EnvironmentMember{
		EnvironmentId: sdkaws.String("env1"), UserArn: sdkaws.String(userARN),
		UserId: sdkaws.String("AIDAX"), Permissions: cloud9types.PermissionsReadWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	return upsertTestResource(t, st, "aws", testAccountID, TypeCloud9EnvironmentMembership,
		cloud9MembershipNativeID(cloud9EnvARN("env1"), userARN), testRegion, string(attrs))
}

func TestResolveCloud9MembershipUsers_ScannedUser(t *testing.T) {
	st := newTestStore(t)
	userARN := cloud9UserARN("alice")
	mID := cloud9MembershipRow(t, st, userARN)
	uID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, userARN, "", "{}")
	otherID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, cloud9UserARN("bob"), "", "{}")

	if err := resolveCloud9MembershipUsers(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveCloud9MembershipUsers: %v", err)
	}
	rels, err := st.RelationshipsFrom(mID)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, mID, uID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("edges = %v; want only the one to %s (not %s)", rels, uID, otherID)
	}
}

// Members that are not a scanned IAM user emit no edge: an unscanned user, an
// assumed-role session and account root.
func TestResolveCloud9MembershipUsers_NoScannedTarget(t *testing.T) {
	for _, userARN := range []string{
		cloud9UserARN("unscanned"),
		fmt.Sprintf("arn:aws:sts::%s:assumed-role/dev/session", testAccountID),
		fmt.Sprintf("arn:aws:iam::%s:root", testAccountID),
	} {
		t.Run(userARN, func(t *testing.T) {
			st := newTestStore(t)
			mID := cloud9MembershipRow(t, st, userARN)
			upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, fmt.Sprintf("arn:aws:iam::%s:role/dev", testAccountID), "", "{}")
			upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, cloud9UserARN("alice"), "", "{}")

			if err := resolveCloud9MembershipUsers(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolveCloud9MembershipUsers: %v", err)
			}
			if rels, _ := st.RelationshipsFrom(mID); len(rels) != 0 {
				t.Errorf("edges = %v; want none", rels)
			}
		})
	}
}

func TestResolveCloud9MembershipUsers_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	mID := upsertTestResource(t, st, "aws", testAccountID, TypeCloud9EnvironmentMembership, cloud9EnvARN("env1")+"/membership/x", testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, cloud9UserARN("alice"), "", "{}")

	if err := resolveCloud9MembershipUsers(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveCloud9MembershipUsers: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(mID); len(rels) != 0 {
		t.Errorf("edges = %v; want none", rels)
	}
}

// A row that fails to decode is returned as an error and costs a row listed
// after it none of its edges. ListResources orders by name, so the names put
// the bad row first.
func TestResolveCloud9MembershipUsers_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeCloud9EnvironmentMembership, cloud9EnvARN("env1")+"/membership/x", testRegion, `{"UserArn":7}`, "a-bad")
	userARN := cloud9UserARN("alice")
	attrs, err := json.Marshal(cloud9types.EnvironmentMember{EnvironmentId: sdkaws.String("env2"), UserArn: sdkaws.String(userARN)})
	if err != nil {
		t.Fatal(err)
	}
	goodID := upsertTestResourceNamed(t, st, TypeCloud9EnvironmentMembership, cloud9MembershipNativeID(cloud9EnvARN("env2"), userARN), testRegion, string(attrs), "b-good")
	uID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, userARN, "", "{}")

	var typeErr *json.UnmarshalTypeError
	if err := resolveCloud9MembershipUsers(newTestAccount(testAccountID), st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v; want a *json.UnmarshalTypeError", err)
	}
	rels, err := st.RelationshipsFrom(goodID)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, goodID, uID, store.RelAttachedTo)
}

func TestResolveCloud9EnvOwnerRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	envARN := fmt.Sprintf("arn:aws:cloud9:%s:%s:environment:env2", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dev", acct.ID)
	attrs := fmt.Sprintf(`{"OwnerArn":%q}`, roleARN)

	eID := upsertTestResource(t, st, "aws", acct.ID, TypeCloud9EnvironmentEC2, envARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")

	if err := resolveCloud9EnvOwner(acct, st); err != nil {
		t.Fatalf("resolveCloud9EnvOwner: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, rID, store.RelAttachedTo)
}
