package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDMSCertificate, Service: "dms", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDMSDataMigration, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSDataProvider, Service: "dms", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDMSEndpoint, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSEventSubscription, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSInstanceProfile, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSMigrationProject, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSReplicationConfig, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSReplicationInstance, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSReplicationSubnetGroup, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSReplicationTask, Service: "dms"})
	registerService(serviceEntry{
		name: "aws:dms",
		fn:   scanDMS,
	})
}

type dmsAPI interface {
	DescribeCertificates(context.Context, *databasemigrationservice.DescribeCertificatesInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeCertificatesOutput, error)
	DescribeDataMigrations(context.Context, *databasemigrationservice.DescribeDataMigrationsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeDataMigrationsOutput, error)
	DescribeDataProviders(context.Context, *databasemigrationservice.DescribeDataProvidersInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeDataProvidersOutput, error)
	DescribeEndpoints(context.Context, *databasemigrationservice.DescribeEndpointsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeEndpointsOutput, error)
	DescribeEventSubscriptions(context.Context, *databasemigrationservice.DescribeEventSubscriptionsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeEventSubscriptionsOutput, error)
	DescribeInstanceProfiles(context.Context, *databasemigrationservice.DescribeInstanceProfilesInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeInstanceProfilesOutput, error)
	DescribeMigrationProjects(context.Context, *databasemigrationservice.DescribeMigrationProjectsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeMigrationProjectsOutput, error)
	DescribeReplicationConfigs(context.Context, *databasemigrationservice.DescribeReplicationConfigsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeReplicationConfigsOutput, error)
	DescribeReplicationInstances(context.Context, *databasemigrationservice.DescribeReplicationInstancesInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeReplicationInstancesOutput, error)
	DescribeReplicationSubnetGroups(context.Context, *databasemigrationservice.DescribeReplicationSubnetGroupsInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeReplicationSubnetGroupsOutput, error)
	DescribeReplicationTasks(context.Context, *databasemigrationservice.DescribeReplicationTasksInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.DescribeReplicationTasksOutput, error)
}

func scanDMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := databasemigrationservice.NewFromConfig(acct.cfg, func(o *databasemigrationservice.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDMSCertificates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSDataMigrations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSDataProviders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSEndpoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSEventSubscriptions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSInstanceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSMigrationProjects(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSReplicationConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSReplicationInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSReplicationSubnetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDMSReplicationTasks(ctx, client, acct, region, st, scanID) },
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

func scanDMSCertificates(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeCertificatesPaginator(client, &databasemigrationservice.DescribeCertificatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeCertificates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeCertificates: %w", perr)
		}
		for _, c := range out.Certificates {
			arn := sv(c.CertificateArn)
			if arn == "" {
				continue
			}
			label := sv(c.CertificateIdentifier)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSCertificate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms certificates")
}

func scanDMSDataMigrations(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeDataMigrationsPaginator(client, &databasemigrationservice.DescribeDataMigrationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeDataMigrations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeDataMigrations: %w", perr)
		}
		for _, d := range out.DataMigrations {
			arn := sv(d.DataMigrationArn)
			if arn == "" {
				continue
			}
			label := sv(d.DataMigrationName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSDataMigration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms data-migrations")
}

func scanDMSDataProviders(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeDataProvidersPaginator(client, &databasemigrationservice.DescribeDataProvidersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeDataProviders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeDataProviders: %w", perr)
		}
		for _, d := range out.DataProviders {
			arn := sv(d.DataProviderArn)
			if arn == "" {
				continue
			}
			label := sv(d.DataProviderName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSDataProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms data-providers")
}

func scanDMSEndpoints(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeEndpointsPaginator(client, &databasemigrationservice.DescribeEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeEndpoints: %w", perr)
		}
		for _, e := range out.Endpoints {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			label := sv(e.EndpointIdentifier)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSEndpoint, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms endpoints")
}

func scanDMSEventSubscriptions(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// EventSubscription has no AWS-issued ARN field; synthesize per DMS ARN format: arn:aws:dms:{r}:{a}:es:{CustSubscriptionId}.
	pager := databasemigrationservice.NewDescribeEventSubscriptionsPaginator(client, &databasemigrationservice.DescribeEventSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeEventSubscriptions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeEventSubscriptions: %w", perr)
		}
		for _, e := range out.EventSubscriptionsList {
			id := sv(e.CustSubscriptionId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:dms:%s:%s:es:%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSEventSubscription, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms event-subscriptions")
}

func scanDMSInstanceProfiles(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeInstanceProfilesPaginator(client, &databasemigrationservice.DescribeInstanceProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeInstanceProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeInstanceProfiles: %w", perr)
		}
		for _, p := range out.InstanceProfiles {
			arn := sv(p.InstanceProfileArn)
			if arn == "" {
				continue
			}
			label := sv(p.InstanceProfileName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSInstanceProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms instance-profiles")
}

func scanDMSMigrationProjects(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeMigrationProjectsPaginator(client, &databasemigrationservice.DescribeMigrationProjectsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeMigrationProjects", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeMigrationProjects: %w", perr)
		}
		for _, m := range out.MigrationProjects {
			arn := sv(m.MigrationProjectArn)
			if arn == "" {
				continue
			}
			label := sv(m.MigrationProjectName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSMigrationProject, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms migration-projects")
}

func scanDMSReplicationConfigs(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeReplicationConfigsPaginator(client, &databasemigrationservice.DescribeReplicationConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeReplicationConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeReplicationConfigs: %w", perr)
		}
		for _, c := range out.ReplicationConfigs {
			arn := sv(c.ReplicationConfigArn)
			if arn == "" {
				continue
			}
			label := sv(c.ReplicationConfigIdentifier)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSReplicationConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms replication-configs")
}

func scanDMSReplicationInstances(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeReplicationInstancesPaginator(client, &databasemigrationservice.DescribeReplicationInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeReplicationInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeReplicationInstances: %w", perr)
		}
		for _, r := range out.ReplicationInstances {
			arn := sv(r.ReplicationInstanceArn)
			if arn == "" {
				continue
			}
			label := sv(r.ReplicationInstanceIdentifier)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSReplicationInstance, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms replication-instances")
}

func scanDMSReplicationSubnetGroups(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// ReplicationSubnetGroup has no ARN field; synthesize per DMS ARN format: arn:aws:dms:{r}:{a}:subgrp:{Identifier}.
	pager := databasemigrationservice.NewDescribeReplicationSubnetGroupsPaginator(client, &databasemigrationservice.DescribeReplicationSubnetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeReplicationSubnetGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeReplicationSubnetGroups: %w", perr)
		}
		for _, g := range out.ReplicationSubnetGroups {
			id := sv(g.ReplicationSubnetGroupIdentifier)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:dms:%s:%s:subgrp:%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSReplicationSubnetGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms replication-subnet-groups")
}

func scanDMSReplicationTasks(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databasemigrationservice.NewDescribeReplicationTasksPaginator(client, &databasemigrationservice.DescribeReplicationTasksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "dms:DescribeReplicationTasks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("dms:DescribeReplicationTasks: %w", perr)
		}
		for _, t := range out.ReplicationTasks {
			arn := sv(t.ReplicationTaskArn)
			if arn == "" {
				continue
			}
			label := sv(t.ReplicationTaskIdentifier)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDMSReplicationTask, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dms replication-tasks")
}
