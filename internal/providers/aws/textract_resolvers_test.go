package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	textracttypes "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

func TestResolveTextractAdapterVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	adapterID := "ad-0001"
	adapterNID := textractAdapterNativeID(testRegion, acct.ID, adapterID)
	verNID := adapterNID + "/adapter-version/1"

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeTextractAdapter, adapterNID, testRegion, "{}")
	vAttrs := mustJSON(textracttypes.AdapterVersionOverview{AdapterId: aws.String(adapterID), AdapterVersion: aws.String("1")})
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeTextractAdapterVersion, verNID, testRegion, vAttrs)

	if err := resolveTextractAdapterVersion(acct, st); err != nil {
		t.Fatalf("resolveTextractAdapterVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, aID, store.RelAttachedTo)
}

func TestResolveTextractAdapterVersion_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	verNID := textractAdapterNativeID(testRegion, acct.ID, "ad-x") + "/adapter-version/1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeTextractAdapterVersion, verNID, testRegion, "{}")

	if err := resolveTextractAdapterVersion(acct, st); err != nil {
		t.Fatalf("resolveTextractAdapterVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
