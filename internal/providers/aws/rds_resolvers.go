package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRDSInstanceRelationships,
		EdgeDecl{TypeRDSDBInstance, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeRDSDBInstance, TypeRDSDBCluster, store.RelAttachedTo},
		EdgeDecl{TypeRDSDBInstance, TypeRDSDBSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeRDSDBInstance, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeRDSDBInstance, TypeRDSDBParameterGroup, store.RelUses},
		EdgeDecl{TypeRDSDBInstance, TypeRDSOptionGroup, store.RelUses},
	)
	registerResolver(
		resolveDBClusterRelationships,
		EdgeDecl{TypeRDSDBCluster, TypeRDSDBSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeRDSDBCluster, TypeRDSDBClusterParameterGroup, store.RelUses},
		EdgeDecl{TypeRDSDBCluster, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveDBSubnetGroupRelationships,
		EdgeDecl{TypeRDSDBSubnetGroup, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveDBProxyRelationships,
		EdgeDecl{TypeRDSDBProxy, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveDBProxyEndpointRelationships,
		EdgeDecl{TypeRDSDBProxyEndpoint, TypeRDSDBProxy, store.RelAttachedTo},
	)
	registerResolver(
		resolveDBProxyTargetGroupRelationships,
		EdgeDecl{TypeRDSDBProxyTargetGroup, TypeRDSDBProxy, store.RelAttachedTo},
	)
	registerResolver(
		resolveDBShardGroupRelationships,
		EdgeDecl{TypeRDSDBShardGroup, TypeRDSDBCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlobalClusterRelationships,
		EdgeDecl{TypeRDSGlobalCluster, TypeRDSDBCluster, store.RelContains},
	)
	registerResolver(
		resolveRDSIntegrationRefs,
		EdgeDecl{TypeRDSIntegration, TypeRDSDBCluster, store.RelAttachedTo},
		EdgeDecl{TypeRDSIntegration, TypeRedshiftCluster, store.RelAttachedTo},
		EdgeDecl{TypeRDSIntegration, TypeRedshiftServerlessNamespace, store.RelAttachedTo},
		EdgeDecl{TypeRDSIntegration, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveRDSSnapshotRefs,
		EdgeDecl{TypeRDSSnapshot, TypeRDSDBInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSClusterSnapshotRefs,
		EdgeDecl{TypeRDSClusterSnapshot, TypeRDSDBCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSClusterEndpointRefs,
		EdgeDecl{TypeRDSClusterEndpoint, TypeRDSDBCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSAutoBackupRefs,
		EdgeDecl{TypeRDSAutoBackup, TypeRDSDBInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSClusterAutoBackupRefs,
		EdgeDecl{TypeRDSClusterAutoBackup, TypeRDSDBCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSTenantDatabaseRefs,
		EdgeDecl{TypeRDSTenantDatabase, TypeRDSDBInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveRDSSnapshotTenantDatabaseRefs,
		EdgeDecl{TypeRDSSnapshotTenantDatabase, TypeRDSSnapshot, store.RelAttachedTo},
	)
}

// resolveRDSIntegrationRefs wires zero-ETL integrations to their source
// (RDS / Aurora cluster) + target (Redshift provisioned cluster or Serverless
// namespace) + KMS CMEK. SourceArn / TargetArn dispatch by ARN substring.
func resolveRDSIntegrationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSIntegration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	rdsSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	rsSet, err := scannedIDSet(acct, st, TypeRedshiftCluster)
	if err != nil {
		return err
	}
	rsnsSet, err := scannedIDSet(acct, st, TypeRedshiftServerlessNamespace)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceArn *string `json:"SourceArn"`
			TargetArn *string `json:"TargetArn"`
			KMSKeyID  *string `json:"KMSKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if src := sv(attrs.SourceArn); strings.Contains(src, ":rds:") && strings.Contains(src, ":cluster:") {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBCluster, src)
			if rdsSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-integration→source: %w", err)
				}
			}
		}
		tgtArn := sv(attrs.TargetArn)
		switch {
		case strings.Contains(tgtArn, ":redshift:") && strings.Contains(tgtArn, ":cluster:"):
			tgt := store.ResourceID("aws", acct.ID, TypeRedshiftCluster, tgtArn)
			if rsSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-integration→redshift: %w", err)
				}
			}
		case strings.Contains(tgtArn, ":redshift-serverless:") && strings.Contains(tgtArn, ":namespace/"):
			tgt := store.ResourceID("aws", acct.ID, TypeRedshiftServerlessNamespace, tgtArn)
			if rsnsSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-integration→rs-namespace: %w", err)
				}
			}
		}
		if ref := sv(attrs.KMSKeyID); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-integration→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSInstanceRelationships links each DB instance to its VPC, cluster,
// and subnet group.
func resolveRDSInstanceRelationships(acct *account, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range dbs {
		var attrs struct {
			DBClusterIdentifier *string `json:"DBClusterIdentifier"`
			DBSubnetGroup       *struct {
				VpcID            *string `json:"VpcID"`
				DBSubnetGroupArn *string `json:"DBSubnetGroupArn"`
			} `json:"DBSubnetGroup"`
			KmsKeyID               *string `json:"KmsKeyID"`
			OptionGroupMemberships []struct {
				OptionGroupName *string `json:"OptionGroupName"`
			} `json:"OptionGroupMemberships"`
			DBParameterGroups []struct {
				DBParameterGroupName *string `json:"DBParameterGroupName"`
			} `json:"DBParameterGroups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Instance → VPC (via subnet group)
		if attrs.DBSubnetGroup != nil && attrs.DBSubnetGroup.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.DBSubnetGroup.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→vpc relationship: %w", err)
			}
		}
		// Instance → DB cluster
		if attrs.DBClusterIdentifier != nil {
			clusterID := store.ResourceID("aws", acct.ID, TypeRDSDBCluster,
				rdsARN(region, acct.ID, "cluster", *attrs.DBClusterIdentifier))
			if err := st.UpsertRelationship(r.ID, clusterID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→cluster relationship: %w", err)
			}
		}
		// Instance → DB subnet group
		if attrs.DBSubnetGroup != nil && attrs.DBSubnetGroup.DBSubnetGroupArn != nil {
			sngID := store.ResourceID("aws", acct.ID, TypeRDSDBSubnetGroup, *attrs.DBSubnetGroup.DBSubnetGroupArn)
			if err := st.UpsertRelationship(r.ID, sngID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→subnet-group relationship: %w", err)
			}
		}
		// Instance → KMS key. resolveKMSKeyID handles ARN/alias/bare-id and
		// returns ok=false when the target wasn't scanned.
		if keyID, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyID), region, acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→kms relationship: %w", err)
			}
		}
		// Instance → DB parameter groups
		for _, pg := range attrs.DBParameterGroups {
			if sv(pg.DBParameterGroupName) == "" {
				continue
			}
			pgID := store.ResourceID("aws", acct.ID, TypeRDSDBParameterGroup,
				rdsARN(region, acct.ID, "pg", *pg.DBParameterGroupName))
			if err := st.UpsertRelationship(r.ID, pgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→parameter-group relationship: %w", err)
			}
		}
		// Instance → Option groups
		for _, ogm := range attrs.OptionGroupMemberships {
			if sv(ogm.OptionGroupName) == "" {
				continue
			}
			ogID := store.ResourceID("aws", acct.ID, TypeRDSOptionGroup,
				rdsARN(region, acct.ID, "og", *ogm.OptionGroupName))
			if err := st.UpsertRelationship(r.ID, ogID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds-instance→option-group relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveDBClusterRelationships links each DB cluster to its subnet group.
func resolveDBClusterRelationships(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			DBSubnetGroup           *string `json:"DBSubnetGroup"`
			DBClusterParameterGroup *string `json:"DBClusterParameterGroup"`
			KmsKeyID                *string `json:"KmsKeyID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBSubnetGroup != nil {
			sngID := store.ResourceID("aws", acct.ID, TypeRDSDBSubnetGroup,
				rdsARN(sv(r.Region), acct.ID, "subgrp", *attrs.DBSubnetGroup))
			if err := st.UpsertRelationship(r.ID, sngID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-cluster→subnet-group relationship: %w", err)
			}
		}
		// Cluster → DB cluster parameter group
		if sv(attrs.DBClusterParameterGroup) != "" {
			pgID := store.ResourceID("aws", acct.ID, TypeRDSDBClusterParameterGroup,
				rdsARN(sv(r.Region), acct.ID, "cluster-pg", *attrs.DBClusterParameterGroup))
			if err := st.UpsertRelationship(r.ID, pgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-cluster→cluster-parameter-group relationship: %w", err)
			}
		}
		// Cluster → KMS key. resolveKMSKeyID normalizes ref shape and skips
		// when target was not scanned.
		if keyID, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KmsKeyID), sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-cluster→kms relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveDBSubnetGroupRelationships links each DB subnet group to its VPC.
func resolveDBSubnetGroupRelationships(acct *account, st *store.Store) error {
	sngs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBSubnetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range sngs {
		var attrs struct {
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-subnet-group→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveDBProxyRelationships links each DB proxy to its VPC.
func resolveDBProxyRelationships(acct *account, st *store.Store) error {
	proxies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBProxy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range proxies {
		var attrs struct {
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-proxy→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// buildProxyNameMap loads all DB proxies and returns a name→resource-ID map.
// Used by proxy endpoint and target group resolvers to locate their parent proxy.
func buildProxyNameMap(acct *account, st *store.Store) (map[string]string, error) {
	proxies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBProxy},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(proxies))
	for _, p := range proxies {
		var attrs struct {
			DBProxyName *string `json:"DBProxyName"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBProxyName != nil {
			m[*attrs.DBProxyName] = p.ID
		}
	}
	return m, nil
}

// resolveDBProxyEndpointRelationships links each DB proxy endpoint to its parent proxy.
func resolveDBProxyEndpointRelationships(acct *account, st *store.Store) error {
	eps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBProxyEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(eps) == 0 {
		return nil
	}
	proxyByName, err := buildProxyNameMap(acct, st)
	if err != nil {
		return err
	}
	for _, r := range eps {
		var attrs struct {
			DBProxyName *string `json:"DBProxyName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBProxyName != nil {
			if proxyID, ok := proxyByName[*attrs.DBProxyName]; ok {
				if err := st.UpsertRelationship(r.ID, proxyID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert db-proxy-endpoint→proxy relationship: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDBProxyTargetGroupRelationships links each DB proxy target group to its parent proxy.
func resolveDBProxyTargetGroupRelationships(acct *account, st *store.Store) error {
	tgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBProxyTargetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(tgs) == 0 {
		return nil
	}
	proxyByName, err := buildProxyNameMap(acct, st)
	if err != nil {
		return err
	}
	for _, r := range tgs {
		var attrs struct {
			DBProxyName *string `json:"DBProxyName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBProxyName != nil {
			if proxyID, ok := proxyByName[*attrs.DBProxyName]; ok {
				if err := st.UpsertRelationship(r.ID, proxyID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert db-proxy-target-group→proxy relationship: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDBShardGroupRelationships links each DB shard group to its DB cluster.
func resolveDBShardGroupRelationships(acct *account, st *store.Store) error {
	sgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSDBShardGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range sgs {
		var attrs struct {
			DBClusterIdentifier *string `json:"DBClusterIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBClusterIdentifier != nil {
			clusterID := store.ResourceID("aws", acct.ID, TypeRDSDBCluster,
				rdsARN(sv(r.Region), acct.ID, "cluster", *attrs.DBClusterIdentifier))
			if err := st.UpsertRelationship(r.ID, clusterID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert db-shard-group→cluster relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveRDSSnapshotRefs links each DB snapshot to its source DB instance.
// FK-safe: the source instance may be deleted, so the edge is emitted only when
// the instance is present in the store.
func resolveRDSSnapshotRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeRDSDBInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBInstanceIdentifier *string `json:"DBInstanceIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.DBInstanceIdentifier); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBInstance, rdsARN(sv(r.Region), acct.ID, "db", id))
			if instSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-snapshot→db-instance: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSClusterSnapshotRefs links each DB cluster snapshot to its source
// DB cluster (FK-safe; source cluster may be deleted).
func resolveRDSClusterSnapshotRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSClusterSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBClusterIdentifier *string `json:"DBClusterIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.DBClusterIdentifier); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBCluster, rdsARN(sv(r.Region), acct.ID, "cluster", id))
			if clusterSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-cluster-snapshot→db-cluster: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSClusterEndpointRefs links each custom cluster endpoint to its DB cluster.
func resolveRDSClusterEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSClusterEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBClusterIdentifier *string `json:"DBClusterIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.DBClusterIdentifier); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBCluster, rdsARN(sv(r.Region), acct.ID, "cluster", id))
			if clusterSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-cluster-endpoint→db-cluster: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSAutoBackupRefs links each automated instance backup to its source
// DB instance via the verbatim DBInstanceArn. FK-safe: the instance is often
// deleted (orphaned backups are the common case), so emit only when present.
func resolveRDSAutoBackupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSAutoBackup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeRDSDBInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBInstanceArn *string `json:"DBInstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.DBInstanceArn); arn != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBInstance, arn)
			if instSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-auto-backup→db-instance: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSClusterAutoBackupRefs links each automated cluster backup to its
// source DB cluster via the verbatim DBClusterArn (FK-safe; often orphaned).
func resolveRDSClusterAutoBackupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSClusterAutoBackup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBClusterArn *string `json:"DBClusterArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.DBClusterArn); arn != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBCluster, arn)
			if clusterSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-cluster-auto-backup→db-cluster: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSTenantDatabaseRefs links each tenant database to its host DB instance.
func resolveRDSTenantDatabaseRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSTenantDatabase}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeRDSDBInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBInstanceIdentifier *string `json:"DBInstanceIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.DBInstanceIdentifier); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSDBInstance, rdsARN(sv(r.Region), acct.ID, "db", id))
			if instSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-tenant-database→db-instance: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRDSSnapshotTenantDatabaseRefs links each snapshot tenant database to
// its parent DB snapshot (rebuilt from DBSnapshotIdentifier). FK-safe.
func resolveRDSSnapshotTenantDatabaseRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSSnapshotTenantDatabase}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	snapSet, err := scannedIDSet(acct, st, TypeRDSSnapshot)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DBSnapshotIdentifier *string `json:"DBSnapshotIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.DBSnapshotIdentifier); id != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeRDSSnapshot, rdsARN(sv(r.Region), acct.ID, "snapshot", id))
			if snapSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rds-snapshot-tenant-database→snapshot: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGlobalClusterRelationships links each global cluster to its member DB clusters.
func resolveGlobalClusterRelationships(acct *account, st *store.Store) error {
	gcs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRDSGlobalCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range gcs {
		var attrs struct {
			GlobalClusterMembers []struct {
				DBClusterArn *string `json:"DBClusterArn"`
			} `json:"GlobalClusterMembers"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, member := range attrs.GlobalClusterMembers {
			if member.DBClusterArn == nil {
				continue
			}
			// Extract account ID from the member cluster ARN (index 4 in colon-split).
			memberAcct := acct.ID
			if parts := strings.Split(*member.DBClusterArn, ":"); len(parts) >= 5 {
				memberAcct = parts[4]
			}
			clusterID := store.ResourceID("aws", memberAcct, TypeRDSDBCluster, *member.DBClusterArn)
			if err := st.UpsertRelationship(r.ID, clusterID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert global-cluster→db-cluster relationship: %w", err)
			}
		}
	}
	return nil
}
