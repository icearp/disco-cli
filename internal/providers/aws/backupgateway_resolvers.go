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
		resolveBackupGatewayRelationships,
		EdgeDecl{TypeBackupGatewayHypervisor, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveBackupGatewayMemberHypervisor,
		EdgeDecl{TypeBackupGatewayGateway, TypeBackupGatewayHypervisor, store.RelAttachedTo},
		EdgeDecl{TypeBackupGatewayVirtualMachine, TypeBackupGatewayHypervisor, store.RelAttachedTo},
	)
	registerResolver(
		resolveBackupGatewayPropertyMappingRoles,
		EdgeDecl{TypeBackupGatewayHypervisorPropertyMapping, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		noopBackupGatewayConfigHierarchy,
		EdgeDecl{TypeBackupGatewayGateway, TypeBackupGatewayBandwidthRateLimitSchedule, store.RelContains},
		EdgeDecl{TypeBackupGatewayHypervisor, TypeBackupGatewayHypervisorPropertyMapping, store.RelContains},
	)
}

// noopBackupGatewayConfigHierarchy only carries the EdgeDecl metadata for the
// contains pairs backupGatewayConfigFanOut records at scan time.
func noopBackupGatewayConfigHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveBackupGatewayPropertyMappingRoles links each hypervisor property
// mapping to the IAM role it names, when that role is stored.
func resolveBackupGatewayPropertyMappingRoles(acct *account, st *store.Store) error {
	mappings, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupGatewayHypervisorPropertyMapping}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(mappings) == 0 {
		return nil
	}
	roleIDs, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, m := range mappings {
		var attrs struct {
			IamRoleArn *string `json:"IamRoleArn"`
		}
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode backupgateway property mapping %s: %w", m.NativeID, err)
		}
		roleID := store.ResourceID("aws", acct.ID, sv(attrs.IamRoleArn))
		if !roleIDs[roleID] {
			continue
		}
		if err := st.UpsertRelationship(m.ID, roleID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backupgateway property mapping→iam-role: %w", err)
		}
	}
	return nil
}

// resolveBackupGatewayMemberHypervisor wires gateway → hypervisor and
// virtual-machine → hypervisor via each member's HypervisorId. Hypervisors are
// stored under their ARN (…:hypervisor/<id>); the id is recovered from the
// ARN's last segment for the lookup. FK-safe: an unknown HypervisorId (or a
// member with none) emits no edge.
func resolveBackupGatewayMemberHypervisor(acct *account, st *store.Store) error {
	hyps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupGatewayHypervisor}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(hyps) == 0 {
		return nil
	}
	byID := make(map[string]string, len(hyps))
	for _, h := range hyps {
		if _, id, ok := strings.Cut(h.NativeID, "hypervisor/"); ok && id != "" {
			byID[id] = h.ID
		}
	}
	members, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupGatewayGateway, TypeBackupGatewayVirtualMachine}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, m := range members {
		var attrs struct {
			HypervisorID *string `json:"HypervisorId"`
		}
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			continue
		}
		hid := sv(attrs.HypervisorID)
		if hid == "" {
			continue
		}
		tgt, ok := byID[hid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(m.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert backupgateway member→hypervisor: %w", err)
		}
	}
	return nil
}

// resolveBackupGatewayRelationships emits hypervisor→KMS edges via the
// shared KMS resolver index. KmsKeyArn is empty for hypervisors that use
// the default AWS-managed key — index resolution skips dangling targets.
func resolveBackupGatewayRelationships(acct *account, st *store.Store) error {
	hyps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeBackupGatewayHypervisor},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range hyps {
		var attrs struct {
			KmsKeyArn *string `json:"KmsKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		kid, ok := idx.resolveKMSKeyID(sv(attrs.KmsKeyArn), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, kid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backupgateway-hypervisor→kms: %w", err)
		}
	}
	return nil
}
