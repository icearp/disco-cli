package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveMediaConnectBridgeChildren,
		EdgeDecl{TypeMediaConnectBridgeOutput, TypeMediaConnectBridge, store.RelAttachedTo},
		EdgeDecl{TypeMediaConnectBridgeSource, TypeMediaConnectBridge, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaConnectFlowVpcInterface,
		EdgeDecl{TypeMediaConnectFlowVpcInterface, TypeMediaConnectFlow, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaConnectFlowChildren,
		EdgeDecl{TypeMediaConnectFlowSource, TypeMediaConnectFlow, store.RelAttachedTo},
		EdgeDecl{TypeMediaConnectFlowOutput, TypeMediaConnectFlow, store.RelAttachedTo},
		EdgeDecl{TypeMediaConnectFlowEntitlement, TypeMediaConnectFlow, store.RelAttachedTo},
	)
	registerResolver(
		resolveMediaConnectBridgePlacement,
		EdgeDecl{TypeMediaConnectBridge, TypeMediaConnectGateway, store.RelAttachedTo},
	)
}

// resolveMediaConnectBridgeChildren attaches bridge-output / bridge-source to
// their parent bridge. NativeID shape: `{bridgeARN}/output/{name}` or
// `{bridgeARN}/source/{name}`.
func resolveMediaConnectBridgeChildren(acct *account, st *store.Store) error {
	bridgeSet, err := scannedIDSet(acct, st, TypeMediaConnectBridge)
	if err != nil {
		return err
	}
	for _, ctype := range []string{TypeMediaConnectBridgeOutput, TypeMediaConnectBridgeSource} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := mcBridgeARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if !bridgeSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert mc %s→bridge: %w", ctype, err)
			}
		}
	}
	return nil
}

// mcBridgeARNFromChild trims a trailing `/output/...` or `/source/...` from a
// bridge child NativeID to recover the parent bridge ARN.
func mcBridgeARNFromChild(arn string) string {
	for _, seg := range []string{"/output/", "/source/"} {
		if i := strings.Index(arn, seg); i >= 0 {
			return arn[:i]
		}
	}
	return ""
}

// resolveMediaConnectFlowVpcInterface attaches each VPC interface to its
// parent flow via `{flowARN}/vpc-interface/{name}`.
func resolveMediaConnectFlowVpcInterface(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaConnectFlowVpcInterface},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	flowSet, err := scannedIDSet(acct, st, TypeMediaConnectFlow)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.LastIndex(r.NativeID, "/vpc-interface/")
		if i < 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !flowSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mc-flow-vpc-iface→flow: %w", err)
		}
	}
	return nil
}

// resolveMediaConnectFlowChildren walks each flow's nested
// Sources[].SourceArn / Outputs[].OutputArn / Entitlements[].EntitlementArn
// and emits attached-to edges from child→flow. The flow row's NativeID is
// the flow ARN; the children carry their own real AWS ARNs, distinct from
// the flow's, so the link must come from the flow side.
func resolveMediaConnectFlowChildren(acct *account, st *store.Store) error {
	flows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaConnectFlow},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(flows) == 0 {
		return nil
	}
	srcSet, err := scannedIDSet(acct, st, TypeMediaConnectFlowSource)
	if err != nil {
		return err
	}
	outSet, err := scannedIDSet(acct, st, TypeMediaConnectFlowOutput)
	if err != nil {
		return err
	}
	entSet, err := scannedIDSet(acct, st, TypeMediaConnectFlowEntitlement)
	if err != nil {
		return err
	}
	for _, fl := range flows {
		var attrs struct {
			Sources []struct {
				SourceArn *string `json:"SourceArn"`
			} `json:"Sources"`
			Outputs []struct {
				OutputArn *string `json:"OutputArn"`
			} `json:"Outputs"`
			Entitlements []struct {
				EntitlementArn *string `json:"EntitlementArn"`
			} `json:"Entitlements"`
		}
		if err := json.Unmarshal([]byte(fl.AttributesJSON), &attrs); err != nil {
			continue
		}
		emit := func(arn, ctype string, set map[string]bool) error {
			if arn == "" {
				return nil
			}
			cID := store.ResourceID("aws", acct.ID, arn)
			if !set[cID] {
				return nil
			}
			return st.UpsertRelationship(cID, fl.ID, store.RelAttachedTo, "directed", nil)
		}
		for _, s := range attrs.Sources {
			if err := emit(sv(s.SourceArn), TypeMediaConnectFlowSource, srcSet); err != nil {
				return fmt.Errorf("upsert mc-flow-source→flow: %w", err)
			}
		}
		for _, o := range attrs.Outputs {
			if err := emit(sv(o.OutputArn), TypeMediaConnectFlowOutput, outSet); err != nil {
				return fmt.Errorf("upsert mc-flow-output→flow: %w", err)
			}
		}
		for _, e := range attrs.Entitlements {
			if err := emit(sv(e.EntitlementArn), TypeMediaConnectFlowEntitlement, entSet); err != nil {
				return fmt.Errorf("upsert mc-flow-entitlement→flow: %w", err)
			}
		}
	}
	return nil
}

// resolveMediaConnectBridgePlacement wires each bridge to the placement
// gateway it runs on (PlacementArn — already on the ListedBridge summary
// shape, no Describe fan-out needed).
func resolveMediaConnectBridgePlacement(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeMediaConnectBridge}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	gwSet, err := scannedIDSet(acct, st, TypeMediaConnectGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PlacementArn *string `json:"PlacementArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ga := sv(attrs.PlacementArn)
		if ga == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, ga)
		if !gwSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mediaconnect bridge→gateway: %w", err)
		}
	}
	return nil
}
