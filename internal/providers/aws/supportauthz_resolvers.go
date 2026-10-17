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
		resolveSupportPermitKMS,
		EdgeDecl{TypeSupportAuthZSupportPermit, TypeKMSKey, store.RelUses},
	)
}

// resolveSupportPermitKMS wires each support permit to the KMS key that signs
// it (SigningKeyInfo.KmsKey, as supportPermitAttrs renders it), when that key
// is scanned.
func resolveSupportPermitKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSupportAuthZSupportPermit}, Limit: util.AllResources,
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
			SigningKeyInfo struct {
				KmsKey string `json:"KmsKey"`
			} `json:"SigningKeyInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeSupportAuthZSupportPermit, r.NativeID, err))
			continue
		}
		keyID, ok := idx.resolveKMSKeyID(attrs.SigningKeyInfo.KmsKey, sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert supportauthz permit→kms: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}
