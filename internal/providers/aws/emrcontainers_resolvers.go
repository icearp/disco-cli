package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
	registerResolver(
		resolveEMRCJobTemplateRefs,
		EdgeDecl{TypeEMRContainersJobTemplate, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeEMRContainersJobTemplate, TypeKMSKey, store.RelUses},
	)
}

// resolveEMRCJobTemplateRefs wires each job template to its execution role
// (JobTemplateData.ExecutionRoleArn) and KMS key (KmsKeyArn).
func resolveEMRCJobTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRContainersJobTemplate}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn       *string `json:"KmsKeyArn"`
			JobTemplateData *struct {
				ExecutionRoleArn *string `json:"ExecutionRoleArn"`
			} `json:"JobTemplateData"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.JobTemplateData != nil {
			if role := sv(attrs.JobTemplateData.ExecutionRoleArn); role != "" {
				tgtID := store.ResourceID("aws", acct.ID, role)
				if roleSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert emr-c job-template→role: %w", err)
					}
				}
			}
		}
		if ref := sv(attrs.KmsKeyArn); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-c job-template→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEMRCEndpointRefs wires each managed endpoint to its parent virtual
// cluster (extracted from NativeID `/virtualclusters/{id}/endpoints/{id}`) and
// to its execution role.
func resolveEMRCEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRContainersEndpoint}, Limit: util.AllResources,
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
			tgtID := store.ResourceID("aws", acct.ID, parent)
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
			tgtID := store.ResourceID("aws", acct.ID, role)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRContainersVirtualCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRContainersSecurityConfig}, Limit: util.AllResources,
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
