package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveIoTSWAssetToModel,
		EdgeDecl{TypeIoTSWAsset, TypeIoTSWAssetModel, store.RelUses},
	)
	registerResolver(resolveIoTSWAccessPolicyTarget,
		EdgeDecl{TypeIoTSWAccessPolicy, TypeIoTSWPortal, store.RelAttachedTo},
		EdgeDecl{TypeIoTSWAccessPolicy, TypeIoTSWProject, store.RelAttachedTo},
	)
}

// resolveIoTSWAssetToModel wires each asset to the asset-model it was
// instantiated from via `AssetModelId`.
func resolveIoTSWAssetToModel(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWAsset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	modelSet, err := scannedIDSet(acct, st, TypeIoTSWAssetModel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AssetModelId *string `json:"AssetModelId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		mid := sv(attrs.AssetModelId)
		if mid == "" {
			continue
		}
		mARN := iotSWARN(sv(r.Region), acct.ID, "asset-model", mid)
		tgtID := store.ResourceID("aws", acct.ID, TypeIoTSWAssetModel, mARN)
		if !modelSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iotsitewise asset→model: %w", err)
		}
	}
	return nil
}

// resolveIoTSWAccessPolicyTarget wires each access-policy to its target portal
// or project via `Resource.Portal.Id` / `Resource.Project.Id`.
func resolveIoTSWAccessPolicyTarget(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWAccessPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	portalSet, err := scannedIDSet(acct, st, TypeIoTSWPortal)
	if err != nil {
		return err
	}
	projectSet, err := scannedIDSet(acct, st, TypeIoTSWProject)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Resource *struct {
				Portal *struct {
					Id *string `json:"Id"`
				} `json:"Portal"`
				Project *struct {
					Id *string `json:"Id"`
				} `json:"Project"`
			} `json:"Resource"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Resource == nil {
			continue
		}
		region := sv(r.Region)
		if attrs.Resource.Portal != nil {
			if id := sv(attrs.Resource.Portal.Id); id != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeIoTSWPortal, iotSWARN(region, acct.ID, "portal", id))
				if portalSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert iotsitewise ap→portal: %w", err)
					}
				}
			}
		}
		if attrs.Resource.Project != nil {
			if id := sv(attrs.Resource.Project.Id); id != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeIoTSWProject, iotSWARN(region, acct.ID, "project", id))
				if projectSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert iotsitewise ap→project: %w", err)
					}
				}
			}
		}
	}
	return nil
}
