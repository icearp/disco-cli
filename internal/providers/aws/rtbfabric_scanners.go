package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/rtbfabric"
)

func init() {
	registerService(serviceEntry{
		name: "aws:rtbfabric",
		fn:   scanRTBFabric,
		emits: []coverage.TypeDecl{
			{Service: "rtbfabric", DiscoType: TypeRTBFabricRequesterGateway, Leaf: true},
			{Service: "rtbfabric", DiscoType: TypeRTBFabricResponderGateway, Leaf: true},
			{Service: "rtbfabric", DiscoType: TypeRTBFabricLink, Leaf: true},
		},
	})
}

type rtbfabricAPI interface {
	ListRequesterGateways(context.Context, *rtbfabric.ListRequesterGatewaysInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListRequesterGatewaysOutput, error)
	ListResponderGateways(context.Context, *rtbfabric.ListResponderGatewaysInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListResponderGatewaysOutput, error)
	ListLinks(context.Context, *rtbfabric.ListLinksInput, ...func(*rtbfabric.Options)) (*rtbfabric.ListLinksOutput, error)
}

// scanRTBFabric discovers RTBFabric (real-time bidding fabric) requester and
// responder gateways plus the links between them. Gateways are returned as
// IDs; ARN synthesized as arn:aws:rtbfabric:{r}:{a}:gateway/{id}. Links
// fetched per gateway.
//
// AWS::RTBFabric::InboundExternalLink and OutboundExternalLink are
// skip-logged: the SDK exposes only Get* / Create* / Delete* per external
// link, with no list endpoint.
func scanRTBFabric(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rtbfabric.NewFromConfig(acct.cfg, func(o *rtbfabric.Options) { o.Region = region })

	reqIDs, t, i, ferr := scanRTBRequesterGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	respIDs, t, i, ferr := scanRTBResponderGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	gatewayIDs := append(append([]string{}, reqIDs...), respIDs...)
	for _, gid := range gatewayIDs {
		t, i, ferr = scanRTBLinks(ctx, client, acct, region, st, scanID, gid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanRTBRequesterGateways(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := rtbfabric.NewListRequesterGatewaysPaginator(client, &rtbfabric.ListRequesterGatewaysInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "rtbfabric:ListRequesterGateways", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("rtbfabric:ListRequesterGateways: %w", err)
		}
		for _, id := range out.GatewayIds {
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:rtbfabric:%s:%s:requester-gateway/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRTBFabricRequesterGateway, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"GatewayId": id}), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "rtbfabric requester-gateways")
	return ids, t, i, err
}

func scanRTBResponderGateways(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := rtbfabric.NewListResponderGatewaysPaginator(client, &rtbfabric.ListResponderGatewaysInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "rtbfabric:ListResponderGateways", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("rtbfabric:ListResponderGateways: %w", err)
		}
		for _, id := range out.GatewayIds {
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:rtbfabric:%s:%s:responder-gateway/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRTBFabricResponderGateway, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"GatewayId": id}), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "rtbfabric responder-gateways")
	return ids, t, i, err
}

func scanRTBLinks(ctx context.Context, client rtbfabricAPI, acct *account, region string, st *store.Store, scanID string, gatewayID string) (int, int, error) {
	gid := gatewayID
	pager := rtbfabric.NewListLinksPaginator(client, &rtbfabric.ListLinksInput{GatewayId: &gid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rtbfabric:ListLinks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rtbfabric:ListLinks: %w", err)
		}
		for _, l := range out.Links {
			lid := sv(l.LinkId)
			if lid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:rtbfabric:%s:%s:gateway/%s/link/%s", region, acct.ID, gid, lid)
			label := lid
			status := string(l.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRTBFabricLink, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "rtbfabric links")
}
