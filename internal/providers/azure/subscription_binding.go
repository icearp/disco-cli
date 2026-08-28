package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// Binding a scanned subscription to the directory that owns it.
//
// [wifConfig.tenantScopeEnabled] documents the hole this closes: every other
// guard in this package keys on the ENV CONTRACT, so a credential that is a
// Lighthouse managing identity but arrives another way leaves them all off,
// and nothing compares the credential's reach against the directory the
// caller says it is scanning. A subscription pin says WHICH subscriptions to
// read; it never says whose they are.
//
// ARM answers both halves in one list. Microsoft.Resources 2022-12-01 returns
// a subscription only when the calling identity can reach it — under
// Lighthouse, only when it was delegated to THIS deployment — and its
// tenantId names the directory that OWNS it, not the managing one. Measured
// against a live delegation rather than read off the schema: called as the
// managing service principal, a delegated subscription came back with
// tenantId = the CUSTOMER directory and managedByTenants = [the managing
// one]. The same run ruled out the two obvious alternatives — GET /tenants
// answers only the credential's own directory and never names a delegated
// one, and this endpoint's count field includes subscriptions its RBAC-
// filtered value array omits, so counting is not listing.
//
// The api-version matters and is why this uses a second SDK package.
// resourcemanager/subscription/armsubscription (singular, used everywhere
// else here) reaches Subscriptions.List at api-version 2016-06-01, whose
// Subscription model carries no TenantID at all.
// resourcemanager/resources/armsubscriptions (plural) is the 2022-12-01 shape
// and carries both TenantID and ManagedByTenants. The singular package stays
// because it is what the rest of this package already calls, not because it
// can do something the plural one cannot — for the three operations disco
// uses, the plural package has an equivalent. On the auto-enumerate path that
// costs a second GET /subscriptions at the other api-version; the pin path,
// which is every federated scan, makes one call either way.
//
// Two fields of that response are deliberately NOT read.
//
// State, because this check decides WHOSE a subscription is and not whether
// the scan will find anything in it. Whether ARM lists a Disabled or Deleted
// subscription at all is NOT measured here — the state enum has those values,
// which is evidence about the schema and not about the list — and it does not
// need to be: if such a subscription is listed it binds on its owner like any
// other, and if it is omitted the scan is refused. The per-service scanners
// report an unreadable subscription separately (unreachableSubscription).
//
// ManagedByTenants, which would name the delegation directly rather than
// inferring it from presence, because it is empty for a subscription that has
// no Lighthouse delegation at all. Requiring it would refuse the operator who
// federated into their own tenant, and requiring it only under federation
// would make the guard weakest exactly where the credential is shared.

// envSubscriptionTenantID names the directory every scanned subscription must
// belong to, checked against ARM rather than taken on trust.
//
// Required under federation and optional otherwise — see
// [wifConfig.subscriptionBindingRequired]. Deliberately NOT a member of the
// contract [wifConfig.partiallyConfigured] counts: it grants nothing, opens
// nothing, and is the one guard here that works against an ambient
// credential.
const envSubscriptionTenantID = "DISCO_AZURE_SUBSCRIPTION_TENANT_ID"

// ErrSubscriptionTenantRequired reports a federated scan that did not say
// which directory owns the subscriptions it was pinned to.
//
// Fail-closed for the same reason as [ErrFederatedEnumeration], one step
// further along: a federated credential is a SHARED identity holding
// delegations from many tenants, so a pin alone selects subscriptions from a
// set that spans customers. Naming the owner is what makes the pin an
// assertion about one of them.
var ErrSubscriptionTenantRequired = errors.New("azure: " + envSubscriptionTenantID +
	" must name the directory that owns the scanned subscriptions when " + envWIFClientID +
	" and " + envWIFTenantID + " are set; refusing to scan a pin nothing binds (fail-closed)." +
	subscriptionWhichDirectory)

// ErrSubscriptionTenantMalformed reports a value that is not a directory GUID.
//
// Same strictness as [envGraphTenantID], for the same reason: the multi-tenant
// aliases azidentity accepts ("common", "organizations") mean "whichever
// directory the caller signs in to", which is the opposite of naming one.
//
// Carries [subscriptionWhichDirectory] as well, because the operator reading
// THIS message is the one about to retype the value.
var ErrSubscriptionTenantMalformed = errors.New("azure: " + envSubscriptionTenantID +
	" is not a directory GUID (8-4-4-4-12)." + subscriptionWhichDirectory + ". Value was")

// subscriptionWhichDirectory says which directory to name, for both audiences.
//
// Purpose-built rather than shared with [graphWhichDirectory], whose tail is
// about which SERVICES a named directory restores — true of the Graph knob and
// false of this one, which restores nothing and only permits a scan already
// asked for. The opening halves agree because the question is the same, and
// the two texts are free to diverge because nothing reads them together.
const subscriptionWhichDirectory = " The directory to name depends on how you federated: into your OWN tenant it is the same value as " + envWIFTenantID +
	"; under Azure Lighthouse it is the CUSTOMER's directory, the one that owns the delegated subscriptions, and never " + envWIFTenantID

