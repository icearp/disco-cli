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
		resolveCoipPoolRelationships,
		EdgeDecl{TypeEC2CoipPool, TypeEC2LocalGatewayRouteTable, store.RelAttachedTo},
	)
	registerResolver(
		resolveOutpostLagRelationships,
		EdgeDecl{TypeEC2OutpostLag, TypeEC2LocalGatewayVirtualInterface, store.RelAttachedTo},
	)
}

// resolveCoipPoolRelationships wires each CoIP pool to its local-gateway route
// table. Route tables are keyed by full ARN, so build an index by the short
// id parsed from the ":local-gateway-route-table/" segment.
func resolveCoipPoolRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2CoipPool}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2LocalGatewayRouteTable}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	rtByID := make(map[string]string, len(rtRows))
	for _, rt := range rtRows {
		if _, after, ok := strings.Cut(rt.NativeID, ":local-gateway-route-table/"); ok && after != "" {
			rtByID[after] = rt.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			LocalGatewayRouteTableID *string `json:"LocalGatewayRouteTableId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := rtByID[sv(attrs.LocalGatewayRouteTableID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert coip-pool→local-gateway-route-table: %w", err)
			}
		}
	}
	return nil
}

// resolveOutpostLagRelationships wires each Outpost LAG to the local-gateway
// virtual interfaces it aggregates.
func resolveOutpostLagRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2OutpostLag}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	viSet, err := scannedIDSet(acct, st, TypeEC2LocalGatewayVirtualInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LocalGatewayVirtualInterfaceIDs []string `json:"LocalGatewayVirtualInterfaceIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, viID := range attrs.LocalGatewayVirtualInterfaceIDs {
			if viID == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "local-gateway-virtual-interface", viID))
			if viSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert outpost-lag→local-gateway-virtual-interface: %w", err)
				}
			}
		}
	}
	return nil
}
