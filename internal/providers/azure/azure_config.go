package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/spf13/viper"
)

// providerCfg mirrors the azure: section of ~/.disco/config.yaml.
type providerCfg struct {
	Subscriptions []subscriptionCfg `mapstructure:"subscriptions"`
}

// subscriptionCfg is one subscription entry in the config file.
type subscriptionCfg struct {
	ID   string `mapstructure:"id"`
	Name string `mapstructure:"name"`
}

// loadSubscriptions parses the viper config and returns resolved subscriptions
// plus the shared credential. Scope precedence: non-nil override pins the scan
// to exactly those subscriptions; else the config 'subscriptions:' list; else
// every accessible subscription auto-enumerates. See resolveSubscriptionScope
// for override's fail-closed semantics.
//
// The last two branches are conditional: a config list that resolves to zero
// usable ids is an error rather than an empty scope, and auto-enumeration is
// refused outright under federation (enumerateScope), where a pin or a config
// list is mandatory.
//
// wif selects the credential: a federated AWS-to-Entra exchange when its
// contract is set, else DefaultAzureCredential. It is passed in rather than
// read here so one scan cannot observe two different configurations.
func loadSubscriptions(ctx context.Context, override []string, wif wifConfig) ([]subscription, azcore.TokenCredential, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("azure", &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse azure config: %w", err)
	}

	// Wrap the credential in a token cache shared by every arm* client: each
	// client builds its own BearerTokenPolicy, and an uncached credential
	// serializes GetToken under the scan's client fan-out — the dominant
	// scan-time cost before this cache (see cachingCredential).
	base, err := newAzureCredential(ctx, wif)
	if err != nil {
		return nil, nil, fmt.Errorf("azure credential: %w", err)
	}
	cred := newCachingCredential(base)

	subs, err := resolveSubscriptionScope(override, cfg, func() ([]subscription, error) {
		return enumerateScope(ctx, cred, wif)
	})
	if err != nil {
		return nil, nil, err
	}

	// Every path lands here, the enumerate one included: a subscription this
	// credential can reach is not thereby a subscription the caller is
	// entitled to scan, and which of the three branches named it says nothing
	// about whose it is. See [bindSubscriptions].
	if err := bindSubscriptions(subs, wif, func() (map[string]string, error) {
		return subscriptionOwners(ctx, cred)
	}); err != nil {
		return nil, nil, err
	}
	return subs, cred, nil
}

// ErrFederatedEnumeration is returned when a federated credential is asked to
// discover its own subscriptions instead of being told which to scan.
//
// Under Azure Lighthouse a federated credential is a SHARED identity holding
// delegations from many tenants, so "every accessible subscription" spans
// other customers. Refused whenever the WIF contract is set, not only under
// Lighthouse: nothing here can tell the two apart, and the safe default for an
// operator federating into their own tenant is naming the subscriptions. The
// hazard resolveSubscriptionScope already names for the pin path applies with
// more force here, and nothing enforced it for this one.
var ErrFederatedEnumeration = errors.New("azure: federated credential requires an explicit subscription pin; refusing to enumerate (fail-closed)")

// enumerateScope discovers the subscriptions a credential can reach, refusing
// under federation. See [ErrFederatedEnumeration].
func enumerateScope(ctx context.Context, cred azcore.TokenCredential, wif wifConfig) ([]subscription, error) {
	if wif.configured() {
		return nil, ErrFederatedEnumeration
	}
	return enumerateSubscriptions(ctx, cred)
}

// resolveSubscriptionScope decides the subscription set for a scan without
// touching the network — the enumerate callback wraps the only ARM call, so
// tests can assert it's never reached. Precedence:
//
//   - override non-nil (explicit pin from --subscriptions): use only those IDs,
//     never call enumerate. Fail-closed — if the pin trims to zero non-empty
//     IDs, error instead of auto-enumerating. Security-critical: under Azure
//     Lighthouse one shared identity is delegated subscriptions from many
//     tenants, so silent enumeration would read every tenant's subscriptions.
//   - override nil + config list present: use the configured subscriptions,
//     under the same fail-closed rule as the pin.
//   - override nil + config empty: call enumerate (auto-discover all accessible).
func resolveSubscriptionScope(override []string, cfg providerCfg, enumerate func() ([]subscription, error)) ([]subscription, error) {
	if override != nil {
		subs := make([]subscription, 0, len(override))
		for _, id := range override {
			if id = strings.TrimSpace(id); id != "" {
				subs = append(subs, subscription{ID: id})
			}
		}
		if len(subs) == 0 {
			return nil, errors.New("azure: subscription pin set but resolved to zero subscriptions; refusing to auto-enumerate (fail-closed)")
		}
		return subs, nil
	}

	if len(cfg.Subscriptions) > 0 {
		subs := make([]subscription, 0, len(cfg.Subscriptions))
		for _, s := range cfg.Subscriptions {
			id := strings.TrimSpace(s.ID)
			if id == "" {
				continue
			}
			// Store the TRIMMED id, not s.ID: a padded id clears the guard
			// above and then fails the EqualFold match in
			// subscriptionResourceBatch, which reports the pin as missing from
			// the page and blames a revoked delegation for whitespace.
			subs = append(subs, subscription{ID: id, Name: strings.TrimSpace(s.Name)})
		}
		// Same fail-closed rule as the pin above, and it matters more since
		// enumeration refuses under federation: an empty id builds a malformed
		// "/subscriptions/" scope string that some clients accept.
		if len(subs) == 0 {
			return nil, errors.New("azure: configured subscriptions resolved to zero usable ids; refusing to auto-enumerate (fail-closed)")
		}
		return subs, nil
	}

	return enumerate()
}

// enumerateSubscriptions lists every subscription the credential can reach via
// ARM. Only invoked when no pin and no config list constrain the scan.
func enumerateSubscriptions(ctx context.Context, cred azcore.TokenCredential) ([]subscription, error) {
	client, err := armsubscription.NewSubscriptionsClient(cred, azClientOptions)
	if err != nil {
		return nil, fmt.Errorf("armsubscription client: %w", err)
	}

	var subs []subscription
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("armsubscription:ListSubscriptions: %w", err)
		}
		for _, s := range page.Value {
			if s.SubscriptionID == nil {
				continue
			}
			sub := subscription{ID: *s.SubscriptionID}
			if s.DisplayName != nil {
				sub.Name = *s.DisplayName
			}
			subs = append(subs, sub)
		}
	}
	return subs, nil
}
