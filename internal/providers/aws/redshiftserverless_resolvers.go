package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRSSNamespaceRefs,
		EdgeDecl{TypeRedshiftServerlessNamespace, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeRedshiftServerlessNamespace, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveRSSWorkgroupRefs,
		EdgeDecl{TypeRedshiftServerlessWorkgroup, TypeRedshiftServerlessNamespace, store.RelAttachedTo},
		EdgeDecl{TypeRedshiftServerlessWorkgroup, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeRedshiftServerlessWorkgroup, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveRSSSnapshotRefs,
		EdgeDecl{TypeRedshiftServerlessSnapshot, TypeRedshiftServerlessNamespace, store.RelAttachedTo},
		EdgeDecl{TypeRedshiftServerlessSnapshot, TypeKMSKey, store.RelUses},
	)
}

func rssNamespaceARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:namespace/%s", region, acct, name)
}

// resolveRSSNamespaceRefs wires each namespace to its KMS key and IAM roles.
func resolveRSSNamespaceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRedshiftServerlessNamespace}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID *string  `json:"KmsKeyId"`
			IamRoles []string `json:"IamRoles"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if k := sv(attrs.KmsKeyID); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert rss ns→kms: %w", err)
				}
			}
		}
		for _, role := range attrs.IamRoles {
			if role == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if !roleSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert rss ns→role: %w", err)
			}
		}
	}
	return nil
}

// resolveRSSWorkgroupRefs wires each workgroup to its namespace and VPC
// network attachments (subnets, security groups).
func resolveRSSWorkgroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRedshiftServerlessWorkgroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	nsSet, err := scannedIDSet(acct, st, TypeRedshiftServerlessNamespace)
	if err != nil {
		return err
	}
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			NamespaceName    *string  `json:"NamespaceName"`
			SubnetIDs        []string `json:"SubnetIds"`
			SecurityGroupIDs []string `json:"SecurityGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if n := sv(attrs.NamespaceName); n != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeRedshiftServerlessNamespace, rssNamespaceARN(region, acct.ID, n))
			if nsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rss wg→ns: %w", err)
				}
			}
		}
		for _, sid := range attrs.SubnetIDs {
			if sid == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sid))
			if !subSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rss wg→subnet: %w", err)
			}
		}
		for _, gid := range attrs.SecurityGroupIDs {
			if gid == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", gid))
			if !sgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rss wg→sg: %w", err)
			}
		}
	}
	return nil
}

// resolveRSSSnapshotRefs wires each snapshot to its source namespace and the
// KMS key used to encrypt it.
func resolveRSSSnapshotRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRedshiftServerlessSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	nsSet, err := scannedIDSet(acct, st, TypeRedshiftServerlessNamespace)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			NamespaceName *string `json:"NamespaceName"`
			KmsKeyID      *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if n := sv(attrs.NamespaceName); n != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeRedshiftServerlessNamespace, rssNamespaceARN(region, acct.ID, n))
			if nsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rss snap→ns: %w", err)
				}
			}
		}
		if k := sv(attrs.KmsKeyID); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert rss snap→kms: %w", err)
				}
			}
		}
	}
	return nil
}
