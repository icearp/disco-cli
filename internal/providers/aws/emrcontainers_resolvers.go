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
		resolveEMRCEndpointRefs,
		EdgeDecl{TypeEMRContainersEndpoint, TypeEMRContainersVirtualCluster, store.RelAttachedTo},
		EdgeDecl{TypeEMRContainersEndpoint, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveEMRCVirtualClusterToSecConfig,
		EdgeDecl{TypeEMRContainersVirtualCluster, TypeEMRContainersSecurityConfig, store.RelUses},
	)
}

// resolveEMRCEndpointRefs wires each managed endpoint to its parent virtual
// cluster (via NativeID `/virtualclusters/{id}/endpoints/{id}` extract) and to
// its execution role.
func resolveEMRCEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEMRContainersEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vcSet, err := scannedIDSet(acct, st, TypeEMRContainersVirtualCluster)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if i := strings.Index(r.NativeID, "/endpoints/"); i >= 0 {
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, TypeEMRContainersVirtualCluster, parent)
			if vcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-c endpoint→vc: %w", err)
				}
			}
		}
		var attrs struct {
			ExecutionRoleArn *string `json:"ExecutionRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.ExecutionRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-c endpoint→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEMRCVirtualClusterToSecConfig wires each virtual-cluster to its
// security-configuration via SecurityConfigurationID.
func resolveEMRCVirtualClusterToSecConfig(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEMRContainersVirtualCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEMRContainersSecurityConfig}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	scByID := make(map[string]string, len(scRows))
	for _, s := range scRows {
		// Security-config NativeID ends in `/securityconfigurations/{id}`.
		if i := strings.LastIndex(s.NativeID, "/"); i >= 0 {
			scByID[s.NativeID[i+1:]] = s.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			SecurityConfigurationID *string `json:"SecurityConfigurationId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.SecurityConfigurationID); id != "" {
			if tgtID, ok := scByID[id]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-c vc→sec-config: %w", err)
				}
			}
		}
	}
	return nil
}
