package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveIoTFWRefs,
		EdgeDecl{TypeIoTFWCampaign, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWFleet, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWModelManifest, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWStateTemplate, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWDecoderManifest, TypeIoTFWModelManifest, store.RelUses},
		EdgeDecl{TypeIoTFWVehicle, TypeIoTFWDecoderManifest, store.RelUses},
		EdgeDecl{TypeIoTFWVehicle, TypeIoTFWModelManifest, store.RelUses},
	)
}

// resolveIoTFWRefs wires every IoT FleetWise resource to the catalog/manifest
// it references. Summaries carry full ARN refs; FK-safe via per-target
// scannedIDSet.
func resolveIoTFWRefs(acct *account, st *store.Store) error {
	scSet, err := scannedIDSet(acct, st, TypeIoTFWSignalCatalog)
	if err != nil {
		return err
	}
	mmSet, err := scannedIDSet(acct, st, TypeIoTFWModelManifest)
	if err != nil {
		return err
	}
	dmSet, err := scannedIDSet(acct, st, TypeIoTFWDecoderManifest)
	if err != nil {
		return err
	}

	emit := func(srcID, tgtType, tgtARN string, set map[string]bool) error {
		if tgtARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
		if !set[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iotfleetwise→%s: %w", tgtType, err)
		}
		return nil
	}

	// Resources that only reference the signal catalog.
	for _, t := range []string{TypeIoTFWCampaign, TypeIoTFWFleet, TypeIoTFWStateTemplate} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				SignalCatalogArn *string `json:"SignalCatalogArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if err := emit(r.ID, TypeIoTFWSignalCatalog, sv(attrs.SignalCatalogArn), scSet); err != nil {
				return err
			}
		}
	}

	// Model manifest → signal catalog.
	mmRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTFWModelManifest}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range mmRows {
		var attrs struct {
			SignalCatalogArn *string `json:"SignalCatalogArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := emit(r.ID, TypeIoTFWSignalCatalog, sv(attrs.SignalCatalogArn), scSet); err != nil {
			return err
		}
	}

	// Decoder manifest → model manifest.
	dmRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTFWDecoderManifest}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range dmRows {
		var attrs struct {
			ModelManifestArn *string `json:"ModelManifestArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := emit(r.ID, TypeIoTFWModelManifest, sv(attrs.ModelManifestArn), mmSet); err != nil {
			return err
		}
	}

	// Vehicle → decoder + model manifest.
	vRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTFWVehicle}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range vRows {
		var attrs struct {
			DecoderManifestArn *string `json:"DecoderManifestArn"`
			ModelManifestArn   *string `json:"ModelManifestArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := emit(r.ID, TypeIoTFWDecoderManifest, sv(attrs.DecoderManifestArn), dmSet); err != nil {
			return err
		}
		if err := emit(r.ID, TypeIoTFWModelManifest, sv(attrs.ModelManifestArn), mmSet); err != nil {
			return err
		}
	}
	return nil
}
