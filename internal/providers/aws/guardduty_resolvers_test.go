package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveGuardDutyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	did := "det-1"
	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s", testRegion, acct.ID, did)
	filterARN := detARN + "/filter/my-filter"
	ipsetARN := detARN + "/ipset/ips-1"
	bucket := "gd-threat-intel"
	ipsetAttrs := fmt.Sprintf(`{"Location":"s3://%s/badips.txt","Name":"bad-ips"}`, bucket)

	detID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyDetector, detARN, testRegion, "{}")
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyFilter, filterARN, testRegion, "{}")
	iID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyIPSet, ipsetARN, testRegion, ipsetAttrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")

	if err := resolveGuardDutyRelationships(acct, st); err != nil {
		t.Fatalf("resolveGuardDutyRelationships: %v", err)
	}

	detRels, _ := st.RelationshipsFrom(detID)
	assertRelationship(t, detRels, detID, fID, store.RelContains)
	assertRelationship(t, detRels, detID, iID, store.RelContains)

	iRels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, iRels, iID, bID, store.RelUses)
}

func TestResolveGuardDutyRelationships_HttpsS3URL(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/det-2", testRegion, acct.ID)
	ipsetARN := detARN + "/ipset/ips-2"
	bucket := "vhost-bucket"
	attrs := fmt.Sprintf(`{"Location":"https://%s.s3.us-east-1.amazonaws.com/ips.txt"}`, bucket)

	_ = upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyDetector, detARN, testRegion, "{}")
	iID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyIPSet, ipsetARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")

	if err := resolveGuardDutyRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, rels, iID, bID, store.RelUses)
}

// TestResolveGuardDutyMemberOrgAccount verifies a member row emits
// attached-to → its corresponding aws:organizations:account row when both
// rows are present in the same scan.
func TestResolveGuardDutyMemberOrgAccount(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	memberAcctID := "111122223333"
	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/det-m", testRegion, acct.ID)
	memberARN := detARN + "/member/" + memberAcctID
	memberAttrs := fmt.Sprintf(`{"AccountId":"%s","RelationshipStatus":"Enabled"}`, memberAcctID)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyMember, memberARN, testRegion, memberAttrs)

	orgAcctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", acct.ID, memberAcctID)
	orgAttrs := fmt.Sprintf(`{"Id":"%s","Arn":"%s"}`, memberAcctID, orgAcctARN)
	orgID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgAcctARN, "", orgAttrs)

	if err := resolveGuardDutyMemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveGuardDutyMemberOrgAccount: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, orgID, store.RelAttachedTo)
}

// TestResolveGuardDutyMemberOrgAccount_NoOrgTree verifies a no-op when the
// org tree was not scanned (loadOrgTargetIndex returns empty).
func TestResolveGuardDutyMemberOrgAccount_NoOrgTree(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/det-x", testRegion, acct.ID)
	memberARN := detARN + "/member/444455556666"
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyMember, memberARN, testRegion, `{"AccountId":"444455556666"}`)

	if err := resolveGuardDutyMemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolve no-org: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	if len(rels) != 0 {
		t.Errorf("expected 0 edges without org tree, got %d", len(rels))
	}
}

func TestParseS3Bucket(t *testing.T) {
	cases := map[string]string{
		"s3://my-bucket/key":                            "my-bucket",
		"s3://my-bucket":                                "my-bucket",
		"https://bucket.s3.amazonaws.com/x":             "bucket",
		"https://bucket.s3.us-east-1.amazonaws.com/x":   "bucket",
		"https://s3.amazonaws.com/path-bucket/x":        "path-bucket",
		"https://s3-us-east-1.amazonaws.com/path-bkt/x": "path-bkt",
		"":                     "",
		"http://example.com/x": "",
	}
	for in, want := range cases {
		if got := parseS3Bucket(in); got != want {
			t.Errorf("parseS3Bucket(%q) = %q; want %q", in, got, want)
		}
	}
}
