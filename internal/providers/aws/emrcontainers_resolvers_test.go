package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveEMRCEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vcARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-1", testRegion, acct.ID)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, vcARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/emr-exec", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	epARN := vcARN + "/endpoints/ep-1"
	attrs := fmt.Sprintf(`{"ExecutionRoleArn":"%s"}`, roleARN)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersEndpoint, epARN, testRegion, attrs)
	if err := resolveEMRCEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveEMRCEndpointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, vcID, store.RelAttachedTo)
	assertRelationship(t, rels, epID, roleID, store.RelUses)
}

func TestResolveEMRCVirtualClusterToSecConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	scARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/securityconfigurations/sc-1", testRegion, acct.ID)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersSecurityConfig, scARN, testRegion, "{}")
	vcARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-1", testRegion, acct.ID)
	attrs := `{"SecurityConfigurationId":"sc-1"}`
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, vcARN, testRegion, attrs)
	if err := resolveEMRCVirtualClusterToSecConfig(acct, st); err != nil {
		t.Fatalf("resolveEMRCVirtualClusterToSecConfig: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vcID)
	assertRelationship(t, rels, vcID, scID, store.RelUses)
}
