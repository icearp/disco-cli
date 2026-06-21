package azure

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for Azure. Upstream truth
// source = ARM `Providers/List?$expand=resourceTypes`. Coverage truth source
// = CollectEmits() in services.go, which unions every registerService /
// registerTenantService emits decl plus extraEmits from compute / sql child
// scanner files and resourcegroups.
type coverageProvider struct{}

func (coverageProvider) Name() string { return "azure" }

func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// Aliases inverts azureAPITypeMap (types.go) — that map is already an
// upstream→disco lookup for live scanner scans; coverage needs the
// reverse direction. Built once at process start.
//
// ARM type keys are stored lowercased ("microsoft.compute/virtualmachines")
// to match coverage.Build's lowercased lookup. Multi-segment children
// (e.g. "microsoft.network/virtualnetworks/subnets") preserved verbatim.
func (coverageProvider) Aliases() map[string]string {
	out := make(map[string]string, len(azureAPITypeMap))
	for upstream, disco := range azureAPITypeMap {
		out[disco] = upstream
	}
	return out
}

// AlgorithmicKey is the fallback for disco types missing from the alias
// map. Disco type "azure:microsoft.compute:virtual-machine" -> ARM key
// "microsoft.compute/virtualmachine". Doesn't pluralise — alias map handles
// the pluralisation correctly. Mostly exists so future types compile-check
// without an alias entry.
func (coverageProvider) AlgorithmicKey(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) != 3 {
		return discoType
	}
	ns, kind := parts[1], parts[2]
	// Strip kebab dashes; ARM uses no separators in resource segments.
	kind = strings.ReplaceAll(kind, "-", "")
	return ns + "/" + kind
}

// Fetch pages ARM Providers/List with $expand=resourceTypes and returns
// every fully-qualified Azure resource type ("microsoft.compute/virtualmachines"
// lowercased). Auto-detects first available subscription when opts.Subscription
// is empty.
func (coverageProvider) Fetch(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}

	subID := opts.Subscription
	if subID == "" {
		subID, err = detectFirstSubscriptionForCoverage(ctx, cred)
		if err != nil {
			return nil, fmt.Errorf("detect subscription: %w", err)
		}
	}

	client, err := armresources.NewProvidersClient(subID, cred, nil)
	if err != nil {
		return nil, err
	}

	expand := "resourceTypes"
	pager := client.NewListPager(&armresources.ProvidersClientListOptions{Expand: &expand})

	var out []coverage.UpstreamType
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Value {
			if p.Namespace == nil {
				continue
			}
			ns := strings.ToLower(*p.Namespace)
			for _, rt := range p.ResourceTypes {
				if rt.ResourceType == nil {
					continue
				}
				key := ns + "/" + strings.ToLower(*rt.ResourceType)
				out = append(out, coverage.UpstreamType{Key: key, Service: ns})
			}
		}
	}
	return out, nil
}

// FetchRegions calls armsubscription.SubscriptionsClient.NewListLocationsPager
// and returns the authoritative ARM location-name list for the supplied (or
// auto-detected) subscription.
func (coverageProvider) FetchRegions(ctx context.Context, opts coverage.FetchOptions) ([]string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	subID := opts.Subscription
	if subID == "" {
		subID, err = detectFirstSubscriptionForCoverage(ctx, cred)
		if err != nil {
			return nil, fmt.Errorf("detect subscription: %w", err)
		}
	}
	client, err := armsubscription.NewSubscriptionsClient(cred, nil)
	if err != nil {
		return nil, err
	}
	pager := client.NewListLocationsPager(subID, nil)
	var out []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("armsubscription:ListLocations: %w", err)
		}
		for _, loc := range page.Value {
			if loc.Name != nil {
				out = append(out, *loc.Name)
			}
		}
	}
	return out, nil
}

// detectFirstSubscriptionForCoverage returns the ID of the first accessible
// subscription. Lifted from cmd/types_azure.go (which gets deleted).
func detectFirstSubscriptionForCoverage(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	client, err := armsubscription.NewSubscriptionsClient(cred, nil)
	if err != nil {
		return "", err
	}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range page.Value {
			if s.SubscriptionID != nil && *s.SubscriptionID != "" {
				return *s.SubscriptionID, nil
			}
		}
	}
	return "", fmt.Errorf("no accessible Azure subscriptions; pass --subscription to specify one")
}
