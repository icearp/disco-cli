package aws

import (
	"testing"

	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveIoTAuditSuppressionTargets(t *testing.T) {
	roleARN := "arn:aws:iam::123456789012:role/r1"
	cases := []struct {
		name       string
		ri         *iottypes.ResourceIdentifier
		targetType string // "" seeds no target
		targetARN  string
	}{
		{"ca certificate id", &iottypes.ResourceIdentifier{CaCertificateId: ptrStr("ca1")}, TypeIoTCACertificate, iotTestARN("cacert", "ca1")},
		{"device certificate id", &iottypes.ResourceIdentifier{DeviceCertificateId: ptrStr("c1")}, TypeIoTCertificate, iotTestARN("cert", "c1")},
		{"device certificate arn", &iottypes.ResourceIdentifier{DeviceCertificateArn: ptrStr(iotTestARN("cert", "c2"))}, TypeIoTCertificate, iotTestARN("cert", "c2")},
		{
			"policy version", &iottypes.ResourceIdentifier{PolicyVersionIdentifier: &iottypes.PolicyVersionIdentifier{PolicyName: ptrStr("p1"), PolicyVersionId: ptrStr("3")}},
			TypeIoTPolicy, iotTestARN("policy", "p1"),
		},
		{"role alias", &iottypes.ResourceIdentifier{RoleAliasArn: ptrStr(iotTestARN("rolealias", "a1"))}, TypeIoTRoleAlias, iotTestARN("rolealias", "a1")},
		{"iam role", &iottypes.ResourceIdentifier{IamRoleArn: ptrStr(roleARN)}, TypeIAMRole, roleARN},
		{"unscanned target", &iottypes.ResourceIdentifier{DeviceCertificateId: ptrStr("gone")}, "", ""},
		{"member with no disco type", &iottypes.ResourceIdentifier{ClientId: ptrStr("dev1")}, "", ""},
		{"no resource identifier", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			attrs := mustJSON(iottypes.AuditSuppression{CheckName: ptrStr("CHECK"), ResourceIdentifier: tc.ri})
			fromID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuditSuppression, iotTestARN("audit-suppression", "CHECK/x"), testRegion, attrs)
			// A target of the right type with the wrong id must never be linked.
			upsertTestResource(t, st, "aws", acct.ID, TypeIoTCertificate, iotTestARN("cert", "other"), testRegion, "{}")
			var toID string
			if tc.targetType != "" {
				toID = upsertTestResource(t, st, "aws", acct.ID, tc.targetType, tc.targetARN, testRegion, "{}")
			}

			if err := resolveIoTAuditSuppressionTargets(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(fromID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if toID == "" {
				if len(rels) != 0 {
					t.Fatalf("relationships = %+v, want none", rels)
				}
				return
			}
			if len(rels) != 1 {
				t.Fatalf("relationships = %d, want 1", len(rels))
			}
			assertRelationship(t, rels, fromID, toID, store.RelAttachedTo)
		})
	}
}

func TestResolveIoTAuditSuppressionTargets_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fromID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuditSuppression, iotTestARN("audit-suppression", "CHECK/x"), testRegion, "{}")
	if err := resolveIoTAuditSuppressionTargets(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(fromID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("relationships = %+v, want none", rels)
	}
}

func TestResolveIoTAuditSuppressionTargets_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeIoTAuditSuppression, iotTestARN("audit-suppression", "CHECK/x"), testRegion, `{"ResourceIdentifier":"not-an-object"}`)
	if err := resolveIoTAuditSuppressionTargets(acct, st); err == nil {
		t.Fatal("resolve returned nil for undecodable attributes")
	}
}
