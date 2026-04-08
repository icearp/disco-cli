package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveFlowLogRelationships)
	registerResolver(resolveNetworkInsightsAnalysisRelationships)
	registerResolver(resolveNetworkInsightsAccessScopeAnalysisRelationships)
}

// resolveNetworkInsightsAnalysisRelationships links each analysis to its path.
func resolveNetworkInsightsAnalysisRelationships(acct *account, st *store.Store) error {
	analyses, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NetworkInsightsAnalysis},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range analyses {
		var attrs struct {
			NetworkInsightsPathId *string `json:"NetworkInsightsPathId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.NetworkInsightsPathId != nil {
			// Path NativeID is its ARN; ec2ARN reconstructs it from the path ID.
			pathID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInsightsPath,
				ec2ARN(sv(r.Region), acct.ID, "network-insights-path", *attrs.NetworkInsightsPathId))
			if err := st.UpsertRelationship(r.ID, pathID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert network-insights-analysis→path relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveNetworkInsightsAccessScopeAnalysisRelationships links each scope analysis
// to its access scope.
func resolveNetworkInsightsAccessScopeAnalysisRelationships(acct *account, st *store.Store) error {
	analyses, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NetworkInsightsAccessScopeAnalysis},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range analyses {
		var attrs struct {
			NetworkInsightsAccessScopeId *string `json:"NetworkInsightsAccessScopeId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.NetworkInsightsAccessScopeId != nil {
			// Scope NativeID is its ARN; ec2ARN reconstructs it from the scope ID.
			scopeID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInsightsAccessScope,
				ec2ARN(sv(r.Region), acct.ID, "network-insights-access-scope", *attrs.NetworkInsightsAccessScopeId))
			if err := st.UpsertRelationship(r.ID, scopeID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert network-insights-scope-analysis→scope relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveFlowLogRelationships(acct *account, st *store.Store) error {
	logs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2FlowLog},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range logs {
		var attrs struct {
			ResourceId   *string `json:"ResourceId"`
			ResourceType *string `json:"ResourceType"` // "VPC", "Subnet", "NetworkInterface"
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceId == nil || attrs.ResourceType == nil {
			continue
		}
		region := sv(r.Region)
		var targetType, arnResourceType string
		switch *attrs.ResourceType {
		case "VPC":
			targetType, arnResourceType = TypeEC2VPC, "vpc"
		case "Subnet":
			targetType, arnResourceType = TypeEC2Subnet, "subnet"
		case "NetworkInterface":
			targetType, arnResourceType = TypeEC2NetworkInterface, "network-interface"
		default:
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, targetType, ec2ARN(region, acct.ID, arnResourceType, *attrs.ResourceId))
		if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert flow-log→resource relationship: %w", err)
		}
	}
	return nil
}
