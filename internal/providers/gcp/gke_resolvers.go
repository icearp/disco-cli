package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Resolver Wave R21 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the first resolvers for `gke_scanners.go`'s Cluster
// and NodePool (no gke_resolvers.go existed before this wave). Cluster's
// `network`/`subnetwork` fields are documented as bare Compute resource
// names (not full self-links), matched via bareNameIndex the same way as
// every other bare-name Compute reference in this package.
// `databaseEncryption.keyName` is a full CryptoKey resource name (no version
// suffix, but stripCryptoKeyVersion is a no-op on an already-bare name, so
// reused for consistency). Both Cluster's own `nodeConfig.serviceAccount`
// (the default node pool's SA, present only on legacy clusters that never
// migrated to per-NodePool config) and NodePool's `config.serviceAccount`
// (the modern per-pool SA field, same NodeConfig struct) are wired the same
// way — the SDK's own sentinel value "default" (meaning the project's
// default Compute Engine SA) naturally produces no match in
// buildSAEmailIndex, so no special-casing is needed. NodePool's own parent
// edge to its owning Cluster is already covered by the scanner's
// `upsertWithParent` hierarchy closure — no resolver edge needed for that.
// Deliberately NOT wired: NodePool.InstanceGroupUrls (the underlying
// Compute-managed instance groups — GKE-owned, ephemeral across upgrades,
// and blue-green upgrades can list two live URLs simultaneously; treated as
// implementation detail, not an addressable edge, same bucket as AWS
// autoscaling-group-owned EC2 instances).
func init() {
	registerResolver(resolveGKEClusterRelationships,
		EdgeDecl{TypeGKECluster, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeGKECluster, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeGKECluster, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeGKECluster, TypeKMSCryptoKey, store.RelUses},
	)
	registerResolver(resolveGKENodePoolRelationships,
		EdgeDecl{TypeGKENodePool, TypeIAMServiceAccount, store.RelUses},
	)
}

func resolveGKEClusterRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeGKECluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netByName, err := bareNameIndex(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	subnetByName, err := bareNameIndex(p, st, TypeComputeSubnet)
	if err != nil {
		return err
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Network    string `json:"network"`
			Subnetwork string `json:"subnetwork"`
			NodeConfig *struct {
				ServiceAccount string `json:"serviceAccount"`
			} `json:"nodeConfig"`
			DatabaseEncryption *struct {
				KeyName string `json:"keyName"`
			} `json:"databaseEncryption"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Network != "" {
			if toID, ok := netByName[lastSegment(attrs.Network)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→network: %w", err)
				}
			}
		}
		if attrs.Subnetwork != "" {
			if toID, ok := subnetByName[lastSegment(attrs.Subnetwork)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→subnetwork: %w", err)
				}
			}
		}
		if attrs.NodeConfig != nil && attrs.NodeConfig.ServiceAccount != "" {
			if toID, ok := saByEmail[attrs.NodeConfig.ServiceAccount]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→serviceAccount: %w", err)
				}
			}
		}
		if attrs.DatabaseEncryption != nil && attrs.DatabaseEncryption.KeyName != "" {
			if toID, ok := keyIDByNative[stripCryptoKeyVersion(attrs.DatabaseEncryption.KeyName)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cluster→cryptoKey: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveGKENodePoolRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeGKENodePool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Config *struct {
				ServiceAccount string `json:"serviceAccount"`
			} `json:"config"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Config == nil || attrs.Config.ServiceAccount == "" {
			continue
		}
		if toID, ok := saByEmail[attrs.Config.ServiceAccount]; ok {
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert nodePool→serviceAccount: %w", err)
			}
		}
	}
	return nil
}
