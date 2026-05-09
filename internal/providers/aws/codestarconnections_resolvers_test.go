package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCSCRepositoryLinkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	connARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:connection/abc-123", testRegion, acct.ID)
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsConnection, connARN, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rlARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/r-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ConnectionArn":"%s","EncryptionKeyArn":"%s","RepositoryLinkId":"r-1"}`, connARN, keyARN)
	rlID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsRepositoryLink, rlARN, testRegion, attrs)
	if err := resolveCSCRepositoryLinkRefs(acct, st); err != nil {
		t.Fatalf("resolveCSCRepositoryLinkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rlID)
	assertRelationship(t, rels, rlID, connID, store.RelAttachedTo)
	assertRelationship(t, rels, rlID, keyID, store.RelUses)
}

func TestResolveCSCSyncConfigurationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rlARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/r-1", testRegion, acct.ID)
	rlID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsRepositoryLink, rlARN, testRegion, `{"RepositoryLinkId":"r-1"}`)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/sync-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	scARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/r-1/CFN_STACK_SYNC/myapp", testRegion, acct.ID)
	scAttrs := fmt.Sprintf(`{"RepositoryLinkId":"r-1","RoleArn":"%s"}`, roleARN)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsSyncConfiguration, scARN, testRegion, scAttrs)
	if err := resolveCSCSyncConfigurationRefs(acct, st); err != nil {
		t.Fatalf("resolveCSCSyncConfigurationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(scID)
	assertRelationship(t, rels, scID, rlID, store.RelAttachedTo)
	assertRelationship(t, rels, scID, roleID, store.RelUses)
}
