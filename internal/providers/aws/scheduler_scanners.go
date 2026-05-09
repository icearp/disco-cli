package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
)

func init() {
	registerService(serviceEntry{
		name: "aws:scheduler",
		fn:   scanScheduler,
		emits: []coverage.TypeDecl{
			{Service: "scheduler", DiscoType: TypeSchedulerSchedule},
			{Service: "scheduler", DiscoType: TypeSchedulerScheduleGroup, Leaf: true},
		},
	})
}

type schedulerAPI interface {
	ListSchedules(context.Context, *scheduler.ListSchedulesInput, ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
	ListScheduleGroups(context.Context, *scheduler.ListScheduleGroupsInput, ...func(*scheduler.Options)) (*scheduler.ListScheduleGroupsOutput, error)
}

// scanScheduler discovers EventBridge Scheduler schedules and schedule groups.
func scanScheduler(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := scheduler.NewFromConfig(acct.cfg, func(o *scheduler.Options) { o.Region = region })

	t, i, ferr := scanSchedulerSchedules(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSchedulerGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSchedulerSchedules(ctx context.Context, client schedulerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSchedules(ctx, &scheduler.ListSchedulesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "scheduler:ListSchedules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("scheduler:ListSchedules: %w", err)
		}
		for _, s := range out.Schedules {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := string(s.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSchedulerSchedule, NativeID: arn,
				Name: s.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "scheduler schedules")
}

func scanSchedulerGroups(ctx context.Context, client schedulerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListScheduleGroups(ctx, &scheduler.ListScheduleGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "scheduler:ListScheduleGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("scheduler:ListScheduleGroups: %w", err)
		}
		for _, g := range out.ScheduleGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			status := string(g.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSchedulerScheduleGroup, NativeID: arn,
				Name: g.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				// Name "default" identifies the AWS-managed default schedule
				// group present in every region.
				ManagedByProvider: sv(g.Name) == "default",
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "scheduler schedule-groups")
}
