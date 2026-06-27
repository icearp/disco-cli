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
		resolveEBArchiveBus,
		EdgeDecl{TypeEventsArchive, TypeEventsEventBus, store.RelAttachedTo},
	)
	registerResolver(
		resolveEBEndpointBuses,
		EdgeDecl{TypeEventsEndpoint, TypeEventsEventBus, store.RelAttachedTo},
		EdgeDecl{TypeEventsEndpoint, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveEBEventBusPolicyToBus,
		EdgeDecl{TypeEventsEventBusPolicy, TypeEventsEventBus, store.RelAttachedTo},
	)
}

// resolveEBArchiveBus wires archive → source event-bus (EventSourceArn).
func resolveEBArchiveBus(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsArchive}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	busSet, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EventSourceArn *string `json:"EventSourceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if b := sv(attrs.EventSourceArn); b != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, b)
			if busSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert eb archive→bus: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEBEndpointBuses wires endpoint → all event-buses[] (EventBuses[].EventBusArn)
// and endpoint → IAM role (RoleArn).
func resolveEBEndpointBuses(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsEndpoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	busSet, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EventBuses []struct {
				EventBusArn *string `json:"EventBusArn"`
			} `json:"EventBuses"`
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, eb := range attrs.EventBuses {
			b := sv(eb.EventBusArn)
			if b == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, b)
			if busSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert eb endpoint→bus: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert eb endpoint→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEBEventBusPolicyToBus wires event-bus-policy → event-bus via NativeID
// strip on `/policy` suffix.
func resolveEBEventBusPolicyToBus(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsEventBusPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	busSet, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, parent)
		if !busSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert eb policy→bus: %w", err)
		}
	}
	return nil
}
