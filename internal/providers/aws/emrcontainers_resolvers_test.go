package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

func TestResolveEMRCJobTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/emr-tmpl", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	jtARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyArn":"%s","JobTemplateData":{"ExecutionRoleArn":"%s"}}`, keyARN, roleARN)
	jtID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, jtARN, testRegion, attrs)
	if err := resolveEMRCJobTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveEMRCJobTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(jtID)
	assertRelationship(t, rels, jtID, roleID, store.RelUses)
	assertRelationship(t, rels, jtID, keyID, store.RelUses)
}

func TestResolveEMRCJobTemplateRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	jtARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-1", testRegion, acct.ID)
	jtID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, jtARN, testRegion, "{}")
	if err := resolveEMRCJobTemplateRefs(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(jtID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
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
