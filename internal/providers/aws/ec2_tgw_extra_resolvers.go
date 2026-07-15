package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveTGWPolicyTableRelationships,
		EdgeDecl{TypeEC2TransitGatewayPolicyTable, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveTGWRouteTableAnnouncementRelationships,
		EdgeDecl{TypeEC2TransitGatewayRouteTableAnnouncement, TypeEC2TransitGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayRouteTableAnnouncement, TypeEC2TransitGatewayRouteTable, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayRouteTableAnnouncement, TypeEC2TransitGatewayPeeringAttachment, store.RelAttachedTo},
	)
}

// resolveTGWPolicyTableRelationships wires each TGW policy table to its
// transit gateway.
func resolveTGWPolicyTableRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayPolicyTable}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgwSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TransitGatewayID *string `json:"TransitGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.TransitGatewayID); id != "" {
			tgwID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "transit-gateway", id))
			if tgwSet[tgwID] {
				if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw policy-table→tgw: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveTGWRouteTableAnnouncementRelationships wires each announcement to its
// transit gateway, source route table, and the peering attachment it advertises
// across.
func resolveTGWRouteTableAnnouncementRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayRouteTableAnnouncement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgwSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	rtSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayRouteTable)
	if err != nil {
		return err
	}
	peerSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayPeeringAttachment)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TransitGatewayID           *string `json:"TransitGatewayId"`
			TransitGatewayRouteTableID *string `json:"TransitGatewayRouteTableId"`
			PeeringAttachmentID        *string `json:"PeeringAttachmentId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.TransitGatewayID); id != "" {
			tgwID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "transit-gateway", id))
			if tgwSet[tgwID] {
				if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw announcement→tgw: %w", err)
				}
			}
		}
		if id := sv(attrs.TransitGatewayRouteTableID); id != "" {
			rtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "transit-gateway-route-table", id))
			if rtSet[rtID] {
				if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw announcement→route-table: %w", err)
				}
			}
		}
		if id := sv(attrs.PeeringAttachmentID); id != "" {
			pID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "transit-gateway-peering-attachment", id))
			if peerSet[pID] {
				if err := st.UpsertRelationship(r.ID, pID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw announcement→peering-attachment: %w", err)
				}
			}
		}
	}
	return nil
}
