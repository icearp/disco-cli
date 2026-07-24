package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSageMakerProjectProduct(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pjARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:project/p1", testRegion, acct.ID)
	prodID := "prod-abc123"
	prodARN := fmt.Sprintf("arn:aws:catalog:%s:%s:product/%s", testRegion, acct.ID, prodID)
	attrs := fmt.Sprintf(`{"ServiceCatalogProvisioningDetails":{"ProductId":%q}}`, prodID)

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerProject, pjARN, testRegion, attrs)
	prID := upsertTestResource(t, st, "aws", acct.ID, TypeServiceCatalogProduct, prodARN, testRegion, "{}")

	if err := resolveSageMakerProjectProduct(acct, st); err != nil {
		t.Fatalf("resolveSageMakerProjectProduct: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, prID, store.RelUses)
}
