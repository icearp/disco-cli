package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveGACustomRoutingEndpointGroupSubnets_RoutesToScannedSubnets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN("eu-west-1", acct.ID, "subnet", "subnet-0abc"), "eu-west-1", "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-0def"), testRegion, "{}")
	attrs := mustJSON(gatypes.CustomRoutingEndpointGroup{
		EndpointGroupArn:    ptrStr(gaTestCREG1),
		EndpointGroupRegion: ptrStr("eu-west-1"),
		EndpointDescriptions: []gatypes.CustomRoutingEndpointDescription{
			{EndpointId: ptrStr("subnet-0abc")},
			{EndpointId: ptrStr("subnet-0def")},
			{EndpointId: ptrStr("subnet-unscanned")},
			{},
		},
	})
	egID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG1, "global", attrs)

	if err := resolveGACustomRoutingEndpointGroupSubnets(acct, st); err != nil {
		t.Fatalf("resolveGACustomRoutingEndpointGroupSubnets: %v", err)
	}
	rels, err := st.RelationshipsFrom(egID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, egID, subnetID, store.RelRoutesTo)
	// subnet-0def is scanned, but in another region than the group's.
	if len(rels) != 1 {
		t.Errorf("edges = %v, want only the eu-west-1 subnet", rels)
	}
}

func TestResolveGACustomRoutingEndpointGroupSubnets_NoEndpoints(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN("eu-west-1", acct.ID, "subnet", "subnet-0abc"), "eu-west-1", "{}")
	egID := upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG1, "global", "{}")
	if err := resolveGACustomRoutingEndpointGroupSubnets(acct, st); err != nil {
		t.Fatalf("resolveGACustomRoutingEndpointGroupSubnets: %v", err)
	}
	rels, err := st.RelationshipsFrom(egID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %v, want none", rels)
	}
}

func TestResolveGACustomRoutingEndpointGroupSubnets_UndecodableAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeGlobalAcceleratorCustomRoutingEndpointGroup, gaTestCREG1, "global", `{"EndpointDescriptions":"not-a-list"}`)
	var typeErr *json.UnmarshalTypeError
	if err := resolveGACustomRoutingEndpointGroupSubnets(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the attributes decode error", err)
	}
}

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
