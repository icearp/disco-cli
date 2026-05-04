package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveIoTSecurityProfileRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cmARN := fmt.Sprintf("arn:aws:iot:%s:%s:custommetric/myMetric", testRegion, acct.ID)
	cmName := "myMetric"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeIoTCustomMetric,
		NativeID: cmARN, Name: &cmName, Region: pstr(testRegion),
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert cm: %v", err)
	}
	cmID := store.ResourceID("aws", acct.ID, TypeIoTCustomMetric, cmARN)
	dARN := fmt.Sprintf("arn:aws:iot:%s:%s:dimension/myDim", testRegion, acct.ID)
	dName := "myDim"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeIoTDimension,
		NativeID: dARN, Name: &dName, Region: pstr(testRegion),
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert dim: %v", err)
	}
	dID := store.ResourceID("aws", acct.ID, TypeIoTDimension, dARN)
	spARN := fmt.Sprintf("arn:aws:iot:%s:%s:securityprofile/sp-1", testRegion, acct.ID)
	attrs := `{"Behaviors":[{"Name":"b1","Metric":"myMetric","MetricDimension":{"DimensionName":"myDim"}},{"Metric":"aws:num-disconnects"}],"AdditionalMetricsToRetainV2":[{"MetricDimension":{"DimensionName":"myDim"}}]}`
	spID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSecurityProfile, spARN, testRegion, attrs)
	if err := resolveIoTSecurityProfileRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTSecurityProfileRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(spID)
	assertRelationship(t, rels, spID, cmID, store.RelUses)
	assertRelationship(t, rels, spID, dID, store.RelUses)
}

func pstr(s string) *string { return &s }
