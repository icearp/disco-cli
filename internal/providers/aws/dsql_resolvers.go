package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDSQLClusterKMS,
		EdgeDecl{TypeDSQLCluster, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveDSQLStreamCluster,
		EdgeDecl{TypeDSQLStream, TypeDSQLCluster, store.RelAttachedTo},
	)
}

// resolveDSQLStreamCluster wires each DSQL stream to its source cluster. The
// stream carries bare ClusterIdentifier, so clusters are indexed by the
// identifier embedded in their ARN (…:cluster/{identifier}).
func resolveDSQLStreamCluster(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDSQLStream}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDSQLCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	clusterByID := make(map[string]string, len(clusters))
	for _, c := range clusters {
		if _, id, ok := strings.Cut(c.NativeID, ":cluster/"); ok && id != "" {
			clusterByID[id] = c.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			ClusterIdentifier *string `json:"ClusterIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if cid := sv(attrs.ClusterIdentifier); cid != "" {
			if tgtID, ok := clusterByID[cid]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dsql stream→cluster: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDSQLClusterKMS wires each Aurora DSQL cluster to its CMEK via
// EncryptionDetails.KmsKeyArn. FK-safe via the shared KMS index.
func resolveDSQLClusterKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDSQLCluster}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			EncryptionDetails *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"EncryptionDetails"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionDetails == nil {
			continue
		}
		ref := sv(attrs.EncryptionDetails.KmsKeyArn)
		if ref == "" {
			continue
		}
		tgt, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert dsql cluster→kms: %w", err)
		}
	}
	return nil
}
