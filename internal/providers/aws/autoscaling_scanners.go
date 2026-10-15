package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutoScalingGroup, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingLaunchConfiguration, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingLifecycleHook, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingNotificationConfiguration, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingScalingPolicy, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingScheduledAction, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingWarmPool, Service: "autoscaling"})
	registerService(serviceEntry{
		name: "aws:autoscaling",
		fn:   scanAutoScaling,
	})
}

// autoScalingAPI is the narrow set of EC2 Auto Scaling operations called by
// the scanAutoScaling sub-phases.
type autoScalingAPI interface {
	DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	DescribeLaunchConfigurations(context.Context, *autoscaling.DescribeLaunchConfigurationsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeLaunchConfigurationsOutput, error)
	DescribeLifecycleHooks(context.Context, *autoscaling.DescribeLifecycleHooksInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeLifecycleHooksOutput, error)
	DescribeNotificationConfigurations(context.Context, *autoscaling.DescribeNotificationConfigurationsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeNotificationConfigurationsOutput, error)
	DescribePolicies(context.Context, *autoscaling.DescribePoliciesInput, ...func(*autoscaling.Options)) (*autoscaling.DescribePoliciesOutput, error)
	DescribeScheduledActions(context.Context, *autoscaling.DescribeScheduledActionsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeScheduledActionsOutput, error)
	DescribeWarmPool(context.Context, *autoscaling.DescribeWarmPoolInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeWarmPoolOutput, error)
}

// autoScalingLifecycleHookARN synthesizes a stable NativeID for a lifecycle
// hook. AWS does not return an ARN on the SDK shape; this format mirrors
// the KMS-grant/Macie-session synth precedent (parent-child path).
func autoScalingLifecycleHookARN(region, accountID, asgName, hookName string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:lifecycle-hook/%s/%s", region, accountID, asgName, hookName)
}

// autoScalingWarmPoolARN synthesizes a NativeID for a warm pool. SDK has no
// ARN on the WarmPoolConfiguration shape, but the warm pool is 1:1 with its
// parent ASG so the ASG name suffices for uniqueness.
func autoScalingWarmPoolARN(region, accountID, asgName string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:warm-pool/%s", region, accountID, asgName)
}

// autoScalingNotificationConfigurationARN synthesizes the NativeID of one
// group's notification configuration for one SNS topic. AWS returns no ARN;
// the form mirrors autoScalingLifecycleHookARN (group name, not group ARN) so
// the row keeps its identity when the group phase fails or misses the group.
func autoScalingNotificationConfigurationARN(region, accountID, asgName, topicARN string) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:notification-configuration/%s/%s", region, accountID, asgName, topicARN)
}

// scanAutoScaling discovers EC2 Auto Scaling primitives in one region.
//
// The older phases stop at their first hard error. The notification
// configuration phase runs after them whatever they returned, so its failure
// costs no older phase its rows and theirs does not skip it. Each side's
// transient error is demoted to a warning, since one transient member would
// make the dispatcher read the whole join as transient; the counts are summed.
func scanAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := autoscaling.NewFromConfig(acct.cfg, func(o *autoscaling.Options) { o.Region = region })
	total, inserted, groupIDs, err := scanAutoScalingOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, nerr := scanAutoScalingNotificationConfigurations(ctx, client, acct, region, st, scanID, groupIDs)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, "autoscaling", acct, region, err),
		demoteTransient(st, "autoscaling:DescribeNotificationConfigurations", acct, region, nerr),
	)
}

// scanAutoScalingOlderPhases runs the phases that predate notification
// configurations sequentially, stopping at the first hard error. ASGs are
// captured during the first phase for the per-ASG fan-outs (LifecycleHook,
// WarmPool) that follow, and returned as name → stored row ID.
func scanAutoScalingOlderPhases(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, groupIDs map[string]string, err error) {
	groupIDs, t, i, ferr := scanAutoScalingGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, groupIDs, ferr
	}
	total += t
	inserted += i
	asgNames := slices.Collect(maps.Keys(groupIDs))

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanAutoScalingLaunchConfigurations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanAutoScalingScalingPolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanAutoScalingScheduledActions(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanAutoScalingLifecycleHooks(ctx, client, acct, region, st, scanID, asgNames)
		},
		func() (int, int, error) {
			return scanAutoScalingWarmPools(ctx, client, acct, region, st, scanID, asgNames)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, groupIDs, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, groupIDs, nil
}

