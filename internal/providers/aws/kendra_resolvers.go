package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveKendraChildToIndex,
		EdgeDecl{TypeKendraDataSource, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraFaq, TypeKendraIndex, store.RelAttachedTo},
	)
	registerResolver(
		resolveKendraIndexRefs,
		EdgeDecl{TypeKendraIndex, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeKendraIndex, TypeKMSKey, store.RelUses},
	)
}

// resolveKendraIndexRefs wires each index to its IAM service role and CMEK
// (ServerSideEncryptionConfiguration.KmsKeyId). DescribeIndex body shape.
func resolveKendraIndexRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKendraIndex}, Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn                           *string `json:"RoleArn"`
			ServerSideEncryptionConfiguration *struct {
				KmsKeyID *string `json:"KmsKeyId"`
			} `json:"ServerSideEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if rarn := sv(attrs.RoleArn); strings.Contains(rarn, ":role/") {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert kendra-index→role: %w", err)
				}
			}
		}
		if attrs.ServerSideEncryptionConfiguration != nil {
			if ref := sv(attrs.ServerSideEncryptionConfiguration.KmsKeyID); ref != "" {
				if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert kendra-index→kms: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveKendraChildToIndex wires data-source + faq to their parent index via
// NativeID strip on `/data-source/{id}` or `/faq/{id}` tail.
func resolveKendraChildToIndex(acct *account, st *store.Store) error {
	idxSet, err := scannedIDSet(acct, st, TypeKendraIndex)
	if err != nil {
		return err
	}
	for _, child := range []struct {
		t, seg string
	}{
		{TypeKendraDataSource, "/data-source/"},
		{TypeKendraFaq, "/faq/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{child.t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			i := strings.LastIndex(r.NativeID, child.seg)
			if i <= 0 {
				continue
			}
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, TypeKendraIndex, parent)
			if !idxSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert kendra %s→index: %w", child.t, err)
			}
		}
	}
	return nil
}
