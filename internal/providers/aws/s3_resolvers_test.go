package aws

import (
	"fmt"
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// --- Bucket Policy ---

// TestResolveS3BucketPolicyRelationships verifies that a bucket policy is
// linked to its bucket by stripping the "/policy" suffix from the NativeID.
func TestResolveS3BucketPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::my-bucket"
	policyARN := bucketARN + "/policy"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeS3BucketPolicy, policyARN, "", `{"Policy":"{}"}`)

	if err := resolveS3BucketPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3BucketPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, bucketID, store.RelAttachedTo)
}

// TestResolveS3BucketPolicyRelationships_Empty verifies that no error is
// returned when there are no bucket policies.
func TestResolveS3BucketPolicyRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveS3BucketPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3BucketPolicyRelationships: %v", err)
	}
}

// --- Access Grant ---

// TestResolveS3AccessGrantRelationships verifies that access grants are linked
// to their access grants instance in the same region.
func TestResolveS3AccessGrantRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instanceARN := fmt.Sprintf("arn:aws:s3:%s:%s:access-grants/default", testRegion, testAccountID)
	instanceID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessGrantsInstance, instanceARN, testRegion, "{}")

	grantARN := fmt.Sprintf("arn:aws:s3:%s:%s:access-grants/default/grant/abc123", testRegion, testAccountID)
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessGrant, grantARN, testRegion, `{"AccessGrantId":"abc123"}`)

	if err := resolveS3AccessGrantRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessGrantRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, grantID, instanceID, store.RelAttachedTo)
}

// TestResolveS3AccessGrantRelationships_Empty verifies graceful handling when
// there are no access grants.
func TestResolveS3AccessGrantRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveS3AccessGrantRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessGrantRelationships: %v", err)
	}
}

// --- Access Grants Location ---

// TestResolveS3AccessGrantsLocationRelationships verifies that each location
// is linked to the access grants instance in its region.
func TestResolveS3AccessGrantsLocationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instanceARN := fmt.Sprintf("arn:aws:s3:%s:%s:access-grants/default", testRegion, testAccountID)
	instanceID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessGrantsInstance, instanceARN, testRegion, "{}")

	locationARN := fmt.Sprintf("arn:aws:s3:%s:%s:access-grants/default/location/default", testRegion, testAccountID)
	locationID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessGrantsLocation, locationARN, testRegion, `{"AccessGrantsLocationId":"default"}`)

	if err := resolveS3AccessGrantsLocationRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessGrantsLocationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(locationID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, locationID, instanceID, store.RelAttachedTo)
}

// TestResolveS3AccessGrantsLocationRelationships_Empty verifies graceful
// handling when there are no locations.
func TestResolveS3AccessGrantsLocationRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveS3AccessGrantsLocationRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessGrantsLocationRelationships: %v", err)
	}
}

// --- Access Point ---

// TestResolveS3AccessPointRelationships verifies that an access point is
// linked to its bucket by constructing the bucket ARN from the Bucket field.
func TestResolveS3AccessPointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::my-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	apARN := fmt.Sprintf("arn:aws:s3:%s:%s:accesspoint/my-ap", testRegion, testAccountID)
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessPoint, apARN, testRegion, `{"Bucket":"my-bucket","Name":"my-ap"}`)

	if err := resolveS3AccessPointRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessPointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(apID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, apID, bucketID, store.RelAttachedTo)
}

// TestResolveS3AccessPointRelationships_NoAttrs verifies that an access point
// with no Bucket attribute does not produce a relationship (no nil-pointer panic).
func TestResolveS3AccessPointRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apARN := fmt.Sprintf("arn:aws:s3:%s:%s:accesspoint/bare-ap", testRegion, testAccountID)
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeS3AccessPoint, apARN, testRegion, "{}")

	if err := resolveS3AccessPointRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3AccessPointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(apID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- MRAP Policy ---

// TestResolveS3MRAPPolicyRelationships verifies that an MRAP policy is linked
// to its MRAP by stripping the "/policy" suffix from the NativeID.
func TestResolveS3MRAPPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	mrapARN := fmt.Sprintf("arn:aws:s3::%s:accesspoint/my-mrap", testAccountID)
	policyARN := mrapARN + "/policy"
	mrapID := upsertTestResource(t, st, "aws", acct.ID, TypeS3MultiRegionAccessPoint, mrapARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeS3MultiRegionAccessPointPolicy, policyARN, "", `{"Established":{"Policy":"{}"}}`)

	if err := resolveS3MRAPPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3MRAPPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, mrapID, store.RelAttachedTo)
}

// TestResolveS3MRAPPolicyRelationships_Empty verifies graceful handling when
// there are no MRAP policies.
func TestResolveS3MRAPPolicyRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveS3MRAPPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveS3MRAPPolicyRelationships: %v", err)
	}
}
