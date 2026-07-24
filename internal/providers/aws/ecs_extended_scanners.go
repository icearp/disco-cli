package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

// isNoTaskSets reports whether err is the InvalidParameterException DescribeTaskSets
// returns for a default rolling-update (ECS) deployment controller service. Only
// EXTERNAL / CODE_DEPLOY services have task sets; others get "TaskSets cannot be
// empty" — a structural fact, not a scanner error, so skip it.
func isNoTaskSets(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidParameterException", "TaskSets cannot be empty")
}

type ecsExtAPI interface {
	DescribeCapacityProviders(context.Context, *ecs.DescribeCapacityProvidersInput, ...func(*ecs.Options)) (*ecs.DescribeCapacityProvidersOutput, error)
	DescribeClusters(context.Context, *ecs.DescribeClustersInput, ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error)
	ListServices(context.Context, *ecs.ListServicesInput, ...func(*ecs.Options)) (*ecs.ListServicesOutput, error)
	DescribeTaskSets(context.Context, *ecs.DescribeTaskSetsInput, ...func(*ecs.Options)) (*ecs.DescribeTaskSetsOutput, error)
}

// scanECSExtended runs three extended phases:
//   - CapacityProviders (account-wide, manual NextToken loop)
//   - ClusterCapacityProviderAssociations (synthesized per cluster from
//     DescribeClusters response)
//   - TaskSets (per-(cluster, service); only EXTERNAL / CODE_DEPLOY services
//     carry task sets — rolling-update (ECS) services reject DescribeTaskSets
//     with "TaskSets cannot be empty", which is skipped).
func scanECSExtended(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (total, inserted int, err error) {
	t, i, ferr := scanECSCapacityProviders(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanECSClusterCapacityProviderAssociations(ctx, client, acct, region, st, scanID, clusterARNs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanECSTaskSets(ctx, client, acct, region, st, scanID, clusterARNs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanECSCapacityProviders(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// SDK exposes no paginator; manual NextToken loop.
	input := &ecs.DescribeCapacityProvidersInput{}
	var batch []*store.Resource
	for {
		out, err := client.DescribeCapacityProviders(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ecs:DescribeCapacityProviders", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:DescribeCapacityProviders: %w", err)
		}
		for _, cp := range out.CapacityProviders {
			arn := sv(cp.CapacityProviderArn)
			if arn == "" {
				continue
			}
			label := sv(cp.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSCapacityProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(cp), DiscoveredBy: scanID,
				// Capacity providers with no Cluster binding (FARGATE / FARGATE_SPOT)
				// are AWS-managed defaults present in every account.
				ManagedByProvider: cp.Cluster == nil,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ecs capacity-providers")
}

func scanECSClusterCapacityProviderAssociations(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (int, int, error) {
	if len(clusterARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	const batchSize = 100
	for i := 0; i < len(clusterARNs); i += batchSize {
		end := i + batchSize
		if end > len(clusterARNs) {
			end = len(clusterARNs)
		}
		out, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: clusterARNs[i:end]})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ecs:DescribeClusters(assoc)", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:DescribeClusters(assoc): %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			if arn == "" {
				continue
			}
			// Synthesized association resource: one per cluster, carries the cluster's
			// CapacityProviders list + DefaultCapacityProviderStrategy as attributes.
			synthArn := arn + "/capacity-provider-associations"
			label := sv(c.ClusterName)
			if label == "" {
				label = arn
			}
			payload := map[string]any{
				"ClusterArn":                      arn,
				"CapacityProviders":               c.CapacityProviders,
				"DefaultCapacityProviderStrategy": c.DefaultCapacityProviderStrategy,
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSClusterCapacityProviderAssociations, NativeID: synthArn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(payload), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecs cluster-capacity-provider-associations")
}

func scanECSTaskSets(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (int, int, error) {
	var batch []*store.Resource
	for _, clusterARN := range clusterARNs {
		ca := clusterARN
		// List services in this cluster, then DescribeTaskSets per service.
		pager := ecs.NewListServicesPaginator(client, &ecs.ListServicesInput{Cluster: &ca})
		for pager.HasMorePages() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "ecs:ListServices(task-sets)", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("ecs:ListServices(task-sets): %w", perr)
			}
			for _, svcARN := range page.ServiceArns {
				sa := svcARN
				out, derr := client.DescribeTaskSets(ctx, &ecs.DescribeTaskSetsInput{Cluster: &ca, Service: &sa})
				if derr != nil {
					if isAccessDenied(derr) {
						continue
					}
					if isNoTaskSets(derr) {
						continue
					}
					return 0, 0, fmt.Errorf("ecs:DescribeTaskSets: %w", derr)
				}
				for _, ts := range out.TaskSets {
					arn := sv(ts.TaskSetArn)
					if arn == "" {
						continue
					}
					label := sv(ts.Id)
					if label == "" {
						label = arn
					}
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeECSTaskSet, NativeID: arn,
						Name: &label, Region: &region, AttributesJSON: mustJSON(ts), DiscoveredBy: scanID,
					})
				}
			}
		}
	}
	return upsertBatch(st, batch, "ecs task-sets")
}
