package azure

import (
	"context"
	"fmt"

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
// plus the shared credential. When no subscriptions are configured, all
// accessible subscriptions are enumerated.
func loadSubscriptions(ctx context.Context) ([]subscription, *azidentity.DefaultAzureCredential, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("azure", &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse azure config: %w", err)
	}

	// DefaultAzureCredential tries: env vars → workload identity → Azure CLI.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("azure credential: %w", err)
	}

	if len(cfg.Subscriptions) > 0 {
		subs := make([]subscription, 0, len(cfg.Subscriptions))
		for _, s := range cfg.Subscriptions {
			subs = append(subs, subscription{ID: s.ID, Name: s.Name})
		}
		return subs, cred, nil
	}

	// Auto-enumerate all accessible subscriptions.
	client, err := armsubscription.NewSubscriptionsClient(cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("armsubscription client: %w", err)
	}

	var subs []subscription
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("armsubscription:ListSubscriptions: %w", err)
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
	return subs, cred, nil
}
