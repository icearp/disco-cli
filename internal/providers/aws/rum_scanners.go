package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/rum"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRUMAppMonitor, Service: "rum", Upstream: "AWS::RUM::AppMonitor", Leaf: true})
	registerService(serviceEntry{
		name: "aws:rum",
		fn:   scanRUM,
	})
}

// scanRUM discovers CloudWatch RUM app monitors. Synth ARN:
// arn:aws:rum:{r}:{a}:appmonitor/{name}.
func scanRUM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rum.NewFromConfig(acct.cfg, func(o *rum.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListAppMonitors(ctx, &rum.ListAppMonitorsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "rum:ListAppMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("rum:ListAppMonitors: %w", err)
		}
		for _, m := range out.AppMonitorSummaries {
			name := sv(m.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:rum:%s:%s:appmonitor/%s", region, acct.ID, name)
			status := string(m.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRUMAppMonitor, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "rum app-monitors")
}
