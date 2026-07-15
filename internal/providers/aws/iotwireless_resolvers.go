package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveIoTWirelessDestinationRole,
		EdgeDecl{TypeIoTWirelessDestination, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveIoTWirelessDeviceToDestination,
		EdgeDecl{TypeIoTWirelessWirelessDevice, TypeIoTWirelessDestination, store.RelAttachedTo},
	)
}

// resolveIoTWirelessDestinationRole wires destination → IAM role (RoleArn,
// already present in ListDestinations summary — no Get fan-out needed).
func resolveIoTWirelessDestinationRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTWirelessDestination}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iotw dest→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveIoTWirelessDeviceToDestination wires wireless-device → destination
// via DestinationName in WirelessDeviceStatistics summary (per-region
// name → resource ID index).
func resolveIoTWirelessDeviceToDestination(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTWirelessWirelessDevice}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	destRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTWirelessDestination}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	destByRegionName := map[string]string{}
	for _, dr := range destRows {
		var da struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(dr.AttributesJSON), &da); err != nil {
			continue
		}
		if n := sv(da.Name); n != "" {
			destByRegionName[sv(dr.Region)+"|"+n] = dr.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			DestinationName *string `json:"DestinationName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if name := sv(attrs.DestinationName); name != "" {
			if tgtID, ok := destByRegionName[sv(r.Region)+"|"+name]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert iotw device→dest: %w", err)
				}
			}
		}
	}
	return nil
}
