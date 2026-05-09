package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveStorageLens_IncludeBuckets verifies uses edges from a Storage Lens
// configuration to each bucket in Include.Buckets[].
func TestResolveStorageLens_IncludeBuckets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	b1ARN := "arn:aws:s3:::bucket-one"
	b2ARN := "arn:aws:s3:::bucket-two"
	b1ID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, b1ARN, "", `{}`)
	b2ID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, b2ARN, "", `{}`)

	slARN := "arn:aws:s3:us-east-1:123456789012:storage-lens/my-dashboard"
	slAttrs := `{"Id":"my-dashboard","Include":{"Buckets":["` + b1ARN + `","` + b2ARN + `"]}}`
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeS3StorageLens, slARN, testRegion, slAttrs)

	if err := resolveStorageLensRelationships(acct, st); err != nil {
		t.Fatalf("resolveStorageLensRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(slID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, slID, b1ID, store.RelUses)
	assertRelationship(t, rels, slID, b2ID, store.RelUses)
}

// TestResolveStorageLens_ExportBucket verifies uses edge to the S3 data-export
// destination bucket.
func TestResolveStorageLens_ExportBucket(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	expARN := "arn:aws:s3:::export-sink"
	expID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, expARN, "", `{}`)

	slARN := "arn:aws:s3:us-east-1:123456789012:storage-lens/export-demo"
	slAttrs := `{"Id":"export-demo","DataExport":{"S3BucketDestination":{"Arn":"` + expARN + `"}}}`
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeS3StorageLens, slARN, testRegion, slAttrs)

	if err := resolveStorageLensRelationships(acct, st); err != nil {
		t.Fatalf("resolveStorageLensRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(slID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, slID, expID, store.RelUses)
}

// TestResolveStorageLens_UnscannedBucket verifies no edge and no FK error when
// the Storage Lens references a bucket not present in the store (cross-account
// export target case).
func TestResolveStorageLens_UnscannedBucket(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	slARN := "arn:aws:s3:us-east-1:123456789012:storage-lens/orphan"
	slAttrs := `{"Id":"orphan","Include":{"Buckets":["arn:aws:s3:::missing-bucket"]},"DataExport":{"S3BucketDestination":{"Arn":"arn:aws:s3:::missing-export"}}}`
	slID := upsertTestResource(t, st, "aws", acct.ID, TypeS3StorageLens, slARN, testRegion, slAttrs)

	if err := resolveStorageLensRelationships(acct, st); err != nil {
		t.Fatalf("resolveStorageLensRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(slID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unscanned buckets: %+v", rels)
	}
}

// TestResolveStorageLens_EmptyAttrs verifies no panic/edges when neither
// Include nor DataExport is set.
func TestResolveStorageLens_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	slARN := "arn:aws:s3:us-east-1:123456789012:storage-lens/minimal"
	slAttrs := `{"Id":"minimal","IsEnabled":true,"AccountLevel":{"BucketLevel":{}}}`
	upsertTestResource(t, st, "aws", acct.ID, TypeS3StorageLens, slARN, testRegion, slAttrs)

	if err := resolveStorageLensRelationships(acct, st); err != nil {
		t.Fatalf("resolveStorageLensRelationships: %v", err)
	}
}

func TestResolveS3MRAPRegionBuckets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	mrapARN := "arn:aws:s3::" + testAccountID + ":accesspoint/myMrap"
	bARN := "arn:aws:s3:::primary-data"
	attrs := `{"Regions":[{"Bucket":"primary-data"},{"Bucket":"unscanned"}]}`
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeS3MultiRegionAccessPoint, mrapARN, "", attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, "us-east-1", "{}")

	if err := resolveS3MRAPRegionBuckets(acct, st); err != nil {
		t.Fatalf("resolveS3MRAPRegionBuckets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, bID, store.RelUses)
}
