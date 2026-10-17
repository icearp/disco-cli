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
		resolveWickrNetworkKMS,
		EdgeDecl{TypeWickrNetwork, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		noopWickrHierarchy,
		EdgeDecl{TypeWickrNetwork, TypeWickrSecurityGroup, store.RelContains},
		EdgeDecl{TypeWickrNetwork, TypeWickrBot, store.RelContains},
		EdgeDecl{TypeWickrNetwork, TypeWickrNetworkSetting, store.RelContains},
		EdgeDecl{TypeWickrNetwork, TypeWickrUser, store.RelContains},
	)
}

// noopWickrHierarchy only declares the network→child contains edges, which the
// child phases of scanWickrAll record as they store each child.
func noopWickrHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveWickrNetworkKMS wires each network to the customer managed key in its
// EncryptionKeyArn, when that key is scanned.
func resolveWickrNetworkKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWickrNetwork}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			EncryptionKeyArn *string `json:"EncryptionKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeWickrNetwork, r.NativeID, err))
			continue
		}
		keyID, ok := idx.resolveKMSKeyID(sv(attrs.EncryptionKeyArn), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert wickr network→kms: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}
