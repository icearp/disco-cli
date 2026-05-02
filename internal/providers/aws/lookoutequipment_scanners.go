package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lookoutequipment"
)

func init() {
	registerService(serviceEntry{
		name: "aws:lookout-equipment",
		fn:   scanLookoutEquipment,
		emits: []coverage.TypeDecl{
			{Service: "lookout-equipment", DiscoType: TypeLookoutEquipmentInferenceScheduler},
		},
	})
}

type lookoutEquipmentAPI interface {
	ListInferenceSchedulers(context.Context, *lookoutequipment.ListInferenceSchedulersInput, ...func(*lookoutequipment.Options)) (*lookoutequipment.ListInferenceSchedulersOutput, error)
}

// scanLookoutEquipment discovers Lookout for Equipment inference schedulers.
func scanLookoutEquipment(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lookoutequipment.NewFromConfig(acct.cfg, func(o *lookoutequipment.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListInferenceSchedulers(ctx, &lookoutequipment.ListInferenceSchedulersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lookoutequipment:ListInferenceSchedulers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lookoutequipment:ListInferenceSchedulers: %w", err)
		}
		for _, s := range out.InferenceSchedulerSummaries {
			arn := sv(s.InferenceSchedulerArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLookoutEquipmentInferenceScheduler, NativeID: arn,
				Name: s.InferenceSchedulerName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "lookout-equipment inference-schedulers")
}
