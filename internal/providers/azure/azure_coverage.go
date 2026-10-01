package azure

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/providers/azure/azureinventory"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for Azure: the types its
// scanners emit (CollectEmits) and, for --cross-check, the live ARM
// `Providers/List?$expand=resourceTypes` registry (CrossCheck).
type coverageProvider struct{}

func (coverageProvider) Name() string { return "azure" }

// ScannerStub is the stub scanner cmd/disco-scaffold emits for a new azure
// service: serviceEntry's fn signature, returning (0,0,nil).
func (coverageProvider) ScannerStub() (imports []string, sig, body string) {
	return []string{"context", "github.com/Azure/azure-sdk-for-go/sdk/azcore", "github.com/icearp/disco-cli/internal/restype", "github.com/icearp/disco-cli/store"},
		"(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)",
		"build the arm* client with cred, page via azPageScan, map each item to\n\t// *store.Resource, then st.UpsertResources(batch). Add a scan%[1]sWithClient seam."
}

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
// ("microsoft.compute/virtualmachines/extensions", lowercased). ARM keeps the
// "locations" segment the extractor strips as a scope pair, so a candidate
// reached only through one gets it back; without that, 15 keys could never
// match and each produced a false drift row on both sides of the comparison.
// Instance ids (blobServices/default) never reach a key: the extractor reads
// them as ids.
func (coverageProvider) RegistryKey(c sdkinv.Candidate) string {
	ns, path, ok := strings.Cut(c.Key, "/")
	if !ok {
		return c.Key
	}
	if slices.Contains(c.Signals, azureinventory.LocationScopeSignal) {
		return ns + "/locations/" + path
	}
	return c.Key
}

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
				typ := strings.ToLower(*rt.ResourceType)
				out = append(out, coverage.UpstreamType{Key: ns + "/" + typ, Service: ns, Reason: armEndpointReason(typ)})
			}
		}
	}
	return out, nil
}

// armAsyncOperationRe matches ARM's async-operation tracking segments:
// ".../operationResults", ".../operationStatuses" and the per-feature
// "...AzureAsyncOperation" variants.
var armAsyncOperationRe = regexp.MustCompile(`(operations?(results?|status(es)?)|asyncoperations?)$`)

// armEndpointReason files registry types that are ARM RPC endpoints, not
// customer resources: the provider's "operations" list, checkNameAvailability,
// async-operation tracking (and the status trees under it), and anything under
// a location. The registry
// lists them next to real types without any marker (their capabilities are
// "None", like hundreds of real proxy types), so only the ARM grammar
// identifies them. Nothing is dropped: a matched candidate still pairs
// first, and an unmatched one is labelled rather than counted as drift.
func armEndpointReason(typ string) string {
	segs := strings.Split(typ, "/")
	switch {
	case typ == "operations", segs[len(segs)-1] == "checknameavailability",
		slices.ContainsFunc(segs, armAsyncOperationRe.MatchString):
		return coverage.ReasonARMOperation
	case slices.Contains(segs, "locations"):
		return coverage.ReasonLocationScoped
	}
	return ""
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
