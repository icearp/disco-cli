package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveAutoScalingRelationships,
		// resolveAutoScalingGroupEdges
		EdgeDecl{TypeAutoScalingGroup, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAutoScalingGroup, TypeAutoScalingLaunchConfiguration, store.RelUses},
		EdgeDecl{TypeAutoScalingGroup, TypeEC2LaunchTemplate, store.RelUses},
		EdgeDecl{TypeAutoScalingGroup, TypeELBv2TargetGroup, store.RelAttachedTo},
		EdgeDecl{TypeAutoScalingGroup, TypeEC2Instance, store.RelContains},
		EdgeDecl{TypeAutoScalingGroup, TypeEC2Subnet, store.RelUses},
		// resolveAutoScalingLaunchConfigEdges
		EdgeDecl{TypeAutoScalingLaunchConfiguration, TypeIAMInstanceProfile, store.RelUses},
		EdgeDecl{TypeAutoScalingLaunchConfiguration, TypeEC2SecurityGroup, store.RelUses},
		// resolveAutoScalingChildEdges — child→ASG attached-to + LifecycleHook outbound
		EdgeDecl{TypeAutoScalingLifecycleHook, TypeAutoScalingGroup, store.RelAttachedTo},
		EdgeDecl{TypeAutoScalingScalingPolicy, TypeAutoScalingGroup, store.RelAttachedTo},
		EdgeDecl{TypeAutoScalingScheduledAction, TypeAutoScalingGroup, store.RelAttachedTo},
		EdgeDecl{TypeAutoScalingWarmPool, TypeAutoScalingGroup, store.RelAttachedTo},
		EdgeDecl{TypeAutoScalingLifecycleHook, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAutoScalingLifecycleHook, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeAutoScalingLifecycleHook, TypeSQSQueue, store.RelUses},
		// resolveAutoScalingNotificationTopicEdges
		EdgeDecl{TypeAutoScalingNotificationConfiguration, TypeSNSTopic, store.RelUses},
		// recorded by scanAutoScalingNotificationConfigurations via RecordHierarchyBatch
		EdgeDecl{TypeAutoScalingGroup, TypeAutoScalingNotificationConfiguration, store.RelContains},
	)
}

// autoScalingGroupKey names a group within the account being resolved: group
// names are unique per account and region only, so a child names its group
// in its own region.
type autoScalingGroupKey struct{ region, name string }

// resolveAutoScalingRelationships wires edges among the EC2 Auto Scaling
// resource family. ASGs anchor most edges (instances, target groups,
// launch templates / configs, subnets, service-linked role); the child types
// LifecycleHook, ScalingPolicy, ScheduledAction and WarmPool hang off their
// parent ASG via attached-to. NotificationConfiguration is contained by its
// ASG at scan time (RecordHierarchyBatch) and uses its SNS topic.
func resolveAutoScalingRelationships(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAutoScalingGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	asgIDByName := make(map[autoScalingGroupKey]string, len(groups))
	for _, g := range groups {
		if g.Name != nil {
			asgIDByName[autoScalingGroupKey{region: sv(g.Region), name: *g.Name}] = g.ID
		}
	}

	roleIDs, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	instProfileIDs, err := scannedIDSet(acct, st, TypeIAMInstanceProfile)
	if err != nil {
		return err
	}
	instanceIDs, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	tgIDs, err := scannedIDSet(acct, st, TypeELBv2TargetGroup)
	if err != nil {
		return err
	}
	lcIDs, err := scannedIDSet(acct, st, TypeAutoScalingLaunchConfiguration)
	if err != nil {
		return err
	}
	ltIDs, err := scannedIDSet(acct, st, TypeEC2LaunchTemplate)
	if err != nil {
		return err
	}
	subnetIDs, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgIDs, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	topicIDs, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	queueIDs, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}

	// Each family runs whatever another returns, so a row that fails to decode
	// costs no other family its edges.
	return errors.Join(
		resolveAutoScalingGroupEdges(acct, st, groups, roleIDs, instanceIDs, tgIDs, lcIDs, ltIDs, subnetIDs),
		resolveAutoScalingLaunchConfigEdges(acct, st, instProfileIDs, sgIDs),
		resolveAutoScalingChildEdges(acct, st, asgIDByName, roleIDs, topicIDs, queueIDs),
		resolveAutoScalingNotificationTopicEdges(acct, st, topicIDs),
	)
}

