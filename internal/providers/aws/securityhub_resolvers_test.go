package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSecurityHubProductSubscriptions_GuardDuty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/guardduty", testRegion, acct.ID)
	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/abc123", testRegion, acct.ID)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyDetector, detARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}

	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, dID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("got %d edges, want 1", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_Macie(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/macie", testRegion, acct.ID)
	macieARN := macieSessionNativeID(acct.ID, testRegion)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeMacieSession, macieARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}

	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, mID, store.RelUses)
}

func TestResolveSecurityHubProductSubscriptions_SelfSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/securityhub", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("self subscription should emit no edges, got %d", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_NoUpstream(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// product matches mapping (guardduty) but no detector scanned — must skip.
	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/guardduty", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("missing upstream should emit no edges, got %d", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_ThirdParty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/crowdstrike/falcon", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("third-party vendor should emit no edges, got %d", len(rels))
	}
}

func TestParseSecurityHubProductSubscriptionARN(t *testing.T) {
	cases := []struct {
		arn             string
		vendor, product string
		ok              bool
	}{
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/aws/guardduty", "aws", "guardduty", true},
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/crowdstrike/falcon-host", "crowdstrike", "falcon-host", true},
		{"arn:aws:securityhub:us-east-1:111122223333:hub/default", "", "", false},
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/aws/", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		v, p, ok := parseSecurityHubProductSubscriptionARN(c.arn)
		if v != c.vendor || p != c.product || ok != c.ok {
			t.Errorf("parse(%q) = (%q,%q,%v), want (%q,%q,%v)", c.arn, v, p, ok, c.vendor, c.product, c.ok)
		}
	}
}

// shSeedMember stores a member row named name (ListResources orders by name)
// with the given attributes.
func shSeedMember(t *testing.T, st *store.Store, name, attrs string) string {
	t.Helper()
	r := &store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeSecurityHubMember, Name: &name, Region: ptrStr(testRegion),
		NativeID: shHubARN(testRegion) + "/member/" + name, AttributesJSON: attrs, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
		t.Fatalf("upsert %s: %v", name, err)
	}
	return r.ID
}

func shSeedOrgAccount(t *testing.T, st *store.Store, accountID string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testAccountID, accountID)
	return upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, arn, "", fmt.Sprintf(`{"Id":"%s","Arn":"%s"}`, accountID, arn))
}

// A member whose account is in the scanned org tree is attached to it; one
// outside the tree gets no edge.
func TestResolveSecurityHubMemberOrgAccount_AttachesScannedAccounts(t *testing.T) {
	st := newTestStore(t)
	inOrg := shSeedMember(t, st, "111111111111", mustJSON(shMember("111111111111", "Enabled")))
	outside := shSeedMember(t, st, "333333333333", mustJSON(shMember("333333333333", "Invited")))
	orgID := shSeedOrgAccount(t, st, "111111111111")
	shSeedOrgAccount(t, st, "222222222222")

	if err := resolveSecurityHubMemberOrgAccount(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(inOrg)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, inOrg, orgID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("member edges = %v, want only the org account", rels)
	}
	if rels, _ := st.RelationshipsFrom(outside); len(rels) != 0 {
		t.Errorf("member outside the org tree has edges %v, want none", rels)
	}
}

func TestResolveSecurityHubMemberOrgAccount_NoOrgTree(t *testing.T) {
	st := newTestStore(t)
	mID := shSeedMember(t, st, "111111111111", mustJSON(shMember("111111111111", "Enabled")))
	if err := resolveSecurityHubMemberOrgAccount(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(mID); len(rels) != 0 {
		t.Errorf("edges without an org tree = %v, want none", rels)
	}
}

// A row whose attributes do not parse is reported, and the rows after it are
// still wired.
func TestResolveSecurityHubMemberOrgAccount_MalformedRowReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	shSeedMember(t, st, "000000000000", "{")
	goodID := shSeedMember(t, st, "111111111111", mustJSON(shMember("111111111111", "Enabled")))
	orgID := shSeedOrgAccount(t, st, "111111111111")

	err := resolveSecurityHubMemberOrgAccount(newTestAccount(testAccountID), st)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("err = %v, want the malformed row's *json.SyntaxError", err)
	}
	rels, rerr := st.RelationshipsFrom(goodID)
	if rerr != nil {
		t.Fatalf("RelationshipsFrom: %v", rerr)
	}
	assertRelationship(t, rels, goodID, orgID, store.RelAttachedTo)
}
