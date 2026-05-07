package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/internetmonitor"
)

func init() {
	registerService(serviceEntry{
		name: "aws:internet-monitor",
		fn:   scanInternetMonitor,
		emits: []coverage.TypeDecl{
			{Service: "internet-monitor", DiscoType: TypeInternetMonitorMonitor},
		},
	})
}

// scanInternetMonitor discovers CloudWatch Internet Monitor monitors.
func scanInternetMonitor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := internetmonitor.NewFromConfig(acct.cfg, func(o *internetmonitor.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListMonitors(ctx, &internetmonitor.ListMonitorsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "internet-monitor:ListMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("internet-monitor:ListMonitors: %w", err)
		}
		for _, m := range out.Monitors {
			arn := sv(m.MonitorArn)
			if arn == "" {
				continue
			}
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInternetMonitorMonitor, NativeID: arn,
				Name: m.MonitorName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "internet-monitor monitors")
}
