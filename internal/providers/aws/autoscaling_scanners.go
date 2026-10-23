package aws

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutoScalingGroup, Service: "autoscaling"})
	registerType(restype.Descriptor{Type: TypeAutoScalingLaunchConfiguration, Service: "autoscaling", Untaggable: true})
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

const (
	autoScalingGroupsOp               = "autoscaling:DescribeAutoScalingGroups"
	autoScalingLaunchConfigurationsOp = "autoscaling:DescribeLaunchConfigurations"
	autoScalingPoliciesOp             = "autoscaling:DescribePolicies"
	autoScalingScheduledActionsOp     = "autoscaling:DescribeScheduledActions"
	autoScalingLifecycleHooksOp       = "autoscaling:DescribeLifecycleHooks"
	autoScalingWarmPoolOp             = "autoscaling:DescribeWarmPool"
	// autoScalingPageCap is the documented MaxRecords maximum of
	// DescribeAutoScalingGroups, DescribeLaunchConfigurations,
	// DescribePolicies and DescribeScheduledActions.
	autoScalingPageCap = 100
)

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

// autoScalingWarmPoolGroup returns the group name autoScalingWarmPoolARN put
// in nativeID, or "" when nativeID is not a warm pool's.
func autoScalingWarmPoolGroup(nativeID string) string {
	_, group, _ := strings.Cut(nativeID, ":warm-pool/")
	return group
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
// The phases are independent: each runs whatever another returns, except
// after a store write failure. The per-group fan-outs (lifecycle hooks, warm
// pools) and the notification configurations' contains pairs use the groups
// the group phase stored, so a failed group listing leaves them fewer groups.
// Each phase's transient error is demoted to a warning only beside a hard
// error (phaseErrs.join); the counts are summed.
func scanAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := autoscaling.NewFromConfig(acct.cfg, func(o *autoscaling.Options) { o.Region = region })
	return scanAutoScalingAll(ctx, client, acct, region, st, scanID)
}

