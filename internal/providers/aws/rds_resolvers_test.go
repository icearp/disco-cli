package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// --- DB Instance ---

// TestResolveRDSInstanceRelationships verifies that a DB instance's VPC, cluster,
// and subnet group are all linked from the nested JSON structure.
func TestResolveRDSInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	dbARN := "arn:aws:rds:us-east-1:123456789012:db:my-db"
	attrsJSON := `{
		"DBClusterIdentifier": "my-cluster",
		"DBSubnetGroup": {
			"VpcId": "vpc-111",
			"DBSubnetGroupArn": "arn:aws:rds:us-east-1:123456789012:subgrp:my-sg"
		}
	}`
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-111"), region, "{}")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster,
		rdsARN(region, acct.ID, "cluster", "my-cluster"), region, "{}")
	sngID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBSubnetGroup,
		"arn:aws:rds:us-east-1:123456789012:subgrp:my-sg", region, "{}")

	if err := resolveRDSInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, dbID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, dbID, clusterID, store.RelAttachedTo)
	assertRelationship(t, rels, dbID, sngID, store.RelAttachedTo)
}

// TestResolveRDSInstanceRelationships_Empty verifies graceful handling when
// DBSubnetGroup and DBClusterIdentifier are absent.
func TestResolveRDSInstanceRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance,
		"arn:aws:rds:us-east-1:123456789012:db:bare-db", "", "{}")

	if err := resolveRDSInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Cluster ---

// TestResolveDBClusterRelationships verifies that a DB cluster is linked to its
// subnet group by constructing the subnet group ARN from the name field.
func TestResolveDBClusterRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	clusterARN := rdsARN(region, acct.ID, "cluster", "my-cluster")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster,
		clusterARN, region, `{"DBSubnetGroup": "my-sg"}`)
	sngID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBSubnetGroup,
		rdsARN(region, acct.ID, "subgrp", "my-sg"), region, "{}")

	if err := resolveDBClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBClusterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, clusterID, sngID, store.RelAttachedTo)
}

// TestResolveDBClusterRelationships_NoSubnetGroup verifies graceful handling
// when the DBSubnetGroup field is absent.
func TestResolveDBClusterRelationships_NoSubnetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster,
		rdsARN("us-east-1", acct.ID, "cluster", "bare-cluster"), "", "{}")

	if err := resolveDBClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Subnet Group ---

// TestResolveDBSubnetGroupRelationships verifies that a DB subnet group is
// linked to its VPC.
func TestResolveDBSubnetGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	sngID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBSubnetGroup,
		rdsARN(region, acct.ID, "subgrp", "my-sg"), region, `{"VpcId": "vpc-222"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-222"), region, "{}")

	if err := resolveDBSubnetGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBSubnetGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(sngID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, sngID, vpcID, store.RelAttachedTo)
}

// TestResolveDBSubnetGroupRelationships_NoVPC verifies graceful handling when
// VpcId is absent.
func TestResolveDBSubnetGroupRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	sngID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBSubnetGroup,
		rdsARN("us-east-1", acct.ID, "subgrp", "bare-sg"), "", "{}")

	if err := resolveDBSubnetGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBSubnetGroupRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sngID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Proxy ---

// TestResolveDBProxyRelationships verifies that a DB proxy is linked to its VPC.
func TestResolveDBProxyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	proxyARN := "arn:aws:rds:us-east-1:123456789012:db-proxy:prx-abc123"
	proxyID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxy,
		proxyARN, region, `{"VpcId": "vpc-333"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-333"), region, "{}")

	if err := resolveDBProxyRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(proxyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, proxyID, vpcID, store.RelAttachedTo)
}

