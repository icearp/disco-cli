package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ssmquicksetup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:ssm-quick-setup",
		fn:   scanSSMQuickSetup,
		emits: []coverage.TypeDecl{
			{Service: "ssm-quick-setup", DiscoType: TypeSSMQuickSetupConfigurationManager},
		},
	})
}

type ssmQuickSetupAPI interface {
	ListConfigurationManagers(context.Context, *ssmquicksetup.ListConfigurationManagersInput, ...func(*ssmquicksetup.Options)) (*ssmquicksetup.ListConfigurationManagersOutput, error)
}

// scanSSMQuickSetup discovers SSM Quick Setup configuration managers.
// LifecycleAutomation is a sub-resource of a ConfigurationManager with no
// listable endpoint — skip-logged.
func scanSSMQuickSetup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmquicksetup.NewFromConfig(acct.cfg, func(o *ssmquicksetup.Options) { o.Region = region })
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListConfigurationManagers(ctx, &ssmquicksetup.ListConfigurationManagersInput{StartingToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-quicksetup:ListConfigurationManagers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-quicksetup:ListConfigurationManagers: %w", err)
		}
		for _, c := range out.ConfigurationManagersList {
			arn := sv(c.ManagerArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMQuickSetupConfigurationManager, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ssm-quick-setup configuration-managers")
}
