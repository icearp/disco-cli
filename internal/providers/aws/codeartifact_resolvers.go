package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCodeArtifactDomainToKMS,
		EdgeDecl{TypeCodeArtifactDomain, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveCodeArtifactChildrenToDomain,
		EdgeDecl{TypeCodeArtifactRepository, TypeCodeArtifactDomain, store.RelAttachedTo},
		EdgeDecl{TypeCodeArtifactPackageGroup, TypeCodeArtifactDomain, store.RelAttachedTo},
	)
}

func codeArtifactDomainARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:codeartifact:%s:%s:domain/%s", region, acct, name)
}

// resolveCodeArtifactDomainToKMS wires each domain to its encryption key via
// EncryptionKey (4-shape KMS resolution).
func resolveCodeArtifactDomainToKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeArtifactDomain}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EncryptionKey *string `json:"EncryptionKey"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		k := sv(attrs.EncryptionKey)
		if k == "" {
			continue
		}
		if keyID, ok := kmsIdx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert codeartifact domain→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveCodeArtifactChildrenToDomain wires each repository and package-group
// to its parent domain via DomainName.
func resolveCodeArtifactChildrenToDomain(acct *account, st *store.Store) error {
	domainSet, err := scannedIDSet(acct, st, TypeCodeArtifactDomain)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeCodeArtifactRepository, TypeCodeArtifactPackageGroup} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				DomainName *string `json:"DomainName"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			n := sv(attrs.DomainName)
			if n == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeCodeArtifactDomain, codeArtifactDomainARN(sv(r.Region), acct.ID, n))
			if !domainSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert codeartifact %s→domain: %w", t, err)
			}
		}
	}
	return nil
}
