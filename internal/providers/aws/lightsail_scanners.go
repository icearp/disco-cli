package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
)

func init() {
	registerService(serviceEntry{
		name: "aws:lightsail",
		fn:   scanLightsail,
		emits: []coverage.TypeDecl{
			{Service: "lightsail", DiscoType: TypeLightsailInstance},
			{Service: "lightsail", DiscoType: TypeLightsailDatabase},
			{Service: "lightsail", DiscoType: TypeLightsailContainerService},
		},
	})
}

// lightsailAPI is the narrow set of Lightsail operations called by the
// scanLightsail sub-phases.
type lightsailAPI interface {
	GetInstances(context.Context, *lightsail.GetInstancesInput, ...func(*lightsail.Options)) (*lightsail.GetInstancesOutput, error)
	GetRelationalDatabases(context.Context, *lightsail.GetRelationalDatabasesInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabasesOutput, error)
	GetContainerServices(context.Context, *lightsail.GetContainerServicesInput, ...func(*lightsail.Options)) (*lightsail.GetContainerServicesOutput, error)
}

// scanLightsail discovers Lightsail instances, relational databases, and
// container services in one region. Lightsail uses manual `pageToken`
// pagination (no SDK paginators). Three phases run sequentially. Per-
// phase AccessDenied tolerated. Snapshots, disks, key pairs, static
// IPs, distributions, domains, buckets, and load balancers deferred —
// Lightsail's resource graph is largely self-contained per service,
// adding little cross-service edge value.
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
