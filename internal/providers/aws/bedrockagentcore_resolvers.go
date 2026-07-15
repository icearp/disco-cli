package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveBACGatewayTargetParent,
		EdgeDecl{TypeBedrockAgentCoreGatewayTarget, TypeBedrockAgentCoreGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACRuntimeEndpointParent,
		EdgeDecl{TypeBedrockAgentCoreRuntimeEndpoint, TypeBedrockAgentCoreRuntime, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACPolicyEngine,
		EdgeDecl{TypeBedrockAgentCorePolicy, TypeBedrockAgentCorePolicyEngine, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACPolicyGenerationEngine,
		EdgeDecl{TypeBedrockAgentCorePolicyGeneration, TypeBedrockAgentCorePolicyEngine, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACHarnessEndpointParent,
		EdgeDecl{TypeBedrockAgentCoreHarnessEndpoint, TypeBedrockAgentCoreHarness, store.RelAttachedTo},
	)
	registerResolver(
		resolveBACRegistryRecordParent,
		EdgeDecl{TypeBedrockAgentCoreRegistryRecord, TypeBedrockAgentCoreRegistry, store.RelAttachedTo},
	)
}

// resolveBACPolicyGenerationEngine wires each policy-generation to its parent
// policy-engine via the bare PolicyEngineId, reusing the engine ID index.
func resolveBACPolicyGenerationEngine(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePolicyGeneration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := bacPolicyEngineIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PolicyEngineID *string `json:"PolicyEngineId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		eid := sv(attrs.PolicyEngineID)
		if eid == "" {
			continue
		}
		tgtID, ok := idx[eid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac policy-generation→engine: %w", err)
		}
	}
	return nil
}

// resolveBACHarnessEndpointParent wires each harness-endpoint to its parent
// harness via the bare HarnessId, FK-safe against a harness ID index.
func resolveBACHarnessEndpointParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCoreHarnessEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := bacHarnessIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			HarnessID *string `json:"HarnessId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		hid := sv(attrs.HarnessID)
		if hid == "" {
			continue
		}
		tgtID, ok := idx[hid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac harness-endpoint→harness: %w", err)
		}
	}
	return nil
}

// bacHarnessIDIndex maps HarnessId → resource ID for FK-safe lookup.
func bacHarnessIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCoreHarness},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			HarnessID *string `json:"HarnessId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.HarnessID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveBACRegistryRecordParent wires each registry-record to its parent
// registry via the RegistryArn SDK field (the registry's NativeID).
func resolveBACRegistryRecordParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCoreRegistryRecord},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rgSet, err := scannedIDSet(acct, st, TypeBedrockAgentCoreRegistry)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RegistryArn *string `json:"RegistryArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.RegistryArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !rgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac registry-record→registry: %w", err)
		}
	}
	return nil
}

// resolveBACGatewayTargetParent wires each gateway-target to its parent
// gateway. Scanner synthesizes NativeID
// `arn:aws:bedrock-agentcore:r:a:gateway-target/{gatewayID}/{targetID}`;
// rebuild the gateway ARN as `arn:aws:bedrock-agentcore:r:a:gateway/{gatewayID}`.
func resolveBACGatewayTargetParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCoreGatewayTarget},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	gwSet, err := scannedIDSet(acct, st, TypeBedrockAgentCoreGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = ":gateway-target/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		gid := tail[:end]
		gwARN := r.NativeID[:i] + ":gateway/" + gid
		tgtID := store.ResourceID("aws", acct.ID, gwARN)
		if !gwSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac gateway-target→gateway: %w", err)
		}
	}
	return nil
}

// resolveBACRuntimeEndpointParent wires each runtime-endpoint to its parent
// runtime via the `AgentRuntimeArn` SDK field on the endpoint summary.
func resolveBACRuntimeEndpointParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCoreRuntimeEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtSet, err := scannedIDSet(acct, st, TypeBedrockAgentCoreRuntime)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AgentRuntimeArn *string `json:"AgentRuntimeArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.AgentRuntimeArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !rtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac endpoint→runtime: %w", err)
		}
	}
	return nil
}

// resolveBACPolicyEngine wires each policy to its parent policy-engine via
// `PolicyEngineID` bare-ID lookup against an engine ID index.
func resolveBACPolicyEngine(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := bacPolicyEngineIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PolicyEngineID *string `json:"PolicyEngineId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		eid := sv(attrs.PolicyEngineID)
		if eid == "" {
			continue
		}
		tgtID, ok := idx[eid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bac policy→engine: %w", err)
		}
	}
	return nil
}

// bacPolicyEngineIDIndex maps PolicyEngineID → resource ID for FK-safe lookup.
func bacPolicyEngineIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBedrockAgentCorePolicyEngine},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			PolicyEngineID *string `json:"PolicyEngineId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.PolicyEngineID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}