// TestResolveDBProxyRelationships_NoVPC verifies graceful handling when VpcId
// is absent.
func TestResolveDBProxyRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	proxyID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxy,
		"arn:aws:rds:us-east-1:123456789012:db-proxy:prx-bare", "", "{}")

	if err := resolveDBProxyRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(proxyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Proxy Endpoint ---

// TestResolveDBProxyEndpointRelationships verifies that a DB proxy endpoint is
// linked to its parent proxy by name lookup.
func TestResolveDBProxyEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	proxyARN := "arn:aws:rds:us-east-1:123456789012:db-proxy:prx-ep-test"
	proxyID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxy,
		proxyARN, region, `{"DBProxyName": "my-proxy"}`)
	epARN := "arn:aws:rds:us-east-1:123456789012:db-proxy-endpoint:prx-ep-abc"
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxyEndpoint,
		epARN, region, `{"DBProxyName": "my-proxy"}`)

	if err := resolveDBProxyEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyEndpointRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, epID, proxyID, store.RelAttachedTo)
}

// TestResolveDBProxyEndpointRelationships_NoProxy verifies graceful handling
// when no matching proxy exists.
func TestResolveDBProxyEndpointRelationships_NoProxy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	epID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxyEndpoint,
		"arn:aws:rds:us-east-1:123456789012:db-proxy-endpoint:prx-ep-bare",
		"", `{"DBProxyName": "nonexistent-proxy"}`)

	if err := resolveDBProxyEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyEndpointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Proxy Target Group ---

// TestResolveDBProxyTargetGroupRelationships verifies that a DB proxy target
// group is linked to its parent proxy by name lookup.
func TestResolveDBProxyTargetGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	proxyARN := "arn:aws:rds:us-east-1:123456789012:db-proxy:prx-tg-test"
	proxyID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxy,
		proxyARN, region, `{"DBProxyName": "my-proxy-tg"}`)
	tgARN := "arn:aws:rds:us-east-1:123456789012:target-group:prx-tg-abc"
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxyTargetGroup,
		tgARN, region, `{"DBProxyName": "my-proxy-tg"}`)

	if err := resolveDBProxyTargetGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyTargetGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, tgID, proxyID, store.RelAttachedTo)
}

// TestResolveDBProxyTargetGroupRelationships_NoProxy verifies graceful handling
// when no matching proxy exists.
func TestResolveDBProxyTargetGroupRelationships_NoProxy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBProxyTargetGroup,
		"arn:aws:rds:us-east-1:123456789012:target-group:prx-tg-bare",
		"", `{"DBProxyName": "nonexistent"}`)

	if err := resolveDBProxyTargetGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBProxyTargetGroupRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- DB Shard Group ---

// TestResolveDBShardGroupRelationships verifies that a DB shard group is linked
// to its DB cluster.
func TestResolveDBShardGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	sgARN := "arn:aws:rds:us-east-1:123456789012:shard-group:sg-abc"
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBShardGroup,
		sgARN, region, `{"DBClusterIdentifier": "my-limitless-cluster"}`)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster,
		rdsARN(region, acct.ID, "cluster", "my-limitless-cluster"), region, "{}")

	if err := resolveDBShardGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBShardGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, sgID, clusterID, store.RelAttachedTo)
}

// TestResolveDBShardGroupRelationships_NoCluster verifies graceful handling
// when DBClusterIdentifier is absent.
func TestResolveDBShardGroupRelationships_NoCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBShardGroup,
		"arn:aws:rds:us-east-1:123456789012:shard-group:sg-bare", "", "{}")

	if err := resolveDBShardGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBShardGroupRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- Global Cluster ---

// TestResolveGlobalClusterRelationships verifies that a global cluster is linked
// to its member DB clusters, with account extracted from the member ARN.
func TestResolveGlobalClusterRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	memberARN := "arn:aws:rds:us-east-1:123456789012:cluster:regional-cluster"
	gcARN := "arn:aws:rds::123456789012:global-cluster:my-global"
	gcID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSGlobalCluster, gcARN, "", `{
		"GlobalClusterMembers": [{"DBClusterArn": "arn:aws:rds:us-east-1:123456789012:cluster:regional-cluster"}]
	}`)
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster,
		memberARN, region, "{}")

	if err := resolveGlobalClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveGlobalClusterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(gcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, gcID, memberID, store.RelContains)
}

