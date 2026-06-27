package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveKafkaConnectConnectorRefs,
		EdgeDecl{TypeKafkaConnectConnector, TypeKafkaConnectCustomPlugin, store.RelUses},
		EdgeDecl{TypeKafkaConnectConnector, TypeKafkaConnectWorkerConfiguration, store.RelUses},
		EdgeDecl{TypeKafkaConnectConnector, TypeIAMRole, store.RelUses},
	)
}

// resolveKafkaConnectConnectorRefs wires each connector to its custom plugins,
// worker configuration, and IAM service-execution role.
func resolveKafkaConnectConnectorRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeKafkaConnectConnector}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pluginSet, err := scannedIDSet(acct, st, TypeKafkaConnectCustomPlugin)
	if err != nil {
		return err
	}
	wcSet, err := scannedIDSet(acct, st, TypeKafkaConnectWorkerConfiguration)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Plugins []struct {
				CustomPlugin *struct {
					CustomPluginArn *string `json:"CustomPluginArn"`
				} `json:"CustomPlugin"`
			} `json:"Plugins"`
			WorkerConfiguration *struct {
				WorkerConfigurationArn *string `json:"WorkerConfigurationArn"`
			} `json:"WorkerConfiguration"`
			ServiceExecutionRoleArn *string `json:"ServiceExecutionRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, p := range attrs.Plugins {
			if p.CustomPlugin == nil {
				continue
			}
			arn := sv(p.CustomPlugin.CustomPluginArn)
			if arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeKafkaConnectCustomPlugin, arn)
			if !pluginSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert kafka-connect connector→plugin: %w", err)
			}
		}
		if attrs.WorkerConfiguration != nil {
			if arn := sv(attrs.WorkerConfiguration.WorkerConfigurationArn); arn != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeKafkaConnectWorkerConfiguration, arn)
				if wcSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert kafka-connect connector→worker-config: %w", err)
					}
				}
			}
		}
		if role := sv(attrs.ServiceExecutionRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert kafka-connect connector→role: %w", err)
				}
			}
		}
	}
	return nil
}
