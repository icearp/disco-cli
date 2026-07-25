package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pipes"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePipesPipe, Service: "pipes"})
	registerService(serviceEntry{
		name: "aws:pipes",
		fn:   scanPipes,
	})
}

// scanPipes discovers EventBridge Pipes.
func scanPipes(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pipes.NewFromConfig(acct.cfg, func(o *pipes.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPipes(ctx, &pipes.ListPipesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pipes:ListPipes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pipes:ListPipes: %w", err)
		}
		for _, p := range out.Pipes {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			status := string(p.CurrentState)
			attrsJSON := mustJSON(p)
			if dout, derr := client.DescribePipe(ctx, &pipes.DescribePipeInput{Name: p.Name}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePipesPipe, NativeID: arn,
				Name: p.Name, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "pipes")
}
