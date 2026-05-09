package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCSCRepositoryLinkRefs,
		EdgeDecl{TypeCodeStarConnectionsRepositoryLink, TypeCodeStarConnectionsConnection, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsRepositoryLink, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveCSCSyncConfigurationRefs,
		EdgeDecl{TypeCodeStarConnectionsSyncConfiguration, TypeCodeStarConnectionsRepositoryLink, store.RelAttachedTo},
		EdgeDecl{TypeCodeStarConnectionsSyncConfiguration, TypeIAMRole, store.RelUses},
	)
}

// resolveCSCRepositoryLinkRefs wires repository-link → connection (ConnectionArn)
// and KMS key (EncryptionKeyArn).
func resolveCSCRepositoryLinkRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsRepositoryLink}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	connSet, err := scannedIDSet(acct, st, TypeCodeStarConnectionsConnection)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConnectionArn    *string `json:"ConnectionArn"`
			EncryptionKeyArn *string `json:"EncryptionKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if c := sv(attrs.ConnectionArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeCodeStarConnectionsConnection, c)
			if connSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc rl→conn: %w", err)
				}
			}
		}
		if k := sv(attrs.EncryptionKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc rl→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveCSCSyncConfigurationRefs wires sync-configuration → repository-link
// (RepositoryLinkID, looked up in an index of scanned link IDs) and IAM role
// (RoleArn).
func resolveCSCSyncConfigurationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsSyncConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	linkRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeStarConnectionsRepositoryLink}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// index: RepositoryLinkID → repository-link resource ID.
	linkByID := map[string]string{}
	for _, lr := range linkRows {
		var la struct {
			RepositoryLinkID *string `json:"RepositoryLinkId"`
		}
		if err := json.Unmarshal([]byte(lr.AttributesJSON), &la); err != nil {
			continue
		}
		if id := sv(la.RepositoryLinkID); id != "" {
			linkByID[id] = lr.ID
		}
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RepositoryLinkID *string `json:"RepositoryLinkId"`
			RoleArn          *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.RepositoryLinkID); id != "" {
			if linkID, ok := linkByID[id]; ok {
				if err := st.UpsertRelationship(r.ID, linkID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc sc→rl: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert csc sc→role: %w", err)
				}
			}
		}
	}
	return nil
}
