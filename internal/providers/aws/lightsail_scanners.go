package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
)

func init() {
	registerService(serviceEntry{
		name: "aws:lightsail",
		fn:   scanLightsail,
		emits: []coverage.TypeDecl{
			{Service: "lightsail", DiscoType: TypeLightsailInstance},
			{Service: "lightsail", DiscoType: TypeLightsailDatabase, Leaf: true},
			{Service: "lightsail", DiscoType: TypeLightsailContainerService, Leaf: true},
			{Service: "lightsail", DiscoType: TypeLightsailAlarm},
			{Service: "lightsail", DiscoType: TypeLightsailBucket, Leaf: true},
			{Service: "lightsail", DiscoType: TypeLightsailCertificate},
			{Service: "lightsail", DiscoType: TypeLightsailDatabaseSnapshot},
			{Service: "lightsail", DiscoType: TypeLightsailDisk},
			{Service: "lightsail", DiscoType: TypeLightsailDiskSnapshot},
			{Service: "lightsail", DiscoType: TypeLightsailDistribution},
			{Service: "lightsail", DiscoType: TypeLightsailDomain, Leaf: true},
			{Service: "lightsail", DiscoType: TypeLightsailInstanceSnapshot},
			{Service: "lightsail", DiscoType: TypeLightsailLoadBalancer},
			{Service: "lightsail", DiscoType: TypeLightsailLoadBalancerTLSCertificate},
			{Service: "lightsail", DiscoType: TypeLightsailStaticIP},
			{Service: "lightsail", DiscoType: TypeLightsailKeyPair, Leaf: true},
			{Service: "lightsail", DiscoType: TypeLightsailContactMethod, Leaf: true},
		},
	})
}

// lightsailAPI is the narrow set of Lightsail operations called by the
// scanLightsail sub-phases.
type lightsailAPI interface {
	GetInstances(context.Context, *lightsail.GetInstancesInput, ...func(*lightsail.Options)) (*lightsail.GetInstancesOutput, error)
	GetRelationalDatabases(context.Context, *lightsail.GetRelationalDatabasesInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabasesOutput, error)
	GetContainerServices(context.Context, *lightsail.GetContainerServicesInput, ...func(*lightsail.Options)) (*lightsail.GetContainerServicesOutput, error)
	GetAlarms(context.Context, *lightsail.GetAlarmsInput, ...func(*lightsail.Options)) (*lightsail.GetAlarmsOutput, error)
	GetBuckets(context.Context, *lightsail.GetBucketsInput, ...func(*lightsail.Options)) (*lightsail.GetBucketsOutput, error)
	GetCertificates(context.Context, *lightsail.GetCertificatesInput, ...func(*lightsail.Options)) (*lightsail.GetCertificatesOutput, error)
	GetRelationalDatabaseSnapshots(context.Context, *lightsail.GetRelationalDatabaseSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabaseSnapshotsOutput, error)
	GetDisks(context.Context, *lightsail.GetDisksInput, ...func(*lightsail.Options)) (*lightsail.GetDisksOutput, error)
	GetDiskSnapshots(context.Context, *lightsail.GetDiskSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetDiskSnapshotsOutput, error)
	GetDistributions(context.Context, *lightsail.GetDistributionsInput, ...func(*lightsail.Options)) (*lightsail.GetDistributionsOutput, error)
	GetDomains(context.Context, *lightsail.GetDomainsInput, ...func(*lightsail.Options)) (*lightsail.GetDomainsOutput, error)
	GetInstanceSnapshots(context.Context, *lightsail.GetInstanceSnapshotsInput, ...func(*lightsail.Options)) (*lightsail.GetInstanceSnapshotsOutput, error)
	GetLoadBalancers(context.Context, *lightsail.GetLoadBalancersInput, ...func(*lightsail.Options)) (*lightsail.GetLoadBalancersOutput, error)
	GetLoadBalancerTlsCertificates(context.Context, *lightsail.GetLoadBalancerTlsCertificatesInput, ...func(*lightsail.Options)) (*lightsail.GetLoadBalancerTlsCertificatesOutput, error)
	GetStaticIps(context.Context, *lightsail.GetStaticIpsInput, ...func(*lightsail.Options)) (*lightsail.GetStaticIpsOutput, error)
	GetKeyPairs(context.Context, *lightsail.GetKeyPairsInput, ...func(*lightsail.Options)) (*lightsail.GetKeyPairsOutput, error)
	GetContactMethods(context.Context, *lightsail.GetContactMethodsInput, ...func(*lightsail.Options)) (*lightsail.GetContactMethodsOutput, error)
}

// scanLightsail discovers Lightsail instances, relational databases, and
// container services in one region. Lightsail uses manual `pageToken`
// pagination (no SDK paginators); three phases run sequentially, each
// tolerating per-phase AccessDenied. Snapshots, disks, key pairs, static IPs,
// distributions, domains, buckets, and load balancers deferred — Lightsail's
// resource graph is largely self-contained per service, adding little
// cross-service edge value.
func scanLightsail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lightsail.NewFromConfig(acct.cfg, func(o *lightsail.Options) { o.Region = region })

	{
		t, i, ferr := scanLightsailInstances(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanLightsailDatabases(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanLightsailContainerServices(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanLightsailExtended(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanLightsailInstances(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var pageToken *string
	for {
		out, perr := client.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "lightsail:GetInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("lightsail:GetInstances: %w", perr)
		}
		for _, i := range out.Instances {
			arn := sv(i.Arn)
			if arn == "" {
				continue
			}
			name := sv(i.Name)
			var status string
			if i.State != nil {
				status = sv(i.State.Name)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLightsailInstance,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(i),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		pageToken = out.NextPageToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert lightsail instances: %w", uerr)
	}
	return len(batch), n, nil
}

func scanLightsailDatabases(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var pageToken *string
	for {
		out, perr := client.GetRelationalDatabases(ctx, &lightsail.GetRelationalDatabasesInput{PageToken: pageToken})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "lightsail:GetRelationalDatabases", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("lightsail:GetRelationalDatabases: %w", perr)
		}
		for _, d := range out.RelationalDatabases {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			status := sv(d.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLightsailDatabase,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		pageToken = out.NextPageToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert lightsail databases: %w", uerr)
	}
	return len(batch), n, nil
}

func scanLightsailContainerServices(ctx context.Context, client lightsailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, perr := client.GetContainerServices(ctx, &lightsail.GetContainerServicesInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			_ = skipIfAccessDenied(st, "lightsail:GetContainerServices", acct.ID, region, perr)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("lightsail:GetContainerServices: %w", perr)
	}
	if len(out.ContainerServices) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(out.ContainerServices))
	for _, c := range out.ContainerServices {
		arn := sv(c.Arn)
		if arn == "" {
			continue
		}
		name := sv(c.ContainerServiceName)
		status := string(c.State)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeLightsailContainerService,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert lightsail container services: %w", uerr)
	}
	return len(batch), n, nil
}
