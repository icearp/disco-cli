package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveRHAppAssessmentToApp,
		EdgeDecl{TypeResilienceHubAppAssessment, TypeResilienceHubApp, store.RelAttachedTo},
	)
}

// resolveRHAppAssessmentToApp wires each app-assessment to the app it assesses
// (AppArn).
func resolveRHAppAssessmentToApp(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeResilienceHubAppAssessment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeResilienceHubApp)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AppArn *string `json:"AppArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if a := sv(attrs.AppArn); a != "" {
			tgtID := store.ResourceID("aws", acct.ID, a)
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert rh app-assessment→app: %w", err)
				}
			}
		}
	}
	return nil
}