// ErrSubscriptionNotBound reports a subscription ARM did not confirm as both
// reachable by this credential and owned by the named directory.
//
// ONE error for two causes, deliberately, and this is a disclosure decision
// rather than a simplification. Splitting them tells whoever can trigger a
// scan whether an arbitrary subscription id is one this deployment holds a
// delegation for — for a shared managing identity, that is "is this
// organisation a customer of ours", answered about a third party. The message
// is also stored on the scan record and rendered to a customer verbatim, so
// there is no channel here that reaches an operator alone.
//
// It names the subscription and the EXPECTED directory, never the one ARM
// answered, for the same reason. What it withholds is precise: the identity of
// the owning directory, and which of the two causes fired. What it cannot
// withhold is that the pairing failed, which is the refusal itself.
//
// The operator who needs the distinction has the credential that settles it,
// so the message says to go and ask ARM rather than answering for it.
var ErrSubscriptionNotBound = errors.New("azure: Azure Resource Manager does not confirm this subscription as reachable by this credential and owned by the named directory; " +
	"list what this credential can reach (az account list --all) to tell an absent delegation from a different owner")

// subscriptionBindingRequired reports whether a scan must name the owning
// directory.
//
// Required exactly when federated, because that is when one credential's reach
// provably spans customers and the deployment knows it. It is NOT a no-op
// unfederated — an ambient credential can be a Lighthouse managing identity
// too ([wifConfig.tenantScopeEnabled] names the shape), and this is the only
// guard in the package that would catch it, because it reads ARM instead of
// the environment. It stays opt-in there because nothing here can tell that
// operator apart from one scanning their own directory, and refusing both
// would refuse every standalone Azure scan.
func (c wifConfig) subscriptionBindingRequired() bool { return c.configured() }

// bindSubscriptions refuses the scan unless ARM agrees that every subscription
// in subs is reachable by this credential and owned by the named directory.
//
// No-op when nothing is named and nothing is required, which is the standalone
// default: one ARM list is spent only by a caller that asked for the check.
//
// The lookup is a callback for the same reason resolveSubscriptionScope's
// enumerate is — it wraps the only network call, so a test can assert this
// function never reaches it rather than infer it from a wall-clock time. It
// bounds THIS function's calls and not the scan's: loadSubscriptions builds
// the credential first, so a federated run has already spent an STS exchange
// before a missing variable is refused here.
func bindSubscriptions(subs []subscription, wif wifConfig, lookup func() (map[string]string, error)) error {
	want := wif.subscriptionTenantID
	if want == "" {
		if wif.subscriptionBindingRequired() {
			return ErrSubscriptionTenantRequired
		}
		return nil
	}
	if !graphTenantGUID.MatchString(want) {
		return fmt.Errorf("%w %q", ErrSubscriptionTenantMalformed, want)
	}

	owners, err := lookup()
	if err != nil {
		return err
	}
	for _, sub := range subs {
		// An absent entry and an empty one fail the same comparison and are
		// reported the same way. EqualFold against a non-empty want is false
		// for "", so an unlisted subscription needs no branch of its own.
		if !strings.EqualFold(owners[strings.ToLower(sub.ID)], want) {
			return fmt.Errorf("%w: %s is not bound to %s", ErrSubscriptionNotBound, sub.ID, want)
		}
	}
	return nil
}

// subscriptionOwners lists every subscription this credential can reach and
// maps its id to the directory that owns it.
func subscriptionOwners(ctx context.Context, cred azcore.TokenCredential) (map[string]string, error) {
	client, err := armsubscriptions.NewClient(cred, azClientOptions)
	if err != nil {
		return nil, fmt.Errorf("armsubscriptions client: %w", err)
	}

	owners := map[string]string{}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// Wrapped, not narrowed: Scanner.Scan narrows an ARM failure once,
			// for this call and enumerateSubscriptions' identical one. Doing it
			// here as well ran the redactor over an already-redacted string,
			// which relabelled a 401 and dropped this prefix.
			return nil, fmt.Errorf("armsubscriptions:Subscriptions.List: %w", err)
		}
		ownersFromPage(owners, page.Value)
	}
	return owners, nil
}

// ownersFromPage folds one ARM page into the owner map.
//
// Split from the pager so the mapping is testable without a credential: the
// lowercasing here is what every comparison depends on, and a scan refusing
// every subscription is the failure it produces.
func ownersFromPage(owners map[string]string, page []*armsubscriptions.Subscription) {
	for _, s := range page {
		if s == nil || s.SubscriptionID == nil {
			continue
		}
		var tid string
		if s.TenantID != nil {
			tid = strings.TrimSpace(*s.TenantID)
		}
		// Lowercased on the way in because the id reaching the comparison came
		// from a pin, a config file or an ARM page, and ARM is not consistent
		// about GUID case across those. Matching case-insensitively at the
		// lookup instead would need the map iterated per subscription.
		owners[strings.ToLower(strings.TrimSpace(*s.SubscriptionID))] = tid
	}
}
