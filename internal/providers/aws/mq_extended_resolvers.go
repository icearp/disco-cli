package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveMQConfigurationAssociationRefs,
		EdgeDecl{TypeMQConfigurationAssociation, TypeMQBroker, store.RelAttachedTo},
		EdgeDecl{TypeMQConfigurationAssociation, TypeMQConfiguration, store.RelUses},
	)
}

// resolveMQConfigurationAssociationRefs wires each synthetic
// ConfigurationAssociation to its broker (Broker) and configuration
// (Configuration) — both stored as ARNs in attrs.
func resolveMQConfigurationAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMQConfigurationAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	brokerSet, err := scannedIDSet(acct, st, TypeMQBroker)
	if err != nil {
		return err
	}
	cfgSet, err := scannedIDSet(acct, st, TypeMQConfiguration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Broker        string `json:"Broker"`
			Configuration string `json:"Configuration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Broker != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeMQBroker, attrs.Broker)
			if brokerSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq assoc→broker: %w", err)
				}
			}
		}
		if attrs.Configuration != "" {
			cfgARN := fmt.Sprintf("arn:aws:mq:%s:%s:configuration:%s", sv(r.Region), acct.ID, attrs.Configuration)
			tgtID := store.ResourceID("aws", acct.ID, TypeMQConfiguration, cfgARN)
			if cfgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mq assoc→config: %w", err)
				}
			}
		}
	}
	return nil
}
