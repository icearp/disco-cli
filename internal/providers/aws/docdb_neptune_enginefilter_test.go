package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/docdb"
	docdbtypes "github.com/aws/aws-sdk-go-v2/service/docdb/types"
	"github.com/aws/aws-sdk-go-v2/service/neptune"
	neptunetypes "github.com/aws/aws-sdk-go-v2/service/neptune/types"
	"github.com/icearp/disco-cli/store"
)

// The DocDB and Neptune control-plane endpoints share the RDS ARN namespace, so
// DescribeDBInstances / DescribeDBParameterGroups on either returns the whole
// account's RDS-family fleet. Resource identity excludes type, so re-reporting a
// plain-Postgres RDS resource under an aws:docdb:* / aws:neptune:* type collides
// with its aws:rds:* row. These tests pin the include-filter that keeps each
// scanner to its own engine/family.

// stubDocDB satisfies docdbAPI; only DescribeDBInstances returns data.
type stubDocDB struct {
	instances []docdbtypes.DBInstance
}

func (s *stubDocDB) DescribeDBClusters(context.Context, *docdb.DescribeDBClustersInput, ...func(*docdb.Options)) (*docdb.DescribeDBClustersOutput, error) {
	return &docdb.DescribeDBClustersOutput{}, nil
}

func (s *stubDocDB) DescribeDBInstances(context.Context, *docdb.DescribeDBInstancesInput, ...func(*docdb.Options)) (*docdb.DescribeDBInstancesOutput, error) {
	return &docdb.DescribeDBInstancesOutput{DBInstances: s.instances}, nil
}

func (s *stubDocDB) DescribeDBClusterParameterGroups(context.Context, *docdb.DescribeDBClusterParameterGroupsInput, ...func(*docdb.Options)) (*docdb.DescribeDBClusterParameterGroupsOutput, error) {
	return &docdb.DescribeDBClusterParameterGroupsOutput{}, nil
}

func (s *stubDocDB) DescribeEventSubscriptions(context.Context, *docdb.DescribeEventSubscriptionsInput, ...func(*docdb.Options)) (*docdb.DescribeEventSubscriptionsOutput, error) {
	return &docdb.DescribeEventSubscriptionsOutput{}, nil
}

func (s *stubDocDB) DescribeGlobalClusters(context.Context, *docdb.DescribeGlobalClustersInput, ...func(*docdb.Options)) (*docdb.DescribeGlobalClustersOutput, error) {
	return &docdb.DescribeGlobalClustersOutput{}, nil
}

func TestScanDocDBInstances_FiltersNonDocDBEngine(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	docdbARN := "arn:aws:rds:" + region + ":" + testAccountID + ":db:real-docdb"
	pgARN := "arn:aws:rds:" + region + ":" + testAccountID + ":db:plain-postgres"
	docdbEngine, pgEngine := "docdb", "postgres"
	docdbID, pgID := "real-docdb", "plain-postgres"
	stub := &stubDocDB{instances: []docdbtypes.DBInstance{
		{DBInstanceArn: &docdbARN, DBInstanceIdentifier: &docdbID, Engine: &docdbEngine},
		{DBInstanceArn: &pgARN, DBInstanceIdentifier: &pgID, Engine: &pgEngine},
	}}

	total, inserted, err := scanDocDBInstances(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanDocDBInstances: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1 — only the docdb-engine instance", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDocDBInstance}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != docdbARN {
		t.Errorf("rows=%+v, want one row with NativeID %s (postgres row must be dropped)", rows, docdbARN)
	}
}

// stubNeptune satisfies neptuneAPI; DescribeDBInstances /
// DescribeDBParameterGroups return canned data, the rest are empty.
type stubNeptune struct {
	instances   []neptunetypes.DBInstance
	paramGroups []neptunetypes.DBParameterGroup
}

