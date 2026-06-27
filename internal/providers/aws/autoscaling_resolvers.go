package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
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
	)
}

// resolveAutoScalingRelationships wires edges among the EC2 Auto Scaling
// resource family. ASGs anchor most edges (instances, target groups,
// launch templates / configs, subnets, service-linked role); the four
// child types (LifecycleHook, ScalingPolicy, ScheduledAction, WarmPool)
// each hang off their parent ASG via attached-to.
func resolveAutoScalingRelationships(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAutoScalingGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	asgIDByName := make(map[string]string, len(groups))
	for _, g := range groups {
		if g.Name != nil {
			asgIDByName[*g.Name] = g.ID
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

	if err := resolveAutoScalingGroupEdges(acct, st, groups, roleIDs, instanceIDs, tgIDs, lcIDs, ltIDs, subnetIDs); err != nil {
		return err
	}
	if err := resolveAutoScalingLaunchConfigEdges(acct, st, instProfileIDs, sgIDs); err != nil {
		return err
	}
	return resolveAutoScalingChildEdges(acct, st, asgIDByName, roleIDs, topicIDs, queueIDs)
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
	for _, g := range groups {
		var a asgAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(g.Region)
		if roleARN := sv(a.ServiceLinkedRoleARN); roleARN != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if roleIDs[roleID] {
				if err := st.UpsertRelationship(g.ID, roleID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→iam-role: %w", err)
				}
			}
		}
		if lcName := sv(a.LaunchConfigurationName); lcName != "" {
			lcARN := fmt.Sprintf("arn:aws:autoscaling:%s:%s:launchConfiguration:*:launchConfigurationName/%s", region, acct.ID, lcName)
			lcID := store.ResourceID("aws", acct.ID, TypeAutoScalingLaunchConfiguration, lcARN)
			if lcIDs[lcID] {
				if err := st.UpsertRelationship(g.ID, lcID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert asg→launch-config: %w", err)
				}
			}
		}
		if a.LaunchTemplate != nil {
			if ltID := sv(a.LaunchTemplate.LaunchTemplateID); ltID != "" {
				ltARN := ec2ARN(region, acct.ID, "launch-template", ltID)
				ltResID := store.ResourceID("aws", acct.ID, TypeEC2LaunchTemplate, ltARN)
				if ltIDs[ltResID] {
					if err := st.UpsertRelationship(g.ID, ltResID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert asg→launch-template: %w", err)
					}
				}
			}
		}
		for _, tgARN := range a.TargetGroupARNs {
			tgID := store.ResourceID("aws", acct.ID, TypeELBv2TargetGroup, tgARN)
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
			instARN := ec2ARN(region, acct.ID, "instance", id)
			instResID := store.ResourceID("aws", acct.ID, TypeEC2Instance, instARN)
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
				snARN := ec2ARN(region, acct.ID, "subnet", sn)
				snResID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, snARN)
				if subnetIDs[snResID] {
					if err := st.UpsertRelationship(g.ID, snResID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert asg→subnet: %w", err)
					}
				}
			}
		}
	}
	return nil
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
	for _, lc := range lcs {
		var a lcAttrs
		if err := json.Unmarshal([]byte(lc.AttributesJSON), &a); err != nil {
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
			ipID := store.ResourceID("aws", acct.ID, TypeIAMInstanceProfile, ipARN)
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
			sgARN := ec2ARN(region, acct.ID, "security-group", sg)
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if sgIDs[sgID] {
				if err := st.UpsertRelationship(lc.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lc→security-group: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveAutoScalingChildEdges(acct *account, st *store.Store, asgIDByName map[string]string, roleIDs, topicIDs, queueIDs map[string]bool) error {
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
				continue
			}
			if asgName := sv(a.AutoScalingGroupName); asgName != "" {
				if asgID, ok := asgIDByName[asgName]; ok {
					if err := st.UpsertRelationship(r.ID, asgID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert %s→asg: %w", ct, err)
					}
				}
			}
			if ct != TypeAutoScalingLifecycleHook {
				continue
			}
			if roleARN := sv(a.RoleARN); roleARN != "" {
				roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
				if roleIDs[roleID] {
					if err := st.UpsertRelationship(r.ID, roleID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert lifecycle-hook→iam-role: %w", err)
					}
				}
			}
			if tgt := sv(a.NotificationTargetARN); tgt != "" {
				switch {
				case strings.HasPrefix(tgt, "arn:aws:sns:"):
					tID := store.ResourceID("aws", acct.ID, TypeSNSTopic, tgt)
					if topicIDs[tID] {
						if err := st.UpsertRelationship(r.ID, tID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert lifecycle-hook→sns: %w", err)
						}
					}
				case strings.HasPrefix(tgt, "arn:aws:sqs:"):
					qID := store.ResourceID("aws", acct.ID, TypeSQSQueue, tgt)
					if queueIDs[qID] {
						if err := st.UpsertRelationship(r.ID, qID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert lifecycle-hook→sqs: %w", err)
						}
					}
				}
			}
		}
	}
	return nil
}
