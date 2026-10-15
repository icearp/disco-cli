package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveCloudTrailResourcePolicyToParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/main", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailTrail, tARN, testRegion, "{}")
	rpTrailID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, tARN+"/policy", testRegion, "{}")

	edsARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/uuid-1", testRegion, acct.ID)
	edsID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailEventDataStore, edsARN, testRegion, "{}")
	rpEDSID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, edsARN+"/policy", testRegion, "{}")

	chARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:channel/c1", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailChannel, chARN, testRegion, "{}")
	rpChID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, chARN+"/policy", testRegion, "{}")

	if err := resolveCloudTrailResourcePolicyToParent(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailResourcePolicyToParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpTrailID)
	assertRelationship(t, rels, rpTrailID, tID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(rpEDSID)
	assertRelationship(t, rels, rpEDSID, edsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(rpChID)
	assertRelationship(t, rels, rpChID, chID, store.RelAttachedTo)
}

func TestResolveCloudTrailChannelDestinations(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	chARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:channel/abc-123", testRegion, acct.ID)
	edsARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/eds-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Destinations":[{"Type":"EVENT_DATA_STORE","Location":%q},{"Type":"AWS_SERVICE","Location":"config.amazonaws.com"}]}`, edsARN)

	chID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailChannel, chARN, testRegion, attrs)
	edsID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailEventDataStore, edsARN, testRegion, "{}")

	if err := resolveCloudTrailChannelDestinations(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailChannelDestinations: %v", err)
	}
	rels, _ := st.RelationshipsFrom(chID)
	assertRelationship(t, rels, chID, edsID, store.RelRoutesTo)
}

// An event data store's Insights selector routes to its scanned destination
// store; one whose destination was not scanned, and a trail's (no
// destination), route nowhere.
func TestResolveCloudTrailInsightDestinations(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	srcARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/src", testRegion, acct.ID)
	destARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/dest", testRegion, acct.ID)
	otherARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/other", testRegion, acct.ID)
	trailARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/main", testRegion, acct.ID)
	selectors := []cloudtrailtypes.InsightSelector{{InsightType: cloudtrailtypes.InsightTypeApiCallRateInsight}}

	destID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailEventDataStore, destARN, testRegion, "{}")
	srcID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailInsightSelector, srcARN+"/insight-selector", testRegion,
		mustJSON(&cloudtrail.GetInsightSelectorsOutput{EventDataStoreArn: &srcARN, InsightsDestination: &destARN, InsightSelectors: selectors}))
	otherID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailInsightSelector, otherARN+"/insight-selector", testRegion,
		mustJSON(&cloudtrail.GetInsightSelectorsOutput{EventDataStoreArn: &otherARN, InsightsDestination: ptrStr(otherARN + "-unscanned"), InsightSelectors: selectors}))
	trailID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailInsightSelector, trailARN+"/insight-selector", testRegion,
		mustJSON(&cloudtrail.GetInsightSelectorsOutput{TrailARN: &trailARN, InsightSelectors: selectors}))

	if err := resolveCloudTrailInsightDestinations(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailInsightDestinations: %v", err)
	}
	rels, err := st.RelationshipsFrom(srcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, srcID, destID, store.RelRoutesTo)
	if len(rels) != 1 {
		t.Errorf("source selector edges = %v, want only the routes-to edge", rels)
	}
	for _, id := range []string{otherID, trailID} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 0 {
			t.Errorf("selector %s edges = %v, want none", id, rels)
		}
	}
}

func TestResolveCloudTrailInsightDestinations_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailInsightSelector,
		"arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/src/insight-selector", testRegion, `{"InsightsDestination":7}`)
	var typeErr *json.UnmarshalTypeError
	if err := resolveCloudTrailInsightDestinations(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the attributes decode error", err)
	}
}
