package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRedshiftCluster, Service: "redshift", Upstream: "AWS::Redshift::Cluster", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRedshiftSubnetGroup, Service: "redshift", Upstream: "AWS::Redshift::ClusterSubnetGroup"})
	registerType(restype.Descriptor{Type: TypeRedshiftClusterParameterGroup, Service: "redshift", Upstream: "AWS::Redshift::ClusterParameterGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftEndpointAccess, Service: "redshift", Upstream: "AWS::Redshift::EndpointAccess", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftEndpointAuthorization, Service: "redshift", Upstream: "AWS::Redshift::EndpointAuthorization", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftEventSubscription, Service: "redshift", Upstream: "AWS::Redshift::EventSubscription", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftIntegration, Service: "redshift", Upstream: "AWS::Redshift::Integration"})
	registerType(restype.Descriptor{Type: TypeRedshiftScheduledAction, Service: "redshift", Upstream: "AWS::Redshift::ScheduledAction", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshot, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshotSchedule, Service: "redshift", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftSnapshotCopyGrant, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftUsageLimit, Service: "redshift"})
	registerType(restype.Descriptor{Type: TypeRedshiftHsmClientCertificate, Service: "redshift", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftHsmConfiguration, Service: "redshift", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftIdcApplication, Service: "redshift", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRedshiftDatashare, Service: "redshift", Leaf: true})
	registerService(serviceEntry{
		name: "aws:redshift",
		fn:   scanRedshift,
	})
}

// redshiftAPI is the narrow set of Redshift operations called by the
// scanRedshift sub-phases.
type redshiftAPI interface {
	DescribeClusters(context.Context, *redshift.DescribeClustersInput, ...func(*redshift.Options)) (*redshift.DescribeClustersOutput, error)
	DescribeClusterSubnetGroups(context.Context, *redshift.DescribeClusterSubnetGroupsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterSubnetGroupsOutput, error)
	DescribeClusterSnapshots(context.Context, *redshift.DescribeClusterSnapshotsInput, ...func(*redshift.Options)) (*redshift.DescribeClusterSnapshotsOutput, error)
	DescribeSnapshotSchedules(context.Context, *redshift.DescribeSnapshotSchedulesInput, ...func(*redshift.Options)) (*redshift.DescribeSnapshotSchedulesOutput, error)
	DescribeSnapshotCopyGrants(context.Context, *redshift.DescribeSnapshotCopyGrantsInput, ...func(*redshift.Options)) (*redshift.DescribeSnapshotCopyGrantsOutput, error)
	DescribeUsageLimits(context.Context, *redshift.DescribeUsageLimitsInput, ...func(*redshift.Options)) (*redshift.DescribeUsageLimitsOutput, error)
	DescribeHsmClientCertificates(context.Context, *redshift.DescribeHsmClientCertificatesInput, ...func(*redshift.Options)) (*redshift.DescribeHsmClientCertificatesOutput, error)
	DescribeHsmConfigurations(context.Context, *redshift.DescribeHsmConfigurationsInput, ...func(*redshift.Options)) (*redshift.DescribeHsmConfigurationsOutput, error)
	DescribeRedshiftIdcApplications(context.Context, *redshift.DescribeRedshiftIdcApplicationsInput, ...func(*redshift.Options)) (*redshift.DescribeRedshiftIdcApplicationsOutput, error)
	DescribeDataShares(context.Context, *redshift.DescribeDataSharesInput, ...func(*redshift.Options)) (*redshift.DescribeDataSharesOutput, error)
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
		// AWS::Redshift::ClusterSecurityGroup discontinued by AWS — modern
		// Redshift uses VPC SGs already covered via aws:ec2:security-group.
		// DescribeClusterSecurityGroups returns InvalidParameterValue.
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
		func() (int, int, error) { return scanRedshiftSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRedshiftSnapshotSchedules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRedshiftSnapshotCopyGrants(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRedshiftUsageLimits(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanRedshiftHsmClientCertificates(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanRedshiftHsmConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRedshiftIdcApplications(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRedshiftDataShares(ctx, client, acct, region, st, scanID) },
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

func scanRedshiftSnapshots(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeClusterSnapshotsPaginator(client, &redshift.DescribeClusterSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeClusterSnapshots", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeClusterSnapshots: %w", perr)
		}
		for _, s := range out.Snapshots {
			name := sv(s.SnapshotIdentifier)
			nativeID := sv(s.SnapshotArn)
			if nativeID == "" {
				nativeID = redshiftARN(region, acct.ID, "snapshot", sv(s.ClusterIdentifier)+"/"+name)
			}
			status := sv(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshot, NativeID: nativeID, Name: &name,
				Region: &region, CreatedAt: tp(s.SnapshotCreateTime), Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift snapshots")
}

func scanRedshiftSnapshotSchedules(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeSnapshotSchedulesPaginator(client, &redshift.DescribeSnapshotSchedulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeSnapshotSchedules", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeSnapshotSchedules: %w", perr)
		}
		for _, s := range out.SnapshotSchedules {
			name := sv(s.ScheduleIdentifier)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshotSchedule, NativeID: redshiftARN(region, acct.ID, "snapshotschedule", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift snapshot schedules")
}

func scanRedshiftSnapshotCopyGrants(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeSnapshotCopyGrantsPaginator(client, &redshift.DescribeSnapshotCopyGrantsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeSnapshotCopyGrants", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeSnapshotCopyGrants: %w", perr)
		}
		for _, g := range out.SnapshotCopyGrants {
			name := sv(g.SnapshotCopyGrantName)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftSnapshotCopyGrant, NativeID: redshiftARN(region, acct.ID, "snapshotcopygrant", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift snapshot copy grants")
}

func scanRedshiftUsageLimits(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeUsageLimitsPaginator(client, &redshift.DescribeUsageLimitsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeUsageLimits", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeUsageLimits: %w", perr)
		}
		for _, u := range out.UsageLimits {
			name := sv(u.UsageLimitId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftUsageLimit, NativeID: redshiftARN(region, acct.ID, "usagelimit", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift usage limits")
}

func scanRedshiftHsmClientCertificates(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeHsmClientCertificatesPaginator(client, &redshift.DescribeHsmClientCertificatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeHsmClientCertificates", acct.ID, region, perr)
			}
			// HSM encryption is being retired region-by-region (newer regions
			// first); where it's gone the op returns UnsupportedOperation. Region
			// gap — silent-skip. Self-heals as the deprecation rolls out.
			if isAPIErrorCode(perr, "UnsupportedOperation") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeHsmClientCertificates: %w", perr)
		}
		for _, c := range out.HsmClientCertificates {
			name := sv(c.HsmClientCertificateIdentifier)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftHsmClientCertificate, NativeID: redshiftARN(region, acct.ID, "hsmclientcertificate", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift hsm client certificates")
}

func scanRedshiftHsmConfigurations(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeHsmConfigurationsPaginator(client, &redshift.DescribeHsmConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeHsmConfigurations", acct.ID, region, perr)
			}
			// See scanRedshiftHsmClientCertificates — HSM encryption is being
			// retired region-by-region; UnsupportedOperation is a region gap.
			if isAPIErrorCode(perr, "UnsupportedOperation") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("redshift:DescribeHsmConfigurations: %w", perr)
		}
		for _, c := range out.HsmConfigurations {
			name := sv(c.HsmConfigurationIdentifier)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftHsmConfiguration, NativeID: redshiftARN(region, acct.ID, "hsmconfiguration", name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift hsm configurations")
}

func scanRedshiftIdcApplications(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeRedshiftIdcApplicationsPaginator(client, &redshift.DescribeRedshiftIdcApplicationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeRedshiftIdcApplications", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeRedshiftIdcApplications: %w", perr)
		}
		for _, a := range out.RedshiftIdcApplications {
			name := sv(a.RedshiftIdcApplicationName)
			status := sv(a.IdcOnboardStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftIdcApplication, NativeID: sv(a.RedshiftIdcApplicationArn),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift idc applications")
}

func scanRedshiftDataShares(ctx context.Context, client redshiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := redshift.NewDescribeDataSharesPaginator(client, &redshift.DescribeDataSharesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "redshift:DescribeDataShares", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("redshift:DescribeDataShares: %w", perr)
		}
		for _, d := range out.DataShares {
			arn := sv(d.DataShareArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRedshiftDatashare, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "redshift data shares")
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
