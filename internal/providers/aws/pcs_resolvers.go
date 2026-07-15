package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolvePCSChildrenToCluster,
		EdgeDecl{TypePCSComputeNodeGroup, TypePCSCluster, store.RelAttachedTo},
		EdgeDecl{TypePCSQueue, TypePCSCluster, store.RelAttachedTo},
	)
}

// resolvePCSChildrenToCluster wires each compute-node-group + queue to its
// parent cluster via ClusterId on the list summary. FK-safe.
func resolvePCSChildrenToCluster(acct *account, st *store.Store) error {
	clusterSet, err := scannedIDSet(acct, st, TypePCSCluster)
	if err != nil {
		return err
	}
	for _, ttyp := range []string{TypePCSComputeNodeGroup, TypePCSQueue} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ttyp},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ClusterID *string `json:"ClusterId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			cid := sv(attrs.ClusterID)
			if cid == "" {
				continue
			}
			cARN := fmt.Sprintf("arn:aws:pcs:%s:%s:cluster/%s", sv(r.Region), acct.ID, cid)
			tgt := store.ResourceID("aws", acct.ID, cARN)
			if !clusterSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert pcs %s→cluster: %w", ttyp, err)
			}
		}
	}
	return nil
}
