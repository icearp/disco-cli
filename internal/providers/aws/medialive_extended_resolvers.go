package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveMediaLiveCWAlarmTemplateGroup,
		EdgeDecl{TypeMediaLiveCloudWatchAlarmTemplate, TypeMediaLiveCloudWatchAlarmTemplateGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaLiveEBRuleTemplateGroup,
		EdgeDecl{TypeMediaLiveEventBridgeRuleTemplate, TypeMediaLiveEventBridgeRuleTemplateGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaLiveClusterRefs,
		EdgeDecl{TypeMediaLiveCluster, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeMediaLiveCluster, TypeMediaLiveNetwork, store.RelAttachedTo},
	)
}

func resolveMediaLiveCWAlarmTemplateGroup(acct *account, st *store.Store) error {
	return resolveMediaLiveTemplateToGroup(acct, st, TypeMediaLiveCloudWatchAlarmTemplate, TypeMediaLiveCloudWatchAlarmTemplateGroup)
}

func resolveMediaLiveEBRuleTemplateGroup(acct *account, st *store.Store) error {
	return resolveMediaLiveTemplateToGroup(acct, st, TypeMediaLiveEventBridgeRuleTemplate, TypeMediaLiveEventBridgeRuleTemplateGroup)
}

// resolveMediaLiveTemplateToGroup links each template summary's `GroupID`
// (bare ID) to its parent group via the SDK Id index.
func resolveMediaLiveTemplateToGroup(acct *account, st *store.Store, childType, parentType string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupIdx, err := medialiveByIDIndex(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GroupID *string `json:"GroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		gid := sv(attrs.GroupID)
		if gid == "" {
			continue
		}
		tgtID, ok := groupIdx[gid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert medialive %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}

// resolveMediaLiveClusterRefs wires cluster → IAM role (InstanceRoleArn) +
// every network referenced by NetworkSettings.InterfaceMappings[].NetworkID.
func resolveMediaLiveClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaLiveCluster}, Limit: util.AllResources,
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
	netIdx, err := medialiveByIDIndex(acct, st, TypeMediaLiveNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceRoleArn *string `json:"InstanceRoleArn"`
			NetworkSettings *struct {
				InterfaceMappings []struct {
					NetworkID *string `json:"NetworkId"`
				} `json:"InterfaceMappings"`
			} `json:"NetworkSettings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.InstanceRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert medialive cluster→role: %w", err)
				}
			}
		}
		if attrs.NetworkSettings == nil {
			continue
		}
		for _, im := range attrs.NetworkSettings.InterfaceMappings {
			nid := sv(im.NetworkID)
			if nid == "" {
				continue
			}
			tgtID, ok := netIdx[nid]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert medialive cluster→network: %w", err)
			}
		}
	}
	return nil
}
