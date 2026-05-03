package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveFDDetectorEventType,
		EdgeDecl{TypeFraudDetectorDetector, TypeFraudDetectorEventType, store.RelUses},
	)
	registerResolver(resolveFDEventTypeRefs,
		EdgeDecl{TypeFraudDetectorEventType, TypeFraudDetectorEntityType, store.RelUses},
		EdgeDecl{TypeFraudDetectorEventType, TypeFraudDetectorLabel, store.RelUses},
		EdgeDecl{TypeFraudDetectorEventType, TypeFraudDetectorVariable, store.RelUses},
	)
}

func fdARN(region, acct, kind, name string) string {
	return fmt.Sprintf("arn:aws:frauddetector:%s:%s:%s/%s", region, acct, kind, name)
}

// resolveFDDetectorEventType wires each detector to its event-type via
// `EventTypeName` (bare name → canonical ARN).
func resolveFDDetectorEventType(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFraudDetectorDetector}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	etSet, err := scannedIDSet(acct, st, TypeFraudDetectorEventType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EventTypeName *string `json:"EventTypeName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		name := sv(attrs.EventTypeName)
		if name == "" {
			continue
		}
		arn := fdARN(sv(r.Region), acct.ID, "event-type", name)
		tgtID := store.ResourceID("aws", acct.ID, TypeFraudDetectorEventType, arn)
		if !etSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert fd detector→event-type: %w", err)
		}
	}
	return nil
}

// resolveFDEventTypeRefs wires event-type → entity-types[], labels[],
// event-variables[] (bare-name lists in SDK shape).
func resolveFDEventTypeRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFraudDetectorEventType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	enSet, err := scannedIDSet(acct, st, TypeFraudDetectorEntityType)
	if err != nil {
		return err
	}
	lSet, err := scannedIDSet(acct, st, TypeFraudDetectorLabel)
	if err != nil {
		return err
	}
	vSet, err := scannedIDSet(acct, st, TypeFraudDetectorVariable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EntityTypes    []string `json:"EntityTypes"`
			Labels         []string `json:"Labels"`
			EventVariables []string `json:"EventVariables"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		emit := func(kind string, names []string, ttyp string, set map[string]bool) error {
			for _, n := range names {
				if n == "" {
					continue
				}
				arn := fdARN(region, acct.ID, kind, n)
				tgtID := store.ResourceID("aws", acct.ID, ttyp, arn)
				if !set[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fd event-type→%s: %w", kind, err)
				}
			}
			return nil
		}
		if err := emit("entity-type", attrs.EntityTypes, TypeFraudDetectorEntityType, enSet); err != nil {
			return err
		}
		if err := emit("label", attrs.Labels, TypeFraudDetectorLabel, lSet); err != nil {
			return err
		}
		if err := emit("variable", attrs.EventVariables, TypeFraudDetectorVariable, vSet); err != nil {
			return err
		}
	}
	return nil
}
