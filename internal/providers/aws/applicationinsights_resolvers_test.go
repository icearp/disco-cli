package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationinsights"
	aitypes "github.com/aws/aws-sdk-go-v2/service/applicationinsights/types"
	"github.com/icearp/disco-cli/store"
)

func aiComponentAttrs(resources ...string) string {
	return mustJSON(&applicationinsights.DescribeComponentOutput{
		ApplicationComponent: &aitypes.ApplicationComponent{ComponentName: sdkaws.String("web")},
		ResourceList:         resources,
	})
}

func TestResolveAppInsightsComponentMembers_ScannedMembersOnly(t *testing.T) {
	st := newTestStore(t)
	instance := "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc"
	queue := "arn:aws:sqs:us-east-1:123456789012:orders"
	unscanned := "arn:aws:ec2:us-east-1:123456789012:instance/i-gone"
	snsTopic := "arn:aws:sns:us-east-1:123456789012:alerts"
	instanceID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2Instance, instance, testRegion, "{}")
	queueID := upsertTestResource(t, st, "aws", testAccountID, TypeSQSQueue, queue, testRegion, "{}")
	// Scanned, but not a type a component can be made of.
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, snsTopic, testRegion, "{}")
	compID := upsertTestResource(t, st, "aws", testAccountID, TypeApplicationInsightsComponent, aiTestCompWeb, testRegion,
		aiComponentAttrs(instance, queue, unscanned, snsTopic))

	if err := resolveAppInsightsComponentMembers(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(compID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, compID, instanceID, store.RelUses)
	assertRelationship(t, rels, compID, queueID, store.RelUses)
	for _, r := range rels {
		if r.ToID == topicID {
			t.Errorf("edge to %s, which is not a component member type", snsTopic)
		}
	}
	if len(rels) != 2 {
		t.Errorf("edges = %d, want 2 (instance and queue only)", len(rels))
	}
}

func TestResolveAppInsightsComponentMembers_CorruptAttributes(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2Instance, "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc", testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeApplicationInsightsComponent, aiTestCompWeb, testRegion, `{"ResourceList":"not-a-list"}`)
	var typeErr *json.UnmarshalTypeError
	if err := resolveAppInsightsComponentMembers(newTestAccount(testAccountID), st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the attributes decode error", err)
	}
}

func TestResolveAppInsightsComponentMembers_NoResourceList(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2Instance, "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc", testRegion, "{}")
	compID := upsertTestResource(t, st, "aws", testAccountID, TypeApplicationInsightsComponent, aiTestCompWeb, testRegion, "{}")
	if err := resolveAppInsightsComponentMembers(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(compID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %d, want 0", len(rels))
	}
}
