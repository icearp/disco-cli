package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloud9"
	cloud9types "github.com/aws/aws-sdk-go-v2/service/cloud9/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cloud9",
		fn:   scanCloud9,
		emits: []coverage.TypeDecl{
			{Service: "cloud9", DiscoType: TypeCloud9EnvironmentEC2},
		},
	})
}

// scanCloud9 discovers Cloud9 EC2 environments. Cloud9 is closed to new
// customers (2024-07-31) but existing tenants continue to use it. ListEnvironments
// returns IDs only — DescribeEnvironments fan-out fills in ARN+Type. Only Type=ec2
// rows emit (CFN models only AWS::Cloud9::EnvironmentEC2; SSH variant has no CFN type).
func scanCloud9(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloud9.NewFromConfig(acct.cfg, func(o *cloud9.Options) { o.Region = region })

	var ids []string
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &cloud9.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			// Cloud9 is closed to new customers (2024-07-31). Accounts that
			// never onboarded surface the closed state two ways: an
			// empty-message AccessDeniedException, or one with the explicit
			// body "This account does not have access to the Cloud9 service".
			// The account can't self-enable it — mark not-entitled so the
			// dispatcher renders (account: not entitled). Existing
			// tenants with real IAM gaps still carry an action-identifying
			// message and surface via skipIfAccessDenied below.
			if isClosedToNewCustomers(err) ||
				isAccessDeniedWithMessage(err, "does not have access to the Cloud9 service") {
				return 0, 0, markServiceNotEntitled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloud9:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloud9:ListEnvironments: %w", err)
		}
		ids = append(ids, out.EnvironmentIds...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}

	var batch []*store.Resource
	// DescribeEnvironments accepts up to 25 IDs per call.
	for i := 0; i < len(ids); i += 25 {
		end := i + 25
		if end > len(ids) {
			end = len(ids)
		}
		out, err := client.DescribeEnvironments(ctx, &cloud9.DescribeEnvironmentsInput{
			EnvironmentIds: ids[i:end],
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloud9:DescribeEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloud9:DescribeEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			if e.Type != cloud9types.EnvironmentTypeEc2 {
				continue
			}
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloud9EnvironmentEC2, NativeID: arn,
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloud9 environments")
}
