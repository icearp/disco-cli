package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions"
	lmustypes "github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:license-manager-user-subscriptions",
		fn:   scanLicenseManagerUserSubscriptions,
		emits: []coverage.TypeDecl{
			{Service: "license-manager-user-subscriptions", DiscoType: TypeLicenseManagerUserSubscriptionsIdentityProvider, Leaf: true},
			{Service: "license-manager-user-subscriptions", DiscoType: TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, Leaf: true},
			{Service: "license-manager-user-subscriptions", DiscoType: TypeLicenseManagerUserSubscriptionsProductSubscription, Leaf: true},
			{Service: "license-manager-user-subscriptions", DiscoType: TypeLicenseManagerUserSubscriptionsInstanceUser, Leaf: true},
		},
	})
}

// licenseManagerUserSubscriptionsAPI is the narrow op set the
// user-subscriptions scanner calls. ListProductSubscriptions requires an
// IdentityProvider input (fanned out from ListIdentityProviders);
// ListUserAssociations requires InstanceId + IdentityProvider (fanned out
// from ListInstances, whose summaries carry both).
type licenseManagerUserSubscriptionsAPI interface {
	ListIdentityProviders(context.Context, *licensemanagerusersubscriptions.ListIdentityProvidersInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListIdentityProvidersOutput, error)
	ListLicenseServerEndpoints(context.Context, *licensemanagerusersubscriptions.ListLicenseServerEndpointsInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListLicenseServerEndpointsOutput, error)
	ListProductSubscriptions(context.Context, *licensemanagerusersubscriptions.ListProductSubscriptionsInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListProductSubscriptionsOutput, error)
	ListInstances(context.Context, *licensemanagerusersubscriptions.ListInstancesInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListInstancesOutput, error)
	ListUserAssociations(context.Context, *licensemanagerusersubscriptions.ListUserAssociationsInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListUserAssociationsOutput, error)
}

func scanLicenseManagerUserSubscriptions(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := licensemanagerusersubscriptions.NewFromConfig(acct.cfg, func(o *licensemanagerusersubscriptions.Options) { o.Region = region })
	return scanLicenseManagerUserSubscriptionsEntities(ctx, client, acct, region, st, scanID)
}

func scanLicenseManagerUserSubscriptionsEntities(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// identity providers — also the fan-out driver for product subscriptions.
	providers, t, i, ferr := scanLMUSIdentityProviders(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLMUSLicenseServerEndpoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanLMUSProductSubscriptions(ctx, client, acct, region, st, scanID, providers)
		},
		func() (int, int, error) { return scanLMUSInstanceUsers(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr = phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanLMUSIdentityProviders upserts identity providers and returns the
// IdentityProvider union values for fan-out into ListProductSubscriptions.
func scanLMUSIdentityProviders(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) ([]lmustypes.IdentityProvider, int, int, error) {
	var batch []*store.Resource
	var providers []lmustypes.IdentityProvider
	p := licensemanagerusersubscriptions.NewListIdentityProvidersPaginator(client, &licensemanagerusersubscriptions.ListIdentityProvidersInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			// Missing service-linked role means the account never activated User
			// Subscriptions; every op fails the same way. Self-enableable (register
			// an identity provider) → (account: disabled); the sentinel halts
			// sibling phases that would otherwise each re-warn.
			if isAccessDeniedWithMessage(err, "Service Linked role is not present") {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "license-manager-user-subscriptions:ListIdentityProviders", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("license-manager-user-subscriptions:ListIdentityProviders: %w", err)
		}
		for _, s := range out.IdentityProviderSummaries {
			if s.IdentityProvider != nil {
				providers = append(providers, s.IdentityProvider)
			}
			arn := sv(s.IdentityProviderArn)
			if arn == "" {
				continue
			}
			status := sv(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsIdentityProvider, NativeID: arn,
				Name: s.Product, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "license-manager-user-subscriptions identity-providers")
	return providers, t, i, err
}

func scanLMUSLicenseServerEndpoints(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := licensemanagerusersubscriptions.NewListLicenseServerEndpointsPaginator(client, &licensemanagerusersubscriptions.ListLicenseServerEndpointsInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager-user-subscriptions:ListLicenseServerEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager-user-subscriptions:ListLicenseServerEndpoints: %w", err)
		}
		for _, e := range out.LicenseServerEndpoints {
			arn := sv(e.LicenseServerEndpointArn)
			if arn == "" {
				continue
			}
			status := string(e.LicenseServerEndpointProvisioningStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, NativeID: arn,
				Name: e.LicenseServerEndpointId, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "license-manager-user-subscriptions license-server-endpoints")
}

// scanLMUSProductSubscriptions fans out ListProductSubscriptions over the
// identity providers discovered upstream — the op rejects a blanket call,
// requiring an IdentityProvider input.
func scanLMUSProductSubscriptions(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string, providers []lmustypes.IdentityProvider) (int, int, error) {
	var batch []*store.Resource
	for _, ip := range providers {
		p := licensemanagerusersubscriptions.NewListProductSubscriptionsPaginator(client, &licensemanagerusersubscriptions.ListProductSubscriptionsInput{IdentityProvider: ip})
		for p.HasMorePages() {
			out, err := p.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "license-manager-user-subscriptions:ListProductSubscriptions", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("license-manager-user-subscriptions:ListProductSubscriptions: %w", err)
			}
			for _, u := range out.ProductUserSummaries {
				arn := sv(u.ProductUserArn)
				if arn == "" {
					continue
				}
				status := sv(u.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeLicenseManagerUserSubscriptionsProductSubscription, NativeID: arn,
					Name: u.Username, Region: &region, Status: &status,
					AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "license-manager-user-subscriptions product-subscriptions")
}

// scanLMUSInstanceUsers fans out ListUserAssociations over the EC2 instances
// that provide user-based subscriptions — the op requires both an InstanceId
// and an IdentityProvider, and ListInstances summaries carry both.
func scanLMUSInstanceUsers(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	instances, err := listLMUSInstances(ctx, client, acct, region, st)
	if err != nil {
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.InstanceId == nil || inst.IdentityProvider == nil {
			continue
		}
		p := licensemanagerusersubscriptions.NewListUserAssociationsPaginator(client, &licensemanagerusersubscriptions.ListUserAssociationsInput{
			InstanceId:       inst.InstanceId,
			IdentityProvider: inst.IdentityProvider,
		})
		for p.HasMorePages() {
			out, err := p.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "license-manager-user-subscriptions:ListUserAssociations", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("license-manager-user-subscriptions:ListUserAssociations: %w", err)
			}
			for _, u := range out.InstanceUserSummaries {
				arn := sv(u.InstanceUserArn)
				if arn == "" {
					continue
				}
				status := sv(u.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeLicenseManagerUserSubscriptionsInstanceUser, NativeID: arn,
					Name: u.Username, Region: &region, Status: &status,
					AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "license-manager-user-subscriptions instance-users")
}

func listLMUSInstances(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store) ([]lmustypes.InstanceSummary, error) {
	var instances []lmustypes.InstanceSummary
	p := licensemanagerusersubscriptions.NewListInstancesPaginator(client, &licensemanagerusersubscriptions.ListInstancesInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied(st, "license-manager-user-subscriptions:ListInstances", acct.ID, region, err)
			}
			return nil, fmt.Errorf("license-manager-user-subscriptions:ListInstances: %w", err)
		}
		instances = append(instances, out.InstanceSummaries...)
	}
	return instances, nil
}
