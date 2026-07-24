package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveM2DeploymentRefs,
		EdgeDecl{TypeM2Deployment, TypeM2Application, store.RelAttachedTo},
		EdgeDecl{TypeM2Deployment, TypeM2Environment, store.RelUses},
	)
}

// resolveM2DeploymentRefs wires each deployment to its parent application
// (strip NativeID `{appARN}/deployment/{id}`) and to its target environment
// (EnvironmentID, via a `lastSegment(envARN) → id` index).
func resolveM2DeploymentRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeM2Deployment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeM2Application)
	if err != nil {
		return err
	}
	envRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeM2Environment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	envByID := make(map[string]string, len(envRows))
	for _, e := range envRows {
		i := strings.LastIndexByte(e.NativeID, '/')
		if i < 0 {
			continue
		}
		envByID[e.NativeID[i+1:]] = e.ID
	}
	for _, r := range rows {
		// → app via NativeID strip
		if i := strings.Index(r.NativeID, "/deployment/"); i >= 0 {
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert m2 dep→app: %w", err)
				}
			}
		}
		// → env via attribute
		var attrs struct {
			EnvironmentID *string `json:"EnvironmentId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		eid := sv(attrs.EnvironmentID)
		if eid == "" {
			continue
		}
		if envID, ok := envByID[eid]; ok {
			if err := st.UpsertRelationship(r.ID, envID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert m2 dep→env: %w", err)
			}
		}
	}
	return nil
}