func scanAutoScalingAll(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	groupIDs, t, i, err := scanAutoScalingGroups(ctx, client, acct, region, st, scanID)
	if phases.addPhase(autoScalingGroupsOp, t, i, err) {
		return phases.result(st, acct, region)
	}
	asgNames := slices.Sorted(maps.Keys(groupIDs))
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{autoScalingLaunchConfigurationsOp, func() (int, int, error) {
			return scanAutoScalingLaunchConfigurations(ctx, client, acct, region, st, scanID)
		}},
		{autoScalingPoliciesOp, func() (int, int, error) {
			return scanAutoScalingScalingPolicies(ctx, client, acct, region, st, scanID)
		}},
		{autoScalingScheduledActionsOp, func() (int, int, error) {
			return scanAutoScalingScheduledActions(ctx, client, acct, region, st, scanID)
		}},
		{autoScalingLifecycleHooksOp, func() (int, int, error) {
			return scanAutoScalingLifecycleHooks(ctx, client, acct, region, st, scanID, asgNames)
		}},
		{autoScalingWarmPoolOp, func() (int, int, error) {
			return scanAutoScalingWarmPools(ctx, client, acct, region, st, scanID, asgNames)
		}},
		{"autoscaling:DescribeNotificationConfigurations", func() (int, int, error) {
			return scanAutoScalingNotificationConfigurations(ctx, client, acct, region, st, scanID, groupIDs)
		}},
	} {
		t, i, err := phase.fn()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// scanAutoScalingGroups lists every group (no name or Filters filter;
// IncludeInstances unset, so instances are included), stores each with the
// Tags its element carries, and returns the stored row ID of every group by
// name for the per-group fan-outs and hierarchy pairs. Groups read before a
// failed page are stored and returned.
func scanAutoScalingGroups(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (groupIDs map[string]string, total, inserted int, err error) {
	groupIDs = map[string]string{}
	total, inserted, err = storeTokenList(ctx, st, acct, region, autoScalingGroupsOp,
		func(ctx context.Context, token *string) ([]astypes.AutoScalingGroup, *string, error) {
			out, err := client.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				MaxRecords: sdkaws.Int32(autoScalingPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AutoScalingGroups, out.NextToken, nil
		},
		func(g astypes.AutoScalingGroup) (*store.Resource, string, string) {
			name := sv(g.AutoScalingGroupName)
			if name == "" {
				return nil, "groups have an empty AutoScalingGroupName", cmp.Or(sv(g.AutoScalingGroupARN), "one without an AutoScalingGroupARN")
			}
			arn := sv(g.AutoScalingGroupARN)
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:*:autoScalingGroupName/%s", region, acct.ID, name)
			}
			groupIDs[name] = store.ResourceID("aws", acct.ID, arn)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         nonEmptyPtr(sv(g.Status)),
				CreatedAt:      tp(g.CreatedTime),
				AttributesJSON: mustJSON(g),
				TagsJSON:       kvTagsJSON(g.Tags, func(t astypes.TagDescription) (*string, *string) { return t.Key, t.Value }),
				DiscoveredBy:   scanID,
			}, "", ""
		})
	return groupIDs, total, inserted, err
}

// scanAutoScalingLaunchConfigurations stores every launch configuration (no
// names filter), those read before a failed page included.
func scanAutoScalingLaunchConfigurations(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, autoScalingLaunchConfigurationsOp,
		func(ctx context.Context, token *string) ([]astypes.LaunchConfiguration, *string, error) {
			out, err := client.DescribeLaunchConfigurations(ctx, &autoscaling.DescribeLaunchConfigurationsInput{
				MaxRecords: sdkaws.Int32(autoScalingPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.LaunchConfigurations, out.NextToken, nil
		},
		func(lc astypes.LaunchConfiguration) (*store.Resource, string, string) {
			name := sv(lc.LaunchConfigurationName)
			if name == "" {
				return nil, "launch configurations have an empty LaunchConfigurationName", cmp.Or(sv(lc.LaunchConfigurationARN), "one without a LaunchConfigurationARN")
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
			}, "", ""
		})
}

// scanAutoScalingScalingPolicies stores every scaling policy (no group,
// name or type filter), those read before a failed page included.
func scanAutoScalingScalingPolicies(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, autoScalingPoliciesOp,
		func(ctx context.Context, token *string) ([]astypes.ScalingPolicy, *string, error) {
			out, err := client.DescribePolicies(ctx, &autoscaling.DescribePoliciesInput{
				MaxRecords: sdkaws.Int32(autoScalingPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ScalingPolicies, out.NextToken, nil
		},
		func(p astypes.ScalingPolicy) (*store.Resource, string, string) {
			arn := sv(p.PolicyARN)
			name := sv(p.PolicyName)
			if arn == "" {
				return nil, "scaling policies have an empty PolicyARN", cmp.Or(name, "one without a PolicyName")
			}
			if name == "" {
				return nil, "scaling policies have an empty PolicyName", arn
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
			}, "", ""
		})
}

// scanAutoScalingScheduledActions stores every scheduled action (no group,
// name or time filter), those read before a failed page included.
func scanAutoScalingScheduledActions(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, autoScalingScheduledActionsOp,
		func(ctx context.Context, token *string) ([]astypes.ScheduledUpdateGroupAction, *string, error) {
			out, err := client.DescribeScheduledActions(ctx, &autoscaling.DescribeScheduledActionsInput{
				MaxRecords: sdkaws.Int32(autoScalingPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ScheduledUpdateGroupActions, out.NextToken, nil
		},
		func(a astypes.ScheduledUpdateGroupAction) (*store.Resource, string, string) {
			arn := sv(a.ScheduledActionARN)
			name := sv(a.ScheduledActionName)
			if arn == "" {
				return nil, "scheduled actions have an empty ScheduledActionARN", cmp.Or(name, "one without a ScheduledActionName")
			}
			if name == "" {
				return nil, "scheduled actions have an empty ScheduledActionName", arn
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
			}, "", ""
		})
}

// scanAutoScalingLifecycleHooks reads each group's lifecycle hooks
// (DescribeLifecycleHooks, which does not paginate) concurrently and stores
// them. NativeIDs are synthesized via autoScalingLifecycleHookARN (no SDK ARN
// field). No group's failure cancels another: a denied group is warned about
// once, and any other error is returned after the other groups' hooks are
// stored.
func scanAutoScalingLifecycleHooks(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string, asgNames []string) (int, int, error) {
	reads, stopped := readEachKey(ctx, asgNames, func(ctx context.Context, group string) autoScalingGroupRead {
		out, err := client.DescribeLifecycleHooks(ctx, &autoscaling.DescribeLifecycleHooksInput{AutoScalingGroupName: &group})
		if err != nil {
			return autoScalingGroupRead{err: err}
		}
		var read autoScalingGroupRead
		for _, h := range out.LifecycleHooks {
			hookName := sv(h.LifecycleHookName)
			if hookName == "" {
				read.nameless++
				continue
			}
			read.rows = append(read.rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingLifecycleHook,
				NativeID:       autoScalingLifecycleHookARN(region, acct.ID, group, hookName),
				Name:           &hookName,
				Region:         &region,
				AttributesJSON: mustJSON(h),
				DiscoveredBy:   scanID,
			})
		}
		return read
	})
	return storeGroupReads(st, acct, region, autoScalingLifecycleHooksOp, asgNames, reads, stopped)
}

// scanAutoScalingWarmPools reads each group's warm pool (DescribeWarmPool)
// concurrently and stores its WarmPoolConfiguration. Most groups have none:
// DescribeWarmPool then returns a nil configuration, and the group has no
// row. The op's NextToken pages only the pool's Instances, which are not
// stored, so one call per group reads the whole configuration. Failures are
// handled as in scanAutoScalingLifecycleHooks.
func scanAutoScalingWarmPools(ctx context.Context, client autoScalingAPI, acct *account, region string, st *store.Store, scanID string, asgNames []string) (int, int, error) {
	reads, stopped := readEachKey(ctx, asgNames, func(ctx context.Context, group string) autoScalingGroupRead {
		out, err := client.DescribeWarmPool(ctx, &autoscaling.DescribeWarmPoolInput{AutoScalingGroupName: &group})
		if err != nil {
			return autoScalingGroupRead{err: err}
		}
		if out.WarmPoolConfiguration == nil {
			return autoScalingGroupRead{}
		}
		return autoScalingGroupRead{rows: []*store.Resource{{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAutoScalingWarmPool,
			NativeID:       autoScalingWarmPoolARN(region, acct.ID, group),
			Name:           &group,
			Region:         &region,
			AttributesJSON: mustJSON(out.WarmPoolConfiguration),
			DiscoveredBy:   scanID,
		}}}
	})
	return storeGroupReads(st, acct, region, autoScalingWarmPoolOp, asgNames, reads, stopped)
}

// autoScalingGroupRead is one group's read in a per-group fan-out: the rows
// built, the items read without a name, and the call's error.
type autoScalingGroupRead struct {
	rows     []*store.Resource
	nameless int
	err      error
}

// storeGroupReads stores the rows of a per-group fan-out, read for groups in
// order, and returns the errors of the groups whose call failed other than by
// a denial, plus stopped. A denied group is warned about once for op, and the
// items read without a name once, after a successful write.
func storeGroupReads(st *store.Store, acct *account, region, op string, groups []string, reads []autoScalingGroupRead, stopped error) (int, int, error) {
	var (
		rows   []*store.Resource
		errs   []error
		denied skipTally
		skips  notStoredSkips
	)
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: groups: %w", op, stopped))
	}
	for i, r := range reads {
		rows = append(rows, r.rows...)
		for range r.nameless {
			skips.skipped("items have an empty name", "one of group "+groups[i])
		}
		switch {
		case r.err == nil:
		case isAccessDenied(r.err):
			denied.skipped(groups[i], r.err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", op, groups[i], r.err))
		}
	}
	return storeReadRows(st, acct, region, op, rows, nil, func() {
		denied.warnDeniedIn(st, op, acct, region, "group(s) whose items were not read were")
		skips.warn(st, op, acct, region)
	}, errs...)
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
