package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	dxtypes "github.com/aws/aws-sdk-go-v2/service/dataexchange/types"
)

func dxDataSetID(t *testing.T, st *store.Store, acct *account, id, arn string) string {
	t.Helper()
	body, _ := json.Marshal(dxtypes.DataSetEntry{Id: ptrStr(id), Arn: ptrStr(arn), Name: ptrStr(id)})
	return upsertTestResource(t, st, "aws", acct.ID, TypeDataExchangeDataSets, arn, testRegion, string(body))
}

func TestResolveDataExchangeDataGrantRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-sets/ds1", testRegion, acct.ID)
	dsID := dxDataSetID(t, st, acct, "ds1", dsARN)

	grantARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-grants/g1", testRegion, acct.ID)
	gBody, _ := json.Marshal(dxtypes.DataGrantSummaryEntry{Arn: ptrStr(grantARN), Id: ptrStr("g1"), Name: ptrStr("grant"), SourceDataSetId: ptrStr("ds1")})
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeDataExchangeDataGrants, grantARN, testRegion, string(gBody))

	if err := resolveDataExchangeDataGrantRefs(acct, st); err != nil {
		t.Fatalf("resolveDataExchangeDataGrantRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gID)
	assertRelationship(t, rels, gID, dsID, store.RelUses)
}

func TestResolveDataExchangeDataGrantRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	grantARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-grants/g1", testRegion, acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeDataExchangeDataGrants, grantARN, testRegion, "{}")
	if err := resolveDataExchangeDataGrantRefs(acct, st); err != nil {
		t.Fatalf("resolveDataExchangeDataGrantRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(gID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveDataExchangeEventActionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-sets/ds1", testRegion, acct.ID)
	dsID := dxDataSetID(t, st, acct, "ds1", dsARN)
	bucketARN := "arn:aws:s3:::export-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")

	eaARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:event-actions/ea1", testRegion, acct.ID)
	eaBody, _ := json.Marshal(dxtypes.EventActionEntry{
		Arn: ptrStr(eaARN),
		Id:  ptrStr("ea1"),
		Event: &dxtypes.Event{
			RevisionPublished: &dxtypes.RevisionPublished{DataSetId: ptrStr("ds1")},
		},
		Action: &dxtypes.Action{
			ExportRevisionToS3: &dxtypes.AutoExportRevisionToS3RequestDetails{
				RevisionDestination: &dxtypes.AutoExportRevisionDestinationEntry{Bucket: ptrStr("export-bucket")},
				Encryption:          &dxtypes.ExportServerSideEncryption{KmsKeyArn: ptrStr(kARN)},
			},
		},
	})
	eaID := upsertTestResource(t, st, "aws", acct.ID, TypeDataExchangeEventActions, eaARN, testRegion, string(eaBody))

	if err := resolveDataExchangeEventActionRefs(acct, st); err != nil {
		t.Fatalf("resolveDataExchangeEventActionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eaID)
	assertRelationship(t, rels, eaID, dsID, store.RelUses)
	assertRelationship(t, rels, eaID, bucketID, store.RelUses)
	assertRelationship(t, rels, eaID, kID, store.RelUses)
}

func TestResolveDataExchangeEventActionRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eaARN := fmt.Sprintf("arn:aws:dataexchange:%s:%s:event-actions/ea1", testRegion, acct.ID)
	eaID := upsertTestResource(t, st, "aws", acct.ID, TypeDataExchangeEventActions, eaARN, testRegion, "{}")
	if err := resolveDataExchangeEventActionRefs(acct, st); err != nil {
		t.Fatalf("resolveDataExchangeEventActionRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(eaID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
