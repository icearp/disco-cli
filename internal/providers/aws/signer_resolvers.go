package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSignerProfilePermissionToProfile,
		EdgeDecl{TypeSignerProfilePermission, TypeSignerSigningProfile, store.RelAttachedTo},
	)
}

// resolveSignerProfilePermissionToProfile wires each profile-permission to its
// parent signing-profile via NativeID `{profileARN}/permission/{stID}` strip.
func resolveSignerProfilePermissionToProfile(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSignerProfilePermission}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	profileSet, err := scannedIDSet(acct, st, TypeSignerSigningProfile)
	if err != nil {
		return err
	}
	for _, r := range rows {
		idx := strings.LastIndex(r.NativeID, "/permission/")
		if idx <= 0 {
			continue
		}
		parent := r.NativeID[:idx]
		tgtID := store.ResourceID("aws", acct.ID, TypeSignerSigningProfile, parent)
		if !profileSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert signer pp→profile: %w", err)
		}
	}
	return nil
}
