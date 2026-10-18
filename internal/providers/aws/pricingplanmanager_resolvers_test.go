package aws

import (
	"testing"

	ppmtypes "github.com/aws/aws-sdk-go-v2/service/pricingplanmanager/types"
	"github.com/icearp/disco-cli/store"
)

// A subscription uses every scanned resource its ResourceArns names, of each
// target type; an unscanned ARN, and a subscription with no attributes, get no
// edge.
func TestResolvePricingPlanSubscriptionResources_Edges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, ppmDistARN, "global", "{}")
	aclID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, ppmWebACLARN, "us-east-1", "{}")
	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, ppmZoneARN, "global", "{}")
	kvsID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontKeyValueStore, ppmKVSARN, "global", "{}")
	all := upsertTestResource(t, st, "aws", acct.ID, TypePricingPlanManagerSubscription, ppmSubA, "global",
		mustJSON(ppmSummary(ppmSubA, ppmtypes.StatusActive, ppmDistARN, ppmWebACLARN, ppmZoneARN, ppmKVSARN, ppmUnknownARN)))
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypePricingPlanManagerSubscription, ppmSubB, "global", "{}")

	if err := resolvePricingPlanSubscriptionResources(acct, st); err != nil {
		t.Fatalf("resolvePricingPlanSubscriptionResources: %v", err)
	}
	got := outEdges(t, st, all)
	if len(got) != 4 {
		t.Errorf("sub-a edges = %v, want 4", got)
	}
	for name, id := range map[string]string{"distribution": distID, "web ACL": aclID, "hosted zone": zoneID, "key value store": kvsID} {
		if got[id] != store.RelUses {
			t.Errorf("sub-a → %s edge = %q, want %s", name, got[id], store.RelUses)
		}
	}
	if got := outEdges(t, st, noAttrs); len(got) != 0 {
		t.Errorf("no-attrs subscription edges = %v, want none", got)
	}
}

// A row whose attributes do not decode is reported, and costs the other rows
// none of their edges.
func TestResolvePricingPlanSubscriptionResources_DecodeError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, ppmDistARN, "global", "{}")
	good := upsertTestResource(t, st, "aws", acct.ID, TypePricingPlanManagerSubscription, ppmSubA, "global",
		mustJSON(ppmSummary(ppmSubA, ppmtypes.StatusActive, ppmDistARN)))
	upsertTestResource(t, st, "aws", acct.ID, TypePricingPlanManagerSubscription, ppmSubB, "global", `{"ResourceArns":"not-a-list"}`)

	if err := resolvePricingPlanSubscriptionResources(acct, st); err == nil {
		t.Error("err = nil, want the decode error")
	}
	if got := outEdges(t, st, good); len(got) != 1 || got[distID] != store.RelUses {
		t.Errorf("sub-a edges = %v, want uses → %s", got, distID)
	}
}