// scanAutoScalingGroups paginates DescribeAutoScalingGroups, persists each
// group, and returns the stored row ID of every group by name for downstream
// fan-outs and hierarchy pairs.
func scanAutoScalingGroups(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (groupIDs map[string]string, total, inserted int, err error) {
	groupIDs = map[string]string{}
	pager := autoscaling.NewDescribeAutoScalingGroupsPaginator(client, &autoscaling.DescribeAutoScalingGroupsInput{})
	t, i, perr := pageScan(ctx, "autoscaling:DescribeAutoScalingGroups", acct, region, st,
		pager.HasMorePages,
		func(c context.Context) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
			return pager.NextPage(c)
		},
		func(o *autoscaling.DescribeAutoScalingGroupsOutput) []astypes.AutoScalingGroup {
			return o.AutoScalingGroups
		},
		func(g astypes.AutoScalingGroup) *store.Resource {
			name := sv(g.AutoScalingGroupName)
			if name == "" {
				return nil
			}
			arn := sv(g.AutoScalingGroupARN)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:*:autoScalingGroupName/%s", region, acct.ID, name)
			}
			groupIDs[name] = store.ResourceID("aws", acct.ID, arn)
			status := sv(g.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(g.CreatedTime),
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			}
		})
	return groupIDs, t, i, perr
}

func scanAutoScalingLaunchConfigurations(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := autoscaling.NewDescribeLaunchConfigurationsPaginator(client, &autoscaling.DescribeLaunchConfigurationsInput{})
	return pageScan(ctx, "autoscaling:DescribeLaunchConfigurations", acct, region, st,
		pager.HasMorePages,
		func(c context.Context) (*autoscaling.DescribeLaunchConfigurationsOutput, error) {
			return pager.NextPage(c)
		},
		func(o *autoscaling.DescribeLaunchConfigurationsOutput) []astypes.LaunchConfiguration {
			return o.LaunchConfigurations
		},
		func(lc astypes.LaunchConfiguration) *store.Resource {
			name := sv(lc.LaunchConfigurationName)
			if name == "" {
				return nil
			}
			arn := sv(lc.LaunchConfigurationARN)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:autoscaling:%s:%s:launchConfiguration:*:launchConfigurationName/%s", region, acct.ID, name)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingLaunchConfiguration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(lc.CreatedTime),
				AttributesJSON: mustJSON(lc),
				DiscoveredBy:   scanID,
			}
		})
}

func scanAutoScalingScalingPolicies(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := autoscaling.NewDescribePoliciesPaginator(client, &autoscaling.DescribePoliciesInput{})
	return pageScan(ctx, "autoscaling:DescribePolicies", acct, region, st,
		pager.HasMorePages,
		func(c context.Context) (*autoscaling.DescribePoliciesOutput, error) { return pager.NextPage(c) },
		func(o *autoscaling.DescribePoliciesOutput) []astypes.ScalingPolicy { return o.ScalingPolicies },
		func(p astypes.ScalingPolicy) *store.Resource {
			arn := sv(p.PolicyARN)
			name := sv(p.PolicyName)
			if arn == "" || name == "" {
				return nil
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingScalingPolicy,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}
		})
}

func scanAutoScalingScheduledActions(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := autoscaling.NewDescribeScheduledActionsPaginator(client, &autoscaling.DescribeScheduledActionsInput{})
	return pageScan(ctx, "autoscaling:DescribeScheduledActions", acct, region, st,
		pager.HasMorePages,
		func(c context.Context) (*autoscaling.DescribeScheduledActionsOutput, error) {
			return pager.NextPage(c)
		},
		func(o *autoscaling.DescribeScheduledActionsOutput) []astypes.ScheduledUpdateGroupAction {
			return o.ScheduledUpdateGroupActions
		},
		func(a astypes.ScheduledUpdateGroupAction) *store.Resource {
			arn := sv(a.ScheduledActionARN)
			name := sv(a.ScheduledActionName)
			if arn == "" || name == "" {
				return nil
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingScheduledAction,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.StartTime),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
		})
}

