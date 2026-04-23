package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveAPIGatewayMethodRelationships_Lambda verifies that a method with
// a Lambda integration Uri produces a uses→function edge.
func TestResolveAPIGatewayMethodRelationships_Lambda(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, testAccountID)
	uri := fmt.Sprintf("arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/%s/invocations", testRegion, fnARN)
	methodARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/abc/resources/r1/methods/GET", testRegion)
	attrs := `{"MethodIntegration":{"Type":"AWS_PROXY","Uri":"` + uri + `"}}`

	methodID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayMethod, methodARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveAPIGatewayMethodRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayMethodRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(methodID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, methodID, fnID, store.RelUses)
}

// TestResolveAPIGatewayMethodRelationships_VPCLink verifies VPC_LINK edge.
func TestResolveAPIGatewayMethodRelationships_VPCLink(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	linkID := "vpclink-abc"
	methodARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/api1/resources/r1/methods/POST", testRegion)
	attrs := fmt.Sprintf(`{"MethodIntegration":{"Type":"VPC_LINK","ConnectionId":%q}}`, linkID)

	methodID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayMethod, methodARN, testRegion, attrs)
	vpcLinkARN := fmt.Sprintf("arn:aws:apigateway:%s::/vpclinks/%s", testRegion, linkID)
	linkResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayVpcLink, vpcLinkARN, testRegion, "{}")

	if err := resolveAPIGatewayMethodRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayMethodRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(methodID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, methodID, linkResID, store.RelAttachedTo)
}

// TestAPIGWLambdaInvokeARN verifies Uri parser.
func TestAPIGWLambdaInvokeARN(t *testing.T) {
	tests := []struct {
		uri, want string
	}{
		{
			"arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:123:function:fn/invocations",
			"arn:aws:lambda:us-east-1:123:function:fn",
		},
		{"arn:aws:apigateway:us-east-1:s3:path/bucket/key", ""},
		{"http://example.com", ""},
	}
	for _, tt := range tests {
		if got := apigwLambdaInvokeARN(tt.uri); got != tt.want {
			t.Errorf("apigwLambdaInvokeARN(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}
