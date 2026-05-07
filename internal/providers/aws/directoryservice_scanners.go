package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:directory-service",
		fn:   scanDirectoryService,
		emits: []coverage.TypeDecl{
			{Service: "directory-service", DiscoType: TypeDSMicrosoftAD},
			{Service: "directory-service", DiscoType: TypeDSSimpleAD},
		},
	})
}

// scanDirectoryService discovers Directory Service Microsoft AD and Simple
// AD directories. DescribeDirectories returns all types; filter by
// DirectoryType for the two CFN-resource shapes (AD Connector + Shared
// Microsoft AD intentionally skipped — not customer-creatable as CFN
// resources).
func scanDirectoryService(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := directoryservice.NewFromConfig(acct.cfg, func(o *directoryservice.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeDirectories(ctx, &directoryservice.DescribeDirectoriesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ds:DescribeDirectories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ds:DescribeDirectories: %w", err)
		}
		for _, d := range out.DirectoryDescriptions {
			id := sv(d.DirectoryId)
			if id == "" {
				continue
			}
			var discoType string
			switch d.Type {
			case types.DirectoryTypeMicrosoftAd, types.DirectoryTypeSharedMicrosoftAd:
				discoType = TypeDSMicrosoftAD
			case types.DirectoryTypeSimpleAd:
				discoType = TypeDSSimpleAD
			default:
				continue
			}
			arn := fmt.Sprintf("arn:aws:ds:%s:%s:directory/%s", region, acct.ID, id)
			status := string(d.Stage)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: discoType, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "directory-service directories")
}
