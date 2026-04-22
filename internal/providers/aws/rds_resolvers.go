package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveRDSInstanceRelationships)
	registerResolver(resolveDBClusterRelationships)
	registerResolver(resolveDBSubnetGroupRelationships)
	registerResolver(resolveDBProxyRelationships)
	registerResolver(resolveDBProxyEndpointRelationships)
	registerResolver(resolveDBProxyTargetGroupRelationships)
	registerResolver(resolveDBShardGroupRelationships)
	registerResolver(resolveGlobalClusterRelationships)
}

// resolveRDSInstanceRelationships links each DB instance to its VPC, cluster,
// and subnet group.
func resolveRDSInstanceRelationships(acct *account, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range dbs {
		var attrs struct {
			DBClusterIdentifier *string `json:"DBClusterIdentifier"`
			DBSubnetGroup       *struct {
				VpcId            *string `json:"VpcId"`
				DBSubnetGroupArn *string `json:"DBSubnetGroupArn"`
			} `json:"DBSubnetGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Instance → VPC (via subnet group)
		if attrs.DBSubnetGroup != nil && attrs.DBSubnetGroup.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.DBSubnetGroup.VpcId))
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
	}
	return nil
}

// resolveDBClusterRelationships links each DB cluster to its subnet group.
func resolveDBClusterRelationships(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			DBSubnetGroup *string `json:"DBSubnetGroup"`
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
	}
	return nil
}

// resolveDBSubnetGroupRelationships links each DB subnet group to its VPC.
func resolveDBSubnetGroupRelationships(acct *account, st *store.Store) error {
	sngs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBSubnetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range sngs {
		var attrs struct {
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBProxy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range proxies {
		var attrs struct {
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBProxy},
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBProxyEndpoint},
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBProxyTargetGroup},
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSDBShardGroup},
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

// resolveGlobalClusterRelationships links each global cluster to its member DB clusters.
func resolveGlobalClusterRelationships(acct *account, st *store.Store) error {
	gcs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRDSGlobalCluster},
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
