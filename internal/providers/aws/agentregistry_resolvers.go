package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopAgentRegistryHierarchy,
		EdgeDecl{TypeAgentRegistryRegistry, TypeAgentRegistryRegistryRecord, store.RelContains},
	)
	registerResolver(
		resolveAgentRegistryRecordSources,
		EdgeDecl{TypeAgentRegistryRegistryRecord, TypeBedrockAgentCoreRuntime, store.RelUses},
		EdgeDecl{TypeAgentRegistryRegistryRecord, TypeBedrockAgentCoreGateway, store.RelUses},
	)
}

// noopAgentRegistryHierarchy only carries the EdgeDecl: scanAgentRegistryRecords
// links each record under its registry as it stores it.
func noopAgentRegistryHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveAgentRegistryRecordSources wires a record that auto-detection
// created to the Bedrock AgentCore runtime or gateway it was detected from
// (ProvenanceSummaryList[].SourceId, an ARN). Only targets scanned in the
// record's own account get an edge: an organization-scope detection from a
// member account is looked up under the registry owner's account and so gets
// none, even when that member account was scanned. A row that fails to
// decode is reported at the end, so that it costs the other rows none of
// their edges.
func resolveAgentRegistryRecordSources(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAgentRegistryRegistryRecord}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	runtimes, err := scannedIDSet(acct, st, TypeBedrockAgentCoreRuntime)
	if err != nil {
		return err
	}
	gateways, err := scannedIDSet(acct, st, TypeBedrockAgentCoreGateway)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ProvenanceSummaryList []struct {
				SourceID string `json:"SourceId"`
			} `json:"ProvenanceSummaryList"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAgentRegistryRegistryRecord, r.NativeID, err))
			continue
		}
		for _, p := range attrs.ProvenanceSummaryList {
			if p.SourceID == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, p.SourceID)
			if !runtimes[tgtID] && !gateways[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert agent-registry record→source: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}
