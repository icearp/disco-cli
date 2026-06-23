package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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
// plus the shared credential. Scope precedence: a non-nil override pins the
// scan to exactly those subscriptions; otherwise the config 'subscriptions:'
// list; otherwise every accessible subscription is auto-enumerated. See
// resolveSubscriptionScope for the fail-closed semantics of override.
func loadSubscriptions(ctx context.Context, override []string) ([]subscription, azcore.TokenCredential, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("azure", &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse azure config: %w", err)
	}

	// DefaultAzureCredential tries: env vars → workload identity → Azure CLI.
	// Wrap it in a token cache shared by every arm* client: each client builds
	// its own BearerTokenPolicy, and an uncached credential serializes GetToken
	// under the scan's client fan-out — the dominant scan-time cost before this
	// cache (see cachingCredential).
	base, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("azure credential: %w", err)
	}
	cred := newCachingCredential(base)

	subs, err := resolveSubscriptionScope(override, cfg, func() ([]subscription, error) {
		return enumerateSubscriptions(ctx, cred)
	})
	if err != nil {
		return nil, nil, err
	}
	return subs, cred, nil
}

// resolveSubscriptionScope decides the subscription set for a scan without
// touching the network — the enumerate callback wraps the only ARM call, so
// tests can assert it is never reached. Precedence:
//
//   - override non-nil (an explicit pin from --subscriptions): use ONLY those
//     IDs and never call enumerate. Fail-closed — if the pin trims to zero
//     non-empty IDs, return an error rather than auto-enumerating. This is the
//     security-critical branch: under Azure Lighthouse one shared identity is
//     delegated subscriptions from many tenants, and silent enumeration would
//     read every tenant's subscriptions.
//   - override nil + config list present: use the configured subscriptions.
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
			subs = append(subs, subscription{ID: s.ID, Name: s.Name})
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
