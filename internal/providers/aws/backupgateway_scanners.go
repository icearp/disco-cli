package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
)

func init() {
	registerService(serviceEntry{
		name: "aws:backupgateway",
		fn:   scanBackupGateway,
		emits: []coverage.TypeDecl{
			{Service: "backupgateway", DiscoType: TypeBackupGatewayHypervisor},
		},
	})
}

// backupGatewayAPI is the narrow surface scanBackupGateway uses.
type backupGatewayAPI interface {
	ListHypervisors(context.Context, *backupgateway.ListHypervisorsInput, ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error)
}

func scanBackupGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := backupgateway.NewFromConfig(acct.cfg, func(o *backupgateway.Options) { o.Region = region })
	return scanBackupGatewayHypervisors(ctx, client, acct, region, st, scanID)
}

func scanBackupGatewayHypervisors(ctx context.Context, client backupGatewayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := backupgateway.NewListHypervisorsPaginator(client, &backupgateway.ListHypervisorsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "backupgateway:ListHypervisors", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("backupgateway:ListHypervisors: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Hypervisors))
		for _, h := range page.Hypervisors {
			arn := sv(h.HypervisorArn)
			if arn == "" {
				continue
			}
			name := sv(h.Name)
			status := string(h.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBackupGatewayHypervisor,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(h),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert backupgateway hypervisors: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
