package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions"
	lmustypes "github.com/aws/aws-sdk-go-v2/service/licensemanagerusersubscriptions/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLicenseManagerUserSubscriptionsIdentityProvider, Service: "license-manager-user-subscriptions"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, Service: "license-manager-user-subscriptions"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerUserSubscriptionsProductSubscription, Service: "license-manager-user-subscriptions"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerUserSubscriptionsInstanceUser, Service: "license-manager-user-subscriptions"})
	registerService(serviceEntry{
		name: "aws:license-manager-user-subscriptions",
		fn:   scanLicenseManagerUserSubscriptions,
	})
}

const (
	lmusListIdentityProvidersOp      = "license-manager-user-subscriptions:ListIdentityProviders"
	lmusListLicenseServerEndpointsOp = "license-manager-user-subscriptions:ListLicenseServerEndpoints"
	lmusListProductSubscriptionsOp   = "license-manager-user-subscriptions:ListProductSubscriptions"
	lmusListInstancesOp              = "license-manager-user-subscriptions:ListInstances"
	lmusListUserAssociationsOp       = "license-manager-user-subscriptions:ListUserAssociations"
	lmusListTagsForResourceOp        = "license-manager-user-subscriptions:ListTagsForResource"
)

// lmusEndpointPageSize is the MaxResults ListLicenseServerEndpoints is sent:
// the model's maximum (range 1-100). The other listings model no range for
// MaxResults, so they are sent none.
const lmusEndpointPageSize = 100

// lmusGone is the not-found code ListTagsForResource models, for a resource
// deleted since it was listed, and the one ListProductSubscriptions and
// ListUserAssociations model, for an identity provider or instance deleted
// since it was listed.
var lmusGone = []string{"ResourceNotFoundException"}

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
	ListTagsForResource(context.Context, *licensemanagerusersubscriptions.ListTagsForResourceInput, ...func(*licensemanagerusersubscriptions.Options)) (*licensemanagerusersubscriptions.ListTagsForResourceOutput, error)
}

func scanLicenseManagerUserSubscriptions(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := licensemanagerusersubscriptions.NewFromConfig(acct.cfg, func(o *licensemanagerusersubscriptions.Options) { o.Region = region })
	return scanLicenseManagerUserSubscriptionsEntities(ctx, client, acct, region, st, scanID)
}

// scanLicenseManagerUserSubscriptionsEntities discovers identity providers,
// license server endpoints, the product subscriptions of every identity
// provider listed, and the users associated with every instance listed, one
// phase at a time. A phase's error does not stop the later ones; a failed
// write and ctx's end do, and so does an NXDOMAIN of the first listing with
// nothing stored (the region does not serve the service) (runHostPhases).
// Every row has its tags read (lmusTags), whose denials are one warning for
// the scan.
//
// A missing service-linked role means the account never activated User
// Subscriptions: every op fails the same way, so when the first page of
// ListIdentityProviders is refused so, no later phase runs and the scan is
// reported disabled (self-enableable by registering an identity provider).
func scanLicenseManagerUserSubscriptionsEntities(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := lmusTags(client).untaggedIfOtherAccount(acct.ID).sharingDenials()
	var (
		notSetUp             error
		providers, instances lmusListings
	)
	unlessNotSetUp := func(ph hostPhase) hostPhase {
		run := ph.run
		ph.run = func(p *phaseTotals) bool {
			if notSetUp != nil {
				return false
			}
			return run(p)
		}
		return ph
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "license-manager-user-subscriptions", 0, []hostPhase{
		{op: lmusListIdentityProvidersOp, run: func(p *phaseTotals) bool {
			return scanLMUSIdentityProviders(ctx, client, acct, region, st, scanID, &notSetUp, &providers, tags, p)
		}},
		unlessNotSetUp(hostPhase{op: lmusListLicenseServerEndpointsOp, run: func(p *phaseTotals) bool {
			return scanLMUSLicenseServerEndpoints(ctx, client, acct, region, st, scanID, tags, p)
		}}),
		unlessNotSetUp(hostPhase{op: lmusListProductSubscriptionsOp, run: func(p *phaseTotals) bool {
			return scanLMUSProductSubscriptions(ctx, client, acct, region, st, scanID, &providers, tags, p)
		}}),
		unlessNotSetUp(hostPhase{op: lmusListInstancesOp, run: func(p *phaseTotals) bool {
			listLMUSInstances(ctx, client, acct, region, st, &instances, p)
			return false
		}}),
		unlessNotSetUp(hostPhase{op: lmusListUserAssociationsOp, run: func(p *phaseTotals) bool {
			return scanLMUSInstanceUsers(ctx, client, acct, region, st, scanID, &instances, tags, p)
		}}),
	})
	tags.warnDenials(st, acct, region)
	// Only the first listing's first page is read once notSetUp is set, so
	// nothing was stored; err is then ctx's end, if anything, which is
	// reported instead.
	if notSetUp != nil && err == nil {
		return 0, 0, markServiceDisabled(notSetUp)
	}
	return total, inserted, err
}

// lmusTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// identity-provider, instance-user, license-server-endpoint and
// product-subscription, every type stored here, under
// ListTagsForResource and TagResource; RegisterIdentityProvider,
// AssociateUser, CreateLicenseServerEndpoint and StartProductSubscription
// take Tags, and no listed element carries them. The op does not page its
// tags. ResourceNotFoundException, its modeled not-found, drops a resource
// deleted after it was listed.
//
// Every type's ARN names its account, and identity providers and instances
// carry an OwnerAccountId, so these listings may return another account's
// resources (unverified live). The scanner reads the account from the ARN
// that the tag op is sent: when the ARN names another account, a denied or
// not-found read leaves the row untagged with no warning
// (untaggedIfOtherAccount). A row whose ARN names this account warns or is
// dropped whatever its OwnerAccountId says, since this account can fix a
// denial of its own resource.
func lmusTags(client licenseManagerUserSubscriptionsAPI) rowTagReader {
	return rowTagReader{
		op:       lmusListTagsForResourceOp,
		notFound: lmusGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &licensemanagerusersubscriptions.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// lmusListing is the input of one per-parent listing: ListProductSubscriptions
// takes an identity provider; ListUserAssociations an instance and the
// identity provider it uses.
type lmusListing struct {
	instanceID *string
	provider   lmustypes.IdentityProvider
}

// lmusListings is the per-parent listings a child phase makes, each under the
// key its warnings and errors name it by, and each input once.
type lmusListings struct {
	parents []childParent
	by      map[string]lmusListing
	inputs  map[string]bool
	// unread tallies the listed parents that lack an input the child listing
	// requires, so their children were not read.
	unread skipTally
}

// addLMUSListing adds the listing of in under key, unless a listing of the
// same input was added. An identity provider registered for several products
// can be listed once per product with the same IdentityProvider (unverified
// live), and ListProductSubscriptions, sent no Product, would return the same
// subscriptions for each. Registrations whose IdentityProvider differs are
// still listed once each. A key another input already has is suffixed with
// the input's IdentityProvider.
func (l *lmusListings) addLMUSListing(key string, in lmusListing) {
	provider := mustJSON(in.provider)
	input := sv(in.instanceID) + " " + provider
	if l.inputs[input] {
		return
	}
	if l.by == nil {
		l.by, l.inputs = map[string]lmusListing{}, map[string]bool{}
	}
	l.inputs[input] = true
	if _, taken := l.by[key]; taken {
		key += " " + provider
	}
	l.by[key] = in
	l.parents = append(l.parents, childParent{id: key})
}

// warnLMUSUnread warns once under op about the listed parents (what) that
// lack field, so their children were not read.
func (l *lmusListings) warnLMUSUnread(st *store.Store, op string, acct *account, region, what, field, children string) {
	if l.unread.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d listed %s have no %s, so their %s were not read; first %s", l.unread.count, what, field, children, l.unread.first),
	})
}

