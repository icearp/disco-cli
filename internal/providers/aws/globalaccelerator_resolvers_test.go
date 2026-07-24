package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestGAParentARN(t *testing.T) {
	cases := []struct {
		arn, seg, want string
	}{
		{"arn:aws:globalaccelerator::123:accelerator/A/listener/L", "listener", "arn:aws:globalaccelerator::123:accelerator/A"},
		{"arn:aws:globalaccelerator::123:accelerator/A/listener/L/endpoint-group/E", "endpoint-group", "arn:aws:globalaccelerator::123:accelerator/A/listener/L"},
		{"arn:aws:globalaccelerator::123:accelerator/A", "listener", ""},
	}
	for _, c := range cases {
		if got := gaParentARN(c.arn, c.seg); got != c.want {
			t.Errorf("gaParentARN(%q,%q)=%q want %q", c.arn, c.seg, got, c.want)
		}
	}
}

func TestResolveGlobalAcceleratorEndpointGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	accARN := "arn:aws:globalaccelerator::" + acct.ID + ":accelerator/abc"
	upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorAccelerator, accARN, testRegion, "{}")
	listenerARN := accARN + "/listener/lst1"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorListener, listenerARN, testRegion, "{}")

	lbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/my-alb/abc123", testRegion, acct.ID)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, testRegion, "{}")
	eipARN := ec2ARN(testRegion, acct.ID, "elastic-ip", "eipalloc-001")
	eipID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2EIP, eipARN, testRegion, "{}")
	instARN := ec2ARN(testRegion, acct.ID, "instance", "i-aaa")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, testRegion, "{}")

	egARN := listenerARN + "/endpoint-group/eg1"
	egAttrs := fmt.Sprintf(`{"EndpointGroupRegion":%q,"EndpointDescriptions":[{"EndpointId":%q},{"EndpointId":"eipalloc-001"},{"EndpointId":"i-aaa"}]}`, testRegion, lbARN)
	egID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorEndpointGroup, egARN, testRegion, egAttrs)

	if err := resolveGlobalAcceleratorEndpointGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveGlobalAcceleratorEndpointGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(egID)
	assertRelationship(t, rels, egID, listenerID, store.RelAttachedTo)
	assertRelationship(t, rels, egID, lbID, store.RelRoutesTo)
	assertRelationship(t, rels, egID, eipID, store.RelRoutesTo)
	assertRelationship(t, rels, egID, instID, store.RelRoutesTo)
}

func TestResolveGlobalAcceleratorListenerParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	accARN := "arn:aws:globalaccelerator::" + acct.ID + ":accelerator/A"
	accID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorAccelerator, accARN, testRegion, "{}")
	listenerARN := accARN + "/listener/L"
	listenerID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorListener, listenerARN, testRegion, "{}")
	if err := resolveGlobalAcceleratorListenerParent(acct, st); err != nil {
		t.Fatalf("resolveGlobalAcceleratorListenerParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(listenerID)
	assertRelationship(t, rels, listenerID, accID, store.RelAttachedTo)
}
