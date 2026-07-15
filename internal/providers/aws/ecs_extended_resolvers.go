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
		resolveECSCapacityProviderToASG,
		EdgeDecl{TypeECSCapacityProvider, TypeAutoScalingGroup, store.RelUses},
	)
	registerResolver(
		resolveECSCCPARefs,
		EdgeDecl{TypeECSClusterCapacityProviderAssociations, TypeECSCluster, store.RelAttachedTo},
		EdgeDecl{TypeECSClusterCapacityProviderAssociations, TypeECSCapacityProvider, store.RelUses},
	)
	registerResolver(
		resolveECSTaskSetRefs,
		EdgeDecl{TypeECSTaskSet, TypeECSCluster, store.RelAttachedTo},
		EdgeDecl{TypeECSTaskSet, TypeECSService, store.RelAttachedTo},
		EdgeDecl{TypeECSTaskSet, TypeECSTaskDefinition, store.RelUses},
	)
}

func ecsCapacityProviderARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:capacity-provider/%s", region, acct, name)
}

// resolveECSCapacityProviderToASG wires each capacity-provider to its
// underlying Auto Scaling group via AutoScalingGroupProvider.AutoScalingGroupArn.
func resolveECSCapacityProviderToASG(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeECSCapacityProvider}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	asgSet, err := scannedIDSet(acct, st, TypeAutoScalingGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AutoScalingGroupProvider *struct {
				AutoScalingGroupArn *string `json:"AutoScalingGroupArn"`
			} `json:"AutoScalingGroupProvider"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AutoScalingGroupProvider == nil {
			continue
		}
		arn := sv(attrs.AutoScalingGroupProvider.AutoScalingGroupArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !asgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ecs cp→asg: %w", err)
		}
	}
	return nil
}

// resolveECSCCPARefs wires the synthetic cluster-capacity-provider-association
// row to its parent cluster (NativeID `{clusterARN}/capacity-provider-associations`)
// and to each capacity-provider listed in CapacityProviders[].
func resolveECSCCPARefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeECSClusterCapacityProviderAssociations}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clSet, err := scannedIDSet(acct, st, TypeECSCluster)
	if err != nil {
		return err
	}
	cpSet, err := scannedIDSet(acct, st, TypeECSCapacityProvider)
	if err != nil {
		return err
	}
	for _, r := range rows {
		clusterARN := strings.TrimSuffix(r.NativeID, "/capacity-provider-associations")
		if clusterARN != r.NativeID {
			tgtID := store.ResourceID("aws", acct.ID, clusterARN)
			if clSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs ccpa→cluster: %w", err)
				}
			}
		}
		var attrs struct {
			CapacityProviders []string `json:"CapacityProviders"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, cpName := range attrs.CapacityProviders {
			if cpName == "" {
				continue
			}
			cpARN := cpName
			if !strings.HasPrefix(cpName, "arn:") {
				cpARN = ecsCapacityProviderARN(region, acct.ID, cpName)
			}
			tgtID := store.ResourceID("aws", acct.ID, cpARN)
			if !cpSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs ccpa→cp: %w", err)
			}
		}
	}
	return nil
}

// resolveECSTaskSetRefs wires each task-set to its cluster (ClusterArn),
// service (ServiceArn) and task-definition (TaskDefinition).
func resolveECSTaskSetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeECSTaskSet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clSet, err := scannedIDSet(acct, st, TypeECSCluster)
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeECSService)
	if err != nil {
		return err
	}
	tdSet, err := scannedIDSet(acct, st, TypeECSTaskDefinition)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClusterArn     *string `json:"ClusterArn"`
			ServiceArn     *string `json:"ServiceArn"`
			TaskDefinition *string `json:"TaskDefinition"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if c := sv(attrs.ClusterArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, c)
			if clSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs ts→cluster: %w", err)
				}
			}
		}
		if s := sv(attrs.ServiceArn); s != "" {
			tgtID := store.ResourceID("aws", acct.ID, s)
			if svcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs ts→service: %w", err)
				}
			}
		}
		if td := sv(attrs.TaskDefinition); td != "" {
			tgtID := store.ResourceID("aws", acct.ID, td)
			if tdSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs ts→td: %w", err)
				}
			}
		}
	}
	return nil
}
