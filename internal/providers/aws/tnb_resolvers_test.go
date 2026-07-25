package aws

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	tnbtypes "github.com/aws/aws-sdk-go-v2/service/tnb/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveTnbRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := func(kind, id string) string {
		return fmt.Sprintf("arn:aws:tnb:%s:%s:%s/%s", testRegion, acct.ID, kind, id)
	}

	// function package + instance referencing it via VnfPkgId.
	fpkgARN := arn("function-package", "fp-1")
	fpAttrs := mustJSON(tnbtypes.ListSolFunctionPackageInfo{Arn: aws.String(fpkgARN), Id: aws.String("fp-1")})
	fpID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbFunctionPackage, fpkgARN, testRegion, fpAttrs)

	fiARN := arn("function-instance", "fi-1")
	fiAttrs := mustJSON(tnbtypes.ListSolFunctionInstanceInfo{Arn: aws.String(fiARN), Id: aws.String("fi-1"), VnfPkgId: aws.String("fp-1")})
	fiID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbFunctionInstance, fiARN, testRegion, fiAttrs)

	// network package + instance referencing it via NsdInfoId.
	npkgARN := arn("network-package", "np-1")
	npAttrs := mustJSON(tnbtypes.ListSolNetworkPackageInfo{Arn: aws.String(npkgARN), Id: aws.String("np-1")})
	npID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbNetworkPackage, npkgARN, testRegion, npAttrs)

	niARN := arn("network-instance", "ni-1")
	niAttrs := mustJSON(tnbtypes.ListSolNetworkInstanceInfo{Arn: aws.String(niARN), Id: aws.String("ni-1"), NsdInfoId: aws.String("np-1")})
	niID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbNetworkInstance, niARN, testRegion, niAttrs)

	// network operation referencing the network instance via NsInstanceId.
	noARN := arn("network-operation", "no-1")
	noAttrs := mustJSON(tnbtypes.ListSolNetworkOperationsInfo{Arn: aws.String(noARN), Id: aws.String("no-1"), NsInstanceId: aws.String("ni-1")})
	noID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbNetworkOperation, noARN, testRegion, noAttrs)

	for _, fn := range []func(*account, *store.Store) error{
		resolveTnbFunctionInstancePackage,
		resolveTnbNetworkInstancePackage,
		resolveTnbNetworkOperationInstance,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolver: %v", err)
		}
	}

	fiRels, _ := st.RelationshipsFrom(fiID)
	assertRelationship(t, fiRels, fiID, fpID, store.RelAttachedTo)
	niRels, _ := st.RelationshipsFrom(niID)
	assertRelationship(t, niRels, niID, npID, store.RelAttachedTo)
	noRels, _ := st.RelationshipsFrom(noID)
	assertRelationship(t, noRels, noID, niID, store.RelAttachedTo)
}

func TestResolveTnbRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fiARN := fmt.Sprintf("arn:aws:tnb:%s:%s:function-instance/fi-x", testRegion, acct.ID)
	fiID := upsertTestResource(t, st, "aws", acct.ID, TypeTnbFunctionInstance, fiARN, testRegion, "{}")

	if err := resolveTnbFunctionInstancePackage(acct, st); err != nil {
		t.Fatalf("resolveTnbFunctionInstancePackage: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fiID)
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
