package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

	// Seed a different bucket so the resolver iterates instead of
	// short-circuiting on the empty id-set fast path.
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

func TestResolveGlueTableDatabase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/sales", testRegion, acct.ID)
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, "{}")
	tblARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/sales/orders", testRegion, acct.ID)
	tblID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tblARN, testRegion, `{"DatabaseName":"sales"}`)

	if err := resolveGlueTableDatabase(acct, st); err != nil {
		t.Fatalf("resolveGlueTableDatabase: %v", err)
	}
	rels, err := st.RelationshipsFrom(tblID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(rels))
	}
	assertRelationship(t, rels, tblID, dbID, store.RelAttachedTo)
}

func TestResolveGlueJobRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/glue-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::scripts", "", "{}")
	jobARN := fmt.Sprintf("arn:aws:glue:%s:%s:job/etl", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Role":%q,"Command":{"ScriptLocation":"s3://scripts/main.py"}}`, roleARN)
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueJob, jobARN, testRegion, attrs)

	if err := resolveGlueJobRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueJobRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(rels))
	}
	assertRelationship(t, rels, jobID, roleID, store.RelAssumes)
	assertRelationship(t, rels, jobID, bID, store.RelUses)
}

func TestResolveGlueJobRefs_BareRoleName(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/glue-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	jobARN := fmt.Sprintf("arn:aws:glue:%s:%s:job/etl", testRegion, acct.ID)
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueJob, jobARN, testRegion, `{"Role":"glue-svc"}`)

	if err := resolveGlueJobRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueJobRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(jobID)
	assertRelationship(t, rels, jobID, roleID, store.RelAssumes)
}

func TestResolveGlueCrawlerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/crawl-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::raw", "", "{}")
	dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/sales", testRegion, acct.ID)
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, "{}")

	cARN := fmt.Sprintf("arn:aws:glue:%s:%s:crawler/raw-crawler", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{
		"Role":%q,
		"DatabaseName":"sales",
		"Targets":{"S3Targets":[{"Path":"s3://raw/orders"},{"Path":"s3://raw/users"}]}
	}`, roleARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueCrawler, cARN, testRegion, attrs)

	if err := resolveGlueCrawlerRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueCrawlerRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(cID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 edges (role, db, dedup-bucket), got %d", len(rels))
	}
	assertRelationship(t, rels, cID, roleID, store.RelAssumes)
	assertRelationship(t, rels, cID, dbID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, bID, store.RelUses)
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
