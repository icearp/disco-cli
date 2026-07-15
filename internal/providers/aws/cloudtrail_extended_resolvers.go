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
		resolveCloudTrailResourcePolicyToParent,
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailTrail, store.RelAttachedTo},
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailEventDataStore, store.RelAttachedTo},
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailChannel, store.RelAttachedTo},
	)
	registerResolver(
		resolveCloudTrailChannelDestinations,
		EdgeDecl{TypeCloudTrailChannel, TypeCloudTrailEventDataStore, store.RelRoutesTo},
	)
}

// resolveCloudTrailChannelDestinations wires each channel to the event-data-stores
// it ingests into (Destinations[].Location, Type=EVENT_DATA_STORE only;
// service-linked channels carry an Amazon-service name in Location, skipped).
func resolveCloudTrailChannelDestinations(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudTrailChannel}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	edsSet, err := scannedIDSet(acct, st, TypeCloudTrailEventDataStore)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Destinations []struct {
				Type     *string `json:"Type"`
				Location *string `json:"Location"`
			} `json:"Destinations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, d := range attrs.Destinations {
			if sv(d.Type) != "EVENT_DATA_STORE" {
				continue
			}
			loc := sv(d.Location)
			if loc == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, loc)
			if !edsSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudtrail channel→eds: %w", err)
			}
		}
	}
	return nil
}

// resolveCloudTrailResourcePolicyToParent wires each resource-policy back to
// its trail / event-data-store / channel parent — NativeID is `{parentARN}/policy`.
// Dispatch by ARN substring; FK-safe via per-target scannedIDSet.
func resolveCloudTrailResourcePolicyToParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudTrailResourcePolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	trailSet, err := scannedIDSet(acct, st, TypeCloudTrailTrail)
	if err != nil {
		return err
	}
	edsSet, err := scannedIDSet(acct, st, TypeCloudTrailEventDataStore)
	if err != nil {
		return err
	}
	chSet, err := scannedIDSet(acct, st, TypeCloudTrailChannel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		var tgtType string
		var present bool
		switch {
		case strings.Contains(parent, ":trail/"):
			tgtType = TypeCloudTrailTrail
			present = trailSet[store.ResourceID("aws", acct.ID, parent)]
		case strings.Contains(parent, ":eventdatastore/"):
			tgtType = TypeCloudTrailEventDataStore
			present = edsSet[store.ResourceID("aws", acct.ID, parent)]
		case strings.Contains(parent, ":channel/"):
			tgtType = TypeCloudTrailChannel
			present = chSet[store.ResourceID("aws", acct.ID, parent)]
		default:
			continue
		}
		if !present {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudtrail rp→%s: %w", tgtType, err)
		}
	}
	return nil
}
