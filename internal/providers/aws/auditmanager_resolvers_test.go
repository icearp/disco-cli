package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testAMAssessmentID  = "11111111-1111-1111-1111-111111111111"
	testAMFrameworkID   = "22222222-2222-2222-2222-222222222222"
	testAMRoleName      = "AuditManagerExecRole"
	testAMReportsBucket = "auditmgr-reports"
)

func amAssessmentARN() string {
	return fmt.Sprintf("arn:aws:auditmanager:%s:%s:assessment/%s", testRegion, testAccountID, testAMAssessmentID)
}

func amFrameworkARN() string {
	return fmt.Sprintf("arn:aws:auditmanager:%s:%s:assessmentFramework/%s", testRegion, testAccountID, testAMFrameworkID)
}

// TestResolveAuditManagerAssessmentTargets exercises every assessment
// outbound edge: framework, IAM role, S3 reports bucket.
func TestResolveAuditManagerAssessmentTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fID := upsertTestResource(t, st, "aws", acct.ID, TypeAuditManagerFramework, amFrameworkARN(), testRegion, "{}")

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testAMRoleName)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	bucketARN := "arn:aws:s3:::" + testAMReportsBucket
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	assessmentAttrs := fmt.Sprintf(`{"Arn":%q,"Framework":{"Arn":%q},"Metadata":{"Name":"q4-soc2","Status":"ACTIVE","Roles":[{"RoleType":"PROCESS_OWNER","RoleArn":%q}],"AssessmentReportsDestination":{"Destination":"s3://%s","DestinationType":"S3"}}}`,
		amAssessmentARN(), amFrameworkARN(), roleARN, testAMReportsBucket)
	assessmentID := upsertTestResource(t, st, "aws", acct.ID, TypeAuditManagerAssessment, amAssessmentARN(), testRegion, assessmentAttrs)

	if err := resolveAuditManagerAssessmentTargets(acct, st); err != nil {
		t.Fatalf("resolveAuditManagerAssessmentTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(assessmentID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assessmentID, fID, store.RelUses)
	assertRelationship(t, rels, assessmentID, roleID, store.RelAssumes)
	assertRelationship(t, rels, assessmentID, bID, store.RelUses)
}

// TestResolveAuditManagerAssessmentTargets_MissingMetadata verifies
// assessments missing Metadata or Framework skip cleanly.
func TestResolveAuditManagerAssessmentTargets_MissingMetadata(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	id := upsertTestResource(t, st, "aws", acct.ID, TypeAuditManagerAssessment, amAssessmentARN(), testRegion,
		fmt.Sprintf(`{"Arn":%q}`, amAssessmentARN()))

	if err := resolveAuditManagerAssessmentTargets(acct, st); err != nil {
		t.Fatalf("resolveAuditManagerAssessmentTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}
