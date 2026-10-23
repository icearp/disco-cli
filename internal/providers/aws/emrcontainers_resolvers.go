package aws

import (
	"encoding/json"
	"errors"
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
// (JobTemplateData.ExecutionRoleArn) and KMS key (KmsKeyArn). A row whose
// attributes fail to decode is reported after every other row is wired.
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
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn       *string `json:"KmsKeyArn"`
			JobTemplateData *struct {
				ExecutionRoleArn *string `json:"ExecutionRoleArn"`
			} `json:"JobTemplateData"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeEMRContainersJobTemplate, r.NativeID, err))
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
	return errors.Join(decodeErrs...)
}

// resolveEMRCEndpointRefs wires each managed endpoint to its parent virtual
// cluster (the NativeID before `/endpoints/{id}`) and to its execution role.
// A row whose NativeID or attributes cannot be read is reported after every
// other row is wired.
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
	var decodeErrs []error
	for _, r := range rows {
		parent, _, ok := strings.Cut(r.NativeID, "/endpoints/")
		if !ok {
			decodeErrs = append(decodeErrs, fmt.Errorf("%s %s: NativeID has no /endpoints/ segment", TypeEMRContainersEndpoint, r.NativeID))
		} else if tgtID := store.ResourceID("aws", acct.ID, parent); vcSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert emr-c endpoint→vc: %w", err)
			}
		}
		var attrs struct {
			ExecutionRoleArn *string `json:"ExecutionRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeEMRContainersEndpoint, r.NativeID, err))
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
	return errors.Join(decodeErrs...)
}

// emrcSecurityConfigARN is the ARN of security configuration id in the
// partition, region and account of the virtual cluster vcARN
// (`arn:{partition}:emr-containers:{region}:{account}:/virtualclusters/{id}`);
// false when vcARN is not of that shape. The model does not document the id
// as unique across regions or accounts, so the cluster's ARN qualifies it.
func emrcSecurityConfigARN(vcARN, id string) (string, bool) {
	prefix, _, ok := strings.Cut(vcARN, ":/virtualclusters/")
	if !ok {
		return "", false
	}
	return prefix + ":/securityconfigurations/" + id, true
}

// resolveEMRCVirtualClusterToSecConfig wires each virtual cluster to the
// security configuration its SecurityConfigurationId names, in the cluster's
// own region and account. A row whose NativeID or attributes cannot be read
// is reported after every other row is wired.
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
	scSet, err := scannedIDSet(acct, st, TypeEMRContainersSecurityConfig)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			SecurityConfigurationID *string `json:"SecurityConfigurationId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeEMRContainersVirtualCluster, r.NativeID, err))
			continue
		}
		id := sv(attrs.SecurityConfigurationID)
		if id == "" {
			continue
		}
		scARN, ok := emrcSecurityConfigARN(r.NativeID, id)
		if !ok {
			decodeErrs = append(decodeErrs, fmt.Errorf("%s %s: NativeID has no :/virtualclusters/ segment", TypeEMRContainersVirtualCluster, r.NativeID))
			continue
		}
		if tgtID := store.ResourceID("aws", acct.ID, scARN); scSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert emr-c vc→sec-config: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}
