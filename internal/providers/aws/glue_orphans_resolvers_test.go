package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveGlueDatabaseTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	catARN := glueResourceARN(testRegion, acct.ID, "catalog", "shared")
	catID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueCatalog, catARN, testRegion, `{}`)
	connARN := glueResourceARN(testRegion, acct.ID, "connection", "fed-conn")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueConnection, connARN, testRegion, `{}`)
	dbARN := glueResourceARN(testRegion, acct.ID, "database", "mydb")
	attrs := `{"TargetDatabase":{"CatalogId":"shared"},"FederatedDatabase":{"ConnectionName":"fed-conn"}}`
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, attrs)

	if err := resolveGlueDatabaseTargets(acct, st); err != nil {
		t.Fatalf("resolveGlueDatabaseTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dbID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, dbID, catID, store.RelAttachedTo)
	assertRelationship(t, rels, dbID, connID, store.RelUses)
}

func TestResolveGlueIntegrationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, `{"KeyId":"abc-123","Arn":"`+keyARN+`"}`)

	rdsARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:src-cluster", testRegion, acct.ID)
	rdsID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, rdsARN, testRegion, `{}`)
	rsARN := fmt.Sprintf("arn:aws:redshift:%s:%s:cluster:dst-cluster", testRegion, acct.ID)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftCluster, rsARN, testRegion, `{}`)

	intARN := fmt.Sprintf("arn:aws:glue:%s:%s:integration/zetl-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyId":%q,"SourceArn":%q,"TargetArn":%q}`, keyARN, rdsARN, rsARN)
	intID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueIntegration, intARN, testRegion, attrs)

	if err := resolveGlueIntegrationRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueIntegrationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(intID)
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, intID, keyID, store.RelUses)
	assertRelationship(t, rels, intID, rdsID, store.RelRoutesTo)
	assertRelationship(t, rels, intID, rsID, store.RelRoutesTo)
}

func TestResolveGlueCatalogTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rsARN := fmt.Sprintf("arn:aws:redshift:%s:%s:cluster:rs1", testRegion, acct.ID)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftCluster, rsARN, testRegion, `{}`)
	catARN := glueResourceARN(testRegion, acct.ID, "catalog", "fed")
	attrs := fmt.Sprintf(`{"TargetRedshiftCatalog":{"CatalogArn":%q}}`, rsARN)
	catID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueCatalog, catARN, testRegion, attrs)

	if err := resolveGlueCatalogTargets(acct, st); err != nil {
		t.Fatalf("resolveGlueCatalogTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(catID)
	assertRelationship(t, rels, catID, rsID, store.RelRoutesTo)
}
