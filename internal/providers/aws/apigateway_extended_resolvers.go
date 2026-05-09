package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveAPIGatewayRestAPIChildren,
		EdgeDecl{TypeAPIGatewayDeployment, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayResource, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayModel, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayRequestValidator, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayGatewayResponse, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayDocumentationPart, TypeAPIGatewayRestAPI, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayDocumentationVersion, TypeAPIGatewayRestAPI, store.RelAttachedTo},
	)
	registerResolver(
		resolveAPIGatewayVpcLinkTargets,
		EdgeDecl{TypeAPIGatewayVpcLink, TypeELBv2LoadBalancer, store.RelRoutesTo},
	)
}

// apigatewayRestAPIARNFromChild extracts the parent rest-api ARN from a
// child resource's NativeID of shape
// `arn:aws:apigateway:r::/restapis/{apiId}/<kind>/...`.
func apigatewayRestAPIARNFromChild(arn string) string {
	const prefix = "/restapis/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// resolveAPIGatewayRestAPIChildren wires deployment / resource / model /
// request-validator / gateway-response / documentation-part /
// documentation-version to the parent rest-api by parsing the
// `restapis/{apiId}` segment in the child's NativeID.
func resolveAPIGatewayRestAPIChildren(acct *account, st *store.Store) error {
	apiSet, err := scannedIDSet(acct, st, TypeAPIGatewayRestAPI)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeAPIGatewayDeployment,
		TypeAPIGatewayResource,
		TypeAPIGatewayModel,
		TypeAPIGatewayRequestValidator,
		TypeAPIGatewayGatewayResponse,
		TypeAPIGatewayDocumentationPart,
		TypeAPIGatewayDocumentationVersion,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := apigatewayRestAPIARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayRestAPI, parent)
			if !apiSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw %s→rest-api: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayVpcLinkTargets walks each VPC link's TargetArns[] and
// emits routes-to → ELBv2 NLB.
func resolveAPIGatewayVpcLinkTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayVpcLink},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	nlbSet, err := scannedIDSet(acct, st, TypeELBv2LoadBalancer)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TargetArns []string `json:"TargetArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range attrs.TargetArns {
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeELBv2LoadBalancer, arn)
			if !nlbSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw-vpc-link→nlb: %w", err)
			}
		}
	}
	return nil
}
