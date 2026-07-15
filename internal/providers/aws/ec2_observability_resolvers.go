package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveFlowLogRelationships,
		EdgeDecl{TypeEC2FlowLog, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeEC2FlowLog, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2FlowLog, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(
		resolveNetworkInsightsAnalysisRelationships,
		EdgeDecl{TypeEC2NetworkInsightsAnalysis, TypeEC2NetworkInsightsPath, store.RelAttachedTo},
	)
	registerResolver(
		resolveNetworkInsightsAccessScopeAnalysisRelationships,
		EdgeDecl{TypeEC2NetworkInsightsAccessScopeAnalysis, TypeEC2NetworkInsightsAccessScope, store.RelAttachedTo},
	)
}

// resolveNetworkInsightsAnalysisRelationships links each analysis to its path.
func resolveNetworkInsightsAnalysisRelationships(acct *account, st *store.Store) error {
	analyses, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2NetworkInsightsAnalysis},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range analyses {
		var attrs struct {
			NetworkInsightsPathID *string `json:"NetworkInsightsPathID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.NetworkInsightsPathID != nil {
			// Path NativeID is its ARN; ec2ARN reconstructs it from the path ID.
			pathID := store.ResourceID("aws", acct.ID,
				ec2ARN(sv(r.Region), acct.ID, "network-insights-path", *attrs.NetworkInsightsPathID))

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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2NetworkInsightsAccessScopeAnalysis},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range analyses {
		var attrs struct {
			NetworkInsightsAccessScopeID *string `json:"NetworkInsightsAccessScopeID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.NetworkInsightsAccessScopeID != nil {
			// Scope NativeID is its ARN; ec2ARN reconstructs it from the scope ID.
			scopeID := store.ResourceID("aws", acct.ID,
				ec2ARN(sv(r.Region), acct.ID, "network-insights-access-scope", *attrs.NetworkInsightsAccessScopeID))

			if err := st.UpsertRelationship(r.ID, scopeID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert network-insights-scope-analysis→scope relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveFlowLogRelationships(acct *account, st *store.Store) error {
	logs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2FlowLog},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range logs {
		var attrs struct {
			ResourceID   *string `json:"ResourceID"`
			ResourceType *string `json:"ResourceType"` // "VPC", "Subnet", "NetworkInterface"
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceID == nil || attrs.ResourceType == nil {
			continue
		}
		region := sv(r.Region)
		var arnResourceType string
		switch *attrs.ResourceType {
		case "VPC":
			arnResourceType = "vpc"
		case "Subnet":
			arnResourceType = "subnet"
		case "NetworkInterface":
			arnResourceType = "network-interface"
		default:
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, arnResourceType, *attrs.ResourceID))
		if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert flow-log→resource relationship: %w", err)
		}
	}
	return nil
}
