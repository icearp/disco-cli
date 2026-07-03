package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/licensemanagerlinuxsubscriptions"
)

func init() {
	registerService(serviceEntry{
		name: "aws:license-manager-linux-subscriptions",
		fn:   scanLicenseManagerLinuxSubscriptions,
		emits: []coverage.TypeDecl{
			{Service: "license-manager-linux-subscriptions", DiscoType: TypeLicenseManagerLinuxSubscriptionsSubscriptionProvider, Leaf: true},
		},
	})
}

// licenseManagerLinuxSubscriptionsAPI is the narrow set of operations called by
// scanLicenseManagerLinuxSubscriptionsEntities. ListLinuxSubscriptions /
// ListLinuxSubscriptionInstances return usage data, not resources — only the
// registered BYOL subscription provider is the Service-Reference-listed resource.
type licenseManagerLinuxSubscriptionsAPI interface {
	ListRegisteredSubscriptionProviders(context.Context, *licensemanagerlinuxsubscriptions.ListRegisteredSubscriptionProvidersInput, ...func(*licensemanagerlinuxsubscriptions.Options)) (*licensemanagerlinuxsubscriptions.ListRegisteredSubscriptionProvidersOutput, error)
}

func scanLicenseManagerLinuxSubscriptions(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := licensemanagerlinuxsubscriptions.NewFromConfig(acct.cfg, func(o *licensemanagerlinuxsubscriptions.Options) { o.Region = region })
	return scanLicenseManagerLinuxSubscriptionsEntities(ctx, client, acct, region, st, scanID)
}

func scanLicenseManagerLinuxSubscriptionsEntities(ctx context.Context, client licenseManagerLinuxSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := licensemanagerlinuxsubscriptions.NewListRegisteredSubscriptionProvidersPaginator(client, &licensemanagerlinuxsubscriptions.ListRegisteredSubscriptionProvidersInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			// Account not onboarded to Linux subscriptions (discovery disabled) —
			// the whole service is inert, so mark it disabled instead of erroring.
			if isAPIErrorWithMessage(err, "ValidationException", "onboarded to Linux subscriptions") {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager-linux-subscriptions:ListRegisteredSubscriptionProviders", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager-linux-subscriptions:ListRegisteredSubscriptionProviders: %w", err)
		}
		for _, sp := range out.RegisteredSubscriptionProviders {
			arn := sv(sp.SubscriptionProviderArn)
			if arn == "" {
				continue
			}
			name := string(sp.SubscriptionProviderSource)
			status := string(sp.SubscriptionProviderStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerLinuxSubscriptionsSubscriptionProvider, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(sp), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "license-manager-linux-subscriptions subscription-providers")
}
