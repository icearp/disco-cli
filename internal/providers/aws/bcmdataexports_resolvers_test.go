package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBCMDataExportsRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucket := "my-billing-bucket"
	bARN := fmt.Sprintf("arn:aws:s3:::%s", bucket)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, "", "{}")

	arn := fmt.Sprintf("arn:aws:bcm-data-exports::%s:export/exp-1", acct.ID)
	attrs := fmt.Sprintf(`{"ExportArn":%q,"DestinationConfigurations":{"S3Destination":{"S3Bucket":%q}}}`, arn, bucket)
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDataExportsExport, arn, testRegion, attrs)

	if err := resolveBCMDataExportsRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, bID, store.RelUses)
}

func TestResolveBCMDataExportsRelationships_UnscannedBucket(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:bcm-data-exports::%s:export/exp-2", acct.ID)
	attrs := fmt.Sprintf(`{"ExportArn":%q,"DestinationConfigurations":{"S3Destination":{"S3Bucket":"foreign-bucket"}}}`, arn)
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDataExportsExport, arn, testRegion, attrs)

	if err := resolveBCMDataExportsRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
