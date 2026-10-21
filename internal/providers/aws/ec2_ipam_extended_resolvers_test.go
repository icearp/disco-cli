package aws

import (
	"encoding/json"
	"errors"
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

func ipxByoasnAttrs(t *testing.T, ipamID string) string {
	t.Helper()
	b, err := json.Marshal(ec2types.Byoasn{Asn: ptrStr(ipxAsn1), IpamId: ptrStr(ipamID), State: ec2types.AsnStateProvisioned})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolveIpamByoasnRelationships_LinksToScannedIpam(t *testing.T) {
	st := newTestStore(t)
	ipamID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IPAM, ipxIpamARN, testRegion, "{}")
	linked := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn, ipxByoasnID(ipxAsn1), testRegion, ipxByoasnAttrs(t, ipxIpamID))
	unscanned := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn,
		ec2ARN(testRegion, testAccountID, "ipam-byoasn", "ipam-0zzz/"+ipxAsn2), testRegion, ipxByoasnAttrs(t, "ipam-0zzz"))
	noAttrs := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn,
		ec2ARN(testRegion, testAccountID, "ipam-byoasn", ipxIpamID+"/64999"), testRegion, "{}")

	if err := resolveIpamByoasnRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveIpamByoasnRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(linked, store.RelAttachedTo)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, linked, ipamID, store.RelAttachedTo)
	for name, id := range map[string]string{"unscanned IPAM": unscanned, "no attributes": noAttrs} {
		rels, err := st.RelationshipsFrom(id, store.RelAttachedTo)
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: edges = %v, want none", name, rels)
		}
	}
}

// A row whose attributes do not decode is reported, and the other rows still
// get their edge.
func TestResolveIpamByoasnRelationships_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	ipamID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IPAM, ipxIpamARN, testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn,
		ec2ARN(testRegion, testAccountID, "ipam-byoasn", ipxIpamID+"/64999"), testRegion, `{"IpamId":7}`)
	linked := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn, ipxByoasnID(ipxAsn1), testRegion, ipxByoasnAttrs(t, ipxIpamID))
	// ListResources orders by type, name, root_id with unnamed rows first, so
	// this named row is resolved after the unnamed bad one.
	after := upsertTestResourceNamed(t, st, TypeEC2IpamByoasn, ipxByoasnID(ipxAsn2), testRegion, ipxByoasnAttrs(t, ipxIpamID), "zz-after")

	var typeErr *json.UnmarshalTypeError
	err := resolveIpamByoasnRelationships(newTestAccount(testAccountID), st)
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
	rels, rerr := st.RelationshipsFrom(linked, store.RelAttachedTo)
	if rerr != nil {
		t.Fatal(rerr)
	}
	assertRelationship(t, rels, linked, ipamID, store.RelAttachedTo)
	rels, rerr = st.RelationshipsFrom(after, store.RelAttachedTo)
	if rerr != nil {
		t.Fatal(rerr)
	}
	assertRelationship(t, rels, after, ipamID, store.RelAttachedTo)
}

func TestResolveIpamByoasnRelationships_NoRows(t *testing.T) {
	st := newTestStore(t)
	if err := resolveIpamByoasnRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveIpamByoasnRelationships: %v", err)
	}
}

func ipxIrAssocAttrs(t *testing.T, ipamID string) string {
	t.Helper()
	b, err := json.Marshal(ec2types.IpamInternetRegistryAssociation{
		IpamInternetRegistryAssociationId: ptrStr(ipxIrAssoc1), IpamId: ptrStr(ipamID),
		State: ec2types.IpamInternetRegistryAssociationStateEnableComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolveIpamInternetRegistryAssociationRelationships_LinksToScannedIpam(t *testing.T) {
	st := newTestStore(t)
	ipamID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IPAM, ipxIpamARN, testRegion, "{}")
	linked := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamInternetRegistryAssociation,
		ipxIrAssocARN(ipxIrAssoc1), testRegion, ipxIrAssocAttrs(t, ipxIpamID))
	unscanned := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamInternetRegistryAssociation,
		ipxIrAssocARN(ipxIrAssoc2), testRegion, ipxIrAssocAttrs(t, "ipam-0zzz"))
	noAttrs := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamInternetRegistryAssociation,
		ipxIrAssocARN("ipam-ira-0009"), testRegion, "{}")
	// A BYOASN naming the same IPAM is not this resolver's row.
	byoasn := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamByoasn, ipxByoasnID(ipxAsn1), testRegion, ipxByoasnAttrs(t, ipxIpamID))

	if err := resolveIpamInternetRegistryAssociationRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveIpamInternetRegistryAssociationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(linked, store.RelAttachedTo)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, linked, ipamID, store.RelAttachedTo)
	for name, id := range map[string]string{"unscanned IPAM": unscanned, "no attributes": noAttrs, "byoasn row": byoasn} {
		rels, err := st.RelationshipsFrom(id, store.RelAttachedTo)
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: edges = %v, want none", name, rels)
		}
	}
}

func TestResolveIpamInternetRegistryAssociationRelationships_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	ipamID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2IPAM, ipxIpamARN, testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamInternetRegistryAssociation,
		ipxIrAssocARN("ipam-ira-0009"), testRegion, `{"IpamId":7}`)
	// Named, so ListResources returns it after the unnamed bad row.
	after := upsertTestResourceNamed(t, st, TypeEC2IpamInternetRegistryAssociation,
		ipxIrAssocARN(ipxIrAssoc1), testRegion, ipxIrAssocAttrs(t, ipxIpamID), "zz-after")

	var typeErr *json.UnmarshalTypeError
	err := resolveIpamInternetRegistryAssociationRelationships(newTestAccount(testAccountID), st)
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
	rels, rerr := st.RelationshipsFrom(after, store.RelAttachedTo)
	if rerr != nil {
		t.Fatal(rerr)
	}
	assertRelationship(t, rels, after, ipamID, store.RelAttachedTo)
}
