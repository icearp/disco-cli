package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveFlowLogRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flARN := ec2ARN(testRegion, acct.ID, "vpc-flow-log", "fl-001")
	flID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2FlowLog, flARN, testRegion,
		`{"ResourceId":"vpc-001","ResourceType":"VPC"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveFlowLogRelationships(acct, st); err != nil {
		t.Fatalf("resolveFlowLogRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(flID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, flID, vpcID, store.RelAttachedTo)
}

func TestResolveFlowLogRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2FlowLog, ec2ARN(testRegion, acct.ID, "vpc-flow-log", "fl-bare"), testRegion, "{}")
	if err := resolveFlowLogRelationships(acct, st); err != nil {
		t.Fatalf("resolveFlowLogRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(flID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNetworkInsightsAnalysisRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	// Path NativeID is its ARN (ec2ARN reconstructs the same string from the path ID).
	pathARN := ec2ARN(region, acct.ID, "network-insights-path", "nip-aaa")
	pathID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsPath, pathARN, region, "{}")

	analysisARN := ec2ARN(region, acct.ID, "network-insights-analysis", "nia-bbb")
	analysisID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsAnalysis, analysisARN, region,
		`{"NetworkInsightsPathId": "nip-aaa"}`)

	if err := resolveNetworkInsightsAnalysisRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInsightsAnalysisRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(analysisID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, analysisID, pathID, store.RelAttachedTo)
}

func TestResolveNetworkInsightsAnalysisRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsAnalysis,
		ec2ARN("us-east-1", acct.ID, "network-insights-analysis", "bare"), "us-east-1", "{}")

	if err := resolveNetworkInsightsAnalysisRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInsightsAnalysisRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(id)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNetworkInsightsAccessScopeAnalysisRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	// Scope NativeID is its ARN (ec2ARN reconstructs the same string from the scope ID).
	scopeARN := ec2ARN(region, acct.ID, "network-insights-access-scope", "nias-aaa")
	scopeID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsAccessScope, scopeARN, region, "{}")

	analysisARN := ec2ARN(region, acct.ID, "network-insights-access-scope-analysis", "niasa-bbb")
	analysisID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsAccessScopeAnalysis, analysisARN, region,
		`{"NetworkInsightsAccessScopeId": "nias-aaa"}`)

	if err := resolveNetworkInsightsAccessScopeAnalysisRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInsightsAccessScopeAnalysisRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(analysisID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, analysisID, scopeID, store.RelAttachedTo)
}

func TestResolveNetworkInsightsAccessScopeAnalysisRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInsightsAccessScopeAnalysis,
		ec2ARN("us-east-1", acct.ID, "network-insights-access-scope-analysis", "bare"), "us-east-1", "{}")

	if err := resolveNetworkInsightsAccessScopeAnalysisRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInsightsAccessScopeAnalysisRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(id)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
