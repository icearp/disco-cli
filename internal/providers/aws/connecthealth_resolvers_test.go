package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/connecthealth"
	chtypes "github.com/aws/aws-sdk-go-v2/service/connecthealth/types"
	"github.com/icearp/disco-cli/store"
)

// haEdgeTargets returns the targets of fromID's edges of kind.
func haEdgeTargets(t *testing.T, st *store.Store, fromID, kind string) []string {
	t.Helper()
	rels, err := st.RelationshipsFrom(fromID, kind)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var to []string
	for _, r := range rels {
		to = append(to, r.ToID)
	}
	return to
}

func TestResolveHealthAgentDomainRefs_KeyAndRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ctxKeyARN := "arn:aws:kms:" + haRegion + ":" + testAccountID + ":key/2222bbbb-0000-0000-0000-000000000000"
	attrs := mustJSON(&connecthealth.GetDomainOutput{
		Arn: ptrStr(haDomARNA), KmsKeyArn: ptrStr(haKeyARN),
		EncryptionContext:   &chtypes.EncryptionContext{EncryptionType: chtypes.EncryptionTypeCustomerManagedKey, KmsKeyArn: ptrStr(ctxKeyARN)},
		WebAppConfiguration: &chtypes.WebAppConfiguration{EhrRole: ptrStr(haRoleARN)},
	})
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeHealthAgentDomain, haDomARNA, haRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, haKeyARN, haRegion, "{}")
	ckID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, ctxKeyARN, haRegion, "{}")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, haRoleARN, "", "{}")

	if err := resolveHealthAgentDomainRefs(acct, st); err != nil {
		t.Fatalf("resolveHealthAgentDomainRefs: %v", err)
	}
	uses, err := st.RelationshipsFrom(dID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, uses, dID, kID, store.RelUses)
	assertRelationship(t, uses, dID, ckID, store.RelUses)
	assumes, err := st.RelationshipsFrom(dID, store.RelAssumes)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, assumes, dID, rID, store.RelAssumes)
}

// A domain with no key, no web app configuration, or refs to unscanned
// targets gets no edge.
func TestResolveHealthAgentDomainRefs_NoEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  *connecthealth.GetDomainOutput
	}{
		{name: "no refs", out: &connecthealth.GetDomainOutput{Arn: ptrStr(haDomARNA)}},
		{name: "unscanned targets", out: &connecthealth.GetDomainOutput{
			Arn: ptrStr(haDomARNA), KmsKeyArn: ptrStr(haKeyARN),
			WebAppConfiguration: &chtypes.WebAppConfiguration{EhrRole: ptrStr(haRoleARN)},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			dID := upsertTestResource(t, st, "aws", acct.ID, TypeHealthAgentDomain, haDomARNA, haRegion, mustJSON(tc.out))
			if err := resolveHealthAgentDomainRefs(acct, st); err != nil {
				t.Fatalf("resolveHealthAgentDomainRefs: %v", err)
			}
			for _, kind := range []string{store.RelUses, store.RelAssumes} {
				if to := haEdgeTargets(t, st, dID, kind); len(to) != 0 {
					t.Errorf("%s edges = %v, want none", kind, to)
				}
			}
		})
	}
}

// A row whose attributes do not decode is reported, and the other rows still
// get their edges.
func TestResolveHealthAgentDomainRefs_DecodeErrorKeepsOtherRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeHealthAgentDomain, haDomARNB, haRegion, `{"KmsKeyArn":7}`)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeHealthAgentDomain, haDomARNA, haRegion,
		mustJSON(&connecthealth.GetDomainOutput{Arn: ptrStr(haDomARNA), KmsKeyArn: ptrStr(haKeyARN)}))
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, haKeyARN, haRegion, "{}")

	if err := resolveHealthAgentDomainRefs(acct, st); err == nil {
		t.Error("resolveHealthAgentDomainRefs = nil, want the decode error")
	}
	uses, err := st.RelationshipsFrom(dID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, uses, dID, kID, store.RelUses)
}
