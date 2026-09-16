package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
	"golang.org/x/sync/semaphore"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for GCP: the types its
// scanners emit (CollectEmits) and, for --cross-check, the public Discovery
// API (CrossCheck).
type coverageProvider struct{}

func (coverageProvider) Name() string { return "gcp" }

func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// ListResolvers and ResolverEdgeSources implement coverage.ResolverAuditor,
// backing `disco coverage resolvers --providers gcp`.
func (coverageProvider) ListResolvers() []coverage.ResolverInfo { return ListResolvers() }
func (coverageProvider) ResolverEdgeSources() []string          { return ResolverEdgeSources() }

// Discovery API endpoints. Public, unauthenticated; rate-limited but generous
// for a one-shot enumeration. discoveryListURL is a var so tests can point the
// fetch at an httptest server (the per-API doc URLs come from the list response).
var discoveryListURL = "https://www.googleapis.com/discovery/v1/apis"

const discoveryFetchLimit = 8

// RegistryKey is the candidate's identity in CanonicalKey's namespace: the
// API name and the canonical singular of the listed collection, so
// "cloudkms/keyrings/cryptokeys" meets Discovery's "cloudkms.googleapis.com/CryptoKey".
func (coverageProvider) RegistryKey(c sdkinv.Candidate) string {
	return c.Service + "/" + sdkinv.Ident(c.Key[strings.LastIndex(c.Key, "/")+1:])
}

// CanonicalKey maps "<api>.googleapis.com/<Resource>" onto RegistryKey's shape.
func (coverageProvider) CanonicalKey(upstreamKey string) string {
	api, res, _ := strings.Cut(upstreamKey, "/")
	return strings.TrimSuffix(api, ".googleapis.com") + "/" + sdkinv.Ident(res)
}

