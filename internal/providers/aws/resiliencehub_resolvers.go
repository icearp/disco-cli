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
	registerResolver(
		noopResilienceHubHierarchy,
		EdgeDecl{TypeResilienceHubService, TypeResilienceHubTest, store.RelContains},
		EdgeDecl{TypeResilienceHubSystem, TypeResilienceHubUserJourney, store.RelContains},
	)
}

// noopResilienceHubHierarchy emits nothing: the service → test and
// system → user-journey contains edges are written at scan time by childFanOut
// (scanRHTests, scanRHUserJourneys). It is registered only so those edges are
// declared to coverage tooling.
func noopResilienceHubHierarchy(_ *account, _ *store.Store) error {
	return nil
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
