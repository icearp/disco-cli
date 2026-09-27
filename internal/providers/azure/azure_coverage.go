package azure

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for Azure: the types its
// scanners emit (CollectEmits) and, for --cross-check, the live ARM
// `Providers/List?$expand=resourceTypes` registry (CrossCheck).
type coverageProvider struct{}

func (coverageProvider) Name() string { return "azure" }

func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// TypeServices implements coverage.ServiceMapper from the registration files.
func (coverageProvider) TypeServices() map[string][]string { return typeOrigin.TypeServices() }

// ListResolvers implements coverage.ResolverAuditor by adapting the package's
// ListResolvers() registry view into the neutral coverage shape, so cmd can
// render `disco coverage resolvers` without importing this package directly.
func (coverageProvider) ListResolvers() []coverage.ResolverInfo {
	src := ListResolvers()
	out := make([]coverage.ResolverInfo, len(src))
	for i, r := range src {
		out[i] = coverage.ResolverInfo{Name: r.Name, EdgeCount: r.EdgeCount, Services: r.Services}
	}
	return out
}

// ResolverEdgeSources implements coverage.ResolverAuditor: the distinct
// EdgeDecl.Source disco-types declared across every registered resolver.
func (coverageProvider) ResolverEdgeSources() []string {
	edges := CollectResolverEdges()
	seen := make(map[string]struct{}, len(edges))
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if _, dup := seen[e.Source]; dup {
			continue
		}
		seen[e.Source] = struct{}{}
		out = append(out, e.Source)
	}
	return out
}

// RegistryKey turns a candidate key into the ARM type it should match
// ("microsoft.compute/virtualmachines/extensions", lowercased). Two shapes
// differ from the key: ARM omits a singleton instance id that the path spells
// out ("blobServices/default/containers" is "blobServices/containers"), and it
// keeps the "locations" segment the extractor strips as a scope pair. Without
// both, 34 keys could never match and each produced a false drift row on both
// sides of the comparison.
func (coverageProvider) RegistryKey(c sdkinv.Candidate) string {
	ns, path, ok := strings.Cut(c.Key, "/")
	if !ok {
		return c.Key
	}
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs)+1)
	if slices.Contains(c.Signals, armLocationScopeSignal) {
		out = append(out, "locations")
	}
	for _, sg := range segs {
		if armSingletonID[sg] {
			continue
		}
		out = append(out, sg)
	}
	return ns + "/" + strings.Join(out, "/")
}

// armLocationScopeSignal is set by internal/sdkinv/azure on a candidate whose
// path reached it through a "locations/{location}" pair.
const armLocationScopeSignal = "scope-pair:locations"

// armSingletonID are ARM's names for the one instance of a singleton child;
// they are ids in a path and absent from the type name.
var armSingletonID = map[string]bool{"default": true, "current": true}

// CanonicalKey lowercases: ARM identifiers are case-insensitive.
func (coverageProvider) CanonicalKey(r coverage.UpstreamType) string { return strings.ToLower(r.Key) }

// CrossCheck pages ARM Providers/List with $expand=resourceTypes and returns
// every fully-qualified Azure resource type ("microsoft.compute/virtualmachines"
// lowercased). Auto-detects first available subscription when opts.Subscription
// is empty.
func (coverageProvider) CrossCheck(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
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
	return "", fmt.Errorf("no accessible Azure subscriptions; pass --subscriptions to specify one")
}
