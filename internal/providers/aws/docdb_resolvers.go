package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDocDBClusterTargets,
		EdgeDecl{TypeDocDBCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDocDBCluster, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveDocDBInstanceCluster,
		EdgeDecl{TypeDocDBCluster, TypeDocDBInstance, store.RelContains},
	)
}

// docdbClusterAttrs mirrors the verbatim DBCluster fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type docdbClusterAttrs struct {
	KmsKeyID          *string `json:"KmsKeyID"`
	VpcSecurityGroups []struct {
		VpcSecurityGroupID *string `json:"VpcSecurityGroupID"`
	} `json:"VpcSecurityGroups"`
}

// resolveDocDBClusterTargets emits cluster outbound edges:
//   - cluster → KMS key (uses)
//   - cluster → security group (uses) per VpcSecurityGroups[]
//
// FK-safe via scanned-SG id set + KMS resolve index. Cluster → subnet
// group + → VPC + → IAM role deferred until subnet groups are scanned
// as their own type (RDS scanner doesn't model `aws:rds:subnet-group`
// either; pattern can be lifted from there in a future iteration).
func resolveDocDBClusterTargets(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeDocDBCluster},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(clusters) == 0 {
		return nil
	}

	sgIDs, err := resourceIDSet(st, acct.ID, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	for _, c := range clusters {
		var attrs docdbClusterAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if c.Region != nil {
			region = *c.Region
		}

		if keyRef := sv(attrs.KmsKeyID); keyRef != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(keyRef, region, acct.ID); ok {
				if err := st.UpsertRelationship(c.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert docdb cluster→kms: %w", err)
				}
			}
		}

		for _, sg := range attrs.VpcSecurityGroups {
			id := sv(sg.VpcSecurityGroupID)
			if id == "" {
				continue
			}
			sgARN := ec2ARN(region, acct.ID, "security-group", id)
			rID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if _, ok := sgIDs[rID]; ok {
				if err := st.UpsertRelationship(c.ID, rID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert docdb cluster→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// docdbInstanceAttrs mirrors the verbatim DBInstance fields used by the
// resolver.
type docdbInstanceAttrs struct {
	DBClusterIdentifier *string `json:"DBClusterIdentifier"`
}

// resolveDocDBInstanceCluster emits instance → cluster (`contains` reverse
// — emitted from cluster side via closure once both are known). Cluster
// is the parent; we wire the closure here rather than during the scanner
// because instance and cluster come from separate List calls and the
// pairing is naturally relational. FK-safe via scanned-cluster id set.
func resolveDocDBInstanceCluster(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeDocDBInstance},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}

	clusters, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeDocDBCluster},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	clusterByName := map[string]string{}
	for _, c := range clusters {
		if c.Name != nil {
			clusterByName[*c.Name] = c.ID
		}
	}
	if len(clusterByName) == 0 {
		return nil
	}

	var pairs [][2]string
	for _, inst := range instances {
		var attrs docdbInstanceAttrs
		if err := json.Unmarshal([]byte(inst.AttributesJSON), &attrs); err != nil {
			continue
		}
		clusterName := sv(attrs.DBClusterIdentifier)
		if clusterName == "" {
			continue
		}
		cID, ok := clusterByName[clusterName]
		if !ok {
			continue
		}
		pairs = append(pairs, [2]string{inst.ID, cID})
	}
	if len(pairs) == 0 {
		return nil
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return fmt.Errorf("closure docdb instance→cluster: %w", err)
	}
	return nil
}
