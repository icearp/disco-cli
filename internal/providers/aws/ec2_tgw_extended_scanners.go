package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2TransitGatewayPolicyTableEntry, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2TransitGatewayPrefixListReference, Service: "ec2"})
}

const (
	ec2TGWPolicyTableEntriesOp   = "ec2:GetTransitGatewayPolicyTableEntries"
	ec2TGWPrefixListReferencesOp = "ec2:GetTransitGatewayPrefixListReferences"
	ec2TGWChildPageLimit         = 1000 // TransitGatewayMaxResults maximum
	// The codes EC2 is expected to return for a policy table or route table
	// deleted between its listing and its child call. Not observed live, so a
	// skip on either warns rather than passing silently.
	ec2TGWPolicyTableNotFound = "InvalidTransitGatewayPolicyTableId.NotFound"
	ec2TGWRouteTableNotFound  = "InvalidRouteTableID.NotFound"
	// ec2TGWParentDeleted is the State a deleted policy table or route table
	// keeps while DescribeTransitGateway*Tables still returns it.
	ec2TGWParentDeleted = "deleted"
)

// errTGWParentNativeID marks a stored parent whose NativeID lacks the
// ec2ARN `{kind}/{id}` shape its own scanner writes.
var errTGWParentNativeID = errors.New("transit gateway parent NativeID is not an ec2ARN of its kind")

// tgwScannedParents returns the parentType rows (policy tables or route
// tables) this scan stored in region, keyed by the id ec2ARN put after kind,
// except those whose Status says they are deleted. Both counts are bounded by
// per-transit-gateway quotas, so the rows are read in one call.
func tgwScannedParents(st *store.Store, acct *account, region, scanID, parentType, kind string) ([]childParent, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{parentType},
		Regions: []string{region}, SeenBy: scanID, Limit: util.AllResources,
	})
	if err != nil {
		return nil, fmt.Errorf("list %s parents: %w", parentType, err)
	}
	var parents []childParent
	for _, r := range rows {
		_, id, ok := strings.Cut(r.NativeID, ":"+kind+"/")
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: %s %q", errTGWParentNativeID, parentType, r.NativeID)
		}
		if sv(r.Status) == ec2TGWParentDeleted {
			continue
		}
		parents = append(parents, childParent{id: id, arn: r.NativeID})
	}
	return parents, nil
}

// tgwIncorrectState is the code EC2 returns for an action its target's state
// does not allow. Whether a pending or deleting table answers a child listing
// with it is unverified, so a skip on it warns.
const tgwIncorrectState = "IncorrectState"

var (
	tgwPolicyTableSkipCodes = []skipCode{
		{ec2TGWPolicyTableNotFound, "a policy table deleted since it was listed"},
		{tgwIncorrectState, "a pending or deleting policy table may refuse the call (unverified)"},
	}
	tgwRouteTableSkipCodes = []skipCode{
		{ec2TGWRouteTableNotFound, "a route table deleted since it was listed"},
		{tgwIncorrectState, "a pending or deleting route table may refuse the call (unverified)"},
	}
)

