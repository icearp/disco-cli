package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutoScalingGroup, Service: "autoscaling", Upstream: "AWS::AutoScaling::AutoScalingGroup"})
	registerType(restype.Descriptor{Type: TypeAutoScalingLaunchConfiguration, Service: "autoscaling", Upstream: "AWS::AutoScaling::LaunchConfiguration"})
	registerType(restype.Descriptor{Type: TypeAutoScalingLifecycleHook, Service: "autoscaling", Upstream: "AWS::AutoScaling::LifecycleHook"})
	registerType(restype.Descriptor{Type: TypeAutoScalingScalingPolicy, Service: "autoscaling", Upstream: "AWS::AutoScaling::ScalingPolicy"})
	registerType(restype.Descriptor{Type: TypeAutoScalingScheduledAction, Service: "autoscaling", Upstream: "AWS::AutoScaling::ScheduledAction"})
	registerType(restype.Descriptor{Type: TypeAutoScalingWarmPool, Service: "autoscaling", Upstream: "AWS::AutoScaling::WarmPool"})
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

// scanAutoScaling discovers EC2 Auto Scaling primitives in one region.
// Phases run sequentially; ASG names are captured during the first phase
// for the per-ASG fan-outs (LifecycleHook, WarmPool) that follow.
func scanAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := autoscaling.NewFromConfig(acct.cfg, func(o *autoscaling.Options) { o.Region = region })

	asgNames, t, i, ferr := scanAutoScalingGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

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
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanAutoScalingGroups paginates DescribeAutoScalingGroups, persists each
// group, and returns the captured ASG name slice for downstream fan-outs.
func scanAutoScalingGroups(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (names []string, total, inserted int, err error) {
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
			names = append(names, name)
			arn := sv(g.AutoScalingGroupARN)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:*:autoScalingGroupName/%s", region, acct.ID, name)
			}
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
	return names, t, i, perr
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
