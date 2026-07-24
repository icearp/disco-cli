package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveMIToSubnet,
		EdgeDecl{Source: TypeSQLManagedInstance, Target: TypeNetworkSubnet, Kind: store.RelUses},
	)
	registerResolver(resolveMIEncryptionProtectorToKey,
		EdgeDecl{Source: TypeSQLManagedInstanceEP, Target: TypeSQLManagedInstanceKey, Kind: store.RelUses},
	)
	registerResolver(resolveManagedDatabaseToSource,
		EdgeDecl{Source: TypeSQLManagedDatabase, Target: TypeSQLManagedDatabase, Kind: store.RelUses},
	)
}

// resolveMIToSubnet adds a uses edge from each managed instance to the subnet
// it is delegated into, derived from properties.subnetId in the MI's stored
// attributes JSON.
func resolveMIToSubnet(sub *subscription, st *store.Store) error {
	mis, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeSQLManagedInstance},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			SubnetID *string `json:"subnetId"`
		} `json:"properties"`
	}

	for _, r := range mis {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.SubnetID == nil {
			continue
		}
		subnetID := store.ResourceID("azure", sub.ID, *attrs.Properties.SubnetID)
		if err := st.UpsertRelationship(r.ID, subnetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert managedInstance→subnet relationship: %w", err)
		}
	}
	return nil
}

// resolveMIEncryptionProtectorToKey adds a uses edge from each MI encryption
// protector to the MI key it references, derived from properties.serverKeyName.
// The referenced key's native ARM ID is reconstructed from the protector's
// own native ID (same MI, swap encryptionProtector/current → keys/{name}).
func resolveMIEncryptionProtectorToKey(sub *subscription, st *store.Store) error {
	return resolveEPToKey(sub, st, TypeSQLManagedInstanceEP, TypeSQLManagedInstanceKey, "managedInstance")
}

// resolveManagedDatabaseToSource adds a uses edge from each managed database
// to its source database (for databases restored from another MDB), derived
// from properties.sourceDatabaseId in the MDB's stored attributes JSON.
func resolveManagedDatabaseToSource(sub *subscription, st *store.Store) error {
	mdbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeSQLManagedDatabase},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			SourceDatabaseID *string `json:"sourceDatabaseId"`
		} `json:"properties"`
	}

	for _, r := range mdbs {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.SourceDatabaseID == nil {
			continue
		}
		srcID := store.ResourceID("azure", sub.ID, *attrs.Properties.SourceDatabaseID)
		if err := st.UpsertRelationship(r.ID, srcID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert managedDatabase→sourceDatabase relationship: %w", err)
		}
	}
	return nil
}

// resolveEPToKey is shared logic for both server-side and MI-side encryption
// protector → key resolvers. The protector's native ID has the shape
// ".../{parent}/{parentName}/encryptionProtector/current"; the key native ID
// has the shape ".../{parent}/{parentName}/keys/{serverKeyName}". We rewrite
// the suffix to derive the key ID.
//
// label is only used for error-wrapping context (e.g. "server", "managedInstance").
func resolveEPToKey(sub *subscription, st *store.Store, epType, keyType, label string) error {
	eps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{epType},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			ServerKeyName *string `json:"serverKeyName"`
		} `json:"properties"`
	}

	for _, r := range eps {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ServerKeyName == nil {
			continue
		}
		keyNativeID := epToKeyNativeID(r.NativeID, *attrs.Properties.ServerKeyName)
		if keyNativeID == "" {
			continue
		}
		keyID := store.ResourceID("azure", sub.ID, keyNativeID)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s encryptionProtector→key relationship: %w", label, err)
		}
	}
	return nil
}

// epToKeyNativeID rewrites an encryption-protector ARM ID into the ARM ID of
// the server key it references. Returns "" if the EP ID doesn't have the
// expected ".../encryptionProtector/..." segment.
func epToKeyNativeID(epNativeID, keyName string) string {
	lower := strings.ToLower(epNativeID)
	idx := strings.Index(lower, "/encryptionprotector/")
	if idx < 0 {
		return ""
	}
	return epNativeID[:idx] + "/keys/" + keyName
}