// scanAutoScalingLifecycleHooks fans out per-ASG calls to DescribeLifecycleHooks
// (no paginator on this op) under fanoutMed concurrency. NativeIDs are
// synthesized via autoScalingLifecycleHookARN (no SDK ARN field).
func scanAutoScalingLifecycleHooks(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string, asgNames []string) (total, inserted int, err error) {
	if len(asgNames) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range asgNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeLifecycleHooks(gctx, &autoscaling.DescribeLifecycleHooksInput{AutoScalingGroupName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("autoscaling:DescribeLifecycleHooks %s: %w", name, derr)
			}
			local := make([]*store.Resource, 0, len(out.LifecycleHooks))
			for _, h := range out.LifecycleHooks {
				hookName := sv(h.LifecycleHookName)
				if hookName == "" {
					continue
				}
				arn := autoScalingLifecycleHookARN(region, acct.ID, name, hookName)
				local = append(local, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAutoScalingLifecycleHook,
					NativeID:       arn,
					Name:           &hookName,
					Region:         &region,
					AttributesJSON: mustJSON(h),
					DiscoveredBy:   scanID,
				})
			}
			if len(local) == 0 {
				return nil
			}
			mu.Lock()
			batch = append(batch, local...)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert autoscaling lifecycle hooks: %w", uerr)
	}
	return len(batch), n, nil
}

// scanAutoScalingWarmPools fans out per-ASG calls to DescribeWarmPool. Most
// ASGs do not have a warm pool — DescribeWarmPool returns an empty
// WarmPoolConfiguration in that case, which is silently skipped.
func scanAutoScalingWarmPools(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string, asgNames []string) (total, inserted int, err error) {
	if len(asgNames) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range asgNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeWarmPool(gctx, &autoscaling.DescribeWarmPoolInput{AutoScalingGroupName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("autoscaling:DescribeWarmPool %s: %w", name, derr)
			}
			if out.WarmPoolConfiguration == nil {
				return nil
			}
			arn := autoScalingWarmPoolARN(region, acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingWarmPool,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out.WarmPoolConfiguration),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert autoscaling warm pools: %w", uerr)
	}
	return len(batch), n, nil
}

// autoScalingNotificationConfiguration is one stored notification
// configuration: every notification type a group sends to one SNS topic. The
// API returns one element per (group, topic, type), but the group+topic pair is
// the unit that is created and deleted together (PutNotificationConfiguration
// replaces the pair's type list; CloudFormation's NotificationConfiguration is
// the same pair), so one row per pair makes a type change a new version of the
// row rather than rows appearing and disappearing. Types are sorted and
// deduplicated because the API promises neither order nor uniqueness across
// pages, and neither may version-split the row.
type autoScalingNotificationConfiguration struct {
	AutoScalingGroupName string   `json:"AutoScalingGroupName"`
	TopicARN             string   `json:"TopicARN"`
	NotificationTypes    []string `json:"NotificationTypes"`
}

// scanAutoScalingNotificationConfigurations lists every group's notification
// configurations with one unfiltered paginated call and stores one row per
// group+topic pair, contained by its group when the group phase listed it.
// A pair is only complete after the last page, so a failed page stores
// nothing: a partial pair would record a type list AWS does not hold.
func scanAutoScalingNotificationConfigurations(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string, groupIDs map[string]string) (int, int, error) {
	const op = "autoscaling:DescribeNotificationConfigurations"
	pager := autoscaling.NewDescribeNotificationConfigurationsPaginator(client, &autoscaling.DescribeNotificationConfigurationsInput{},
		func(o *autoscaling.DescribeNotificationConfigurationsPaginatorOptions) { o.Limit = 100 })
	configs := map[string]*autoScalingNotificationConfiguration{}
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, op, acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, nc := range page.NotificationConfigurations {
			group, topic, kind := sv(nc.AutoScalingGroupName), sv(nc.TopicARN), sv(nc.NotificationType)
			if group == "" || topic == "" || kind == "" {
				continue
			}
			nativeID := autoScalingNotificationConfigurationARN(region, acct.ID, group, topic)
			c, ok := configs[nativeID]
			if !ok {
				c = &autoScalingNotificationConfiguration{AutoScalingGroupName: group, TopicARN: topic}
				configs[nativeID] = c
			}
			c.NotificationTypes = append(c.NotificationTypes, kind)
		}
	}

	batch := make([]*store.Resource, 0, len(configs))
	var pairs [][2]string
	for nativeID, c := range configs {
		slices.Sort(c.NotificationTypes)
		c.NotificationTypes = slices.Compact(c.NotificationTypes)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAutoScalingNotificationConfiguration,
			NativeID:       nativeID,
			Region:         &region,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:   scanID,
		})
		if groupID, ok := groupIDs[c.AutoScalingGroupName]; ok {
			pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, nativeID), groupID})
		}
	}
	total, inserted, err := upsertBatch(st, batch, op)
	if err != nil {
		return 0, 0, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure %s: %w", op, err)
	}
	return total, inserted, nil
}
