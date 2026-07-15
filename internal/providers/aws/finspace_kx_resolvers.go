package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveFinspaceKxChildEnv,
		EdgeDecl{TypeFinspaceKxCluster, TypeFinspaceKxEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeFinspaceKxDatabase, TypeFinspaceKxEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeFinspaceKxScalingGroup, TypeFinspaceKxEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeFinspaceKxUser, TypeFinspaceKxEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeFinspaceKxVolume, TypeFinspaceKxEnvironment, store.RelAttachedTo},
	)
	registerResolver(
		resolveFinspaceKxDataview,
		EdgeDecl{TypeFinspaceKxDataview, TypeFinspaceKxDatabase, store.RelAttachedTo},
		EdgeDecl{TypeFinspaceKxDataview, TypeFinspaceKxEnvironment, store.RelAttachedTo},
	)
	registerResolver(
		resolveFinspaceKxEnvironmentKMS,
		EdgeDecl{TypeFinspaceKxEnvironment, TypeKMSKey, store.RelUses},
	)
}

// resolveFinspaceKxChildEnv wires each kdb child (cluster, database, scaling
// group, user, volume) to its parent environment. Every child NativeID encodes
// the environment ARN as the prefix before a `/<segment>/` marker
// (`{envARN}/cluster/{name}`; KxUser's real ARN uses `/kxUser/`).
func resolveFinspaceKxChildEnv(acct *account, st *store.Store) error {
	envSet, err := scannedIDSet(acct, st, TypeFinspaceKxEnvironment)
	if err != nil {
		return err
	}
	if len(envSet) == 0 {
		return nil
	}
	for _, m := range []struct {
		childType string
		segment   string
	}{
		{TypeFinspaceKxCluster, "/cluster/"},
		{TypeFinspaceKxDatabase, "/database/"},
		{TypeFinspaceKxScalingGroup, "/scaling-group/"},
		{TypeFinspaceKxUser, "/kxUser/"},
		{TypeFinspaceKxVolume, "/volume/"},
	} {
		if err := wireFinspaceKxParent(acct, st, m.childType, m.segment, TypeFinspaceKxEnvironment, envSet); err != nil {
			return err
		}
	}
	return nil
}

// resolveFinspaceKxDataview wires each dataview to its parent database and
// environment. Dataview NativeID = `{envARN}/database/{db}/dataview/{name}`.
func resolveFinspaceKxDataview(acct *account, st *store.Store) error {
	dbSet, err := scannedIDSet(acct, st, TypeFinspaceKxDatabase)
	if err != nil {
		return err
	}
	envSet, err := scannedIDSet(acct, st, TypeFinspaceKxEnvironment)
	if err != nil {
		return err
	}
	if err := wireFinspaceKxParent(acct, st, TypeFinspaceKxDataview, "/dataview/", TypeFinspaceKxDatabase, dbSet); err != nil {
		return err
	}
	return wireFinspaceKxParent(acct, st, TypeFinspaceKxDataview, "/database/", TypeFinspaceKxEnvironment, envSet)
}

// wireFinspaceKxParent emits child→parent `attached-to` edges where the parent
// NativeID is the child NativeID truncated at the first occurrence of segment.
func wireFinspaceKxParent(acct *account, st *store.Store, childType, segment, parentType string, parentSet map[string]bool) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, segment)
		if i < 0 {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, r.NativeID[:i])
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert finspace %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}

// resolveFinspaceKxEnvironmentKMS wires each kdb environment to the KMS key that
// encrypts it (KxEnvironment.KmsKeyId).
func resolveFinspaceKxEnvironmentKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFinspaceKxEnvironment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.KmsKeyID)
		if ref == "" {
			continue
		}
		if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert finspace kx-environment→kms: %w", err)
			}
		}
	}
	return nil
}
