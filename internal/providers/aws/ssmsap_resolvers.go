package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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

// ssmSAPAppKey names an application within an account: an ApplicationId is
// unique only per region.
type ssmSAPAppKey struct{ region, appID string }

// ssmSAPCompKey names a component within an account: a ComponentId is unique
// only within its application.
type ssmSAPCompKey struct {
	ssmSAPAppKey
	compID string
}

// ssmSAPRow is a stored application, component or database row with the ids
// its summary carries (Id only on an application).
type ssmSAPRow struct {
	store.Resource
	attrs struct {
		ID            string `json:"Id"`
		ApplicationID string `json:"ApplicationId"`
		ComponentID   string `json:"ComponentId"`
	}
}

func (r ssmSAPRow) appKey(appID string) ssmSAPAppKey { return ssmSAPAppKey{sv(r.Region), appID} }

// listSSMSAPRows returns the account's rows of typ. A row whose attributes do
// not decode is left out and its error appended to decodeErrs, so one bad row
// costs no other row its edge; the returned error is the store read's.
func listSSMSAPRows(acct *account, st *store.Store, typ string, decodeErrs *[]error) ([]ssmSAPRow, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ssmSAPRow, 0, len(rows))
	for _, r := range rows {
		row := ssmSAPRow{Resource: r}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &row.attrs); err != nil {
			*decodeErrs = append(*decodeErrs, fmt.Errorf("decode %s %s attributes: %w", typ, r.NativeID, err))
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// ssmSAPAppIndex maps each stored application's region and Id to its
// resource id.
func ssmSAPAppIndex(acct *account, st *store.Store, decodeErrs *[]error) (map[ssmSAPAppKey]string, error) {
	apps, err := listSSMSAPRows(acct, st, TypeSSMSAPApplication, decodeErrs)
	if err != nil {
		return nil, err
	}
	idx := make(map[ssmSAPAppKey]string, len(apps))
	for _, a := range apps {
		if a.attrs.ID != "" {
			idx[a.appKey(a.attrs.ID)] = a.ID
		}
	}
	return idx, nil
}

// resolveSSMSAPComponentApplication wires each SAP component to its parent
// application in the same region via the ComponentSummary.ApplicationId.
func resolveSSMSAPComponentApplication(acct *account, st *store.Store) error {
	var decodeErrs []error
	comps, err := listSSMSAPRows(acct, st, TypeSystemsManagerSAPComponent, &decodeErrs)
	if err != nil || len(comps) == 0 {
		return errors.Join(append(decodeErrs, err)...)
	}
	apps, err := ssmSAPAppIndex(acct, st, &decodeErrs)
	if err != nil {
		return errors.Join(append(decodeErrs, err)...)
	}
	for _, c := range comps {
		appID, ok := apps[c.appKey(c.attrs.ApplicationID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(c.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm-sap component→application: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveSSMSAPDatabaseParent wires each SAP database to its parent component
// (preferred) or, when the component is absent, to its application, both
// looked up in the database's region and application.
func resolveSSMSAPDatabaseParent(acct *account, st *store.Store) error {
	var decodeErrs []error
	dbs, err := listSSMSAPRows(acct, st, TypeSystemsManagerSAPDatabase, &decodeErrs)
	if err != nil || len(dbs) == 0 {
		return errors.Join(append(decodeErrs, err)...)
	}
	apps, err := ssmSAPAppIndex(acct, st, &decodeErrs)
	if err != nil {
		return errors.Join(append(decodeErrs, err)...)
	}
	comps, err := listSSMSAPRows(acct, st, TypeSystemsManagerSAPComponent, &decodeErrs)
	if err != nil {
		return errors.Join(append(decodeErrs, err)...)
	}
	compIdx := make(map[ssmSAPCompKey]string, len(comps))
	for _, c := range comps {
		if c.attrs.ComponentID != "" {
			compIdx[ssmSAPCompKey{c.appKey(c.attrs.ApplicationID), c.attrs.ComponentID}] = c.ID
		}
	}
	for _, d := range dbs {
		app := d.appKey(d.attrs.ApplicationID)
		if compID, ok := compIdx[ssmSAPCompKey{app, d.attrs.ComponentID}]; ok {
			if err := st.UpsertRelationship(d.ID, compID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm-sap database→component: %w", err)
			}
			continue
		}
		if appID, ok := apps[app]; ok {
			if err := st.UpsertRelationship(d.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm-sap database→application: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}
