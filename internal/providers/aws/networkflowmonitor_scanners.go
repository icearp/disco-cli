package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/networkflowmonitor"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkFlowMonitorMonitor, Service: "networkflowmonitor", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkFlowMonitorScope, Service: "networkflowmonitor", Leaf: true})
	registerService(serviceEntry{
		name: "aws:networkflowmonitor",
		fn:   scanNetworkFlowMonitor,
	})
}

// scanNetworkFlowMonitor discovers Network Flow Monitor monitors and the
// per-account monitoring scopes (which accounts/regions are observed).
func scanNetworkFlowMonitor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := networkflowmonitor.NewFromConfig(acct.cfg, func(o *networkflowmonitor.Options) { o.Region = region })

	mt, mi, err := scanNFMMonitors(ctx, client, acct, region, st, scanID)
	if err != nil {
		return mt, mi, err
	}
	st2, si, err := scanNFMScopes(ctx, client, acct, region, st, scanID)
	return mt + st2, mi + si, err
}

func scanNFMMonitors(ctx context.Context, client *networkflowmonitor.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := networkflowmonitor.NewListMonitorsPaginator(client, &networkflowmonitor.ListMonitorsInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkflowmonitor:ListMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkflowmonitor:ListMonitors: %w", err)
		}
		for _, m := range out.Monitors {
			arn := sv(m.MonitorArn)
			if arn == "" {
				continue
			}
			status := string(m.MonitorStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFlowMonitorMonitor, NativeID: arn,
				Name: m.MonitorName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkflowmonitor monitors")
}

func scanNFMScopes(ctx context.Context, client *networkflowmonitor.Client, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := networkflowmonitor.NewListScopesPaginator(client, &networkflowmonitor.ListScopesInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkflowmonitor:ListScopes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkflowmonitor:ListScopes: %w", err)
		}
		for _, s := range out.Scopes {
			arn := sv(s.ScopeArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			name := sv(s.ScopeId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFlowMonitorScope, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkflowmonitor scopes")
}
