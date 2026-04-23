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

	fRels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, fRels, fID, detID, store.RelContains)

	iRels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, iRels, iID, detID, store.RelContains)
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
