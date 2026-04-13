package aws

import (
	"testing"
)

// --- resolveLogsDeliveryLinks ---

func TestResolveLogsDeliveryLinks(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	srcARN := "arn:aws:logs:us-east-1:123456789012:delivery-source:my-source"
	destARN := "arn:aws:logs:us-east-1:123456789012:delivery-destination:my-dest"
	deliveryARN := "arn:aws:logs:us-east-1:123456789012:delivery:my-delivery"

	attrsJSON := `{"DeliverySourceName":"my-source","DeliveryDestinationArn":"` + destARN + `"}`

	deliveryID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsDelivery, deliveryARN, "us-east-1", attrsJSON)
	srcID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsDeliverySource, srcARN, "us-east-1", `{"Name":"my-source"}`)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsDeliveryDest, destARN, "us-east-1", "{}")

	if err := resolveLogsDeliveryLinks(acct, st); err != nil {
		t.Fatalf("resolveLogsDeliveryLinks: %v", err)
	}

	rels, err := st.RelationshipsFrom(deliveryID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, deliveryID, srcID, "uses")
	assertRelationship(t, rels, deliveryID, destID, "uses")
}

func TestResolveLogsDeliveryLinks_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	deliveryARN := "arn:aws:logs:us-east-1:123456789012:delivery:empty"
	upsertTestResource(t, st, "aws", acct.ID, TypeLogsDelivery, deliveryARN, "us-east-1", "{}")

	// Should not error when attrs have no source/destination references.
	if err := resolveLogsDeliveryLinks(acct, st); err != nil {
		t.Fatalf("resolveLogsDeliveryLinks (empty): %v", err)
	}
}

func TestResolveLogsDeliveryLinks_NoDeliveries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// No resources at all — should return nil without panicking.
	if err := resolveLogsDeliveryLinks(acct, st); err != nil {
		t.Fatalf("resolveLogsDeliveryLinks (no deliveries): %v", err)
	}
}

// --- resolveLogsGroupAnomalyDetectors ---

func TestResolveLogsGroupAnomalyDetectors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Log group NativeID is the clean ARN (no trailing ":*").
	groupARN := "arn:aws:logs:us-east-1:123456789012:log-group:my-group"
	detectorARN := "arn:aws:logs:us-east-1:123456789012:anomaly-detector:det1"

	// Anomaly detector stores ARNs with trailing ":*" as returned by the API;
	// the resolver must strip it before lookup.
	attrsJSON := `{"LogGroupArnList":["` + groupARN + `:*"]}`

	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, groupARN, "us-east-1", "{}")
	detectorID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogAnomalyDetector, detectorARN, "us-east-1", attrsJSON)

	if err := resolveLogsGroupAnomalyDetectors(acct, st); err != nil {
		t.Fatalf("resolveLogsGroupAnomalyDetectors: %v", err)
	}

	rels, err := st.RelationshipsFrom(detectorID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, detectorID, groupID, "uses")
}

func TestResolveLogsGroupAnomalyDetectors_EmptyList(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	detectorARN := "arn:aws:logs:us-east-1:123456789012:anomaly-detector:empty"
	upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogAnomalyDetector, detectorARN, "us-east-1", `{"LogGroupArnList":[]}`)

	if err := resolveLogsGroupAnomalyDetectors(acct, st); err != nil {
		t.Fatalf("resolveLogsGroupAnomalyDetectors (empty): %v", err)
	}
}

func TestResolveLogsGroupAnomalyDetectors_NoDetectors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// No detectors at all — should return nil without panicking.
	if err := resolveLogsGroupAnomalyDetectors(acct, st); err != nil {
		t.Fatalf("resolveLogsGroupAnomalyDetectors (no detectors): %v", err)
	}
}
