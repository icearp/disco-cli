package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestApigatewayRestAPIARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:apigateway:us-east-1::/restapis/abc/resources/r1", "arn:aws:apigateway:us-east-1::/restapis/abc"},
		{"arn:aws:apigateway:us-east-1::/restapis/abc/models/MyModel", "arn:aws:apigateway:us-east-1::/restapis/abc"},
		{"arn:aws:apigateway:us-east-1::/restapis/abc", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := apigatewayRestAPIARNFromChild(c.in); got != c.want {
			t.Errorf("apigatewayRestAPIARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAPIGatewayRestApiChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apiARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/abc", testRegion)
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, apiARN, testRegion, "{}")
	resARN := apiARN + "/resources/r1"
	resID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayResource, resARN, testRegion, "{}")
	mdlARN := apiARN + "/models/MyModel"
	mdlID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayModel, mdlARN, testRegion, "{}")

	if err := resolveAPIGatewayRestAPIChildren(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayRestAPIChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(resID)
	assertRelationship(t, rels, resID, apiID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(mdlID)
	assertRelationship(t, rels, mdlID, apiID, store.RelAttachedTo)
}

func TestResolveAPIGatewayVpcLinkTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nlbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/net/my-nlb/abc", testRegion, acct.ID)
	nlbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, nlbARN, testRegion, "{}")
	vlARN := fmt.Sprintf("arn:aws:apigateway:%s::/vpclinks/vl-1", testRegion)
	attrs := fmt.Sprintf(`{"TargetArns":[%q]}`, nlbARN)
	vlID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayVpcLink, vlARN, testRegion, attrs)
	if err := resolveAPIGatewayVpcLinkTargets(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayVpcLinkTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vlID)
	assertRelationship(t, rels, vlID, nlbID, store.RelRoutesTo)
}
