package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

func init() {
	registerService(serviceEntry{
		name: "aws:redshift",
		fn:   scanRedshift,
		emits: []coverage.TypeDecl{
			{Service: "redshift", DiscoType: TypeRedshiftCluster},
			{Service: "redshift", DiscoType: TypeRedshiftSubnetGroup},
			{Service: "redshift", DiscoType: TypeRedshiftClusterParameterGroup},
			{Service: "redshift", DiscoType: TypeRedshiftClusterSecurityGroup},
			{Service: "redshift", DiscoType: TypeRedshiftEndpointAccess},
			{Service: "redshift", DiscoType: TypeRedshiftEndpointAuthorization},
			{Service: "redshift", DiscoType: TypeRedshiftEventSubscription},
			{Service: "redshift", DiscoType: TypeRedshiftIntegration},
			{Service: "redshift", DiscoType: TypeRedshiftScheduledAction},
		},
	})
}

// redshiftAPI is the narrow set of Redshift operations called by the
// scanRedshift sub-phases.
type redshiftAPI interface {
	DescribeClusters(context.Context, *redshift.DescribeClustersInput, ...func(*redshift.Options)) (*redshift.DescribeClustersOutput, error)
	DescribeClusterSubnetGroups(context.Context, *redshift.DescribeClusterSubnetGroupsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterSubnetGroupsOutput, error)
}

// scanRedshift discovers Redshift provisioned clusters and cluster subnet
// groups in one region. Two phases run sequentially. Each phase is
// paginator-native; List bodies carry edge-bearing fields, no Describe
// fan-out needed. Per-phase AccessDenied tolerated. Cluster parameter
// groups intentionally deferred — no graph edges (config artefacts).
// Redshift Serverless workgroups + namespaces tracked separately
// (different SDK package).
func scanRedshift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := redshift.NewFromConfig(acct.cfg, func(o *redshift.Options) { o.Region = region })

	{
		t, i, ferr := scanRedshiftClusters(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanRedshiftClusterSubnetGroups(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanRedshiftClusterParameterGroups(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanRedshiftClusterSecurityGroups(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanRedshiftEndpointAccess(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanRedshiftEndpointAuthorization(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanRedshiftEventSubscriptions(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanRedshiftIntegrations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanRedshiftScheduledActions(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func redshiftClusterARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:redshift:%s:%s:cluster:%s", region, accountID, name)
}

func redshiftSubnetGroupARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:redshift:%s:%s:subnetgroup:%s", region, accountID, name)
}

func scanRedshiftClusters(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := redshift.NewDescribeClustersPaginator(client, &redshift.DescribeClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeClusters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeClusters: %w", perr)
		}
		for _, c := range out.Clusters {
			name := sv(c.ClusterIdentifier)
			if name == "" {
				continue
			}
			arn := redshiftClusterARN(region, acct.ID, name)
			status := sv(c.ClusterStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeRedshiftCluster,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert redshift clusters: %w", uerr)
	}
	return len(batch), n, nil
}

func scanRedshiftClusterSubnetGroups(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := redshift.NewDescribeClusterSubnetGroupsPaginator(client, &redshift.DescribeClusterSubnetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "redshift:DescribeClusterSubnetGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeClusterSubnetGroups: %w", perr)
		}
		for _, g := range out.ClusterSubnetGroups {
			name := sv(g.ClusterSubnetGroupName)
			if name == "" {
				continue
			}
			arn := redshiftSubnetGroupARN(region, acct.ID, name)
			status := sv(g.SubnetGroupStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeRedshiftSubnetGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert redshift subnet groups: %w", uerr)
	}
	return len(batch), n, nil
}
