package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glacier"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlacierVault, Service: "glacier", Leaf: true})
	registerService(serviceEntry{
		name: "aws:glacier",
		fn:   scanGlacier,
	})
}

type glacierAPI interface {
	ListVaults(context.Context, *glacier.ListVaultsInput, ...func(*glacier.Options)) (*glacier.ListVaultsOutput, error)
}

// scanGlacier discovers S3 Glacier vaults. ListVaults requires an AccountId
// input; literal "-" resolves to the caller's account.
func scanGlacier(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := glacier.NewFromConfig(acct.cfg, func(o *glacier.Options) { o.Region = region })
	return scanGlacierVaults(ctx, client, acct, region, st, scanID)
}

func scanGlacierVaults(ctx context.Context, client glacierAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glacier.NewListVaultsPaginator(client, &glacier.ListVaultsInput{AccountId: sdkaws.String("-")})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glacier:ListVaults", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glacier:ListVaults: %w", err)
		}
		for _, v := range out.VaultList {
			arn := sv(v.VaultARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlacierVault, NativeID: arn,
				Name: v.VaultName, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "glacier vaults")
}