func (s *stubNeptune) DescribeDBClusters(context.Context, *neptune.DescribeDBClustersInput, ...func(*neptune.Options)) (*neptune.DescribeDBClustersOutput, error) {
	return &neptune.DescribeDBClustersOutput{}, nil
}

func (s *stubNeptune) DescribeDBInstances(context.Context, *neptune.DescribeDBInstancesInput, ...func(*neptune.Options)) (*neptune.DescribeDBInstancesOutput, error) {
	return &neptune.DescribeDBInstancesOutput{DBInstances: s.instances}, nil
}

func (s *stubNeptune) DescribeDBClusterParameterGroups(context.Context, *neptune.DescribeDBClusterParameterGroupsInput, ...func(*neptune.Options)) (*neptune.DescribeDBClusterParameterGroupsOutput, error) {
	return &neptune.DescribeDBClusterParameterGroupsOutput{}, nil
}

func (s *stubNeptune) DescribeDBParameterGroups(context.Context, *neptune.DescribeDBParameterGroupsInput, ...func(*neptune.Options)) (*neptune.DescribeDBParameterGroupsOutput, error) {
	return &neptune.DescribeDBParameterGroupsOutput{DBParameterGroups: s.paramGroups}, nil
}

func (s *stubNeptune) DescribeEventSubscriptions(context.Context, *neptune.DescribeEventSubscriptionsInput, ...func(*neptune.Options)) (*neptune.DescribeEventSubscriptionsOutput, error) {
	return &neptune.DescribeEventSubscriptionsOutput{}, nil
}

func (s *stubNeptune) DescribeGlobalClusters(context.Context, *neptune.DescribeGlobalClustersInput, ...func(*neptune.Options)) (*neptune.DescribeGlobalClustersOutput, error) {
	return &neptune.DescribeGlobalClustersOutput{}, nil
}

func TestScanNeptuneInstances_FiltersNonNeptuneEngine(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	nepARN := "arn:aws:rds:" + region + ":" + testAccountID + ":db:real-neptune"
	pgARN := "arn:aws:rds:" + region + ":" + testAccountID + ":db:plain-postgres"
	nepEngine, pgEngine := "neptune", "postgres"
	nepID, pgID := "real-neptune", "plain-postgres"
	stub := &stubNeptune{instances: []neptunetypes.DBInstance{
		{DBInstanceArn: &nepARN, DBInstanceIdentifier: &nepID, Engine: &nepEngine},
		{DBInstanceArn: &pgARN, DBInstanceIdentifier: &pgID, Engine: &pgEngine},
	}}

	total, inserted, err := scanNeptuneInstances(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanNeptuneInstances: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1 — only the neptune-engine instance", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeNeptuneInstance}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != nepARN {
		t.Errorf("rows=%+v, want one row with NativeID %s (postgres row must be dropped)", rows, nepARN)
	}
}

func TestScanNeptuneDBPGs_FiltersNonNeptuneFamily(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	nepARN := "arn:aws:rds:" + region + ":" + testAccountID + ":pg:neptune-pg"
	pgARN := "arn:aws:rds:" + region + ":" + testAccountID + ":pg:postgres-pg"
	nepName, pgName := "neptune-pg", "postgres-pg"
	nepFamily, pgFamily := "neptune1.3", "postgres16"
	stub := &stubNeptune{paramGroups: []neptunetypes.DBParameterGroup{
		{DBParameterGroupArn: &nepARN, DBParameterGroupName: &nepName, DBParameterGroupFamily: &nepFamily},
		{DBParameterGroupArn: &pgARN, DBParameterGroupName: &pgName, DBParameterGroupFamily: &pgFamily},
	}}

	total, inserted, err := scanNeptuneDBPGs(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanNeptuneDBPGs: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1 — only the neptune-family group", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeNeptuneDBParameterGroup}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != nepARN {
		t.Errorf("rows=%+v, want one row with NativeID %s (postgres family must be dropped)", rows, nepARN)
	}
}
