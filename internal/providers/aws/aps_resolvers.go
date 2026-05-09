package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
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
// Scraper → workspace (uses) via Destination.AmpConfiguration.WorkspaceArn.
// Scraper → EKS cluster (uses) via Source.EksConfiguration.ClusterArn.
// Scraper → security groups (uses) and subnets (attached-to) via
// Source.EksConfiguration.SecurityGroupIds / SubnetIds.
//
// SDK marshals union types under their member-name key (e.g.
// `{"AmpConfiguration":{...}}`, `{"EksConfiguration":{...}}`).
func resolveAPSRelationships(acct *account, st *store.Store) error {
	if err := resolveAPSWorkspaceTargets(acct, st); err != nil {
		return err
	}
	return resolveAPSScraperTargets(acct, st)
}

type apsWsAttrs struct {
	KmsKeyArn *string `json:"KmsKeyArn"`
}

func resolveAPSWorkspaceTargets(acct *account, st *store.Store) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPSWorkspace},
		Limit: util.AllResources,
	})
	if err != nil || len(workspaces) == 0 {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		var a apsWsAttrs
		if err := json.Unmarshal([]byte(w.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(w.Region)
		if keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.KmsKeyArn), region, acct.ID); ok {
			if err := st.UpsertRelationship(w.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-workspace→kms: %w", err)
			}
		}
	}
	return nil
}

type apsAmpCfg struct {
	WorkspaceArn *string `json:"WorkspaceArn"`
}

type apsEksCfg struct {
	ClusterArn       *string  `json:"ClusterArn"`
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
	SubnetIDs        []string `json:"SubnetIds"`
}

type apsVpcCfg struct {
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
	SubnetIDs        []string `json:"SubnetIds"`
}

type apsSrcUnion struct {
	EksConfiguration *apsEksCfg `json:"EksConfiguration"`
	VpcConfiguration *apsVpcCfg `json:"VpcConfiguration"`
}

type apsDstUnion struct {
	AmpConfiguration *apsAmpCfg `json:"AmpConfiguration"`
}

type apsScraperAttrs struct {
	RoleArn     *string      `json:"RoleArn"`
	Source      *apsSrcUnion `json:"Source"`
	Destination *apsDstUnion `json:"Destination"`
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPSScraper},
		Limit: util.AllResources,
	})
	if err != nil || len(scrapers) == 0 {
		return err
	}
	idx, err := loadAPSScraperIDs(acct, st)
	if err != nil {
		return err
	}
	for _, s := range scrapers {
		var a apsScraperAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitAPSScraperRoleAndWorkspace(st, acct, s.ID, a, idx); err != nil {
			return err
		}
		if err := emitAPSScraperVPCEdges(st, acct, s.ID, sv(s.Region), a.Source, idx); err != nil {
			return err
		}
	}
	return nil
}

func emitAPSScraperRoleAndWorkspace(st *store.Store, acct *account, srcID string, a apsScraperAttrs, idx *apsScraperIDs) error {
	if roleARN := sv(a.RoleArn); roleARN != "" {
		roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
		if _, ok := idx.roles[roleID]; ok {
			if err := st.UpsertRelationship(srcID, roleID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→iam-role: %w", err)
			}
		}
	}
	if a.Destination != nil && a.Destination.AmpConfiguration != nil {
		if wsARN := sv(a.Destination.AmpConfiguration.WorkspaceArn); wsARN != "" {
			wsID := store.ResourceID("aws", acct.ID, TypeAPSWorkspace, wsARN)
			if _, ok := idx.ws[wsID]; ok {
				if err := st.UpsertRelationship(srcID, wsID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aps-scraper→workspace: %w", err)
				}
			}
		}
	}
	return nil
}

func emitAPSScraperVPCEdges(st *store.Store, acct *account, srcID, region string, src *apsSrcUnion, idx *apsScraperIDs) error {
	if src == nil {
		return nil
	}
	var sgList, subnetList []string
	if eks := src.EksConfiguration; eks != nil {
		if clusterARN := sv(eks.ClusterArn); clusterARN != "" {
			eksID := store.ResourceID("aws", acct.ID, TypeEKSCluster, clusterARN)
			if _, ok := idx.eks[eksID]; ok {
				if err := st.UpsertRelationship(srcID, eksID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aps-scraper→eks: %w", err)
				}
			}
		}
		sgList = eks.SecurityGroupIDs
		subnetList = eks.SubnetIDs
	}
	if vpc := src.VpcConfiguration; vpc != nil {
		sgList = append(sgList, vpc.SecurityGroupIDs...)
		subnetList = append(subnetList, vpc.SubnetIDs...)
	}
	for _, sgID := range sgList {
		if sgID == "" {
			continue
		}
		id := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sgID))
		if _, ok := idx.sg[id]; ok {
			if err := st.UpsertRelationship(srcID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→sg: %w", err)
			}
		}
	}
	for _, subnetID := range subnetList {
		if subnetID == "" {
			continue
		}
		id := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", subnetID))
		if _, ok := idx.subnet[id]; ok {
			if err := st.UpsertRelationship(srcID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert aps-scraper→subnet: %w", err)
			}
		}
	}
	return nil
}
