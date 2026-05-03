package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGameLiftAliasFleet,
		EdgeDecl{TypeGameLiftAlias, TypeGameLiftFleet, store.RelRoutesTo},
	)
	registerResolver(resolveGameLiftFleetRefs,
		EdgeDecl{TypeGameLiftFleet, TypeGameLiftBuild, store.RelUses},
		EdgeDecl{TypeGameLiftFleet, TypeGameLiftScript, store.RelUses},
		EdgeDecl{TypeGameLiftFleet, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(resolveGameLiftContainerFleetRefs,
		EdgeDecl{TypeGameLiftContainerFleet, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGameLiftContainerFleet, TypeGameLiftContainerGroupDefinition, store.RelUses},
	)
	registerResolver(resolveGameLiftGameServerGroupRefs,
		EdgeDecl{TypeGameLiftGameServerGroup, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGameLiftGameServerGroup, TypeAutoScalingGroup, store.RelAttachedTo},
	)
	registerResolver(resolveGameLiftGameSessionQueueRefs,
		EdgeDecl{TypeGameLiftGameSessionQueue, TypeGameLiftFleet, store.RelRoutesTo},
		EdgeDecl{TypeGameLiftGameSessionQueue, TypeGameLiftAlias, store.RelRoutesTo},
		EdgeDecl{TypeGameLiftGameSessionQueue, TypeSNSTopic, store.RelRoutesTo},
	)
	registerResolver(resolveGameLiftMatchmakingConfigRefs,
		EdgeDecl{TypeGameLiftMatchmakingConfiguration, TypeGameLiftGameSessionQueue, store.RelRoutesTo},
		EdgeDecl{TypeGameLiftMatchmakingConfiguration, TypeGameLiftMatchmakingRuleSet, store.RelUses},
		EdgeDecl{TypeGameLiftMatchmakingConfiguration, TypeSNSTopic, store.RelRoutesTo},
	)
}

// gameliftFleetARN rebuilds `arn:aws:gamelift:{region}:{acct}:fleet/{id}` from
// a bare fleet ID. Used for refs that carry only `FleetId` (alias routing
// strategy).
func gameliftFleetARN(region, acct, fleetID string) string {
	return fmt.Sprintf("arn:aws:gamelift:%s:%s:fleet/%s", region, acct, fleetID)
}

// resolveGameLiftAliasFleet links each SIMPLE-routed alias to its target
// fleet via `RoutingStrategy.FleetId`. TERMINAL aliases (no fleet) skip.
func resolveGameLiftAliasFleet(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftAlias}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fleetSet, err := scannedIDSet(acct, st, TypeGameLiftFleet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoutingStrategy *struct {
				FleetId *string `json:"FleetId"`
				Type    *string `json:"Type"`
			} `json:"RoutingStrategy"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RoutingStrategy == nil {
			continue
		}
		fid := sv(attrs.RoutingStrategy.FleetId)
		if fid == "" {
			continue
		}
		region := sv(r.Region)
		fARN := gameliftFleetARN(region, acct.ID, fid)
		tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftFleet, fARN)
		if !fleetSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert gamelift alias→fleet: %w", err)
		}
	}
	return nil
}

// resolveGameLiftFleetRefs wires fleet → build (BuildArn) + script (ScriptArn,
// when present) + IAM role (InstanceRoleArn).
func resolveGameLiftFleetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftFleet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	buildSet, err := scannedIDSet(acct, st, TypeGameLiftBuild)
	if err != nil {
		return err
	}
	scriptSet, err := scannedIDSet(acct, st, TypeGameLiftScript)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			BuildArn        *string `json:"BuildArn"`
			ScriptArn       *string `json:"ScriptArn"`
			InstanceRoleArn *string `json:"InstanceRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if b := sv(attrs.BuildArn); b != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftBuild, b)
			if buildSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift fleet→build: %w", err)
				}
			}
		}
		if s := sv(attrs.ScriptArn); s != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftScript, s)
			if scriptSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift fleet→script: %w", err)
				}
			}
		}
		if role := sv(attrs.InstanceRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift fleet→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGameLiftContainerFleetRefs wires container-fleet → IAM role
// (FleetRoleArn) + container-group-definition (GameServerContainerGroupDefinitionArn).
func resolveGameLiftContainerFleetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftContainerFleet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	cgdSet, err := scannedIDSet(acct, st, TypeGameLiftContainerGroupDefinition)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FleetRoleArn                          *string `json:"FleetRoleArn"`
			GameServerContainerGroupDefinitionArn *string `json:"GameServerContainerGroupDefinitionArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.FleetRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift container-fleet→role: %w", err)
				}
			}
		}
		if cgd := sv(attrs.GameServerContainerGroupDefinitionArn); cgd != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftContainerGroupDefinition, cgd)
			if cgdSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift container-fleet→cgd: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGameLiftGameServerGroupRefs wires game-server-group → IAM role +
// auto-scaling group.
func resolveGameLiftGameServerGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftGameServerGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	asgSet, err := scannedIDSet(acct, st, TypeAutoScalingGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn             *string `json:"RoleArn"`
			AutoScalingGroupArn *string `json:"AutoScalingGroupArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift gsg→role: %w", err)
				}
			}
		}
		if asg := sv(attrs.AutoScalingGroupArn); asg != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeAutoScalingGroup, asg)
			if asgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift gsg→asg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGameLiftGameSessionQueueRefs wires queue → fleet/alias destinations
