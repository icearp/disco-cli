package aws

import (
	"fmt"
	"testing"

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
