package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/redshiftserverless"
)

func init() {
	registerService(serviceEntry{
		name: "aws:redshift-serverless",
		fn:   scanRedshiftServerless,
		emits: []coverage.TypeDecl{
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessNamespace},
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessSnapshot},
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessWorkgroup},
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessEndpointAccess},
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessRecoveryPoint},
			{Service: "redshift-serverless", DiscoType: TypeRedshiftServerlessManagedWorkgroup, Leaf: true},
		},
	})
}

type redshiftServerlessAPI interface {
	ListNamespaces(context.Context, *redshiftserverless.ListNamespacesInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListNamespacesOutput, error)
	ListSnapshots(context.Context, *redshiftserverless.ListSnapshotsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListSnapshotsOutput, error)
	ListWorkgroups(context.Context, *redshiftserverless.ListWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListWorkgroupsOutput, error)
	ListEndpointAccess(context.Context, *redshiftserverless.ListEndpointAccessInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListEndpointAccessOutput, error)
	ListRecoveryPoints(context.Context, *redshiftserverless.ListRecoveryPointsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListRecoveryPointsOutput, error)
	ListManagedWorkgroups(context.Context, *redshiftserverless.ListManagedWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListManagedWorkgroupsOutput, error)
}

// scanRedshiftServerless discovers Redshift Serverless namespaces, snapshots,
// and workgroups. ARNs native on every type.
func scanRedshiftServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := redshiftserverless.NewFromConfig(acct.cfg, func(o *redshiftserverless.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanRSSNamespaces(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSWorkgroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSEndpointAccess(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSRecoveryPoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRSSManagedWorkgroups(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanRSSNamespaces(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListNamespacesPaginator(client, &redshiftserverless.ListNamespacesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListNamespaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListNamespaces: %w", err)
		}
		for _, n := range out.Namespaces {
			arn := sv(n.NamespaceArn)
			if arn == "" {
				continue
			}
			status := string(n.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessNamespace, NativeID: arn,
				Name: n.NamespaceName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless namespaces")
}

func scanRSSSnapshots(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListSnapshotsPaginator(client, &redshiftserverless.ListSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListSnapshots: %w", err)
		}
		for _, s := range out.Snapshots {
			arn := sv(s.SnapshotArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessSnapshot, NativeID: arn,
				Name: s.SnapshotName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless snapshots")
}

func scanRSSWorkgroups(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListWorkgroupsPaginator(client, &redshiftserverless.ListWorkgroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListWorkgroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListWorkgroups: %w", err)
		}
		for _, w := range out.Workgroups {
			arn := sv(w.WorkgroupArn)
			if arn == "" {
				continue
			}
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessWorkgroup, NativeID: arn,
				Name: w.WorkgroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless workgroups")
}

func scanRSSEndpointAccess(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListEndpointAccessPaginator(client, &redshiftserverless.ListEndpointAccessInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListEndpointAccess", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListEndpointAccess: %w", err)
		}
		for _, e := range out.Endpoints {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			status := sv(e.EndpointStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessEndpointAccess, NativeID: arn,
				Name: e.EndpointName, Region: &region, CreatedAt: tp(e.EndpointCreateTime),
				Status: &status, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless endpoint access")
}

func scanRSSRecoveryPoints(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListRecoveryPointsPaginator(client, &redshiftserverless.ListRecoveryPointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListRecoveryPoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListRecoveryPoints: %w", err)
		}
		for _, p := range out.RecoveryPoints {
			id := sv(p.RecoveryPointId)
			nativeID := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:recovery-point/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessRecoveryPoint, NativeID: nativeID,
				Name: &id, Region: &region, CreatedAt: tp(p.RecoveryPointCreateTime),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless recovery points")
}

// scanRSSManagedWorkgroups discovers AWS-managed (Amazon-operated) workgroups,
// e.g. those backing zero-ETL / SageMaker integrations. Leaf + provider-managed.
func scanRSSManagedWorkgroups(ctx context.Context, client redshiftServerlessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshiftserverless.NewListManagedWorkgroupsPaginator(client, &redshiftserverless.ListManagedWorkgroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "redshiftserverless:ListManagedWorkgroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("redshiftserverless:ListManagedWorkgroups: %w", err)
		}
		for _, w := range out.ManagedWorkgroups {
			id := sv(w.ManagedWorkgroupId)
			nativeID := sv(w.SourceArn)
			if nativeID == "" {
				nativeID = fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:managed-workgroup/%s", region, acct.ID, id)
			}
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftServerlessManagedWorkgroup, NativeID: nativeID,
				Name: w.ManagedWorkgroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID, ManagedByProvider: true,
			})
		}
	}
	return upsertBatch(st, batch, "redshiftserverless managed workgroups")
}
