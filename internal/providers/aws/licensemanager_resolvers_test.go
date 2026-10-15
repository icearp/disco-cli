package aws

import (
	"fmt"
	"testing"

	lmtypes "github.com/aws/aws-sdk-go-v2/service/licensemanager/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveLicenseManagerGrantToLicense(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-1", acct.ID)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, lARN, "", "{}")
	gARN := fmt.Sprintf("arn:aws:license-manager::%s:grant:g-1", acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerGrant, gARN, "", fmt.Sprintf(`{"LicenseArn":"%s"}`, lARN))
	if err := resolveLicenseManagerGrantToLicense(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerGrantToLicense: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gID)
	assertRelationship(t, rels, gID, lID, store.RelAttachedTo)
}

// lmRelsFrom returns every relationship from id, failing the test on a read error.
func lmRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	return rels
}

func TestResolveLicenseManagerReceivedLicenseToLicense_SelfGrant(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ownARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-own", acct.ID)
	foreignARN := "arn:aws:license-manager::444455556666:license:l-foreign"
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, ownARN, "global", "{}")
	selfID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerReceivedLicense, lmReceivedLicenseNativeID(ownARN), "global",
		mustJSON(lmtypes.GrantedLicense{LicenseArn: &ownARN}))
	foreignID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerReceivedLicense, lmReceivedLicenseNativeID(foreignARN), "global",
		mustJSON(lmtypes.GrantedLicense{LicenseArn: &foreignARN}))
	if err := resolveLicenseManagerReceivedLicenseToLicense(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerReceivedLicenseToLicense: %v", err)
	}
	rels := lmRelsFrom(t, st, selfID)
	assertRelationship(t, rels, selfID, lID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("self-granted license edges = %v, want only the attached-to", rels)
	}
	if rels := lmRelsFrom(t, st, foreignID); len(rels) != 0 {
		t.Errorf("license issued elsewhere got edges %v, want none", rels)
	}
}

func TestResolveLicenseManagerReceivedLicenseToLicense_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ownARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-own", acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, ownARN, "global", "{}")
	id := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerReceivedLicense, lmReceivedLicenseNativeID(ownARN), "global", "{}")
	if err := resolveLicenseManagerReceivedLicenseToLicense(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerReceivedLicenseToLicense: %v", err)
	}
	if rels := lmRelsFrom(t, st, id); len(rels) != 0 {
		t.Errorf("edges = %v, want none without a LicenseArn", rels)
	}
}

func TestResolveLicenseManagerTokenRelationships_LicenseAndRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	licARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-1", acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/scanned", acct.ID)
	unscannedRole := fmt.Sprintf("arn:aws:iam::%s:role/unscanned", acct.ID)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, licARN, "global", "{}")
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "global", "{}")
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerToken, lmTokenNativeID(licARN, "t1"), "global",
		mustJSON(lmtypes.TokenData{TokenId: ptrStr("t1"), LicenseArn: &licARN, RoleArns: []string{roleARN, unscannedRole}}))
	if err := resolveLicenseManagerTokenRelationships(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerTokenRelationships: %v", err)
	}
	rels := lmRelsFrom(t, st, tokID)
	assertRelationship(t, rels, tokID, lID, store.RelUses)
	assertRelationship(t, rels, tokID, roleID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("token edges = %v, want 2 (license, scanned role); the unscanned role gets none", rels)
	}
}

func TestResolveLicenseManagerTokenRelationships_ForeignIssuerUsesReceivedLicense(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	foreignARN := "arn:aws:license-manager::444455556666:license:l-foreign"
	ownARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-own", acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, ownARN, "global", "{}")
	recvID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerReceivedLicense, lmReceivedLicenseNativeID(foreignARN), "global",
		mustJSON(lmtypes.GrantedLicense{LicenseArn: &foreignARN}))
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerToken, lmTokenNativeID(foreignARN, "t1"), "global",
		mustJSON(lmtypes.TokenData{TokenId: ptrStr("t1"), LicenseArn: &foreignARN}))
	if err := resolveLicenseManagerTokenRelationships(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerTokenRelationships: %v", err)
	}
	rels := lmRelsFrom(t, st, tokID)
	assertRelationship(t, rels, tokID, recvID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("token edges = %v, want only the received license; the issuer's license is not a row here", rels)
	}
}

func TestResolveLicenseManagerTokenRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	licARN := fmt.Sprintf("arn:aws:license-manager::%s:license:l-1", acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerLicense, licARN, "global", "{}")
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeLicenseManagerToken, lmTokenNativeID(licARN, "t1"), "global", "{}")
	if err := resolveLicenseManagerTokenRelationships(acct, st); err != nil {
		t.Fatalf("resolveLicenseManagerTokenRelationships: %v", err)
	}
	if rels := lmRelsFrom(t, st, tokID); len(rels) != 0 {
		t.Errorf("edges = %v, want none without refs", rels)
	}
}

func TestResolveLicenseManager_UndecodableAttrsReturnError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		resolve func(*account, *store.Store) error
	}{
		{"received-license", TypeLicenseManagerReceivedLicense, resolveLicenseManagerReceivedLicenseToLicense},
		{"token", TypeLicenseManagerToken, resolveLicenseManagerTokenRelationships},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, tc.typ, "arn:aws:license-manager::123456789012:license:l-1/x", "global", `{"LicenseArn":7}`)
			if err := tc.resolve(acct, st); err == nil {
				t.Error("err = nil, want the attribute decode error")
			}
		})
	}
}
