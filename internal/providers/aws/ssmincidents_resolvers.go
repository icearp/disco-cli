package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveSSMIRSetKMS,
		EdgeDecl{TypeSSMIncidentsReplicationSet, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSSMIResponsePlanRefs,
		EdgeDecl{TypeSSMIncidentsResponsePlan, TypeSSMContactsContact, store.RelUses},
		EdgeDecl{TypeSSMIncidentsResponsePlan, TypeSSMContactsPlan, store.RelUses},
	)
}

// resolveSSMIResponsePlanRefs wires each response plan to the ssm-contacts
// contacts/escalation plans it engages (Engagements[] carries either contact
// or escalation-plan ARNs — distinguish by ARN suffix `:contact/` vs
// `:contact-plan/`).
func resolveSSMIResponsePlanRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMIncidentsResponsePlan}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	contactSet, err := scannedIDSet(acct, st, TypeSSMContactsContact)
	if err != nil {
		return err
	}
	planSet, err := scannedIDSet(acct, st, TypeSSMContactsPlan)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Engagements []string `json:"Engagements"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, e := range attrs.Engagements {
			if !strings.Contains(e, ":contact/") {
				continue
			}
			// Both contact and escalation-plan ARNs use the `:contact/` segment;
			// disco splits them by ContactType at scan time. Try plan first then
			// contact — whichever id-set has the row wins.
			if pID := store.ResourceID("aws", acct.ID, TypeSSMContactsPlan, e); planSet[pID] {
				if err := st.UpsertRelationship(r.ID, pID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssmi response-plan→escalation-plan: %w", err)
				}
				continue
			}
			if cID := store.ResourceID("aws", acct.ID, TypeSSMContactsContact, e); contactSet[cID] {
				if err := st.UpsertRelationship(r.ID, cID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssmi response-plan→contact: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSSMIRSetKMS wires replication-set → per-region KMS keys via
// RegionMap[region].SseKmsKeyID.
func resolveSSMIRSetKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMIncidentsReplicationSet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RegionMap map[string]struct {
				SseKmsKeyID *string `json:"SseKmsKeyId"`
			} `json:"RegionMap"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for region, info := range attrs.RegionMap {
			ref := sv(info.SseKmsKeyID)
			if ref == "" {
				continue
			}
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssmi rset→kms: %w", err)
				}
			}
		}
	}
	return nil
}
