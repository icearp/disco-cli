package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCloudTrailResourcePolicyToParent,
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailTrail, store.RelAttachedTo},
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailEventDataStore, store.RelAttachedTo},
		EdgeDecl{TypeCloudTrailResourcePolicy, TypeCloudTrailChannel, store.RelAttachedTo},
	)
}

// resolveCloudTrailResourcePolicyToParent wires each resource-policy back to
// its trail / event-data-store / channel parent — NativeID is `{parentARN}/policy`.
// Dispatch by ARN substring; FK-safe via per-target scannedIDSet.
func resolveCloudTrailResourcePolicyToParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudTrailResourcePolicy}, Limit: util.AllResources,
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
			present = trailSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, ":eventdatastore/"):
			tgtType = TypeCloudTrailEventDataStore
			present = edsSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, ":channel/"):
			tgtType = TypeCloudTrailChannel
			present = chSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		default:
			continue
		}
		if !present {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, parent)
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudtrail rp→%s: %w", tgtType, err)
		}
	}
	return nil
}
