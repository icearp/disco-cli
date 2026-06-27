package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveNeptuneClusterTargets,
		EdgeDecl{TypeNeptuneCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeNeptuneCluster, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveNeptuneInstanceCluster,
		EdgeDecl{TypeNeptuneCluster, TypeNeptuneInstance, store.RelContains},
	)
}

// neptuneClusterAttrs mirrors the verbatim DBCluster fields used by
// the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type neptuneClusterAttrs struct {
	KmsKeyID          *string `json:"KmsKeyID"`
	VpcSecurityGroups []struct {
		VpcSecurityGroupID *string `json:"VpcSecurityGroupID"`
	} `json:"VpcSecurityGroups"`
}

// resolveNeptuneClusterTargets emits cluster outbound edges:
//   - cluster → KMS key (uses)
//   - cluster → security group (uses) per VpcSecurityGroups[]
//
// FK-safe via scanned-SG id set + KMS resolve index. Cluster → subnet
// group + → VPC + → IAM role deferred (no aws:neptune:subnet-group
// type yet, mirroring DocumentDB choice).
func resolveNeptuneClusterTargets(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeNeptuneCluster},
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
		var attrs neptuneClusterAttrs
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
					return fmt.Errorf("upsert neptune cluster→kms: %w", err)
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
					return fmt.Errorf("upsert neptune cluster→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// neptuneInstanceAttrs mirrors the verbatim DBInstance fields used by
// the resolver.
type neptuneInstanceAttrs struct {
	DBClusterIdentifier *string `json:"DBClusterIdentifier"`
}

// resolveNeptuneInstanceCluster wires instance → cluster containment via
// hierarchy closure, keyed on DBClusterIdentifier matched against
// scanned clusters' Name. FK-safe via scanned-cluster index.
func resolveNeptuneInstanceCluster(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeNeptuneInstance},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}

	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeNeptuneCluster},
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
		var attrs neptuneInstanceAttrs
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
		return fmt.Errorf("closure neptune instance→cluster: %w", err)
	}
	return nil
}