// scanLMUSIdentityProviders stores the identity providers with their tags, a
// page at a time, and adds a product subscription listing to providers for
// every IdentityProvider listed, those on pages before a failed one and those
// of rows not stored included. A "Service Linked role is not present" denial
// on the first page sets notSetUp and stores nothing; on a later page it is
// the denial it then is. It reports whether a write failed, after which the
// scan must stop.
func scanLMUSIdentityProviders(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string, notSetUp *error, providers *lmusListings, tags rowTagReader, p *phaseTotals) bool {
	notSetUpGap := func(err error) bool {
		if !isAccessDeniedWithMessage(err, "Service Linked role is not present") {
			return false
		}
		*notSetUp = err
		return true
	}
	failed := storeTokenPagesTags(ctx, st, acct, region, lmusListIdentityProvidersOp,
		firstPageGapPages(notSetUpGap, func(ctx context.Context, token *string) ([]lmustypes.IdentityProviderSummary, *string, error) {
			out, err := client.ListIdentityProviders(ctx, &licensemanagerusersubscriptions.ListIdentityProvidersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			for _, s := range out.IdentityProviderSummaries {
				arn := sv(s.IdentityProviderArn)
				if s.IdentityProvider == nil {
					providers.unread.skipped(cmp.Or(arn, noIdentifier), nil)
					continue
				}
				providers.addLMUSListing(cmp.Or(arn, mustJSON(s.IdentityProvider)), lmusListing{provider: s.IdentityProvider})
			}
			return out.IdentityProviderSummaries, out.NextToken, nil
		}),
		func(s lmustypes.IdentityProviderSummary) (*store.Resource, string, string) {
			status := sv(s.Status)
			return arnRow("identity providers", s.IdentityProviderArn, s.Product, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsIdentityProvider, NativeID: sv(s.IdentityProviderArn),
				Name: s.Product, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		},
		tags, p)
	if !failed {
		providers.warnLMUSUnread(st, lmusListIdentityProvidersOp, acct, region, "identity providers", "IdentityProvider", "product subscriptions")
	}
	return failed
}

// scanLMUSLicenseServerEndpoints stores the license server endpoints with
// their tags, a page at a time. It reports whether a write failed, after
// which the scan must stop.
func scanLMUSLicenseServerEndpoints(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, lmusListLicenseServerEndpointsOp,
		func(ctx context.Context, token *string) ([]lmustypes.LicenseServerEndpoint, *string, error) {
			out, err := client.ListLicenseServerEndpoints(ctx, &licensemanagerusersubscriptions.ListLicenseServerEndpointsInput{
				MaxResults: sdkaws.Int32(lmusEndpointPageSize), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.LicenseServerEndpoints, out.NextToken, nil
		},
		func(e lmustypes.LicenseServerEndpoint) (*store.Resource, string, string) {
			status := string(e.LicenseServerEndpointProvisioningStatus)
			return arnRow("license server endpoints", e.LicenseServerEndpointArn, e.LicenseServerEndpointId, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint, NativeID: sv(e.LicenseServerEndpointArn),
				Name: e.LicenseServerEndpointId, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanLMUSProductSubscriptions stores the product subscriptions of every
// identity provider in providers with their tags, the listings read
// concurrently and each page written as it is read (storeEachParentPagesTags):
// the op rejects a blanket call, requiring an IdentityProvider input. It
// reports whether a write failed, after which the scan must stop.
func scanLMUSProductSubscriptions(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string, providers *lmusListings, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, lmusListProductSubscriptionsOp, lmusGone, providers.parents,
		func(ctx context.Context, key string, token *string) ([]lmustypes.ProductUserSummary, *string, error) {
			out, err := client.ListProductSubscriptions(ctx, &licensemanagerusersubscriptions.ListProductSubscriptionsInput{
				IdentityProvider: providers.by[key].provider, NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ProductUserSummaries, out.NextToken, nil
		},
		func(_ string, u lmustypes.ProductUserSummary) (*store.Resource, string, string) {
			status := sv(u.Status)
			return arnRow("product subscriptions", u.ProductUserArn, u.Username, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsProductSubscription, NativeID: sv(u.ProductUserArn),
				Name: u.Username, Region: &region, Status: &status,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// listLMUSInstances adds a user association listing to instances for every
// instance that provides user-based subscriptions, those on pages before a
// failed one included; an instance without an InstanceId or an
// IdentityProvider, both of which ListUserAssociations requires, is warned
// about. Instances are not stored. A denial is warned about; any other
// error, a repeated NextToken included, goes to p.
func listLMUSInstances(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, instances *lmusListings, p *phaseTotals) {
	listed, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]lmustypes.InstanceSummary, *string, error) {
		out, err := client.ListInstances(ctx, &licensemanagerusersubscriptions.ListInstancesInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.InstanceSummaries, out.NextToken, nil
	})
	for _, inst := range listed {
		id := sv(inst.InstanceId)
		if id == "" || inst.IdentityProvider == nil {
			instances.unread.skipped(cmp.Or(id, noIdentifier), nil)
			continue
		}
		instances.addLMUSListing(id, lmusListing{instanceID: inst.InstanceId, provider: inst.IdentityProvider})
	}
	instances.warnLMUSUnread(st, lmusListInstancesOp, acct, region, "instances", "InstanceId or IdentityProvider", "user associations")
	if isAccessDenied(err) {
		_ = skipIfAccessDenied(st, lmusListInstancesOp, acct.ID, region, err)
		return
	}
	if err != nil {
		p.addPhase(lmusListInstancesOp, 0, 0, fmt.Errorf("%s: %w", lmusListInstancesOp, err))
	}
}

// scanLMUSInstanceUsers stores the users associated with every instance in
// instances with their tags, the listings read concurrently and each page
// written as it is read (storeEachParentPagesTags). It reports whether a
// write failed, after which the scan must stop.
func scanLMUSInstanceUsers(ctx context.Context, client licenseManagerUserSubscriptionsAPI, acct *account, region string, st *store.Store, scanID string, instances *lmusListings, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, lmusListUserAssociationsOp, lmusGone, instances.parents,
		func(ctx context.Context, key string, token *string) ([]lmustypes.InstanceUserSummary, *string, error) {
			in := instances.by[key]
			out, err := client.ListUserAssociations(ctx, &licensemanagerusersubscriptions.ListUserAssociationsInput{
				InstanceId: in.instanceID, IdentityProvider: in.provider, NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.InstanceUserSummaries, out.NextToken, nil
		},
		func(_ string, u lmustypes.InstanceUserSummary) (*store.Resource, string, string) {
			status := sv(u.Status)
			return arnRow("instance users", u.InstanceUserArn, u.Username, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerUserSubscriptionsInstanceUser, NativeID: sv(u.InstanceUserArn),
				Name: u.Username, Region: &region, Status: &status,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		},
		tags, p)
}
