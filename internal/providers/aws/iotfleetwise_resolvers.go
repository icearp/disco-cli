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
		resolveIoTFWRefs,
		EdgeDecl{TypeIoTFWCampaign, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWFleet, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWModelManifest, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWStateTemplate, TypeIoTFWSignalCatalog, store.RelUses},
		EdgeDecl{TypeIoTFWDecoderManifest, TypeIoTFWModelManifest, store.RelUses},
		EdgeDecl{TypeIoTFWVehicle, TypeIoTFWDecoderManifest, store.RelUses},
		EdgeDecl{TypeIoTFWVehicle, TypeIoTFWModelManifest, store.RelUses},
	)
	// The scanner records these contains pairs itself (childFanOutStored);
	// this registration only declares them.
	registerResolver(
		noopIoTFWHierarchy,
		EdgeDecl{TypeIoTFWDecoderManifest, TypeIoTFWDecoderManifestNetworkInterface, store.RelContains},
		EdgeDecl{TypeIoTFWDecoderManifest, TypeIoTFWDecoderManifestSignal, store.RelContains},
	)
	registerResolver(
		resolveIoTFWSignalInterfaces,
		EdgeDecl{TypeIoTFWDecoderManifestSignal, TypeIoTFWDecoderManifestNetworkInterface, store.RelUses},
	)
}

func noopIoTFWHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveIoTFWSignalInterfaces wires each signal decoder to the network
// interface of the same decoder manifest that its InterfaceId names.
func resolveIoTFWSignalInterfaces(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTFWDecoderManifestSignal}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	interfaces, err := scannedIDSet(acct, st, TypeIoTFWDecoderManifestNetworkInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		manifestARN, _, ok := strings.Cut(r.NativeID, "/"+iotFWSignalKind+"/")
		if !ok {
			return fmt.Errorf("signal decoder %s: NativeID has no /%s/ segment", r.NativeID, iotFWSignalKind)
		}
		var attrs struct {
			InterfaceID *string `json:"InterfaceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode signal decoder %s: %w", r.NativeID, err)
		}
		target := store.ResourceID("aws", acct.ID, iotFWDecoderManifestChildNativeID(manifestARN, iotFWNetworkInterfaceKind, sv(attrs.InterfaceID)))
		if !interfaces[target] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, target, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert iotfleetwise signal decoder→network interface: %w", err)
		}
	}
	return nil
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
		tgtID := store.ResourceID("aws", acct.ID, tgtARN)
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
