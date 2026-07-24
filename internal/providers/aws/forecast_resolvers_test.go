package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveForecastDatasetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := fmt.Sprintf("arn:aws:forecast:%s:%s:dataset/sales", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/forecast", acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"EncryptionConfig":{"KMSKeyArn":%q,"RoleArn":%q}}`, keyARN, roleARN)

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastDataset, dsARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveForecastDatasetRefs(acct, st); err != nil {
		t.Fatalf("resolveForecastDatasetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
	assertRelationship(t, rels, dID, rID, store.RelAssumes)
}

func TestResolveForecastDatasetGroupMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	gARN := fmt.Sprintf("arn:aws:forecast:%s:%s:dataset-group/g1", testRegion, acct.ID)
	dsARN := fmt.Sprintf("arn:aws:forecast:%s:%s:dataset/sales", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"DatasetArns":[%q]}`, dsARN)

	gID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastDatasetGroup, gARN, testRegion, attrs)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastDataset, dsARN, testRegion, "{}")

	if err := resolveForecastDatasetGroupMembers(acct, st); err != nil {
		t.Fatalf("resolveForecastDatasetGroupMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gID)
	assertRelationship(t, rels, gID, dID, store.RelContains)
}
