package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveKafkaRelationships,
		EdgeDecl{TypeMSKCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeMSKCluster, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeMSKCluster, TypeKMSKey, store.RelUses},
	)
}

// kafkaClusterAttrs is a minimal view of types.Cluster covering the fields
// that carry edges to other scanned resources. Both Provisioned and Serverless
// cluster types are handled — one branch is populated per cluster.
type kafkaClusterAttrs struct {
	Provisioned *struct {
		BrokerNodeGroupInfo *struct {
			ClientSubnets  []string `json:"ClientSubnets"`
			SecurityGroups []string `json:"SecurityGroups"`
		} `json:"BrokerNodeGroupInfo"`
		EncryptionInfo *struct {
			EncryptionAtRest *struct {
				DataVolumeKMSKeyID *string `json:"DataVolumeKMSKeyID"`
			} `json:"EncryptionAtRest"`
		} `json:"EncryptionInfo"`
	} `json:"Provisioned"`
	Serverless *struct {
		VpcConfigs []struct {
			SubnetIDs        []string `json:"SubnetIDs"`
			SecurityGroupIDs []string `json:"SecurityGroupIDs"`
		} `json:"VpcConfigs"`
	} `json:"Serverless"`
}

// resolveKafkaRelationships emits cluster → subnet (attached-to), cluster →
// security-group (uses), and cluster → KMS key (uses) edges from MSK cluster
// attributes. Edges are FK-safe: targets missing from the store are silently
// skipped rather than blowing the FK constraint.
func resolveKafkaRelationships(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeMSKCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(clusters) == 0 {
		return nil
	}

	known, err := kafkaTargetIDSet(acct, st)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	for _, c := range clusters {
		var attrs kafkaClusterAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(c.Region)

		var subnets, sgs []string
		var kmsRef string
		switch {
		case attrs.Provisioned != nil:
			if bng := attrs.Provisioned.BrokerNodeGroupInfo; bng != nil {
				subnets = bng.ClientSubnets
				sgs = bng.SecurityGroups
			}
			if ei := attrs.Provisioned.EncryptionInfo; ei != nil && ei.EncryptionAtRest != nil {
				kmsRef = sv(ei.EncryptionAtRest.DataVolumeKMSKeyID)
			}
		case attrs.Serverless != nil:
			for _, v := range attrs.Serverless.VpcConfigs {
				subnets = append(subnets, v.SubnetIDs...)
				sgs = append(sgs, v.SecurityGroupIDs...)
			}
		}

		for _, id := range subnets {
			if id == "" {
				continue
			}
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if !known[subnetID] {
				continue
			}
			if err := st.UpsertRelationship(c.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert msk-cluster→subnet: %w", err)
			}
		}
		for _, id := range sgs {
			if id == "" {
				continue
			}
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
			if !known[sgID] {
				continue
			}
			if err := st.UpsertRelationship(c.ID, sgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert msk-cluster→security-group: %w", err)
			}
		}
		if keyID, ok := kmsIdx.resolveKMSKeyID(kmsRef, region, acct.ID); ok {
			if err := st.UpsertRelationship(c.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert msk-cluster→kms: %w", err)
			}
		}
	}
	return nil
}

// kafkaTargetIDSet builds a single-lookup set of scanned target resource IDs
// across subnet, security-group, and KMS key types so edge emit is FK-safe.
func kafkaTargetIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeEC2Subnet, TypeEC2SecurityGroup, TypeKMSKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(targets))
	for _, r := range targets {
		m[r.ID] = true
	}
	return m, nil
}
