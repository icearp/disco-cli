package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSpotInstanceRequestRelationships,
		EdgeDecl{TypeEC2SpotInstanceRequest, TypeEC2Instance, store.RelAttachedTo},
	)
	registerResolver(
		resolveInstanceEventWindowRelationships,
		EdgeDecl{TypeEC2InstanceEventWindow, TypeEC2Instance, store.RelAttachedTo},
		EdgeDecl{TypeEC2InstanceEventWindow, TypeEC2Host, store.RelAttachedTo},
	)
}

// resolveSpotInstanceRequestRelationships wires each fulfilled Spot request to
// the instance it launched (FK-safe — open/closed requests carry no instance).
func resolveSpotInstanceRequestRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2SpotInstanceRequest}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceID *string `json:"InstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.InstanceID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "instance", id))
			if instSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert spot-request→instance: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveInstanceEventWindowRelationships wires each event window to the
// instances and dedicated hosts it targets (FK-safe — a window may target by
// tag instead, or its targets may be unscanned).
func resolveInstanceEventWindowRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2InstanceEventWindow}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	hostSet, err := scannedIDSet(acct, st, TypeEC2Host)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AssociationTarget *struct {
				InstanceIDs      []string `json:"InstanceIds"`
				DedicatedHostIDs []string `json:"DedicatedHostIds"`
			} `json:"AssociationTarget"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AssociationTarget == nil {
			continue
		}
		region := sv(r.Region)
		for _, id := range attrs.AssociationTarget.InstanceIDs {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "instance", id))
			if instSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert event-window→instance: %w", err)
				}
			}
		}
		for _, id := range attrs.AssociationTarget.DedicatedHostIDs {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "dedicated-host", id))
			if hostSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert event-window→host: %w", err)
				}
			}
		}
	}
	return nil
}