// (Destinations[].DestinationArn dispatched by ARN segment) + SNS notification.
func resolveGameLiftGameSessionQueueRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftGameSessionQueue},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fleetSet, err := scannedIDSet(acct, st, TypeGameLiftFleet)
	if err != nil {
		return err
	}
	aliasSet, err := scannedIDSet(acct, st, TypeGameLiftAlias)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Destinations []struct {
				DestinationArn *string `json:"DestinationArn"`
			} `json:"Destinations"`
			NotificationTarget *string `json:"NotificationTarget"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, d := range attrs.Destinations {
			arn := sv(d.DestinationArn)
			if arn == "" {
				continue
			}
			var tgtType string
			switch {
			case strings.Contains(arn, ":fleet/"):
				tgtType = TypeGameLiftFleet
			case strings.Contains(arn, ":alias/"):
				tgtType = TypeGameLiftAlias
			default:
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtType, arn)
			set := fleetSet
			if tgtType == TypeGameLiftAlias {
				set = aliasSet
			}
			if !set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert gamelift queue→%s: %w", tgtType, err)
			}
		}
		if nt := sv(attrs.NotificationTarget); strings.HasPrefix(nt, "arn:aws") && strings.Contains(nt, ":sns:") {
			tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, nt)
			if snsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift queue→sns: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveGameLiftMatchmakingConfigRefs wires matchmaking-config → queues
// (GameSessionQueueArns[]) + rule-set (RuleSetArn) + SNS notification.
func resolveGameLiftMatchmakingConfigRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGameLiftMatchmakingConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	queueSet, err := scannedIDSet(acct, st, TypeGameLiftGameSessionQueue)
	if err != nil {
		return err
	}
	rsSet, err := scannedIDSet(acct, st, TypeGameLiftMatchmakingRuleSet)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GameSessionQueueArns []string `json:"GameSessionQueueArns"`
			RuleSetArn           *string  `json:"RuleSetArn"`
			NotificationTarget   *string  `json:"NotificationTarget"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, q := range attrs.GameSessionQueueArns {
			tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftGameSessionQueue, q)
			if queueSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift mm→queue: %w", err)
				}
			}
		}
		if rs := sv(attrs.RuleSetArn); rs != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeGameLiftMatchmakingRuleSet, rs)
			if rsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift mm→rs: %w", err)
				}
			}
		}
		if nt := sv(attrs.NotificationTarget); strings.HasPrefix(nt, "arn:aws") && strings.Contains(nt, ":sns:") {
			tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, nt)
			if snsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert gamelift mm→sns: %w", err)
				}
			}
		}
	}
	return nil
}
