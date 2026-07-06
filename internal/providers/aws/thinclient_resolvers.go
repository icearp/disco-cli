package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveThinClientDeviceEnvironment,
		EdgeDecl{TypeThinClientDevice, TypeThinClientEnvironment, store.RelAttachedTo},
	)
}

// resolveThinClientDeviceEnvironment wires each thin-client device to the
// environment it's enrolled in. Device EnvironmentId matches the environment's
// own service Id, so environments are indexed by Id before matching.
func resolveThinClientDeviceEnvironment(acct *account, st *store.Store) error {
	devices, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeThinClientDevice}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	envs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeThinClientEnvironment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	envByID := make(map[string]string, len(envs))
	for _, e := range envs {
		var ea struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(e.AttributesJSON), &ea); err != nil {
			continue
		}
		if id := sv(ea.ID); id != "" {
			envByID[id] = e.ID
		}
	}
	for _, d := range devices {
		var da struct {
			EnvironmentID *string `json:"EnvironmentId"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &da); err != nil {
			continue
		}
		tgt, ok := envByID[sv(da.EnvironmentID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(d.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert thinclient device→environment: %w", err)
		}
	}
	return nil
}
