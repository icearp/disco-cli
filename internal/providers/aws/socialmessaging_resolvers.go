package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSocialMessagingPhoneNumberWABA,
		EdgeDecl{TypeSocialMessagingPhoneNumberID, TypeSocialMessagingWaba, store.RelAttachedTo},
	)
}

// resolveSocialMessagingPhoneNumberWABA wires each WhatsApp phone number to
// its linked WhatsApp Business Account. The parent WABA ARN is embedded as
// WabaArn at scan time (the SDK phone-number entry carries no back-reference).
// FK-safe against the scanned WABA set.
func resolveSocialMessagingPhoneNumberWABA(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSocialMessagingPhoneNumberID}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	wabaSet, err := scannedIDSet(acct, st, TypeSocialMessagingWaba)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WabaArn string `json:"WabaArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.WabaArn == "" {
			continue
		}
		wabaID := store.ResourceID("aws", acct.ID, attrs.WabaArn)
		if !wabaSet[wabaID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, wabaID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert social-messaging phone-number→waba: %w", err)
		}
	}
	return nil
}