// TestResolveGlobalClusterRelationships_NoMembers verifies graceful handling
// when GlobalClusterMembers is empty.
func TestResolveGlobalClusterRelationships_NoMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	gcID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSGlobalCluster,
		"arn:aws:rds::123456789012:global-cluster:bare-global", "",
		`{"GlobalClusterMembers": []}`)

	if err := resolveGlobalClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveGlobalClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(gcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveRDSInstanceRelationships_KMSAndOptionGroup verifies the KMS and
// option group edges added by the instance resolver.
func TestResolveRDSInstanceRelationships_KMSAndOptionGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	keyARN := "arn:aws:kms:us-east-1:123456789012:key/1234"
	dbARN := "arn:aws:rds:us-east-1:123456789012:db:enc-db"
	attrsJSON := `{
		"KmsKeyId": "` + keyARN + `",
		"OptionGroupMemberships": [{"OptionGroupName":"default:mysql-8-0"}]
	}`
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, region, attrsJSON)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")
	ogARN := rdsARN(region, acct.ID, "og", "default:mysql-8-0")
	ogID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSOptionGroup, ogARN, region, "{}")

	if err := resolveRDSInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, dbID, keyID, store.RelUses)
	assertRelationship(t, rels, dbID, ogID, store.RelUses)
}

// TestResolveRDSInstanceRelationships_ParameterGroup verifies the instance→pg edge.
func TestResolveRDSInstanceRelationships_ParameterGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	dbARN := "arn:aws:rds:us-east-1:123456789012:db:pg-db"
	attrs := `{"DBParameterGroups":[{"DBParameterGroupName":"my-pg"}]}`
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, region, attrs)
	pgARN := rdsARN(region, acct.ID, "pg", "my-pg")
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBParameterGroup, pgARN, region, "{}")

	if err := resolveRDSInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, dbID, pgID, store.RelUses)
}

// TestResolveDBClusterRelationships_ParameterGroup verifies cluster→cluster-pg edge.
func TestResolveDBClusterRelationships_ParameterGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	clusterARN := rdsARN(region, acct.ID, "cluster", "c-pg")
	attrs := `{"DBClusterParameterGroup":"my-cluster-pg"}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, clusterARN, region, attrs)
	pgARN := rdsARN(region, acct.ID, "cluster-pg", "my-cluster-pg")
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBClusterParameterGroup, pgARN, region, "{}")

	if err := resolveDBClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, pgID, store.RelUses)
}

// TestResolveDBClusterRelationships_KMS verifies KMS link for encrypted clusters.
func TestResolveDBClusterRelationships_KMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	keyARN := "arn:aws:kms:us-east-1:123456789012:key/c1"
	clusterARN := rdsARN(region, acct.ID, "cluster", "enc-cluster")
	attrs := `{"KmsKeyId":"` + keyARN + `"}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, clusterARN, region, attrs)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")

	if err := resolveDBClusterRelationships(acct, st); err != nil {
		t.Fatalf("resolveDBClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, keyID, store.RelUses)
}

func TestResolveRDSIntegrationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	srcARN := rdsARN(region, acct.ID, "cluster", "src")
	tgtARN := "arn:aws:redshift:" + region + ":" + acct.ID + ":cluster:tgt"
	keyARN := "arn:aws:kms:" + region + ":" + acct.ID + ":key/k-int"
	intgARN := "arn:aws:rds:" + region + ":" + acct.ID + ":integration:i-1"
	attrs := `{"SourceArn":"` + srcARN + `","TargetArn":"` + tgtARN + `","KMSKeyId":"` + keyARN + `"}`

	intgID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSIntegration, intgARN, region, attrs)
	srcID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, srcARN, region, "{}")
	tgtID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftCluster, tgtARN, region, "{}")
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")

	if err := resolveRDSIntegrationRefs(acct, st); err != nil {
		t.Fatalf("resolveRDSIntegrationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(intgID)
	assertRelationship(t, rels, intgID, srcID, store.RelAttachedTo)
	assertRelationship(t, rels, intgID, tgtID, store.RelAttachedTo)
	assertRelationship(t, rels, intgID, keyID, store.RelUses)
}
