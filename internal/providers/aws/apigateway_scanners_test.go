package aws

import (
	"testing"

	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
)

// TestAPIGatewayDomainIsPrivate covers the EndpointConfiguration.Types branch
// used by scanAPIGatewayDomainNames to route private custom-domain rows to
// the V2 (TypeAPIGatewayPrivateDomainName) disco type.
func TestAPIGatewayDomainIsPrivate(t *testing.T) {
	cases := []struct {
		name string
		ec   *apigatewaytypes.EndpointConfiguration
		want bool
	}{
		{name: "nil", ec: nil, want: false},
		{name: "empty types", ec: &apigatewaytypes.EndpointConfiguration{}, want: false},
		{name: "regional only", ec: &apigatewaytypes.EndpointConfiguration{Types: []apigatewaytypes.EndpointType{apigatewaytypes.EndpointTypeRegional}}, want: false},
		{name: "edge only", ec: &apigatewaytypes.EndpointConfiguration{Types: []apigatewaytypes.EndpointType{apigatewaytypes.EndpointTypeEdge}}, want: false},
		{name: "private only", ec: &apigatewaytypes.EndpointConfiguration{Types: []apigatewaytypes.EndpointType{apigatewaytypes.EndpointTypePrivate}}, want: true},
		{name: "regional+private", ec: &apigatewaytypes.EndpointConfiguration{Types: []apigatewaytypes.EndpointType{apigatewaytypes.EndpointTypeRegional, apigatewaytypes.EndpointTypePrivate}}, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := apigatewayDomainIsPrivate(c.ec); got != c.want {
				t.Errorf("apigatewayDomainIsPrivate = %v, want %v", got, c.want)
			}
		})
	}
}
