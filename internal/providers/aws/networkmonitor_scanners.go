package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/networkmonitor"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:networkmonitor",
		fn:   scanNetworkMonitor,
		emits: []coverage.TypeDecl{
			{Service: "networkmonitor", DiscoType: TypeNetworkMonitorMonitor, Leaf: true},
			{Service: "networkmonitor", DiscoType: TypeNetworkMonitorProbe, Leaf: true},
		},
	})
}

// scanNetworkMonitor discovers CloudWatch Network Monitor monitors and their
// probes. Probes are embedded in the GetMonitor response, so each monitor is
// described per-row to surface its probes as standalone resources.
func scanNetworkMonitor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := networkmonitor.NewFromConfig(acct.cfg, func(o *networkmonitor.Options) { o.Region = region })

	var monitorBatch []*store.Resource
	var names []string
	p := networkmonitor.NewListMonitorsPaginator(client, &networkmonitor.ListMonitorsInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkmonitor:ListMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkmonitor:ListMonitors: %w", err)
		}
		for _, m := range out.Monitors {
			arn := sv(m.MonitorArn)
			if arn == "" {
				continue
			}
			state := string(m.State)
			monitorBatch = append(monitorBatch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkMonitorMonitor, NativeID: arn,
				Name: m.MonitorName, Region: &region, Status: &state,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
			names = append(names, sv(m.MonitorName))
		}
	}

	total, inserted, err = upsertBatch(st, monitorBatch, "networkmonitor monitors")
	if err != nil {
		return total, inserted, err
	}

	probeBatch, err := scanNetworkMonitorProbes(ctx, client, acct, region, st, scanID, names)
	if err != nil {
		return total, inserted, err
	}
	pt, pi, err := upsertBatch(st, probeBatch, "networkmonitor probes")
	return total + pt, inserted + pi, err
}

func scanNetworkMonitorProbes(ctx context.Context, client *networkmonitor.Client, acct *account, region string, st *store.Store, scanID string, monitorNames []string) ([]*store.Resource, error) {
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, name := range monitorNames {
		if name == "" {
			continue
		}
		g.Go(func() error {
			out, err := client.GetMonitor(gctx, &networkmonitor.GetMonitorInput{MonitorName: &name})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "networkmonitor:GetMonitor", acct.ID, region, err)
					return nil
				}
				return fmt.Errorf("networkmonitor:GetMonitor %s: %w", name, err)
			}
			monitorArn := sv(out.MonitorArn)
			var rows []*store.Resource
			for _, pr := range out.Probes {
				arn := sv(pr.ProbeArn)
				if arn == "" {
					// Probes are not always issued a standalone ARN; synthesize one
					// off the monitor ARN so the row stays addressable.
					arn = monitorArn + "/probe/" + sv(pr.ProbeId)
				}
				if arn == "" || arn == "/probe/" {
					continue
				}
				state := string(pr.State)
				label := sv(pr.ProbeId)
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkMonitorProbe, NativeID: arn,
					Name: &label, Region: &region, Status: &state,
					AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
				})
			}
			if len(rows) > 0 {
				mu.Lock()
				batch = append(batch, rows...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return batch, nil
}
