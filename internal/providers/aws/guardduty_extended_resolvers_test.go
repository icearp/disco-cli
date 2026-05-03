package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestGuarddutyDetectorARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:guardduty:us-east-1:123:detector/abc/filter/f1", "arn:aws:guardduty:us-east-1:123:detector/abc"},
		{"arn:aws:guardduty:us-east-1:123:detector/abc/publishingDestination/p1", "arn:aws:guardduty:us-east-1:123:detector/abc"},
		{"arn:aws:guardduty:us-east-1:123:detector/abc", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := guarddutyDetectorARNFromChild(c.in); got != c.want {
			t.Errorf("guarddutyDetectorARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveGuardDutyChildrenToDetector(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/abc", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyDetector, dARN, testRegion, "{}")
	fARN := dARN + "/filter/f1"
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyFilter, fARN, testRegion, "{}")
	pdARN := dARN + "/publishingDestination/p1"
	pdID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyPublishingDestination, pdARN, testRegion, "{}")
	tisARN := dARN + "/threatintelset/ti1"
	tisID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyThreatIntelSet, tisARN, testRegion, "{}")
	if err := resolveGuardDutyChildrenToDetector(acct, st); err != nil {
		t.Fatalf("resolveGuardDutyChildrenToDetector: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, dID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(pdID)
	assertRelationship(t, rels, pdID, dID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(tisID)
	assertRelationship(t, rels, tisID, dID, store.RelAttachedTo)
}
