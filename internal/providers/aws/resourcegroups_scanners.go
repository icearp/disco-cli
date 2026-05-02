package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroups"
)

func init() {
	registerService(serviceEntry{
		name: "aws:resource-groups",
		fn:   scanResourceGroups,
		emits: []coverage.TypeDecl{
			{Service: "resource-groups", DiscoType: TypeResourceGroupsGroup},
			{Service: "resource-groups", DiscoType: TypeResourceGroupsTagSyncTask},
		},
	})
}

type resourceGroupsAPI interface {
	ListGroups(context.Context, *resourcegroups.ListGroupsInput, ...func(*resourcegroups.Options)) (*resourcegroups.ListGroupsOutput, error)
	ListTagSyncTasks(context.Context, *resourcegroups.ListTagSyncTasksInput, ...func(*resourcegroups.Options)) (*resourcegroups.ListTagSyncTasksOutput, error)
}

// scanResourceGroups discovers Resource Groups groups and tag-sync tasks.
func scanResourceGroups(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := resourcegroups.NewFromConfig(acct.cfg, func(o *resourcegroups.Options) { o.Region = region })

	t, i, ferr := scanRGGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanRGTagSyncTasks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanRGGroups(ctx context.Context, client resourceGroupsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListGroups(ctx, &resourcegroups.ListGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resource-groups:ListGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resource-groups:ListGroups: %w", err)
		}
		for _, g := range out.GroupIdentifiers {
			arn := sv(g.GroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResourceGroupsGroup, NativeID: arn,
				Name: g.GroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "resource-groups groups")
}

func scanRGTagSyncTasks(ctx context.Context, client resourceGroupsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListTagSyncTasks(ctx, &resourcegroups.ListTagSyncTasksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resource-groups:ListTagSyncTasks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resource-groups:ListTagSyncTasks: %w", err)
		}
		for _, t := range out.TagSyncTasks {
			arn := sv(t.TaskArn)
			if arn == "" {
				continue
			}
			status := string(t.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResourceGroupsTagSyncTask, NativeID: arn,
				Name: t.GroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "resource-groups tag-sync-tasks")
}
