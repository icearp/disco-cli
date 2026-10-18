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
		resolveHealthAgentDomainRefs,
		EdgeDecl{TypeHealthAgentDomain, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeHealthAgentDomain, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		noopHealthAgentHierarchy,
		EdgeDecl{TypeHealthAgentDomain, TypeHealthAgentSubscription, store.RelContains},
	)
}

// noopHealthAgentHierarchy only carries the EdgeDecl: scanHealthAgentSubscriptions
// links each subscription under its domain as it stores it.
func noopHealthAgentHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveHealthAgentDomainRefs wires each domain to its customer managed KMS
// key (KmsKeyArn, and EncryptionContext.KmsKeyArn) and to the IAM role its web
// app uses for EHR operations (WebAppConfiguration.EhrRole), when those are
// scanned.
func resolveHealthAgentDomainRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeHealthAgentDomain}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keys, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	roles, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn         *string `json:"KmsKeyArn"`
			EncryptionContext *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"EncryptionContext"`
			WebAppConfiguration *struct {
				EhrRole *string `json:"EhrRole"`
			} `json:"WebAppConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeHealthAgentDomain, r.NativeID, err))
			continue
		}
		keyRefs := []string{sv(attrs.KmsKeyArn)}
		if attrs.EncryptionContext != nil {
			keyRefs = append(keyRefs, sv(attrs.EncryptionContext.KmsKeyArn))
		}
		for _, ref := range keyRefs {
			if ref == "" {
				continue
			}
			if keyID, ok := keys.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert health-agent domain→kms: %w", err)
				}
			}
		}
		if attrs.WebAppConfiguration == nil {
			continue
		}
		roleID := store.ResourceID("aws", acct.ID, sv(attrs.WebAppConfiguration.EhrRole))
		if roles[roleID] {
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert health-agent domain→role: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}
