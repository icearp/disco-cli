package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveTnbFunctionInstancePackage,
		EdgeDecl{TypeTnbFunctionInstance, TypeTnbFunctionPackage, store.RelAttachedTo},
	)
	registerResolver(
		resolveTnbNetworkInstancePackage,
		EdgeDecl{TypeTnbNetworkInstance, TypeTnbNetworkPackage, store.RelAttachedTo},
	)
	registerResolver(
		resolveTnbNetworkOperationInstance,
		EdgeDecl{TypeTnbNetworkOperation, TypeTnbNetworkInstance, store.RelAttachedTo},
	)
}

// tnbParentIndex maps each row of parentType by its service Id (the value
// child resources carry as a reference) to its disco resource ID.
func tnbParentIndex(acct *account, st *store.Store, parentType string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{parentType}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var a struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// tnbWireChildren emits a directed attached-to edge from each child of
// childType to the parent it references via parentField (a parent Id).
func tnbWireChildren(acct *account, st *store.Store, childType, parentType, parentField, label string) error {
	children, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return nil
	}
	idx, err := tnbParentIndex(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, c := range children {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.AttributesJSON), &m); err != nil {
			continue
		}
		raw, ok := m[parentField]
		if !ok {
			continue
		}
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil || ref == "" {
			continue
		}
		tgt, ok := idx[ref]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(c.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert tnb %s: %w", label, err)
		}
	}
	return nil
}

func resolveTnbFunctionInstancePackage(acct *account, st *store.Store) error {
	return tnbWireChildren(acct, st, TypeTnbFunctionInstance, TypeTnbFunctionPackage, "VnfPkgId", "function-instance→function-package")
}

func resolveTnbNetworkInstancePackage(acct *account, st *store.Store) error {
	return tnbWireChildren(acct, st, TypeTnbNetworkInstance, TypeTnbNetworkPackage, "NsdInfoId", "network-instance→network-package")
}

func resolveTnbNetworkOperationInstance(acct *account, st *store.Store) error {
	return tnbWireChildren(acct, st, TypeTnbNetworkOperation, TypeTnbNetworkInstance, "NsInstanceId", "network-operation→network-instance")
}
