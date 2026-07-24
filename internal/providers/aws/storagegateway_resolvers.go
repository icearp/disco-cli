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
		resolveStorageGatewayChildren,
		EdgeDecl{TypeStorageGatewayVolume, TypeStorageGatewayGateway, store.RelAttachedTo},
		EdgeDecl{TypeStorageGatewayShare, TypeStorageGatewayGateway, store.RelAttachedTo},
		EdgeDecl{TypeStorageGatewayTape, TypeStorageGatewayGateway, store.RelAttachedTo},
		EdgeDecl{TypeStorageGatewayFsAssociation, TypeStorageGatewayGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveStorageGatewayDevices,
		EdgeDecl{TypeStorageGatewayDevice, TypeStorageGatewayGateway, store.RelAttachedTo},
	)
}

// resolveStorageGatewayChildren wires volumes, file shares, tapes, and file
// system associations to their parent gateway via each row's GatewayARN
// attribute. FK-safe: emits only when the gateway was scanned (archived
// tapes carry an empty GatewayARN — skipped).
func resolveStorageGatewayChildren(acct *account, st *store.Store) error {
	gwSet, err := scannedIDSet(acct, st, TypeStorageGatewayGateway)
	if err != nil {
		return err
	}
	if len(gwSet) == 0 {
		return nil
	}
	for _, ctype := range []string{
		TypeStorageGatewayVolume,
		TypeStorageGatewayShare,
		TypeStorageGatewayTape,
		TypeStorageGatewayFsAssociation,
	} {
		rows, lerr := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if lerr != nil {
			return lerr
		}
		for _, r := range rows {
			var attrs struct {
				GatewayARN *string `json:"GatewayARN"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			gwARN := sv(attrs.GatewayARN)
			if gwARN == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, gwARN)
			if !gwSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert storagegateway %s→gateway: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveStorageGatewayDevices wires each VTL device to its gateway.
// VTLDeviceARN embeds the gateway ARN as a prefix
// (`{gatewayARN}/device/{deviceId}`), so the parent comes from NativeID,
// not an attribute.
func resolveStorageGatewayDevices(acct *account, st *store.Store) error {
	gwSet, err := scannedIDSet(acct, st, TypeStorageGatewayGateway)
	if err != nil {
		return err
	}
	if len(gwSet) == 0 {
		return nil
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeStorageGatewayDevice},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/device/")
		if i < 0 {
			continue
		}
		gwARN := r.NativeID[:i]
		tgt := store.ResourceID("aws", acct.ID, gwARN)
		if !gwSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert storagegateway device→gateway: %w", err)
		}
	}
	return nil
}
