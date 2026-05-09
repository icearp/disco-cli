package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveIoTSWAssetToModel,
		EdgeDecl{TypeIoTSWAsset, TypeIoTSWAssetModel, store.RelUses},
	)
	registerResolver(
		resolveIoTSWAccessPolicyTarget,
		EdgeDecl{TypeIoTSWAccessPolicy, TypeIoTSWPortal, store.RelAttachedTo},
		EdgeDecl{TypeIoTSWAccessPolicy, TypeIoTSWProject, store.RelAttachedTo},
	)
	registerResolver(
		resolveIoTSWPortalRole,
		EdgeDecl{TypeIoTSWPortal, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveIoTSWGatewayThing,
		EdgeDecl{TypeIoTSWGateway, TypeIoTThing, store.RelUses},
	)
	registerResolver(
		resolveIoTSWDatasetRefs,
		EdgeDecl{TypeIoTSWDataset, TypeBedrockKnowledgeBase, store.RelUses},
		EdgeDecl{TypeIoTSWDataset, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveIoTSWAssetModelHierarchies,
		EdgeDecl{TypeIoTSWAssetModel, TypeIoTSWAssetModel, store.RelUses},
	)
	registerResolver(
		resolveIoTSWComputationModelBindings,
		EdgeDecl{TypeIoTSWComputationModel, TypeIoTSWAssetModel, store.RelUses},
		EdgeDecl{TypeIoTSWComputationModel, TypeIoTSWAsset, store.RelUses},
	)
	// Hierarchy emitted at scan time: portal contains project, project contains
	// dashboard. Declared so coverage gap-analysis treats portal/project as
	// containing parents rather than orphans.
	registerResolver(
		noopIoTSWHierarchy,
		EdgeDecl{TypeIoTSWPortal, TypeIoTSWProject, store.RelContains},
		EdgeDecl{TypeIoTSWProject, TypeIoTSWDashboard, store.RelContains},
	)
}

// noopIoTSWHierarchy is a placeholder: the IoT SiteWise scanner emits the
// portal→project and project→dashboard contains edges directly via
// RecordHierarchyBatch in scanIoTSWProjects / scanIoTSWDashboards. This
// resolver only carries the EdgeDecl metadata so coverage tooling sees the
// edges as wired.
func noopIoTSWHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveIoTSWAssetToModel wires each asset to the asset-model it was
// instantiated from via `AssetModelID`.
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
			AssetModelID *string `json:"AssetModelId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		mid := sv(attrs.AssetModelID)
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
// or project via `Resource.Portal.ID` / `Resource.Project.ID`.
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
					ID *string `json:"Id"`
				} `json:"Portal"`
				Project *struct {
					ID *string `json:"Id"`
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
			if id := sv(attrs.Resource.Portal.ID); id != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeIoTSWPortal, iotSWARN(region, acct.ID, "portal", id))
				if portalSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert iotsitewise ap→portal: %w", err)
					}
				}
			}
		}
		if attrs.Resource.Project != nil {
			if id := sv(attrs.Resource.Project.ID); id != "" {
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

// resolveIoTSWPortalRole wires each portal to the IAM service role that grants
// portal users access to IoT SiteWise resources (RoleArn).
func resolveIoTSWPortalRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWPortal}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ra := sv(attrs.RoleArn)
		if ra == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert iotsitewise portal→iam-role: %w", err)
		}
	}
	return nil
}

// computationBindingValue mirrors the SDK ComputationModelDataBindingValue
// shape for JSON walk. Recursive via List.
type computationBindingValue struct {
	AssetModelProperty *struct {
		AssetModelID *string `json:"AssetModelId"`
	} `json:"AssetModelProperty"`
	AssetProperty *struct {
		AssetID *string `json:"AssetId"`
	} `json:"AssetProperty"`
	List []computationBindingValue `json:"List"`
}

// resolveIoTSWComputationModelBindings wires each computation-model to the
// asset-models and assets referenced in ComputationModelDataBinding values.
// The map entries (and nested List entries) carry either AssetModelProperty
// or AssetProperty bindings — walk both, build target ARNs from per-region
// (id) shape, FK-safe.
func resolveIoTSWComputationModelBindings(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWComputationModel}, Limit: util.AllResources,
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
	assetSet, err := scannedIDSet(acct, st, TypeIoTSWAsset)
	if err != nil {
		return err
	}
	var walk func(v computationBindingValue, region string, srcID string) error
	walk = func(v computationBindingValue, region string, srcID string) error {
		if v.AssetModelProperty != nil {
			if id := sv(v.AssetModelProperty.AssetModelID); id != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeIoTSWAssetModel, iotSWARN(region, acct.ID, "asset-model", id))
				if modelSet[tgt] {
					if err := st.UpsertRelationship(srcID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert iotsitewise computation-model→asset-model: %w", err)
					}
				}
			}
		}
		if v.AssetProperty != nil {
			if id := sv(v.AssetProperty.AssetID); id != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeIoTSWAsset, iotSWARN(region, acct.ID, "asset", id))
				if assetSet[tgt] {
					if err := st.UpsertRelationship(srcID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert iotsitewise computation-model→asset: %w", err)
					}
				}
			}
		}
		for _, c := range v.List {
			if err := walk(c, region, srcID); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			ComputationModelDataBinding map[string]computationBindingValue `json:"ComputationModelDataBinding"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, v := range attrs.ComputationModelDataBinding {
			if err := walk(v, region, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveIoTSWAssetModelHierarchies wires each asset-model to the child
// asset-models declared in its AssetModelHierarchies (type-level reference:
// any asset of this model can contain children of the referenced model type).
// Self-edges and unscanned refs are skipped FK-safe.
func resolveIoTSWAssetModelHierarchies(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWAssetModel}, Limit: util.AllResources,
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
			AssetModelHierarchies []struct {
				ChildAssetModelID *string `json:"ChildAssetModelId"`
			} `json:"AssetModelHierarchies"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, h := range attrs.AssetModelHierarchies {
			cid := sv(h.ChildAssetModelID)
			if cid == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypeIoTSWAssetModel, iotSWARN(region, acct.ID, "asset-model", cid))
			if tgt == r.ID || !modelSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert iotsitewise asset-model→child: %w", err)
			}
		}
	}
	return nil
}

// resolveIoTSWDatasetRefs wires each dataset to its Bedrock knowledge-base
// (Source.SourceDetail.Kendra.KnowledgeBaseArn — the SDK field is named
// "Kendra" but carries a Bedrock KB ARN) and its IAM role.
func resolveIoTSWDatasetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWDataset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kbSet, err := scannedIDSet(acct, st, TypeBedrockKnowledgeBase)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DatasetSource *struct {
				SourceDetail *struct {
					Kendra *struct {
						KnowledgeBaseArn *string `json:"KnowledgeBaseArn"`
						RoleArn          *string `json:"RoleArn"`
					} `json:"Kendra"`
				} `json:"SourceDetail"`
			} `json:"DatasetSource"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DatasetSource == nil || attrs.DatasetSource.SourceDetail == nil || attrs.DatasetSource.SourceDetail.Kendra == nil {
			continue
		}
		k := attrs.DatasetSource.SourceDetail.Kendra
		if kb := sv(k.KnowledgeBaseArn); kb != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeBedrockKnowledgeBase, kb)
			if kbSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert iotsitewise dataset→bedrock-kb: %w", err)
				}
			}
		}
		if ra := sv(k.RoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert iotsitewise dataset→iam-role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveIoTSWGatewayThing wires each Greengrass V2 gateway to the IoT thing
// that hosts its core device runtime
// (GatewayPlatform.GreengrassV2.CoreDeviceThingName).
func resolveIoTSWGatewayThing(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTSWGateway}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	thingByNameRegion := map[string]string{}
	thingRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIoTThing}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, t := range thingRows {
		if name := sv(t.Name); name != "" {
			thingByNameRegion[sv(t.Region)+"|"+name] = t.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			GatewayPlatform *struct {
				GreengrassV2 *struct {
					CoreDeviceThingName *string `json:"CoreDeviceThingName"`
				} `json:"GreengrassV2"`
			} `json:"GatewayPlatform"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GatewayPlatform == nil || attrs.GatewayPlatform.GreengrassV2 == nil {
			continue
		}
		name := sv(attrs.GatewayPlatform.GreengrassV2.CoreDeviceThingName)
		if name == "" {
			continue
		}
		tgtID, ok := thingByNameRegion[sv(r.Region)+"|"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iotsitewise gateway→thing: %w", err)
		}
	}
	return nil
}
