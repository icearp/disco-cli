package aws

import (
	"fmt"
	"testing"

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
