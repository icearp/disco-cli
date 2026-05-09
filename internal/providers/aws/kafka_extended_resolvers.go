package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveMSKChildrenToCluster,
		EdgeDecl{TypeMSKClusterPolicy, TypeMSKCluster, store.RelAttachedTo},
		EdgeDecl{TypeMSKBatchScramSecret, TypeMSKCluster, store.RelAttachedTo},
		EdgeDecl{TypeMSKBatchScramSecret, TypeSecretsManagerSecret, store.RelUses},
	)
	registerResolver(
		resolveMSKVpcConnectionRefs,
		EdgeDecl{TypeMSKVpcConnection, TypeMSKCluster, store.RelAttachedTo},
		EdgeDecl{TypeMSKVpcConnection, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveMSKReplicatorRefs,
		EdgeDecl{TypeMSKReplicator, TypeMSKCluster, store.RelUses},
	)
}

// resolveMSKChildrenToCluster wires cluster-policy and batch-scram-secret to
// their parent cluster via NativeID parent extract; batch-scram-secret also
// references its underlying secretsmanager secret.
func resolveMSKChildrenToCluster(acct *account, st *store.Store) error {
	clSet, err := scannedIDSet(acct, st, TypeMSKCluster)
	if err != nil {
		return err
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}

	emit := func(srcID, tgtType, tgtARN, kind string, set map[string]bool) error {
		if tgtARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
		if !set[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, kind, "directed", nil); err != nil {
			return fmt.Errorf("upsert msk %s→%s: %w", srcID, tgtType, err)
		}
		return nil
	}

	// Cluster-policy: NativeID = `{clusterARN}/cluster-policy`.
	cpRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMSKClusterPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range cpRows {
		parent := strings.TrimSuffix(r.NativeID, "/cluster-policy")
		if parent == r.NativeID {
			continue
		}
		if err := emit(r.ID, TypeMSKCluster, parent, store.RelAttachedTo, clSet); err != nil {
			return err
		}
	}

	// Batch-scram-secret: NativeID = `{clusterARN}/batch-scram-secret/{secretARN}`.
	bsRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMSKBatchScramSecret}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	const seg = "/batch-scram-secret/"
	for _, r := range bsRows {
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		clusterARN := r.NativeID[:i]
		secretARN := r.NativeID[i+len(seg):]
		if err := emit(r.ID, TypeMSKCluster, clusterARN, store.RelAttachedTo, clSet); err != nil {
			return err
		}
		if err := emit(r.ID, TypeSecretsManagerSecret, secretARN, store.RelUses, secretSet); err != nil {
			return err
		}
	}
	return nil
}

// resolveMSKVpcConnectionRefs wires each VPC connection to its target cluster
// (TargetClusterArn) and the VPC it lives in (VpcID).
func resolveMSKVpcConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMSKVpcConnection}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clSet, err := scannedIDSet(acct, st, TypeMSKCluster)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TargetClusterArn *string `json:"TargetClusterArn"`
			VpcID            *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if c := sv(attrs.TargetClusterArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeMSKCluster, c)
			if clSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert msk vpc-conn→cluster: %w", err)
				}
			}
		}
		if vid := sv(attrs.VpcID); vid != "" {
			vpcARN := ec2ARN(sv(r.Region), acct.ID, "vpc", vid)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert msk vpc-conn→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveMSKReplicatorRefs wires each replicator to the MSK clusters it
// connects via KafkaClustersSummary[].AmazonMskCluster.MskClusterArn.
func resolveMSKReplicatorRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMSKReplicator}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clSet, err := scannedIDSet(acct, st, TypeMSKCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KafkaClustersSummary []struct {
				AmazonMskCluster *struct {
					MskClusterArn *string `json:"MskClusterArn"`
				} `json:"AmazonMskCluster"`
			} `json:"KafkaClustersSummary"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, kc := range attrs.KafkaClustersSummary {
			if kc.AmazonMskCluster == nil {
				continue
			}
			arn := sv(kc.AmazonMskCluster.MskClusterArn)
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeMSKCluster, arn)
			if !clSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert msk replicator→cluster: %w", err)
			}
		}
	}
	return nil
}
