package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testAthenaWG       = "primary"
	testAthenaCatalog  = "lambda-pg"
	testAthenaBucket   = "athena-results"
	testAthenaKMSKeyID = "12345678-1234-1234-1234-123456789012"
	testAthenaLambdaFn = "athena-conn-fn"
)

// TestResolveAthenaWorkgroupTargets_HappyPath verifies workgroup →
// {S3 bucket, KMS key} edges land when both targets are scanned.
func TestResolveAthenaWorkgroupTargets_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::" + testAthenaBucket
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testAthenaKMSKeyID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testAthenaKMSKeyID, keyARN))

	wgARN := athenaWorkGroupARN(testRegion, testAccountID, testAthenaWG)
	wgAttrs := fmt.Sprintf(`{"Name":%q,"State":"ENABLED","Configuration":{"ResultConfiguration":{"OutputLocation":"s3://%s/queries/","EncryptionConfiguration":{"EncryptionOption":"SSE_KMS","KmsKey":%q}}}}`,
		testAthenaWG, testAthenaBucket, keyARN)
	wgID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaWorkgroup, wgARN, testRegion, wgAttrs)

	if err := resolveAthenaWorkgroupTargets(acct, st); err != nil {
		t.Fatalf("resolveAthenaWorkgroupTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(wgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, wgID, bID, store.RelUses)
	assertRelationship(t, rels, wgID, kID, store.RelUses)
}

// TestResolveAthenaWorkgroupTargets_NoConfig verifies workgroups without
// ResultConfiguration skip without erroring.
func TestResolveAthenaWorkgroupTargets_NoConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	wgARN := athenaWorkGroupARN(testRegion, testAccountID, testAthenaWG)
	upsertTestResource(t, st, "aws", acct.ID, TypeAthenaWorkgroup, wgARN, testRegion, `{"Name":"primary"}`)

	if err := resolveAthenaWorkgroupTargets(acct, st); err != nil {
		t.Fatalf("resolveAthenaWorkgroupTargets: %v", err)
	}
}

// TestResolveAthenaDataCatalogLambda_HappyPath verifies LAMBDA-type
// catalog → Lambda function edge via Parameters[`function`].
func TestResolveAthenaDataCatalogLambda_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", testRegion, testAccountID, testAthenaLambdaFn)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	catARN := athenaDataCatalogARN(testRegion, testAccountID, testAthenaCatalog)
	catAttrs := fmt.Sprintf(`{"Name":%q,"Type":"LAMBDA","Parameters":{"function":%q}}`, testAthenaCatalog, fnARN)
	catID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaDataCatalog, catARN, testRegion, catAttrs)

	if err := resolveAthenaDataCatalogLambda(acct, st); err != nil {
		t.Fatalf("resolveAthenaDataCatalogLambda: %v", err)
	}
	rels, err := st.RelationshipsFrom(catID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, catID, fnID, store.RelUses)
}

// TestResolveAthenaDataCatalogLambda_GlueSkipped verifies GLUE-type
// catalogs emit no Lambda edge (no Parameters.function).
func TestResolveAthenaDataCatalogLambda_GlueSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed a Lambda so the resolver iterates rather than fast-pathing
	// past on empty id-set.
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:other", testRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	catARN := athenaDataCatalogARN(testRegion, testAccountID, "AwsDataCatalog")
	catAttrs := `{"Name":"AwsDataCatalog","Type":"GLUE","Parameters":{"catalog-id":"123456789012"}}`
	catID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaDataCatalog, catARN, testRegion, catAttrs)

	if err := resolveAthenaDataCatalogLambda(acct, st); err != nil {
		t.Fatalf("resolveAthenaDataCatalogLambda: %v", err)
	}
	rels, err := st.RelationshipsFrom(catID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges for GLUE-type catalog, got %d", len(rels))
	}
}

// TestResolveAthenaDataCatalogLambda_HiveDualFn verifies HIVE-type
// catalog with both metadata-function and record-function emits two
// edges (deduplicated if same function).
func TestResolveAthenaDataCatalogLambda_HiveDualFn(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	metaARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:meta", testRegion, testAccountID)
	recARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:rec", testRegion, testAccountID)
	metaID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, metaARN, testRegion, "{}")
	recID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, recARN, testRegion, "{}")

	catARN := athenaDataCatalogARN(testRegion, testAccountID, "hive-cat")
	catAttrs := fmt.Sprintf(`{"Type":"HIVE","Parameters":{"metadata-function":%q,"record-function":%q}}`, metaARN, recARN)
	catID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaDataCatalog, catARN, testRegion, catAttrs)

	if err := resolveAthenaDataCatalogLambda(acct, st); err != nil {
		t.Fatalf("resolveAthenaDataCatalogLambda: %v", err)
	}
	rels, err := st.RelationshipsFrom(catID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, catID, metaID, store.RelUses)
	assertRelationship(t, rels, catID, recID, store.RelUses)
}

func TestResolveAthenaSavedQueryWorkgroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	wgARN := athenaWorkGroupARN(testRegion, testAccountID, "analytics")
	wgID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaWorkgroup, wgARN, testRegion, "{}")

	nqARN := wgARN + "/named-query/q-1"
	nqID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaNamedQuery, nqARN, testRegion, "{}")
	psARN := wgARN + "/prepared-statement/stmt1"
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaPreparedStatement, psARN, testRegion, "{}")

	if err := resolveAthenaSavedQueryWorkgroup(acct, st); err != nil {
		t.Fatalf("resolveAthenaSavedQueryWorkgroup: %v", err)
	}
	for _, src := range []string{nqID, psID} {
		rels, _ := st.RelationshipsFrom(src)
		assertRelationship(t, rels, src, wgID, store.RelAttachedTo)
	}
}

// Saved queries whose workgroup is unscanned emit no edge.
func TestResolveAthenaSavedQueryWorkgroup_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// A different workgroup exists so the resolver runs.
	upsertTestResource(t, st, "aws", acct.ID, TypeAthenaWorkgroup, athenaWorkGroupARN(testRegion, testAccountID, "real"), testRegion, "{}")

	goneWG := athenaWorkGroupARN(testRegion, testAccountID, "gone")
	nqARN := goneWG + "/named-query/q-1"
	nqID := upsertTestResource(t, st, "aws", acct.ID, TypeAthenaNamedQuery, nqARN, testRegion, "{}")

	if err := resolveAthenaSavedQueryWorkgroup(acct, st); err != nil {
		t.Fatalf("resolveAthenaSavedQueryWorkgroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(nqID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
