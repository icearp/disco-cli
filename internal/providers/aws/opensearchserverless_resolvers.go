package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveOSSCollectionRefs,
		EdgeDecl{TypeOSSCollection, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOSSCollection, TypeOSSCollectionGroup, store.RelAttachedTo},
	)
}

// resolveOSSCollectionRefs wires each collection to its KMS key (KmsKeyArn) and
// optional collection-group (CollectionGroupName).
func resolveOSSCollectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeOSSCollection}, Limit: util.AllResources,
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
	groupRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeOSSCollectionGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// index: (region, name) → group resource ID. Groups are per-region.
	groupByRegionName := map[string]string{}
	for _, gr := range groupRows {
		var ga struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(gr.AttributesJSON), &ga); err != nil {
			continue
		}
		if n := sv(ga.Name); n != "" {
			groupByRegionName[sv(gr.Region)+"|"+n] = gr.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn           *string `json:"KmsKeyArn"`
			CollectionGroupName *string `json:"CollectionGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if k := sv(attrs.KmsKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert oss coll→kms: %w", err)
				}
			}
		}
		if g := sv(attrs.CollectionGroupName); g != "" {
			if gid, ok := groupByRegionName[region+"|"+g]; ok {
				if err := st.UpsertRelationship(r.ID, gid, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert oss coll→cg: %w", err)
				}
			}
		}
	}
	return nil
}
