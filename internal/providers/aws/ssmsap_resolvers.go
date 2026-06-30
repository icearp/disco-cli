package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSSMSAPComponentApplication,
		EdgeDecl{TypeSystemsManagerSAPComponent, TypeSSMSAPApplication, store.RelAttachedTo},
	)
	registerResolver(
		resolveSSMSAPDatabaseParent,
		EdgeDecl{TypeSystemsManagerSAPDatabase, TypeSystemsManagerSAPComponent, store.RelAttachedTo},
		EdgeDecl{TypeSystemsManagerSAPDatabase, TypeSSMSAPApplication, store.RelAttachedTo},
	)
}

// ssmsapIDIndex maps a child summary's ApplicationId / ComponentId back to the
// resource id of the parent row scanned in this account.
type ssmsapIDIndex struct {
	appByID  map[string]string // ApplicationId → application resource id
	compByID map[string]string // ComponentId   → component resource id
}

func loadSSMSAPIndex(acct *account, st *store.Store) (ssmsapIDIndex, error) {
	idx := ssmsapIDIndex{appByID: map[string]string{}, compByID: map[string]string{}}
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSSMSAPApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return idx, err
	}
	for _, a := range apps {
		var at struct {
			ID string `json:"Id"`
		}
		if json.Unmarshal([]byte(a.AttributesJSON), &at) == nil && at.ID != "" {
			idx.appByID[at.ID] = a.ID
		}
	}
	comps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSystemsManagerSAPComponent}, Limit: util.AllResources,
	})
	if err != nil {
		return idx, err
	}
	for _, c := range comps {
		var ct struct {
			ComponentID string `json:"ComponentId"`
		}
		if json.Unmarshal([]byte(c.AttributesJSON), &ct) == nil && ct.ComponentID != "" {
			idx.compByID[ct.ComponentID] = c.ID
		}
	}
	return idx, nil
}

// resolveSSMSAPComponentApplication wires each SAP component to its parent
// application via the ComponentSummary.ApplicationId.
func resolveSSMSAPComponentApplication(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSystemsManagerSAPComponent}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadSSMSAPIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ApplicationID string `json:"ApplicationId"`
		}
		if json.Unmarshal([]byte(r.AttributesJSON), &attrs) != nil {
			continue
		}
		appID, ok := idx.appByID[attrs.ApplicationID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm-sap component→application: %w", err)
		}
	}
	return nil
}

// resolveSSMSAPDatabaseParent wires each SAP database to its parent component
// (preferred) or, when the component is absent, to its application.
func resolveSSMSAPDatabaseParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSystemsManagerSAPDatabase}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadSSMSAPIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ApplicationID string `json:"ApplicationId"`
			ComponentID   string `json:"ComponentId"`
		}
		if json.Unmarshal([]byte(r.AttributesJSON), &attrs) != nil {
			continue
		}
		if compID, ok := idx.compByID[attrs.ComponentID]; ok {
			if err := st.UpsertRelationship(r.ID, compID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm-sap database→component: %w", err)
			}
			continue
		}
		if appID, ok := idx.appByID[attrs.ApplicationID]; ok {
			if err := st.UpsertRelationship(r.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm-sap database→application: %w", err)
			}
		}
	}
	return nil
}
