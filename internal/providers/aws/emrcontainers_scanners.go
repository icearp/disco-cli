package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/emrcontainers"
)

func init() {
	registerService(serviceEntry{
		name: "aws:emr-containers",
		fn:   scanEMRContainers,
		emits: []coverage.TypeDecl{
			{Service: "emr-containers", DiscoType: TypeEMRContainersVirtualCluster},
			{Service: "emr-containers", DiscoType: TypeEMRContainersEndpoint},
			{Service: "emr-containers", DiscoType: TypeEMRContainersSecurityConfig, Leaf: true},
		},
	})
}

type emrContainersAPI interface {
	ListVirtualClusters(context.Context, *emrcontainers.ListVirtualClustersInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListVirtualClustersOutput, error)
	ListManagedEndpoints(context.Context, *emrcontainers.ListManagedEndpointsInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListManagedEndpointsOutput, error)
	ListSecurityConfigurations(context.Context, *emrcontainers.ListSecurityConfigurationsInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListSecurityConfigurationsOutput, error)
}

// scanEMRContainers discovers EMR-on-EKS virtual clusters, their managed
// endpoints, and security configurations.
func scanEMRContainers(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := emrcontainers.NewFromConfig(acct.cfg, func(o *emrcontainers.Options) { o.Region = region })

	vcIDs, t, i, ferr := scanEMRCVirtualClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanEMRCEndpoints(ctx, client, vcIDs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanEMRCSecurityConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanEMRCVirtualClusters(ctx context.Context, client emrContainersAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListVirtualClusters(ctx, &emrcontainers.ListVirtualClustersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "emr-containers:ListVirtualClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("emr-containers:ListVirtualClusters: %w", err)
		}
		for _, vc := range out.VirtualClusters {
			arn := sv(vc.Arn)
			id := sv(vc.Id)
			if arn == "" || id == "" {
				continue
			}
			ids = append(ids, id)
			status := string(vc.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRContainersVirtualCluster, NativeID: arn,
				Name: vc.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(vc), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "emr-containers virtual-clusters")
	return ids, t, i, err
}

func scanEMRCEndpoints(ctx context.Context, client emrContainersAPI, vcIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, vcID := range vcIDs {
		id := vcID
		var nextToken *string
		for {
			out, err := client.ListManagedEndpoints(ctx, &emrcontainers.ListManagedEndpointsInput{
				VirtualClusterId: &id,
				NextToken:        nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "emr-containers:ListManagedEndpoints", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("emr-containers:ListManagedEndpoints vc=%s: %w", id, err)
			}
			for _, e := range out.Endpoints {
				arn := sv(e.Arn)
				if arn == "" {
					continue
				}
				status := string(e.State)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeEMRContainersEndpoint, NativeID: arn,
					Name: e.Name, Region: &region, Status: &status,
					AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "emr-containers endpoints")
}

func scanEMRCSecurityConfigs(ctx context.Context, client emrContainersAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSecurityConfigurations(ctx, &emrcontainers.ListSecurityConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "emr-containers:ListSecurityConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("emr-containers:ListSecurityConfigurations: %w", err)
		}
		for _, c := range out.SecurityConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRContainersSecurityConfig, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "emr-containers security-configurations")
}
