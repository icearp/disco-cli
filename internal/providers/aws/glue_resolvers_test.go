package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testGlueDB     = "analytics"
	testGlueTable  = "events"
	testGlueBucket = "raw-events-bucket"
)

// TestResolveGlueTableS3Location_HappyPath verifies the table→bucket edge
// lands when both are scanned and Location uses the s3:// scheme.
func TestResolveGlueTableS3Location_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::" + testGlueBucket
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	tableARN := glueTableARN(testRegion, testAccountID, testGlueDB, testGlueTable)
	tableAttrs := fmt.Sprintf(`{"Name":%q,"DatabaseName":%q,"StorageDescriptor":{"Location":"s3://%s/raw/year=2024/"}}`,
		testGlueTable, testGlueDB, testGlueBucket)
	tableID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tableARN, testRegion, tableAttrs)

	if err := resolveGlueTableS3Location(acct, st); err != nil {
		t.Fatalf("resolveGlueTableS3Location: %v", err)
	}
	rels, err := st.RelationshipsFrom(tableID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tableID, bID, store.RelUses)
}

// TestResolveGlueTableS3Location_FKSafe verifies cross-account / unscanned
// buckets emit no edge.
func TestResolveGlueTableS3Location_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed a different bucket so the resolver actually iterates rather
	// than short-circuiting on the empty id-set fast-path.
	upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::other", "", "{}")

	tableARN := glueTableARN(testRegion, testAccountID, testGlueDB, testGlueTable)
	tableAttrs := `{"StorageDescriptor":{"Location":"s3://ghost-bucket/data/"}}`
	tableID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tableARN, testRegion, tableAttrs)

	if err := resolveGlueTableS3Location(acct, st); err != nil {
		t.Fatalf("resolveGlueTableS3Location: %v", err)
	}
	rels, err := st.RelationshipsFrom(tableID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

// TestResolveGlueTableS3Location_NonS3 verifies non-s3:// locations (HDFS,
// JDBC, federated catalogs, empty) skip silently.
func TestResolveGlueTableS3Location_NonS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	for i, loc := range []string{
		"hdfs://nn/path",
		"jdbc:redshift://cluster.example/db",
		"",
		"not a url",
	} {
		tableARN := glueTableARN(testRegion, testAccountID, testGlueDB, fmt.Sprintf("t%d", i))
		tableAttrs := fmt.Sprintf(`{"StorageDescriptor":{"Location":%q}}`, loc)
		upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tableARN, testRegion, tableAttrs)
	}

	if err := resolveGlueTableS3Location(acct, st); err != nil {
		t.Fatalf("resolveGlueTableS3Location: %v", err)
	}
}

func TestS3BucketARNFromS3URL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"s3://bucket", "arn:aws:s3:::bucket"},
		{"s3://bucket/prefix/", "arn:aws:s3:::bucket"},
		{"s3://bucket/a/b/c", "arn:aws:s3:::bucket"},
		{"s3a://bucket/prefix", ""},
		{"hdfs://bucket/path", ""},
		{"", ""},
		{"s3://", ""},
	}
	for _, c := range cases {
		if got := s3BucketARNFromS3URL(c.in); got != c.want {
			t.Errorf("s3BucketARNFromS3URL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
