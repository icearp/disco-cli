package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestAppMeshMeshARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appmesh:us-east-1:123:mesh/m1/virtualGateway/vg1", "arn:aws:appmesh:us-east-1:123:mesh/m1"},
		{"arn:aws:appmesh:us-east-1:123:mesh/m1/virtualRouter/vr1/route/r1", "arn:aws:appmesh:us-east-1:123:mesh/m1"},
		{"arn:aws:appmesh:us-east-1:123:mesh/m1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := appMeshMeshARNFromChild(c.in); got != c.want {
			t.Errorf("appMeshMeshARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAppMeshGrandparentARN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appmesh:us-east-1:123:mesh/m1/virtualRouter/vr1/route/r1", "arn:aws:appmesh:us-east-1:123:mesh/m1/virtualRouter/vr1"},
		{"arn:aws:appmesh:us-east-1:123:mesh/m1/virtualGateway/vg1/gatewayRoute/gr1", "arn:aws:appmesh:us-east-1:123:mesh/m1/virtualGateway/vg1"},
		{"foo", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := appMeshGrandparentARN(c.in); got != c.want {
			t.Errorf("appMeshGrandparentARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAppMeshChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	mARN := fmt.Sprintf("arn:aws:appmesh:%s:%s:mesh/m1", testRegion, acct.ID)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshMesh, mARN, testRegion, "{}")
	vgARN := mARN + "/virtualGateway/vg1"
	vgID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshVirtualGateway, vgARN, testRegion, "{}")
	vnARN := mARN + "/virtualNode/vn1"
	vnID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshVirtualNode, vnARN, testRegion, "{}")

	if err := resolveAppMeshChildren(acct, st); err != nil {
		t.Fatalf("resolveAppMeshChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vgID)
	assertRelationship(t, rels, vgID, mID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(vnID)
	assertRelationship(t, rels, vnID, mID, store.RelAttachedTo)
}

func TestResolveAppMeshRouteParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vrARN := fmt.Sprintf("arn:aws:appmesh:%s:%s:mesh/m1/virtualRouter/vr1", testRegion, acct.ID)
	vrID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshVirtualRouter, vrARN, testRegion, "{}")
	rARN := vrARN + "/route/r1"
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshRoute, rARN, testRegion, "{}")
	if err := resolveAppMeshRouteParent(acct, st); err != nil {
		t.Fatalf("resolveAppMeshRouteParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, vrID, store.RelAttachedTo)
}

func TestResolveAppMeshGatewayRouteParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vgARN := fmt.Sprintf("arn:aws:appmesh:%s:%s:mesh/m1/virtualGateway/vg1", testRegion, acct.ID)
	vgID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshVirtualGateway, vgARN, testRegion, "{}")
	grARN := vgARN + "/gatewayRoute/gr1"
	grID := upsertTestResource(t, st, "aws", acct.ID, TypeAppMeshGatewayRoute, grARN, testRegion, "{}")
	if err := resolveAppMeshGatewayRouteParent(acct, st); err != nil {
		t.Fatalf("resolveAppMeshGatewayRouteParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grID)
	assertRelationship(t, rels, grID, vgID, store.RelAttachedTo)
}
