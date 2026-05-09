package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveNotifChildrenToConfig,
		EdgeDecl{TypeNotificationsChannelAssociation, TypeNotificationsNotificationConfiguration, store.RelAttachedTo},
		EdgeDecl{TypeNotificationsEventRule, TypeNotificationsNotificationConfiguration, store.RelAttachedTo},
		EdgeDecl{TypeNotificationsOrganizationalUnitAssociation, TypeNotificationsNotificationConfiguration, store.RelAttachedTo},
	)
}

// resolveNotifChildrenToConfig wires per-config children (channel-association,
// event-rule, ou-association) to their parent notification-configuration via
// the NotificationConfigurationArn attribute.
func resolveNotifChildrenToConfig(acct *account, st *store.Store) error {
	configSet, err := scannedIDSet(acct, st, TypeNotificationsNotificationConfiguration)
	if err != nil {
		return err
	}
	for _, ctype := range []string{
		TypeNotificationsChannelAssociation,
		TypeNotificationsEventRule,
		TypeNotificationsOrganizationalUnitAssociation,
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				NotificationConfigurationArn *string `json:"NotificationConfigurationArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			parent := sv(attrs.NotificationConfigurationArn)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeNotificationsNotificationConfiguration, parent)
			if !configSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert notif %s→config: %w", ctype, err)
			}
		}
	}
	return nil
}
