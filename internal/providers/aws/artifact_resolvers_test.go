package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	artifacttypes "github.com/aws/aws-sdk-go-v2/service/artifact/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveArtifactCustomerAgreementOrg(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	orgARN := fmt.Sprintf("arn:aws:organizations::%s:organization/o-abcd1234", testAccountID)
	orgID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganization, orgARN, "", "{}")

	caARN := fmt.Sprintf("arn:aws:artifact::%s:customer-agreement/customer-agreement-123", testAccountID)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeArtifactCustomerAgreement, caARN, "us-east-1",
		marshalArtifactCustomerAgreement(t, orgARN))

	if err := resolveArtifactCustomerAgreementOrg(acct, st); err != nil {
		t.Fatalf("resolveArtifactCustomerAgreementOrg: %v", err)
	}
	rels, err := st.RelationshipsFrom(caID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, caID, orgID, store.RelAttachedTo)
}

// A standalone-account agreement (no OrganizationArn) and an unscanned org both
// emit no edge.
func TestResolveArtifactCustomerAgreementOrg_NoOrg(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed an org so the resolver's index is non-empty, then a standalone
	// agreement that references a *different*, unscanned org.
	orgARN := fmt.Sprintf("arn:aws:organizations::%s:organization/o-present0", testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeOrganization, orgARN, "", "{}")

	caARN := fmt.Sprintf("arn:aws:artifact::%s:customer-agreement/standalone", testAccountID)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeArtifactCustomerAgreement, caARN, "us-east-1", "{}")

	if err := resolveArtifactCustomerAgreementOrg(acct, st); err != nil {
		t.Fatalf("resolveArtifactCustomerAgreementOrg: %v", err)
	}
	rels, err := st.RelationshipsFrom(caID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("standalone agreement emitted %d edges, want 0", len(rels))
	}
}

func marshalArtifactCustomerAgreement(t *testing.T, orgARN string) string {
	t.Helper()
	name := "Enterprise Agreement"
	ca := artifacttypes.CustomerAgreementSummary{
		Name:            &name,
		OrganizationArn: &orgARN,
	}
	b, err := json.Marshal(ca)
	if err != nil {
		t.Fatalf("marshalArtifactCustomerAgreement: %v", err)
	}
	return string(b)
}