// CrossCheck enumerates every API the public Discovery list advertises and
// returns each resource collection as an UpstreamType. All ~650 docs are
// fetched (no allowlist): the universe covers every cloud-rooted API, so a
// narrower registry would report its other candidates as candidate-only.
//
// The list keeps advertising retired APIs whose doc 404s or 502s (poly v1,
// area120tables v1alpha1, datalabeling v1beta1). A doc failure is fatal only
// for an API disco scans — there it would surface as false drift; any other
// failed API is named on stderr and its candidates read as candidate-only.
func (coverageProvider) CrossCheck(ctx context.Context, _ coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	scanned := map[string]bool{}
	for _, e := range CollectEmits() {
		scanned[e.Service] = true
	}

	apis, err := fetchDiscoveryAPIList(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("discovery list: %w", err)
	}

	type apiRef struct{ name, url, version string }
	var todo []apiRef
	seenURL := map[string]bool{}
	for _, a := range apis {
		if a.Name == "" || a.DiscoveryRestURL == "" || seenURL[a.DiscoveryRestURL] {
			continue
		}
		seenURL[a.DiscoveryRestURL] = true
		todo = append(todo, apiRef{name: a.Name, url: a.DiscoveryRestURL, version: a.Version})
	}

	var (
		mu       sync.Mutex
		out      []coverage.UpstreamType
		firstErr error // guarded by mu
		skipped  []string
		sem      = semaphore.NewWeighted(discoveryFetchLimit)
		wg       sync.WaitGroup
	)
	for _, ref := range todo {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		wg.Go(func() {
			defer sem.Release(1)
			doc, err := fetchDiscoveryDoc(ctx, client, ref.url)
			if err != nil && !scanned[ref.name] {
				mu.Lock()
				skipped = append(skipped, ref.name+"/"+ref.version)
				mu.Unlock()
				return
			}
			if err != nil {
				// A per-API doc failure would leave that API's types absent
				// from the upstream set, falsely bucketing them upstream-missing.
				// Record and propagate rather than silently degrade — cmd turns
				// this into a fatal "registry unreachable" (exit 2).
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("discovery doc %s: %w", ref.name, err)
				}
				mu.Unlock()
				return
			}
			types := walkResourceCollections(ref.name, doc)
			mu.Lock()
			out = append(out, types...)
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(skipped) > 0 {
		sort.Strings(skipped)
		fmt.Fprintf(os.Stderr, "  gcp: %d Discovery docs unreachable (retired APIs, not scanned): %s\n", len(skipped), strings.Join(skipped, " "))
	}
	if firstErr != nil {
		return nil, firstErr
	}

	// Dedupe across versions by full upstream key — same API across v1/v2
	// often reports the same resource collection twice. Service segment of
	// the first occurrence wins.
	seen := make(map[string]bool, len(out))
	deduped := out[:0]
	for _, u := range out {
		if seen[u.Key] {
			continue
		}
		seen[u.Key] = true
		deduped = append(deduped, u)
	}
	return deduped, nil
}

// discoveryAPI is the relevant subset of one entry in the Discovery list.
type discoveryAPI struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	DiscoveryRestURL string `json:"discoveryRestUrl"`
	Preferred        bool   `json:"preferred"`
}

func fetchDiscoveryAPIList(ctx context.Context, client *http.Client) ([]discoveryAPI, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryListURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery list status %d", resp.StatusCode)
	}
	var body struct {
		Items []discoveryAPI `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	// Return every version of every API. Different versions expose different
	// resource collections (e.g. cloudbuild v1 has Triggers, v2 has
	// Connection/Repository). Fetcher dedupes the union by full upstream key
	// post-walk.
	return body.Items, nil
}

// discoveryDoc carries the recursive resource-collection tree of one API's
// Discovery document. We only care about resource names and their methods —
// the presence of a `get` or `list` method indicates a fetchable collection.
type discoveryDoc struct {
	Resources map[string]discoveryResource `json:"resources"`
}

type discoveryResource struct {
	Methods   map[string]json.RawMessage   `json:"methods"`
	Resources map[string]discoveryResource `json:"resources"`
}

func fetchDiscoveryDoc(ctx context.Context, client *http.Client, url string) (*discoveryDoc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery doc %s status %d", url, resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// walkResourceCollections recursively walks a Discovery doc's resources tree
// and emits one UpstreamType per fetchable collection — any node carrying a
// `get` or `list` method, matching GCP's notion of a resource type in
// tooling like gcloud + asset inventory.
//
// The key keeps the collection's own spelling ("forwardingRules");
// CanonicalKey reduces it to the equality stem.
func walkResourceCollections(api string, doc *discoveryDoc) []coverage.UpstreamType {
	if doc == nil {
		return nil
	}
	var out []coverage.UpstreamType
	var walk func(name string, r discoveryResource)
	walk = func(name string, r discoveryResource) {
		if hasFetchMethod(r.Methods) {
			out = append(out, coverage.UpstreamType{Key: api + ".googleapis.com/" + name, Service: api})
		}
		for childName, child := range r.Resources {
			walk(childName, child)
		}
	}
	for name, r := range doc.Resources {
		walk(name, r)
	}
	return out
}

func hasFetchMethod(methods map[string]json.RawMessage) bool {
	if len(methods) == 0 {
		return false
	}
	for k := range methods {
		switch strings.ToLower(k) {
		case "get", "list", "aggregatedlist":
			return true
		}
	}
	return false
}

// FetchRegions calls compute.Regions.List for the first accessible project
// and returns the region-name list. Reuses gcpRegions(ctx, *project) so
// scanner-side and coverage-side fetch paths share the same shape, including
// silent-skip on permission denied / API not enabled (yields empty slice —
// DiffRegions then marks every static entry "stale", a clear signal the
// caller needs broader creds).
func (coverageProvider) FetchRegions(ctx context.Context, _ coverage.FetchOptions) ([]string, error) {
	// Coverage tooling uses ambient/config credentials; no per-scan override.
	projects, err := loadProjects(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no accessible GCP projects; coverage --regions needs at least one")
	}
	return gcpRegions(ctx, &projects[0])
}
