package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCSCRepositoryLinkRefs,
		EdgeDecl{TypeCodeStarConnectionsRepositoryLink, TypeCodeStarConnectionsConnection, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsRepositoryLink, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveCSCSyncConfigurationRefs,
		EdgeDecl{TypeCodeStarConnectionsSyncConfiguration, TypeCodeStarConnectionsRepositoryLink, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsSyncConfiguration, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveCSCHostNetwork,
		EdgeDecl{TypeCodeStarConnectionsHost, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsHost, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsHost, TypeEC2SecurityGroup, store.RelUses},
	)
}

// resolveCSCHostNetwork wires a self-managed-SCM host to its VpcConfiguration's
// VPC, subnets, and security groups (present only when installed inside a
// private network), all FK-safe.
func resolveCSCHostNetwork(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsHost}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcConfiguration *struct {
				VpcID            *string  `json:"VpcId"`
				SubnetIDs        []string `json:"SubnetIds"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.VpcConfiguration == nil {
			continue
		}
		region := sv(r.Region)
		if err := cscHostNetEdge(st, acct.ID, r.ID, vpcSet, region, "vpc", sv(attrs.VpcConfiguration.VpcID), TypeEC2VPC, store.RelAttachedTo); err != nil {
			return err
		}
		for _, sn := range attrs.VpcConfiguration.SubnetIDs {
			if err := cscHostNetEdge(st, acct.ID, r.ID, subnetSet, region, "subnet", sn, TypeEC2Subnet, store.RelAttachedTo); err != nil {
				return err
			}
		}
		for _, sg := range attrs.VpcConfiguration.SecurityGroupIDs {
			if err := cscHostNetEdge(st, acct.ID, r.ID, sgSet, region, "security-group", sg, TypeEC2SecurityGroup, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// cscHostNetEdge emits one FK-safe edge from a host to the EC2 resource named by
// rawID; skips empty refs and unscanned targets.
func cscHostNetEdge(st *store.Store, acctID, srcID string, tgtSet map[string]bool, region, kind, rawID, tgtType, edgeKind string) error {
	if rawID == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acctID, tgtType, ec2ARN(region, acctID, kind, rawID))
	if !tgtSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, edgeKind, "directed", nil); err != nil {
		return fmt.Errorf("upsert csc host→%s: %w", kind, err)
	}
	return nil
}

// resolveCSCRepositoryLinkRefs wires repository-link → connection (ConnectionArn)
// and KMS key (EncryptionKeyArn).
func resolveCSCRepositoryLinkRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsRepositoryLink}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	connSet, err := scannedIDSet(acct, st, TypeCodeStarConnectionsConnection)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConnectionArn    *string `json:"ConnectionArn"`
			EncryptionKeyArn *string `json:"EncryptionKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if c := sv(attrs.ConnectionArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeCodeStarConnectionsConnection, c)
			if connSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc rl→conn: %w", err)
				}
			}
		}
		if k := sv(attrs.EncryptionKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc rl→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveCSCSyncConfigurationRefs wires sync-configuration → repository-link
// (RepositoryLinkID, via scanned-link-ID index) and IAM role (RoleArn).
func resolveCSCSyncConfigurationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsSyncConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	linkRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsRepositoryLink}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// index: RepositoryLinkID → repository-link resource ID.
	linkByID := map[string]string{}
	for _, lr := range linkRows {
		var la struct {
			RepositoryLinkID *string `json:"RepositoryLinkId"`
		}
		if err := json.Unmarshal([]byte(lr.AttributesJSON), &la); err != nil {
			continue
		}
		if id := sv(la.RepositoryLinkID); id != "" {
			linkByID[id] = lr.ID
		}
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RepositoryLinkID *string `json:"RepositoryLinkId"`
			RoleArn          *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.RepositoryLinkID); id != "" {
			if linkID, ok := linkByID[id]; ok {
				if err := st.UpsertRelationship(r.ID, linkID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc sc→rl: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc sc→role: %w", err)
				}
			}
		}
	}
	return nil
}
