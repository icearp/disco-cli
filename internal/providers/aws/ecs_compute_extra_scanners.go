package aws

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeECSContainerInstance, Service: "ecs", Upstream: "AWS::ecs::container-instance"})
	registerType(restype.Descriptor{Type: TypeECSTask, Service: "ecs"})
	registerType(restype.Descriptor{Type: TypeECSDaemon, Service: "ecs", Leaf: true})
	registerType(restype.Descriptor{Type: TypeECSDaemonTaskDefinition, Service: "ecs", Leaf: true})
}

type ecsComputeExtAPI interface {
	ListContainerInstances(context.Context, *ecs.ListContainerInstancesInput, ...func(*ecs.Options)) (*ecs.ListContainerInstancesOutput, error)
	DescribeContainerInstances(context.Context, *ecs.DescribeContainerInstancesInput, ...func(*ecs.Options)) (*ecs.DescribeContainerInstancesOutput, error)
	ListTasks(context.Context, *ecs.ListTasksInput, ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(context.Context, *ecs.DescribeTasksInput, ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	ListDaemons(context.Context, *ecs.ListDaemonsInput, ...func(*ecs.Options)) (*ecs.ListDaemonsOutput, error)
	ListDaemonTaskDefinitions(context.Context, *ecs.ListDaemonTaskDefinitionsInput, ...func(*ecs.Options)) (*ecs.ListDaemonTaskDefinitionsOutput, error)
}

// scanECSComputeExtra discovers per-cluster container instances, running tasks,
// and daemons, plus account-wide daemon task definitions.
func scanECSComputeExtra(ctx context.Context, client ecsComputeExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (total, inserted int, err error) {
	for _, cl := range clusterARNs {
		t, i, ferr := scanECSContainerInstances(ctx, client, acct, region, cl, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanECSTasks(ctx, client, acct, region, cl, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanECSDaemons(ctx, client, acct, region, cl, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	t, i, ferr := scanECSDaemonTaskDefinitions(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanECSContainerInstances(ctx context.Context, client ecsComputeExtAPI, acct *account, region, clusterARN string, st *store.Store, scanID string) (int, int, error) {
	var arns []string
	pager := ecs.NewListContainerInstancesPaginator(client, &ecs.ListContainerInstancesInput{Cluster: &clusterARN})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecs:ListContainerInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:ListContainerInstances: %w", perr)
		}
		arns = append(arns, out.ContainerInstanceArns...)
	}
	var batch []*store.Resource
	for chunk := range slices.Chunk(arns, 100) {
		out, derr := client.DescribeContainerInstances(ctx, &ecs.DescribeContainerInstancesInput{Cluster: &clusterARN, ContainerInstances: chunk})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "ecs:DescribeContainerInstances", acct.ID, region, derr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:DescribeContainerInstances: %w", derr)
		}
		for _, ci := range out.ContainerInstances {
			arn := sv(ci.ContainerInstanceArn)
			if arn == "" {
				continue
			}
			status := sv(ci.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSContainerInstance, NativeID: arn,
				Region: &region, Status: &status,
				TagsJSON: awsTagsJSON(ci.Tags), AttributesJSON: mustJSON(ci), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecs container-instances")
}

func scanECSTasks(ctx context.Context, client ecsComputeExtAPI, acct *account, region, clusterARN string, st *store.Store, scanID string) (int, int, error) {
	var arns []string
	pager := ecs.NewListTasksPaginator(client, &ecs.ListTasksInput{Cluster: &clusterARN})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecs:ListTasks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:ListTasks: %w", perr)
		}
		arns = append(arns, out.TaskArns...)
	}
	var batch []*store.Resource
	for chunk := range slices.Chunk(arns, 100) {
		out, derr := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{Cluster: &clusterARN, Tasks: chunk})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "ecs:DescribeTasks", acct.ID, region, derr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:DescribeTasks: %w", derr)
		}
		for _, tk := range out.Tasks {
			arn := sv(tk.TaskArn)
			if arn == "" {
				continue
			}
			status := sv(tk.LastStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSTask, NativeID: arn,
				Region: &region, Zone: tk.AvailabilityZone, Status: &status,
				TagsJSON: awsTagsJSON(tk.Tags), AttributesJSON: mustJSON(tk), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecs tasks")
}

// scanECSDaemons — ListDaemons has no SDK paginator. Manual NextToken loop.
func scanECSDaemons(ctx context.Context, client ecsComputeExtAPI, acct *account, region, clusterARN string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListDaemons(ctx, &ecs.ListDaemonsInput{ClusterArn: &clusterARN, NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecs:ListDaemons", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:ListDaemons: %w", perr)
		}
		for _, d := range out.DaemonSummariesList {
			arn := sv(d.DaemonArn)
			if arn == "" {
				continue
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSDaemon, NativeID: arn,
				Region: &region, Status: &status, CreatedAt: tp(d.CreatedAt),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "ecs daemons")
}

// scanECSDaemonTaskDefinitions — ListDaemonTaskDefinitions has no SDK paginator.
func scanECSDaemonTaskDefinitions(ctx context.Context, client ecsComputeExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.ListDaemonTaskDefinitions(ctx, &ecs.ListDaemonTaskDefinitionsInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "ecs:ListDaemonTaskDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ecs:ListDaemonTaskDefinitions: %w", perr)
		}
		for _, td := range out.DaemonTaskDefinitions {
			arn := sv(td.Arn)
			if arn == "" {
				continue
			}
			status := string(td.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSDaemonTaskDefinition, NativeID: arn,
				Region: &region, Status: &status, CreatedAt: tp(td.RegisteredAt),
				AttributesJSON: mustJSON(td), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "ecs daemon-task-definitions")
}
