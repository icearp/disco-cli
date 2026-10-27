package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDMSCertificate, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSDataMigration, Service: "dms"})
	registerType(restype.Descriptor{Type: TypeDMSDataProvider, Service: "dms"})
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
	ListTagsForResource(context.Context, *databasemigrationservice.ListTagsForResourceInput, ...func(*databasemigrationservice.Options)) (*databasemigrationservice.ListTagsForResourceOutput, error)
}

// dmsPageCap is the MaxRecords maximum ("Constraints: Minimum 20, maximum
// 100") the docs of DescribeEndpoints, DescribeEventSubscriptions,
// DescribeReplicationInstances, DescribeReplicationSubnetGroups and
// DescribeReplicationTasks state; the model has no range trait. The other
// Describe ops document no maximum, so they are sent none and page at the
// service default.
const dmsPageCap int32 = 100

// dmsTags reads a row's tags with ListTagsForResource, sent the row's ARN
// (its NativeID) as ResourceArn. The op also takes a ResourceArnList; each
// row is read alone instead, so that every error stays attributed to its row:
// how the list form answers when one of its ARNs is missing is not documented,
// and the list has no modeled bound. The op does not page. The Service Reference gives no DMS resource a resource-policy or
// share action, so no listed row is expected to belong to another account.
//
// The op also models InvalidResourceStateFault; it is not known which states
// answer it, so it is returned like any other error and the row kept untagged.
func dmsTags(client dmsAPI) rowTagReader {
	return rowTagReader{
		op:       "dms:ListTagsForResource",
		notFound: []string{"ResourceNotFoundFault"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &databasemigrationservice.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.TagList, func(t dmstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanDMS discovers Database Migration Service resources. Every listing runs
// as its own phase: a phase's error does not stop the later ones; a failed
// write and ctx's end do (runHostPhases). The phases run one at a time, so
// readEachKey's own bound is the bound on the tag reads in flight.
func scanDMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := databasemigrationservice.NewFromConfig(acct.cfg, func(o *databasemigrationservice.Options) { o.Region = region })
	tags := dmsTags(client)
	// Event subscriptions and replication subnet groups carry no ARN, so
	// their tag reads send one built from the Service Reference format, not
	// verified against a live account. The listing just returned the resource,
	// so a not-found answer more likely means that ARN is wrong than that the
	// resource is gone: it keeps the row and is returned as an error rather
	// than dropping every such row silently.
	synthTags := tags
	synthTags.notFound = nil
	return runHostPhases(ctx, st, acct, region, "dms", 0, []hostPhase{
		{op: "dms:DescribeCertificates", run: func(p *phaseTotals) bool {
			return scanDMSCertificates(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeDataMigrations", run: func(p *phaseTotals) bool {
			return scanDMSDataMigrations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeDataProviders", run: func(p *phaseTotals) bool {
			return scanDMSDataProviders(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeEndpoints", run: func(p *phaseTotals) bool {
			return scanDMSEndpoints(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeEventSubscriptions", run: func(p *phaseTotals) bool {
			return scanDMSEventSubscriptions(ctx, client, acct, region, st, scanID, synthTags, p)
		}},
		{op: "dms:DescribeInstanceProfiles", run: func(p *phaseTotals) bool {
			return scanDMSInstanceProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeMigrationProjects", run: func(p *phaseTotals) bool {
			return scanDMSMigrationProjects(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeReplicationConfigs", run: func(p *phaseTotals) bool {
			return scanDMSReplicationConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeReplicationInstances", run: func(p *phaseTotals) bool {
			return scanDMSReplicationInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dms:DescribeReplicationSubnetGroups", run: func(p *phaseTotals) bool {
			return scanDMSReplicationSubnetGroups(ctx, client, acct, region, st, scanID, synthTags, p)
		}},
		{op: "dms:DescribeReplicationTasks", run: func(p *phaseTotals) bool {
			return scanDMSReplicationTasks(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
}

// dmsARNRow is the row of a listed element whose NativeID is its ARN, named
// by name, or by its ARN when it has none. An element without an ARN (the
// model marks every one optional) is not stored; what names the elements in
// the warning.
func dmsARNRow(acct *account, region, scanID, typ, what, arn, name string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(name, "(an element with no identifier)")
	}
	label := cmp.Or(name, arn)
	return dmsRow(acct, region, scanID, typ, arn, label, item), "", ""
}

func dmsRow(acct *account, region, scanID, typ, nativeID, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

func scanDMSCertificates(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeCertificates",
		func(ctx context.Context, marker *string) ([]dmstypes.Certificate, *string, error) {
			out, err := client.DescribeCertificates(ctx, &databasemigrationservice.DescribeCertificatesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.Certificates, out.Marker, nil
		},
		func(c dmstypes.Certificate) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSCertificate, "certificates", sv(c.CertificateArn), sv(c.CertificateIdentifier), c)
		}, tags, p)
}

func scanDMSDataMigrations(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeDataMigrations",
		func(ctx context.Context, marker *string) ([]dmstypes.DataMigration, *string, error) {
			out, err := client.DescribeDataMigrations(ctx, &databasemigrationservice.DescribeDataMigrationsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DataMigrations, out.Marker, nil
		},
		func(d dmstypes.DataMigration) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSDataMigration, "data migrations", sv(d.DataMigrationArn), sv(d.DataMigrationName), d)
		}, tags, p)
}

func scanDMSDataProviders(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeDataProviders",
		func(ctx context.Context, marker *string) ([]dmstypes.DataProvider, *string, error) {
			out, err := client.DescribeDataProviders(ctx, &databasemigrationservice.DescribeDataProvidersInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DataProviders, out.Marker, nil
		},
		func(d dmstypes.DataProvider) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSDataProvider, "data providers", sv(d.DataProviderArn), sv(d.DataProviderName), d)
		}, tags, p)
}

func scanDMSEndpoints(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeEndpoints",
		func(ctx context.Context, marker *string) ([]dmstypes.Endpoint, *string, error) {
			out, err := client.DescribeEndpoints(ctx, &databasemigrationservice.DescribeEndpointsInput{Marker: marker, MaxRecords: sdkaws.Int32(dmsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Endpoints, out.Marker, nil
		},
		func(e dmstypes.Endpoint) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSEndpoint, "endpoints", sv(e.EndpointArn), sv(e.EndpointIdentifier), e)
		}, tags, p)
}

// scanDMSEventSubscriptions stores event subscriptions, whose element carries
// no ARN; NativeID is the DMS ARN format the Service Reference gives them,
// arn:{p}:dms:{r}:{a}:es:{CustSubscriptionId}, and that ARN is what the tag
// read sends.
func scanDMSEventSubscriptions(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeEventSubscriptions",
		func(ctx context.Context, marker *string) ([]dmstypes.EventSubscription, *string, error) {
			out, err := client.DescribeEventSubscriptions(ctx, &databasemigrationservice.DescribeEventSubscriptionsInput{Marker: marker, MaxRecords: sdkaws.Int32(dmsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.EventSubscriptionsList, out.Marker, nil
		},
		func(e dmstypes.EventSubscription) (*store.Resource, string, string) {
			id := sv(e.CustSubscriptionId)
			if id == "" {
				return nil, "event subscriptions have no CustSubscriptionId", cmp.Or(sv(e.SnsTopicArn), "(an element with no SNS topic)")
			}
			return dmsRow(acct, region, scanID, TypeDMSEventSubscription, arnFor(acct.partition(), "dms", region, acct.ID, "es:"+id), id, e), "", ""
		}, tags, p)
}

func scanDMSInstanceProfiles(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeInstanceProfiles",
		func(ctx context.Context, marker *string) ([]dmstypes.InstanceProfile, *string, error) {
			out, err := client.DescribeInstanceProfiles(ctx, &databasemigrationservice.DescribeInstanceProfilesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.InstanceProfiles, out.Marker, nil
		},
		func(ip dmstypes.InstanceProfile) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSInstanceProfile, "instance profiles", sv(ip.InstanceProfileArn), sv(ip.InstanceProfileName), ip)
		}, tags, p)
}

func scanDMSMigrationProjects(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeMigrationProjects",
		func(ctx context.Context, marker *string) ([]dmstypes.MigrationProject, *string, error) {
			out, err := client.DescribeMigrationProjects(ctx, &databasemigrationservice.DescribeMigrationProjectsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.MigrationProjects, out.Marker, nil
		},
		func(m dmstypes.MigrationProject) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSMigrationProject, "migration projects", sv(m.MigrationProjectArn), sv(m.MigrationProjectName), m)
		}, tags, p)
}

func scanDMSReplicationConfigs(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeReplicationConfigs",
		func(ctx context.Context, marker *string) ([]dmstypes.ReplicationConfig, *string, error) {
			out, err := client.DescribeReplicationConfigs(ctx, &databasemigrationservice.DescribeReplicationConfigsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.ReplicationConfigs, out.Marker, nil
		},
		func(c dmstypes.ReplicationConfig) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSReplicationConfig, "replication configs", sv(c.ReplicationConfigArn), sv(c.ReplicationConfigIdentifier), c)
		}, tags, p)
}

func scanDMSReplicationInstances(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeReplicationInstances",
		func(ctx context.Context, marker *string) ([]dmstypes.ReplicationInstance, *string, error) {
			out, err := client.DescribeReplicationInstances(ctx, &databasemigrationservice.DescribeReplicationInstancesInput{Marker: marker, MaxRecords: sdkaws.Int32(dmsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReplicationInstances, out.Marker, nil
		},
		func(r dmstypes.ReplicationInstance) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSReplicationInstance, "replication instances", sv(r.ReplicationInstanceArn), sv(r.ReplicationInstanceIdentifier), r)
		}, tags, p)
}

// scanDMSReplicationSubnetGroups stores replication subnet groups, whose
// element carries no ARN; NativeID is the DMS ARN format the Service Reference
// gives them, arn:{p}:dms:{r}:{a}:subgrp:{Identifier}, and that ARN is what the
// tag read sends.
func scanDMSReplicationSubnetGroups(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeReplicationSubnetGroups",
		func(ctx context.Context, marker *string) ([]dmstypes.ReplicationSubnetGroup, *string, error) {
			out, err := client.DescribeReplicationSubnetGroups(ctx, &databasemigrationservice.DescribeReplicationSubnetGroupsInput{Marker: marker, MaxRecords: sdkaws.Int32(dmsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReplicationSubnetGroups, out.Marker, nil
		},
		func(g dmstypes.ReplicationSubnetGroup) (*store.Resource, string, string) {
			id := sv(g.ReplicationSubnetGroupIdentifier)
			if id == "" {
				return nil, "replication subnet groups have no ReplicationSubnetGroupIdentifier", cmp.Or(sv(g.VpcId), "(an element with no VPC)")
			}
			return dmsRow(acct, region, scanID, TypeDMSReplicationSubnetGroup, dmsReplicationSubnetGroupARNFromName(acct.partition(), region, acct.ID, id), id, g), "", ""
		}, tags, p)
}

func scanDMSReplicationTasks(ctx context.Context, client dmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "dms:DescribeReplicationTasks",
		func(ctx context.Context, marker *string) ([]dmstypes.ReplicationTask, *string, error) {
			out, err := client.DescribeReplicationTasks(ctx, &databasemigrationservice.DescribeReplicationTasksInput{Marker: marker, MaxRecords: sdkaws.Int32(dmsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ReplicationTasks, out.Marker, nil
		},
		func(t dmstypes.ReplicationTask) (*store.Resource, string, string) {
			return dmsARNRow(acct, region, scanID, TypeDMSReplicationTask, "replication tasks", sv(t.ReplicationTaskArn), sv(t.ReplicationTaskIdentifier), t)
		}, tags, p)
}
