package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveTGWPolicyTableEntryRelationships,
		EdgeDecl{TypeEC2TransitGatewayPolicyTable, TypeEC2TransitGatewayPolicyTableEntry, store.RelContains},
		EdgeDecl{TypeEC2TransitGatewayPolicyTableEntry, TypeEC2TransitGatewayRouteTable, store.RelAttachedTo},
	)
	registerResolver(
		resolveTGWPrefixListReferenceRelationships,
		EdgeDecl{TypeEC2TransitGatewayRouteTable, TypeEC2TransitGatewayPrefixListReference, store.RelContains},
		EdgeDecl{TypeEC2TransitGatewayPrefixListReference, TypeEC2PrefixList, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayPrefixListReference, TypeEC2TransitGatewayAttachment, store.RelAttachedTo},
	)
}

// resolveTGWPolicyTableEntryRelationships links each policy table entry to
// the scanned route table it targets, in the entry's Region. The
// policy-table→entry contains pairs are recorded by
// scanEC2TGWPolicyTableEntries. A row whose attributes do not decode is
// reported and costs no other row its edge.
func resolveTGWPolicyTableEntryRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayPolicyTableEntry}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayRouteTable)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			TargetRouteTableID *string `json:"TargetRouteTableId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode transit-gateway-policy-table-entry %s attributes: %w", r.NativeID, err))
			continue
		}
		id := sv(attrs.TargetRouteTableID)
		if id == "" {
			continue
		}
		rtID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), sv(r.Region), acct.ID, "transit-gateway-route-table", id))
		if rtSet[rtID] {
			if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw policy-table-entry→route-table: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveTGWPrefixListReferenceRelationships links each prefix list reference
// to the scanned prefix list it names and the scanned attachment it routes
// to. The route-table→reference contains pairs are recorded by
// scanEC2TGWPrefixListReferences. A row whose attributes do not decode is
// reported and costs no other row its edges.
func resolveTGWPrefixListReferenceRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayPrefixListReference}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	prefixLists, err := prefixListsByRegionID(acct, st)
	if err != nil {
		return err
	}
	attSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayAttachment)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			PrefixListID             *string `json:"PrefixListId"`
			TransitGatewayAttachment *struct {
				TransitGatewayAttachmentID *string `json:"TransitGatewayAttachmentId"`
			} `json:"TransitGatewayAttachment"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode transit-gateway-prefix-list-reference %s attributes: %w", r.NativeID, err))
			continue
		}
		region := sv(r.Region)
		if plID, ok := prefixLists[region+"/"+sv(attrs.PrefixListID)]; ok {
			if err := st.UpsertRelationship(r.ID, plID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw prefix-list-reference→prefix-list: %w", err)
			}
		}
		if attrs.TransitGatewayAttachment == nil {
			continue
		}
		if id := sv(attrs.TransitGatewayAttachment.TransitGatewayAttachmentID); id != "" {
			attID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "transit-gateway-attachment", id))
			if attSet[attID] {
				if err := st.UpsertRelationship(r.ID, attID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw prefix-list-reference→attachment: %w", err)
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// prefixListsByRegionID indexes the scanned prefix lists by "{region}/{id}".
// A reference names its prefix list by id alone, and the list's ARN carries
// its owner, which may be another account or AWS itself, so the ARN cannot be
// rebuilt from the reference. IncludeManaged: AWS-owned prefix lists are
// provider-managed rows.
func prefixListsByRegionID(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2PrefixList},
		IncludeManaged: true, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, id, ok := strings.Cut(r.NativeID, ":prefix-list/"); ok && id != "" {
			idx[sv(r.Region)+"/"+id] = r.ID
		}
	}
	return idx, nil
}
