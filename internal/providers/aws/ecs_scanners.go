package aws

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:ecs", fn: scanECS}) }

// scanECS discovers ECS clusters, services, and task definitions in one region.
// Clusters are described first so their ARNs can be used for service listing.
func scanECS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := ecs.NewFromConfig(acct.cfg, func(o *ecs.Options) { o.Region = region })

	clusterARNs, err := scanECSClusters(ctx, client, acct, region, st, scanID)
	if err != nil {
		return err
	}
	if err := scanECSServices(ctx, client, acct, region, clusterARNs, st, scanID); err != nil {
		return err
	}
	return scanECSTaskDefinitions(ctx, client, acct, region, st, scanID)
}

// scanECSClusters pages through ListClusters, batch-describes each page via
// DescribeClusters (max 100 per call), and returns all cluster ARNs for use
// by scanECSServices.
func scanECSClusters(ctx context.Context, client *ecs.Client, acct *account, region string, st *store.Store, scanID string) ([]string, error) {
	var allARNs []string
	pager := ecs.NewListClustersPaginator(client, &ecs.ListClustersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied("ecs:ListClusters", acct.ID, region, err)
			}
			return nil, fmt.Errorf("ecs:ListClusters: %w", err)
		}
		allARNs = append(allARNs, page.ClusterArns...)
	}

	// DescribeClusters accepts up to 100 ARNs per call.
	const batchSize = 100
	for i := 0; i < len(allARNs); i += batchSize {
		end := min(i+batchSize, len(allARNs))
		resp, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: allARNs[i:end],
			Include:  []ecstypes.ClusterField{ecstypes.ClusterFieldTags},
		})
		if err != nil {
			if isAccessDenied(err) {
				return allARNs, nil // proceed with services even if describe is denied
			}
			return nil, fmt.Errorf("ecs:DescribeClusters: %w", err)
		}
		var batch []*store.Resource
		for _, c := range resp.Clusters {
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeECSCluster,
				NativeID:       sv(c.ClusterArn),
				Name:           c.ClusterName,
				Region:         &region,
				Status:         c.Status,
				AttributesJSON: mustJSON(c),
				TagsJSON:       awsTagsJSON(c.Tags),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return nil, fmt.Errorf("upsert ECS clusters: %w", err)
			}
		}
	}
	return allARNs, nil
}

// scanECSServices iterates each cluster ARN, pages through ListServices, and
// batch-describes services via DescribeServices (max 10 per call).
func scanECSServices(ctx context.Context, client *ecs.Client, acct *account, region string, clusterARNs []string, st *store.Store, scanID string) error {
	for _, clusterARN := range clusterARNs {
		var serviceARNs []string
		pager := ecs.NewListServicesPaginator(client, &ecs.ListServicesInput{Cluster: &clusterARN})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					continue
				}
				return fmt.Errorf("ecs:ListServices (cluster %s): %w", clusterARN, err)
			}
			serviceARNs = append(serviceARNs, page.ServiceArns...)
		}

		// DescribeServices accepts up to 10 ARNs per call.
		const batchSize = 10
		for i := 0; i < len(serviceARNs); i += batchSize {
			end := min(i+batchSize, len(serviceARNs))
			resp, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
				Cluster:  &clusterARN,
				Services: serviceARNs[i:end],
				Include:  []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
			})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return fmt.Errorf("ecs:DescribeServices (cluster %s): %w", clusterARN, err)
			}
			var batch []*store.Resource
			for _, svc := range resp.Services {
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeECSService,
					NativeID:       sv(svc.ServiceArn),
					Name:           svc.ServiceName,
					Region:         &region,
					CreatedAt:      tp(svc.CreatedAt),
					Status:         svc.Status,
					AttributesJSON: mustJSON(svc),
					TagsJSON:       awsTagsJSON(svc.Tags),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
			}
			if len(batch) > 0 {
				if err := st.UpsertResources(batch); err != nil {
					return fmt.Errorf("upsert ECS services: %w", err)
				}
			}
		}
	}
	return nil
}

// scanECSTaskDefinitions lists all ACTIVE task definition ARNs and describes
// each concurrently (DescribeTaskDefinition has no batch API).
func scanECSTaskDefinitions(ctx context.Context, client *ecs.Client, acct *account, region string, st *store.Store, scanID string) error {
	var allARNs []string
	pager := ecs.NewListTaskDefinitionsPaginator(client, &ecs.ListTaskDefinitionsInput{
		Status: ecstypes.TaskDefinitionStatusActive,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ecs:ListTaskDefinitions", acct.ID, region, err)
			}
			return fmt.Errorf("ecs:ListTaskDefinitions: %w", err)
		}
		allARNs = append(allARNs, page.TaskDefinitionArns...)
	}

	// Describe each task definition concurrently; collect into a batch.
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range allARNs {
		g.Go(func() error {
			resp, err := client.DescribeTaskDefinition(gctx, &ecs.DescribeTaskDefinitionInput{
				TaskDefinition: &arn,
				Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
			})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("ecs:DescribeTaskDefinition %s: %w", arn, err)
			}
			td := resp.TaskDefinition
			// Use family:revision as the human-readable name.
			name := fmt.Sprintf("%s:%d", sv(td.Family), td.Revision)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeECSTaskDefinition,
				NativeID:       sv(td.TaskDefinitionArn),
				Name:           &name,
				Region:         &region,
				Status:         sp(string(td.Status)),
				AttributesJSON: mustJSON(td),
				TagsJSON:       awsTagsJSON(resp.Tags),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert ECS task definitions: %w", err)
		}
	}
	return nil
}