// resolveAutoScalingNotificationTopicEdges links each notification
// configuration to the SNS topic it publishes to, when that topic was scanned.
func resolveAutoScalingNotificationTopicEdges(acct *account, st *store.Store, topicIDs map[string]bool) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAutoScalingNotificationConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rs {
		var a autoScalingNotificationConfiguration
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAutoScalingNotificationConfiguration, r.NativeID, err))
			continue
		}
		topicID := store.ResourceID("aws", acct.ID, a.TopicARN)
		if !topicIDs[topicID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, topicID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert notification-configuration→sns: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

func resolveAutoScalingGroupEdges(acct *account, st *store.Store, groups []store.Resource, roleIDs, instanceIDs, tgIDs, lcIDs, ltIDs, subnetIDs map[string]bool) error {
	type lt struct {
		LaunchTemplateID *string `json:"LaunchTemplateId"`
	}
	type instance struct {
		InstanceID *string `json:"InstanceId"`
	}
	type asgAttrs struct {
		LaunchConfigurationName *string    `json:"LaunchConfigurationName"`
		LaunchTemplate          *lt        `json:"LaunchTemplate"`
		ServiceLinkedRoleARN    *string    `json:"ServiceLinkedRoleARN"`
		TargetGroupARNs         []string   `json:"TargetGroupARNs"`
		Instances               []instance `json:"Instances"`
		VPCZoneIdentifier       *string    `json:"VPCZoneIdentifier"`
	}
	var decodeErrs []error
	for _, g := range groups {
		var a asgAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAutoScalingGroup, g.NativeID, err))
			continue
		}
		region := sv(g.Region)
		if roleARN := sv(a.ServiceLinkedRoleARN); roleARN != "" {
			roleID := store.ResourceID("aws", acct.ID, roleARN)
			if roleIDs[roleID] {
				if err := st.UpsertRelationship(g.ID, roleID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→iam-role: %w", err)
				}
			}
		}
		if lcName := sv(a.LaunchConfigurationName); lcName != "" {
			lcARN := fmt.Sprintf("arn:aws:autoscaling:%s:%s:launchConfiguration:*:launchConfigurationName/%s", region, acct.ID, lcName)
			lcID := store.ResourceID("aws", acct.ID, lcARN)
			if lcIDs[lcID] {
				if err := st.UpsertRelationship(g.ID, lcID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→launch-config: %w", err)
				}
			}
		}
		if a.LaunchTemplate != nil {
			if ltID := sv(a.LaunchTemplate.LaunchTemplateID); ltID != "" {
				ltARN := ec2ARNIn(acct.partition(), region, acct.ID, "launch-template", ltID)
				ltResID := store.ResourceID("aws", acct.ID, ltARN)
				if ltIDs[ltResID] {
					if err := st.UpsertRelationship(g.ID, ltResID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert asg→launch-template: %w", err)
					}
				}
			}
		}
		for _, tgARN := range a.TargetGroupARNs {
			tgID := store.ResourceID("aws", acct.ID, tgARN)
			if tgIDs[tgID] {
				if err := st.UpsertRelationship(g.ID, tgID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→target-group: %w", err)
				}
			}
		}
		for _, inst := range a.Instances {
			id := sv(inst.InstanceID)
			if id == "" {
				continue
			}
			instARN := ec2ARNIn(acct.partition(), region, acct.ID, "instance", id)
			instResID := store.ResourceID("aws", acct.ID, instARN)
			if instanceIDs[instResID] {
				if err := st.UpsertRelationship(g.ID, instResID, store.RelContains, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→ec2-instance: %w", err)
				}
			}
		}
		if zones := sv(a.VPCZoneIdentifier); zones != "" {
			for sn := range strings.SplitSeq(zones, ",") {
				sn = strings.TrimSpace(sn)
				if sn == "" {
					continue
				}
				snARN := ec2ARNIn(acct.partition(), region, acct.ID, "subnet", sn)
				snResID := store.ResourceID("aws", acct.ID, snARN)
				if subnetIDs[snResID] {
					if err := st.UpsertRelationship(g.ID, snResID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert asg→subnet: %w", err)
					}
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}

func resolveAutoScalingLaunchConfigEdges(acct *account, st *store.Store, instProfileIDs, sgIDs map[string]bool) error {
	lcs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAutoScalingLaunchConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type lcAttrs struct {
		IamInstanceProfile *string  `json:"IamInstanceProfile"`
		SecurityGroups     []string `json:"SecurityGroups"`
	}
	var decodeErrs []error
	for _, lc := range lcs {
		var a lcAttrs
		if err := json.Unmarshal([]byte(lc.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAutoScalingLaunchConfiguration, lc.NativeID, err))
			continue
		}
		region := sv(lc.Region)
		if ip := sv(a.IamInstanceProfile); ip != "" {
			// May be an ARN or a name. Treat ARN-prefixed as full ARN; name
			// form is rebuilt as the canonical IAM instance-profile ARN.
			ipARN := ip
			if !strings.HasPrefix(ip, "arn:") {
				ipARN = fmt.Sprintf("arn:aws:iam::%s:instance-profile/%s", acct.ID, ip)
			}
			ipID := store.ResourceID("aws", acct.ID, ipARN)
			if instProfileIDs[ipID] {
				if err := st.UpsertRelationship(lc.ID, ipID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lc→iam-instance-profile: %w", err)
				}
			}
		}
		for _, sg := range a.SecurityGroups {
			if sg == "" {
				continue
			}
			sgARN := ec2ARNIn(acct.partition(), region, acct.ID, "security-group", sg)
			sgID := store.ResourceID("aws", acct.ID, sgARN)
			if sgIDs[sgID] {
				if err := st.UpsertRelationship(lc.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lc→security-group: %w", err)
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}

func resolveAutoScalingChildEdges(acct *account, st *store.Store, asgIDByName map[autoScalingGroupKey]string, roleIDs, topicIDs, queueIDs map[string]bool) error {
	var decodeErrs []error
	for _, ct := range []string{
		TypeAutoScalingLifecycleHook,
		TypeAutoScalingScalingPolicy,
		TypeAutoScalingScheduledAction,
		TypeAutoScalingWarmPool,
	} {
		rs, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ct},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		type childAttrs struct {
			AutoScalingGroupName  *string `json:"AutoScalingGroupName"`
			RoleARN               *string `json:"RoleARN"`
			NotificationTargetARN *string `json:"NotificationTargetARN"`
		}
		for _, r := range rs {
			var a childAttrs
			if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
				decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", ct, r.NativeID, err))
				continue
			}
			asgName := sv(a.AutoScalingGroupName)
			if ct == TypeAutoScalingWarmPool {
				// The stored WarmPoolConfiguration names no group; the
				// scanner keys the warm pool's NativeID on it instead.
				asgName = autoScalingWarmPoolGroup(r.NativeID)
			}
			if asgName != "" {
				if asgID, ok := asgIDByName[autoScalingGroupKey{region: sv(r.Region), name: asgName}]; ok {
					if err := st.UpsertRelationship(r.ID, asgID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert %s→asg: %w", ct, err)
					}
				}
			}
			if ct != TypeAutoScalingLifecycleHook {
				continue
			}
			if roleARN := sv(a.RoleARN); roleARN != "" {
				roleID := store.ResourceID("aws", acct.ID, roleARN)
				if roleIDs[roleID] {
					if err := st.UpsertRelationship(r.ID, roleID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert lifecycle-hook→iam-role: %w", err)
					}
				}
			}
			if tgt := sv(a.NotificationTargetARN); tgt != "" {
				switch {
				case strings.HasPrefix(tgt, "arn:aws:sns:"):
					tID := store.ResourceID("aws", acct.ID, tgt)
					if topicIDs[tID] {
						if err := st.UpsertRelationship(r.ID, tID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert lifecycle-hook→sns: %w", err)
						}
					}
				case strings.HasPrefix(tgt, "arn:aws:sqs:"):
					qID := store.ResourceID("aws", acct.ID, tgt)
					if queueIDs[qID] {
						if err := st.UpsertRelationship(r.ID, qID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert lifecycle-hook→sqs: %w", err)
						}
					}
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}
