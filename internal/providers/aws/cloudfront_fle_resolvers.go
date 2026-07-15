package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCloudFrontFLEConfigProfile,
		EdgeDecl{TypeCloudFrontFieldLevelEncryptionConfig, TypeCloudFrontFieldLevelEncryptionProfile, store.RelUses},
	)
	registerResolver(
		resolveCloudFrontFLEProfilePublicKey,
		EdgeDecl{TypeCloudFrontFieldLevelEncryptionProfile, TypeCloudFrontPublicKey, store.RelUses},
	)
}

// fleProfileRef mirrors the nested profile-id references a FLE config summary
// carries under ContentTypeProfileConfig / QueryArgProfileConfig.
type fleConfigAttrs struct {
	ContentTypeProfileConfig *struct {
		ContentTypeProfiles *struct {
			Items []struct {
				ProfileID *string `json:"ProfileId"`
			} `json:"Items"`
		} `json:"ContentTypeProfiles"`
	} `json:"ContentTypeProfileConfig"`
	QueryArgProfileConfig *struct {
		QueryArgProfiles *struct {
			Items []struct {
				ProfileID *string `json:"ProfileId"`
			} `json:"Items"`
		} `json:"QueryArgProfiles"`
	} `json:"QueryArgProfileConfig"`
}

func (a fleConfigAttrs) profileIDs() []string {
	var ids []string
	if a.ContentTypeProfileConfig != nil && a.ContentTypeProfileConfig.ContentTypeProfiles != nil {
		for _, it := range a.ContentTypeProfileConfig.ContentTypeProfiles.Items {
			if id := sv(it.ProfileID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if a.QueryArgProfileConfig != nil && a.QueryArgProfileConfig.QueryArgProfiles != nil {
		for _, it := range a.QueryArgProfileConfig.QueryArgProfiles.Items {
			if id := sv(it.ProfileID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// fleProfileAttrs mirrors the public-key references a FLE profile summary carries
// under EncryptionEntities.
type fleProfileAttrs struct {
	EncryptionEntities *struct {
		Items []struct {
			PublicKeyID *string `json:"PublicKeyId"`
		} `json:"Items"`
	} `json:"EncryptionEntities"`
}

// resolveCloudFrontFLEProfilePublicKey wires each FLE profile to the CloudFront
// public keys it encrypts fields with (EncryptionEntities[].PublicKeyId), FK-safe.
func resolveCloudFrontFLEProfilePublicKey(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudFrontFieldLevelEncryptionProfile},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keySet, err := scannedIDSet(acct, st, TypeCloudFrontPublicKey)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs fleProfileAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionEntities == nil {
			continue
		}
		seen := map[string]bool{}
		for _, it := range attrs.EncryptionEntities.Items {
			pk := sv(it.PublicKeyID)
			if pk == "" || seen[pk] {
				continue
			}
			seen[pk] = true
			tgtID := store.ResourceID("aws", acct.ID, pk)
			if !keySet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudfront fle-profile→public-key: %w", err)
			}
		}
	}
	return nil
}

// resolveCloudFrontFLEConfigProfile wires each field-level-encryption config to
// the FLE profiles it references (by ProfileId), FK-safe.
func resolveCloudFrontFLEConfigProfile(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudFrontFieldLevelEncryptionConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	profSet, err := scannedIDSet(acct, st, TypeCloudFrontFieldLevelEncryptionProfile)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs fleConfigAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, pid := range attrs.profileIDs() {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			tgtID := store.ResourceID("aws", acct.ID, pid)
			if !profSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudfront fle-config→profile: %w", err)
			}
		}
	}
	return nil
}
