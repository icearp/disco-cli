package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveGlueTableToDatabase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/sales", testRegion, acct.ID)
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, "{}")
	tARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/sales/orders", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tARN, testRegion, "{}")
	if err := resolveGlueTableToDatabase(acct, st); err != nil {
		t.Fatalf("resolveGlueTableToDatabase: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, dbID, store.RelAttachedTo)
}

func TestResolveGluePartitionToTable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/sales/orders", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tARN, testRegion, "{}")
	pARN := fmt.Sprintf("arn:aws:glue:%s:%s:partition/sales/orders/v1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeGluePartition, pARN, testRegion, "{}")
	if err := resolveGluePartitionToTable(acct, st); err != nil {
		t.Fatalf("resolveGluePartitionToTable: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, tID, store.RelAttachedTo)
}

func TestResolveGlueTableOptimizerToTable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/sales/orders", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tARN, testRegion, "{}")
	oARN := fmt.Sprintf("arn:aws:glue:%s:%s:table-optimizer/sales/orders/COMPACTION", testRegion, acct.ID)
	oID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTableOptimizer, oARN, testRegion, "{}")
	if err := resolveGlueTableOptimizerToTable(acct, st); err != nil {
		t.Fatalf("resolveGlueTableOptimizerToTable: %v", err)
	}
	rels, _ := st.RelationshipsFrom(oID)
	assertRelationship(t, rels, oID, tID, store.RelAttachedTo)
}

func TestResolveGlueSchemaVersionToSchema(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:glue:%s:%s:schema/default-registry/my-schema", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueSchema, sARN, testRegion, "{}")
	vARN := sARN + "/version/abcdef"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueSchemaVersion, vARN, testRegion, "{}")
	if err := resolveGlueSchemaVersionToSchema(acct, st); err != nil {
		t.Fatalf("resolveGlueSchemaVersionToSchema: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, sID, store.RelAttachedTo)
}

func TestResolveGlueDataQualityRulesetTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/sales", testRegion, acct.ID)
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, "{}")
	tARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/sales/orders", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tARN, testRegion, "{}")
	rsARN := fmt.Sprintf("arn:aws:glue:%s:%s:dataQualityRuleset/rs1", testRegion, acct.ID)
	attrs := `{"TargetTable":{"DatabaseName":"sales","TableName":"orders"}}`
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDataQualityRuleset, rsARN, testRegion, attrs)
	if err := resolveGlueDataQualityRulesetTargets(acct, st); err != nil {
		t.Fatalf("resolveGlueDataQualityRulesetTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, dbID, store.RelUses)
	assertRelationship(t, rels, rsID, tID, store.RelUses)
}
