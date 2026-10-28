package aws

import (
	"context"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	"github.com/icearp/disco-cli/store"
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

// TestScanAPIGateway_Tags: every taggable REST API Gateway type stores the
// tags its list element carries, nil when it carries none. The other types
// are stored without tags even under a tagged parent: AWS documents that tags
// cannot be set on them, that they inherit their parent's tags only for
// attribute-based access control, and that GetTags does not return inherited
// tags (apigateway-tagging-supported-resources.html), so no tag op is called.
func TestScanAPIGateway_Tags(t *testing.T) {
	tagged := map[string]string{"team": "core"}
	private := &apigatewaytypes.EndpointConfiguration{Types: []apigatewaytypes.EndpointType{apigatewaytypes.EndpointTypePrivate}}
	items := func(id string) []string { return []string{id + "-tagged", id + "-plain"} }
	tagsOf := func(id string) map[string]string {
		if strings.HasSuffix(id, "-tagged") {
			return tagged
		}
		return nil
	}
	handlers := map[string]func(in any) (any, error){
		"GetRestApis": func(any) (any, error) {
			var out apigateway.GetRestApisOutput
			for _, id := range items("api") {
				out.Items = append(out.Items, apigatewaytypes.RestApi{Id: sdkaws.String(id), Name: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetStages": func(any) (any, error) {
			var out apigateway.GetStagesOutput
			for _, id := range items("stage") {
				out.Item = append(out.Item, apigatewaytypes.Stage{StageName: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetApiKeys": func(any) (any, error) {
			var out apigateway.GetApiKeysOutput
			for _, id := range items("key") {
				out.Items = append(out.Items, apigatewaytypes.ApiKey{Id: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetClientCertificates": func(any) (any, error) {
			var out apigateway.GetClientCertificatesOutput
			for _, id := range items("cert") {
				out.Items = append(out.Items, apigatewaytypes.ClientCertificate{ClientCertificateId: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetDomainNames": func(any) (any, error) {
			var out apigateway.GetDomainNamesOutput
			for _, id := range items("public.example.com") {
				out.Items = append(out.Items, apigatewaytypes.DomainName{DomainName: sdkaws.String(id), Tags: tagsOf(id)})
			}
			for _, id := range items("private.example.com") {
				out.Items = append(out.Items, apigatewaytypes.DomainName{DomainName: sdkaws.String(id), EndpointConfiguration: private, Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetBasePathMappings": func(any) (any, error) {
			return &apigateway.GetBasePathMappingsOutput{Items: []apigatewaytypes.BasePathMapping{{BasePath: sdkaws.String("v1")}}}, nil
		},
		"GetDomainNameAccessAssociations": func(any) (any, error) {
			var out apigateway.GetDomainNameAccessAssociationsOutput
			for _, id := range items("assoc") {
				out.Items = append(out.Items, apigatewaytypes.DomainNameAccessAssociation{DomainNameAccessAssociationArn: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetUsagePlans": func(any) (any, error) {
			var out apigateway.GetUsagePlansOutput
			for _, id := range items("plan") {
				out.Items = append(out.Items, apigatewaytypes.UsagePlan{Id: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetUsagePlanKeys": func(any) (any, error) {
			return &apigateway.GetUsagePlanKeysOutput{Items: []apigatewaytypes.UsagePlanKey{{Id: sdkaws.String("upk")}}}, nil
		},
		"GetVpcLinks": func(any) (any, error) {
			var out apigateway.GetVpcLinksOutput
			for _, id := range items("link") {
				out.Items = append(out.Items, apigatewaytypes.VpcLink{Id: sdkaws.String(id), Tags: tagsOf(id)})
			}
			return &out, nil
		},
		"GetAccount": func(any) (any, error) { return &apigateway.GetAccountOutput{}, nil },
		"GetAuthorizers": func(any) (any, error) {
			return &apigateway.GetAuthorizersOutput{Items: []apigatewaytypes.Authorizer{{Id: sdkaws.String("auth")}}}, nil
		},
		"GetDeployments": func(any) (any, error) {
			return &apigateway.GetDeploymentsOutput{Items: []apigatewaytypes.Deployment{{Id: sdkaws.String("dep")}}}, nil
		},
		"GetResources": func(any) (any, error) {
			return &apigateway.GetResourcesOutput{Items: []apigatewaytypes.Resource{{
				Id: sdkaws.String("res"), Path: sdkaws.String("/"),
				ResourceMethods: map[string]apigatewaytypes.Method{"GET": {}},
			}}}, nil
		},
		"GetModels": func(any) (any, error) {
			return &apigateway.GetModelsOutput{Items: []apigatewaytypes.Model{{Name: sdkaws.String("model")}}}, nil
		},
		"GetRequestValidators": func(any) (any, error) {
			return &apigateway.GetRequestValidatorsOutput{Items: []apigatewaytypes.RequestValidator{{Id: sdkaws.String("rv")}}}, nil
		},
		"GetGatewayResponses": func(any) (any, error) {
			return &apigateway.GetGatewayResponsesOutput{Items: []apigatewaytypes.GatewayResponse{{ResponseType: apigatewaytypes.GatewayResponseTypeDefault4xx}}}, nil
		},
		"GetDocumentationParts": func(any) (any, error) {
			return &apigateway.GetDocumentationPartsOutput{Items: []apigatewaytypes.DocumentationPart{{Id: sdkaws.String("part")}}}, nil
		},
		"GetDocumentationVersions": func(any) (any, error) {
			return &apigateway.GetDocumentationVersionsOutput{Items: []apigatewaytypes.DocumentationVersion{{Version: sdkaws.String("1")}}}, nil
		},
	}
	acct := newTestAccount(testAccountID)
	// An op not stubbed, a tag op included, fails the scan.
	acct.cfg = opStubConfig(handlers, apiErr("BadRequestException", "unstubbed op"))
	st := newTestStore(t)
	if _, _, err := scanAPIGateway(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanAPIGateway: %v", err)
	}

	arn := func(path ...string) string { return apigatewayARNIn("aws", testRegion, path...) }
	api := []string{"restapis", "api-tagged"}
	cases := []struct {
		typ, nativeID string
		tags          map[string]string
	}{
		{TypeAPIGatewayRestAPI, arn("restapis", "api-tagged"), tagged},
		{TypeAPIGatewayRestAPI, arn("restapis", "api-plain"), nil},
		{TypeAPIGatewayStage, arn(append(api, "stages", "stage-tagged")...), tagged},
		{TypeAPIGatewayStage, arn(append(api, "stages", "stage-plain")...), nil},
		{TypeAPIGatewayAPIKey, arn("apikeys", "key-tagged"), tagged},
		{TypeAPIGatewayAPIKey, arn("apikeys", "key-plain"), nil},
		{TypeAPIGatewayClientCertificate, arn("clientcertificates", "cert-tagged"), tagged},
		{TypeAPIGatewayClientCertificate, arn("clientcertificates", "cert-plain"), nil},
		{TypeAPIGatewayDomainName, arn("domainnames", "public.example.com-tagged"), tagged},
		{TypeAPIGatewayDomainName, arn("domainnames", "public.example.com-plain"), nil},
		{TypeAPIGatewayPrivateDomainName, arn("domainnames", "private.example.com-tagged"), tagged},
		{TypeAPIGatewayPrivateDomainName, arn("domainnames", "private.example.com-plain"), nil},
		{TypeAPIGatewayDomainNameAccessAssoc, "assoc-tagged", tagged},
		{TypeAPIGatewayDomainNameAccessAssoc, "assoc-plain", nil},
		{TypeAPIGatewayUsagePlan, arn("usageplans", "plan-tagged"), tagged},
		{TypeAPIGatewayUsagePlan, arn("usageplans", "plan-plain"), nil},
		{TypeAPIGatewayVpcLink, arn("vpclinks", "link-tagged"), tagged},
		{TypeAPIGatewayVpcLink, arn("vpclinks", "link-plain"), nil},
		// Children of tagged parents: not taggable themselves.
		{TypeAPIGatewayBasePathMapping, arn("domainnames", "public.example.com-tagged", "basepathmappings", "v1"), nil},
		{TypeAPIGatewayPrivateBasePathMapping, arn("domainnames", "private.example.com-tagged", "basepathmappings", "v1"), nil},
		{TypeAPIGatewayUsagePlanKey, arn("usageplans", "plan-tagged", "keys", "upk"), nil},
		{TypeAPIGatewayAuthorizer, arn(append(api, "authorizers", "auth")...), nil},
		{TypeAPIGatewayDeployment, arn(append(api, "deployments", "dep")...), nil},
		{TypeAPIGatewayResource, arn(append(api, "resources", "res")...), nil},
		{TypeAPIGatewayMethod, arn(append(api, "resources", "res", "methods", "GET")...), nil},
		{TypeAPIGatewayModel, arn(append(api, "models", "model")...), nil},
		{TypeAPIGatewayRequestValidator, arn(append(api, "requestvalidators", "rv")...), nil},
		{TypeAPIGatewayGatewayResponse, arn(append(api, "gatewayresponses", "DEFAULT_4XX")...), nil},
		{TypeAPIGatewayDocumentationPart, arn(append(api, "documentation", "parts", "part")...), nil},
		{TypeAPIGatewayDocumentationVersion, arn(append(api, "documentation", "versions", "1")...), nil},
	}
	rowsOf := map[string]map[string]*store.Resource{}
	for _, c := range cases {
		if rowsOf[c.typ] != nil {
			continue
		}
		rows, err := st.ListResources(store.ResourceFilter{Types: []string{c.typ}})
		if err != nil {
			t.Fatalf("ListResources(%s): %v", c.typ, err)
		}
		rowsOf[c.typ] = map[string]*store.Resource{}
		for i := range rows {
			rowsOf[c.typ][rows[i].NativeID] = &rows[i]
		}
	}
	for _, c := range cases {
		t.Run(c.typ+" "+c.nativeID, func(t *testing.T) {
			r, ok := rowsOf[c.typ][c.nativeID]
			if !ok {
				t.Fatalf("no %s row %s", c.typ, c.nativeID)
			}
			if c.tags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
				}
				return
			}
			assertTagsMap(t, r.TagsJSON, c.tags)
		})
	}
}
