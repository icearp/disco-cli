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
		resolveAPSRelationships,
		EdgeDecl{TypeAPSWorkspace, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeAPSScraper, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAPSScraper, TypeAPSWorkspace, store.RelUses},
		EdgeDecl{TypeAPSScraper, TypeEKSCluster, store.RelUses},
		EdgeDecl{TypeAPSScraper, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeAPSScraper, TypeEC2Subnet, store.RelAttachedTo},
	)
}

// resolveAPSRelationships emits APS workspace and scraper edges.
//
// Workspace → KMS key (uses) via KmsKeyArn.
// Scraper → IAM role (uses) via RoleArn.
// Scraper → workspace (uses) via Destination.Value.WorkspaceArn.
// Scraper → EKS cluster (uses) via Source.Value.ClusterArn.
// Scraper → security groups (uses) and subnets (attached-to) via
// Source.Value.SecurityGroupIds / SubnetIds.
//
// Those are stored-row paths, not SDK member paths: see apsUnion.
func resolveAPSRelationships(acct *account, st *store.Store) error {
	// Both halves run, so an undecodable workspace row costs the scrapers
	// none of their edges.
	return errors.Join(
		resolveAPSWorkspaceTargets(acct, st),
		resolveAPSScraperTargets(acct, st),
	)
}

type apsWsAttrs struct {
	KmsKeyArn *string `json:"KmsKeyArn"`
}

func resolveAPSWorkspaceTargets(acct *account, st *store.Store) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAPSWorkspace},
		Limit: util.AllResources,
	})
	if err != nil || len(workspaces) == 0 {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, w := range workspaces {
		var a apsWsAttrs
		if err := json.Unmarshal([]byte(w.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", w.Type, w.NativeID, err))
			continue
		}
		region := sv(w.Region)
		if keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.KmsKeyArn), region, acct.ID); ok {
			if err := st.UpsertRelationship(w.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-workspace→kms: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// apsSrcCfg decodes either Source member. EksConfiguration and
// VpcConfiguration share the SecurityGroupIds / SubnetIds keys; only
// EksConfiguration has ClusterArn (required by the SDK model), so a non-empty
// ClusterArn is what marks an EKS source.
type apsSrcCfg struct {
	ClusterArn       *string  `json:"ClusterArn"`
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
	SubnetIDs        []string `json:"SubnetIds"`
}

// apsDstCfg decodes the AmpConfiguration Destination member. The other
// member, CloudWatchConfiguration, carries DatasetArn and leaves WorkspaceArn
// empty.
type apsDstCfg struct {
	WorkspaceArn *string `json:"WorkspaceArn"`
}

// apsUnion is a stored Source or Destination. They are SDK unions, and
// json.Marshal stores a member as `{"Value":{...}}` without the member name,
// so the stored row cannot say which member it held; the fields inside Value
// must tell them apart. Value stays raw because an UnknownUnionMember (a
// member this SDK version cannot name) stores `{"Tag":"…","Value":"<base64>"}`,
// and a string there must not cost the row its other edges.
type apsUnion struct {
	Value json.RawMessage `json:"Value"`
}

// decodeAPSUnionValue decodes u's Value into dst when it is a JSON object, and
// leaves dst zero when there is no member or the member is unknown.
func decodeAPSUnionValue(u *apsUnion, dst any) error {
	if u == nil || len(u.Value) == 0 || u.Value[0] != '{' {
		return nil
	}
	return json.Unmarshal(u.Value, dst)
}

type apsScraperAttrs struct {
	RoleArn     *string   `json:"RoleArn"`
	Source      *apsUnion `json:"Source"`
	Destination *apsUnion `json:"Destination"`
}

type apsScraperIDs struct {
	roles, ws, eks, sg, subnet map[string]bool
}

func loadAPSScraperIDs(acct *account, st *store.Store) (*apsScraperIDs, error) {
	roles, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return nil, err
	}
	ws, err := scannedIDSet(acct, st, TypeAPSWorkspace)
	if err != nil {
		return nil, err
	}
	eks, err := scannedIDSet(acct, st, TypeEKSCluster)
	if err != nil {
		return nil, err
	}
	sg, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return nil, err
	}
	subnet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return nil, err
	}
	return &apsScraperIDs{roles: roles, ws: ws, eks: eks, sg: sg, subnet: subnet}, nil
}

func resolveAPSScraperTargets(acct *account, st *store.Store) error {
	scrapers, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAPSScraper},
		Limit: util.AllResources,
	})
	if err != nil || len(scrapers) == 0 {
		return err
	}
	idx, err := loadAPSScraperIDs(acct, st)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, s := range scrapers {
		var a apsScraperAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", s.Type, s.NativeID, err))
			continue
		}
		// A member that fails to decode is reported and treated as absent, so
		// the row keeps the edges its other fields carry.
		var src apsSrcCfg
		if err := decodeAPSUnionValue(a.Source, &src); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s Source: %w", s.Type, s.NativeID, err))
		}
		var dst apsDstCfg
		if err := decodeAPSUnionValue(a.Destination, &dst); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s Destination: %w", s.Type, s.NativeID, err))
		}
		if err := emitAPSScraperRoleAndWorkspace(st, acct, s.ID, sv(a.RoleArn), sv(dst.WorkspaceArn), idx); err != nil {
			return err
		}
		if err := emitAPSScraperVPCEdges(st, acct, s.ID, sv(s.Region), src, idx); err != nil {
			return err
		}
	}
	return errors.Join(decodeErrs...)
}

func emitAPSScraperRoleAndWorkspace(st *store.Store, acct *account, srcID, roleARN, wsARN string, idx *apsScraperIDs) error {
	if roleARN != "" {
		roleID := store.ResourceID("aws", acct.ID, roleARN)
		if _, ok := idx.roles[roleID]; ok {
			if err := st.UpsertRelationship(srcID, roleID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→iam-role: %w", err)
			}
		}
	}
	if wsARN != "" {
		wsID := store.ResourceID("aws", acct.ID, wsARN)
		if _, ok := idx.ws[wsID]; ok {
			if err := st.UpsertRelationship(srcID, wsID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→workspace: %w", err)
			}
		}
	}
	return nil
}

func emitAPSScraperVPCEdges(st *store.Store, acct *account, srcID, region string, cfg apsSrcCfg, idx *apsScraperIDs) error {
	if clusterARN := sv(cfg.ClusterArn); clusterARN != "" {
		eksID := store.ResourceID("aws", acct.ID, clusterARN)
		if _, ok := idx.eks[eksID]; ok {
			if err := st.UpsertRelationship(srcID, eksID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→eks: %w", err)
			}
		}
	}
	for _, sgID := range cfg.SecurityGroupIDs {
		if sgID == "" {
			continue
		}
		id := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "security-group", sgID))
		if _, ok := idx.sg[id]; ok {
			if err := st.UpsertRelationship(srcID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→sg: %w", err)
			}
		}
	}
	for _, subnetID := range cfg.SubnetIDs {
		if subnetID == "" {
			continue
		}
		id := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "subnet", subnetID))
		if _, ok := idx.subnet[id]; ok {
			if err := st.UpsertRelationship(srcID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→subnet: %w", err)
			}
		}
	}
	return nil
}
