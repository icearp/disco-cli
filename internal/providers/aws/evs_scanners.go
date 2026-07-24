package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/evs"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEVSEnvironment, Service: "evs", Upstream: "AWS::EVS::Environment", Leaf: true})
	registerService(serviceEntry{
		name: "aws:evs",
		fn:   scanEVS,
	})
}

// scanEVS discovers Elastic VMware Service (EVS) environments.
func scanEVS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := evs.NewFromConfig(acct.cfg, func(o *evs.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &evs.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "evs:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("evs:ListEnvironments: %w", err)
		}
		for _, e := range out.EnvironmentSummaries {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			status := string(e.EnvironmentState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEVSEnvironment, NativeID: arn,
				Name: e.EnvironmentName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "evs environments")
}
