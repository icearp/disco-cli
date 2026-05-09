package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestApigatewayV2APIARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:apigateway:us-east-1::/apis/abc/routes/r1", "arn:aws:apigateway:us-east-1::/apis/abc"},
		{"arn:aws:apigateway:us-east-1::/apis/abc/integrations/i1/integrationresponses/ir1", "arn:aws:apigateway:us-east-1::/apis/abc"},
		{"arn:aws:apigateway:us-east-1::/apis/abc", ""},
		{"arn:aws:apigateway:us-east-1::/domainnames/x", ""},
	}
	for _, c := range cases {
		if got := apigatewayV2APIARNFromChild(c.in); got != c.want {
			t.Errorf("apigatewayV2APIARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestApigatewayV2DomainARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:apigateway:us-east-1::/domainnames/d.example.com/apimappings/m1", "arn:aws:apigateway:us-east-1::/domainnames/d.example.com"},
		{"arn:aws:apigateway:us-east-1::/domainnames/d.example.com/routingrules/rr1", "arn:aws:apigateway:us-east-1::/domainnames/d.example.com"},
		{"arn:aws:apigateway:us-east-1::/apis/abc", ""},
	}
	for _, c := range cases {
		if got := apigatewayV2DomainARNFromChild(c.in); got != c.want {
			t.Errorf("apigatewayV2DomainARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAPIGatewayV2APIChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apiARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc", testRegion)
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2API, apiARN, testRegion, "{}")
	intARN := apiARN + "/integrations/i1"
	intID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Integration, intARN, testRegion, "{}")
	stARN := apiARN + "/stages/prod"
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Stage, stARN, testRegion, "{}")
	if err := resolveAPIGatewayV2APIChildren(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2APIChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(intID)
	assertRelationship(t, rels, intID, apiID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(stID)
	assertRelationship(t, rels, stID, apiID, store.RelAttachedTo)
}

func TestResolveAPIGatewayV2GrandparentChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	intARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/integrations/i1", testRegion)
	intID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Integration, intARN, testRegion, "{}")
	irARN := intARN + "/integrationresponses/ir1"
	irID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2IntegrationResponse, irARN, testRegion, "{}")
	rtARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/routes/r1", testRegion)
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Route, rtARN, testRegion, "{}")
	rrARN := rtARN + "/routeresponses/rr1"
	rrID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2RouteResponse, rrARN, testRegion, "{}")
	if err := resolveAPIGatewayV2GrandparentChildren(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2GrandparentChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(irID)
	assertRelationship(t, rels, irID, intID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(rrID)
	assertRelationship(t, rels, rrID, rtID, store.RelAttachedTo)
}

func TestResolveAPIGatewayV2DomainChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/d.example.com", testRegion)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDomainNameV2, dARN, testRegion, "{}")
	mARN := dARN + "/apimappings/m1"
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayBasePathMappingV2, mARN, testRegion, "{}")
	if err := resolveAPIGatewayV2DomainChildren(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2DomainChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, dID, store.RelAttachedTo)
}

func TestResolveAPIGatewayV2BasePathMappingTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apiARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc", testRegion)
	apiID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2API, apiARN, testRegion, "{}")
	stARN := apiARN + "/stages/prod"
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Stage, stARN, testRegion, "{}")
	mARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/d.example.com/apimappings/m1", testRegion)
	attrs := `{"ApiId":"abc","Stage":"prod"}`
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayBasePathMappingV2, mARN, testRegion, attrs)
	if err := resolveAPIGatewayV2BasePathMappingTargets(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2BasePathMappingTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, apiID, store.RelRoutesTo)
	assertRelationship(t, rels, mID, stID, store.RelRoutesTo)
}

func TestResolveAPIGatewayV2RouteTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	authARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/authorizers/a1", testRegion)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, authARN, testRegion, "{}")
	intARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/integrations/i1", testRegion)
	intID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Integration, intARN, testRegion, "{}")
	rtARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/routes/r1", testRegion)
	attrs := `{"AuthorizerId":"a1","Target":"integrations/i1"}`
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Route, rtARN, testRegion, attrs)
	if err := resolveAPIGatewayV2RouteTargets(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2RouteTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtID)
	assertRelationship(t, rels, rtID, authID, store.RelUses)
	assertRelationship(t, rels, rtID, intID, store.RelRoutesTo)
}

func TestResolveAPIGatewayV2IntegrationVpcLink(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vlARN := fmt.Sprintf("arn:aws:apigateway:%s::/vpclinks/vl1", testRegion)
	vlID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2VpcLink, vlARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/r", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	intARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/integrations/i1", testRegion)
	attrs := fmt.Sprintf(`{"ConnectionId":"vl1","ConnectionType":"VPC_LINK","CredentialsArn":%q}`, roleARN)
	intID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Integration, intARN, testRegion, attrs)
	if err := resolveAPIGatewayV2IntegrationVpcLink(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2IntegrationVpcLink: %v", err)
	}
	rels, _ := st.RelationshipsFrom(intID)
	assertRelationship(t, rels, intID, vlID, store.RelAttachedTo)
	assertRelationship(t, rels, intID, rID, store.RelAssumes)
}

func TestResolveAPIGatewayV2StageRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	depARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/deployments/d1", testRegion)
	depID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Deployment, depARN, testRegion, "{}")
	stARN := fmt.Sprintf("arn:aws:apigateway:%s::/apis/abc/stages/prod", testRegion)
	attrs := `{"DeploymentId":"d1"}`
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Stage, stARN, testRegion, attrs)
	if err := resolveAPIGatewayV2StageRefs(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayV2StageRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(stID)
	assertRelationship(t, rels, stID, depID, store.RelUses)
}
