package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveRekProjectChildren,
		EdgeDecl{TypeRekognitionProjectVersion, TypeRekognitionProject, store.RelAttachedTo},
		EdgeDecl{TypeRekognitionDataset, TypeRekognitionProject, store.RelAttachedTo},
	)
}

// resolveRekProjectChildren wires each project-version and dataset back to its
// parent project (ProjectArn embedded in attrs by the scanner).
func resolveRekProjectChildren(acct *account, st *store.Store) error {
	projSet, err := scannedIDSet(acct, st, TypeRekognitionProject)
	if err != nil {
		return err
	}
	for _, childType := range []string{TypeRekognitionProjectVersion, TypeRekognitionDataset} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID,
			Types: []string{childType}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ProjectArn string `json:"ProjectArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if attrs.ProjectArn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, attrs.ProjectArn)
			if !projSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rekognition %s→project: %w", childType, err)
			}
		}
	}
	return nil
}