// scanTGWChildren stores, under each parent, the rows list returns for it. A
// parent failing with one of codes is skipped, with one warning per code; a
// denial or a transient failure is skipped with one warning; anything else
// fails the phase. Rows other parents stored stay.
func scanTGWChildren(ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	what string, codes []skipCode, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	return childFanOutStoredSkipCodes(ctx, st, acct, region, op, parents, func(error) bool { return false }, byParentID, what, codes,
		func(ctx context.Context, p childParent) (int, int, error) {
			rows, err := list(ctx, p)
			if err != nil {
				return 0, 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			children := make([]containedChild, len(rows))
			for i, r := range rows {
				children[i] = containedChild{row: r, parentID: parentID}
			}
			return upsertContained(st, op, children)
		})
}

// tgwPolicyTableEntryNativeID builds the NativeID of one policy table entry.
// AWS documents no ARN for an entry; the rule number is the key
// DeleteTransitGatewayPolicyTableEntry takes within its policy table.
func tgwPolicyTableEntryNativeID(policyTableARN, ruleNumber string) string {
	return policyTableARN + "/entry/" + ruleNumber
}

// tgwPrefixListReferenceNativeID builds the NativeID of one prefix list
// reference. AWS documents no ARN for a reference; the prefix list id is the
// key DeleteTransitGatewayPrefixListReference takes within its route table.
func tgwPrefixListReferenceNativeID(routeTableARN, prefixListID string) string {
	return routeTableARN + "/prefix-list-reference/" + prefixListID
}

// scanEC2TGWPolicyTableEntries stores, under each policy table this scan
// stored in region, the entries the table returns. Deleted entries are
// dropped.
func scanEC2TGWPolicyTableEntries(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tables, err := tgwScannedParents(st, acct, region, scanID, TypeEC2TransitGatewayPolicyTable, "transit-gateway-policy-table")
	if err != nil {
		return 0, 0, err
	}
	return scanTGWChildren(ctx, st, acct, region, ec2TGWPolicyTableEntriesOp, tables, "policy table(s)", tgwPolicyTableSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return listTGWPolicyTableEntries(ctx, client, acct, region, scanID, p)
		})
}

func listTGWPolicyTableEntries(ctx context.Context, client ec2API, acct *account, region, scanID string, p childParent) ([]*store.Resource, error) {
	pager := ec2.NewGetTransitGatewayPolicyTableEntriesPaginator(client,
		&ec2.GetTransitGatewayPolicyTableEntriesInput{TransitGatewayPolicyTableId: &p.id},
		func(o *ec2.GetTransitGatewayPolicyTableEntriesPaginatorOptions) {
			o.Limit = ec2TGWChildPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range page.TransitGatewayPolicyTableEntries {
			rule := sv(e.PolicyRuleNumber)
			if rule == "" || e.State == ec2types.TransitGatewayPolicyTableEntryStateDeleted {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2TransitGatewayPolicyTableEntry,
				NativeID:       tgwPolicyTableEntryNativeID(p.arn, rule),
				Name:           e.PolicyRuleNumber,
				Region:         &region,
				Status:         nonEmptyPtr(string(e.State)),
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	return rows, nil
}

// scanEC2TGWPrefixListReferences stores, under each transit gateway route
// table this scan stored in region, the prefix list references the table
// returns. The model has no deleted state for a reference, so none is
// dropped.
func scanEC2TGWPrefixListReferences(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tables, err := tgwScannedParents(st, acct, region, scanID, TypeEC2TransitGatewayRouteTable, "transit-gateway-route-table")
	if err != nil {
		return 0, 0, err
	}
	return scanTGWChildren(ctx, st, acct, region, ec2TGWPrefixListReferencesOp, tables, "route table(s)", tgwRouteTableSkipCodes,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return listTGWPrefixListReferences(ctx, client, acct, region, scanID, p)
		})
}

func listTGWPrefixListReferences(ctx context.Context, client ec2API, acct *account, region, scanID string, p childParent) ([]*store.Resource, error) {
	pager := ec2.NewGetTransitGatewayPrefixListReferencesPaginator(client,
		&ec2.GetTransitGatewayPrefixListReferencesInput{TransitGatewayRouteTableId: &p.id},
		func(o *ec2.GetTransitGatewayPrefixListReferencesPaginatorOptions) {
			o.Limit = ec2TGWChildPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, ref := range page.TransitGatewayPrefixListReferences {
			plID := sv(ref.PrefixListId)
			if plID == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2TransitGatewayPrefixListReference,
				NativeID:       tgwPrefixListReferenceNativeID(p.arn, plID),
				Name:           ref.PrefixListId,
				Region:         &region,
				Status:         nonEmptyPtr(string(ref.State)),
				AttributesJSON: mustJSON(ref),
				DiscoveredBy:   scanID,
			})
		}
	}
	return rows, nil
}
