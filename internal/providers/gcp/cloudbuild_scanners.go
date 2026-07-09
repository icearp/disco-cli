package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	cloudbuildv2 "google.golang.org/api/cloudbuild/v2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudBuildTrigger, Service: "cloudbuild", Upstream: "cloudbuild.googleapis.com/Trigger", Redact: []redact.Rule{{Path: "substitutions.*", Mode: redact.RedactScalar}, {Path: "build.steps[*].env[*]", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudBuildWorkerPool, Service: "cloudbuild", Upstream: "cloudbuild.googleapis.com/WorkerPool"})
	registerType(restype.Descriptor{Type: TypeCloudBuildConnection, Service: "cloudbuild", Upstream: "cloudbuild.googleapis.com/Connection"})
	registerType(restype.Descriptor{Type: TypeCloudBuildRepository, Service: "cloudbuild", Upstream: "cloudbuild.googleapis.com/Repository", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudBuildGithubEnterpriseConfig, Service: "cloudbuild", Upstream: "cloudbuild.googleapis.com/GithubEnterpriseConfig"})
	registerService(serviceEntry{
		name: "gcp:cloudbuild",
		fn:   scanCloudBuildTriggers,
	})
}

// maxConcurrentCloudBuildFanout caps the per-location (WorkerPools/
// Connections) and per-Connection (Repositories) fan-out phases.
const maxConcurrentCloudBuildFanout = 10

// scanCloudBuildTriggers discovers Cloud Build triggers, worker pools,
// 2nd-gen repository connections + their repositories, and legacy GitHub
// Enterprise configs.
//
// Deliberately NOT scanned: GitLabConfig, BitbucketServerConfig, and their
// nested Repo child (1st-gen VCS connector configs, v1 API) — both list
// endpoints are marked "experimental" in the SDK's own doc comment, superseded
// by the 2nd-gen Connections/Repositories (v2) API already scanned here. This
// is a usage/risk tradeoff, not a "duplicate data" claim: BitbucketServerConfig
// in particular carries real, not-otherwise-captured data (PeeredNetwork for
// reaching an on-prem Bitbucket Server, SslCa) that 2nd-gen Connection doesn't
// expose. Accepted because both configs are self-reported experimental and
// scanning them would stack another unverified per-location assumption (see
// below) onto an already-niche, likely-near-zero-usage legacy surface. Build
// (raw build execution records) is deliberately out of scope — ephemeral,
// high-cardinality execution history, not an addressable infrastructure
// resource, same bucket as AWS CodeBuild build runs.
//
// Cloud Build v1 has no Locations.List of its own; this scanner uses the
// cloudbuild/v2 Locations.List (Connections/Repositories' own client) to
// discover the product's regional footprint and reuses that same location
// list for the v1 WorkerPools fan-out too, on the assumption that v1 and v2
// are two API-generation clients over the same underlying Cloud Build
// regional deployment, not two independently-scoped location catalogs. The
// specific residual risk: WorkerPools (private pools) is the older, more
// mature product — it's possible for it to be available in a region the
// newer 2nd-gen Connections API hasn't rolled out to yet, in which case this
// scanner would under-cover that region's worker pools. Revisit if a live
// account surfaces a region mismatch between the two.
func scanCloudBuildTriggers(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudbuild.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudbuild client: %w", err)
	}
	svc2, err := cloudbuildv2.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudbuild v2 client: %w", err)
	}
	return scanCloudBuildWithClient(ctx, svc, svc2, p, st, scanID)
}

// scanCloudBuildWithClient is the test seam for scanCloudBuildTriggers —
// takes the pre-built v1 and v2 clients directly so tests can point both at
// a fake server.
func scanCloudBuildWithClient(ctx context.Context, svc *cloudbuild.Service, svc2 *cloudbuildv2.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: triggers — unchanged.
	t, n, err := runPaginated(ctx, st, p, "cloudbuild:triggers.list",
		svc.Projects.Triggers.List(p.ID),
		func(page *cloudbuild.ListBuildTriggersResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Triggers))
			for _, tr := range page.Triggers {
				name := tr.Name
				nativeID := tr.ResourceName
				if nativeID == "" {
					nativeID = fmt.Sprintf("projects/%s/triggers/%s", p.ID, tr.Id)
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudBuildTrigger,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(tr.CreateTime),
					AttributesJSON: mustJSON(tr),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 2: discover Cloud Build's regional footprint via the v2
	// Locations.List catalog, reused for both the v1 WorkerPools and v2
	// Connections per-location fan-out below.
	var locations []string
	if err := svc2.Projects.Locations.List(fmt.Sprintf("projects/%s", p.ID)).Pages(ctx, func(page *cloudbuildv2.ListLocationsResponse) error {
		for _, loc := range page.Locations {
			if loc != nil && loc.LocationId != "" {
				locations = append(locations, loc.LocationId)
			}
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			// Locations.List runs after the project-scoped Triggers.List
			// (phase 1) already proved the cloudbuild service enabled — API
			// enablement is per-service, not per-API-version, so a v2-client
			// isAPINotEnabled-shaped error here would be spurious. Never let
			// it escalate to the whole-service disabled sentinel.
			_ = skipIfDenied(st, "cloudbuild:locations.list", p.ID, err)
		} else {
			return total, inserted, err
		}
	}

	// Phase 3: per-location fan-out — WorkerPools (v1), Connections (v2,
	// capturing refs for the per-connection Repositories fan-out below).
	type connRef struct {
		name string
		id   string
	}
	var mu sync.Mutex
	var connRefs []connRef
	err = forEachItem(ctx, maxConcurrentCloudBuildFanout, locations, func(gctx context.Context, loc string) error {
		parent := fmt.Sprintf("projects/%s/locations/%s", p.ID, loc)

		werr := svc.Projects.Locations.WorkerPools.List(parent).Pages(gctx, func(page *cloudbuild.ListWorkerPoolsResponse) error {
			batch := make([]*store.Resource, 0, len(page.WorkerPools))
			for _, wp := range page.WorkerPools {
				if wp == nil || wp.Name == "" {
					continue
				}
				name := lastSegment(wp.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudBuildWorkerPool,
					NativeID:       wp.Name,
					Name:           &name,
					Region:         &loc,
					Status:         strp(wp.State),
					CreatedAt:      strp(wp.CreateTime),
					AttributesJSON: mustJSON(wp),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			wt, wn, werr := upsertWithProjClosure(p, st, batch)
			total += wt
			inserted += wn
			return werr
		})
		if werr != nil {
			if isPermissionDenied(werr) {
				// A per-location call nested after the project-scoped
				// Triggers.List (phase 1) already proved the cloudbuild API
				// enabled — never let a location's own denial escalate to
				// the whole-service disabled sentinel.
				_ = skipIfDenied(st, "cloudbuild:workerPools.list", p.ID, werr)
			} else {
				return werr
			}
		}

		var localConnRefs []connRef
		cerr := svc2.Projects.Locations.Connections.List(parent).Pages(gctx, func(page *cloudbuildv2.ListConnectionsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Connections))
			for _, c := range page.Connections {
				if c == nil || c.Name == "" {
					continue
				}
				localConnRefs = append(localConnRefs, connRef{
					name: c.Name,
					id:   store.ResourceID("gcp", p.ID, TypeCloudBuildConnection, c.Name),
				})
				name := lastSegment(c.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudBuildConnection,
					NativeID:       c.Name,
					Name:           &name,
					Region:         &loc,
					CreatedAt:      strp(c.CreateTime),
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			ct, cn, cerr := upsertWithProjClosure(p, st, batch)
			total += ct
			inserted += cn
			return cerr
		})
		if cerr != nil {
			if isPermissionDenied(cerr) {
				_ = skipIfDenied(st, "cloudbuild:connections.list", p.ID, cerr)
			} else {
				return cerr
			}
		} else {
			mu.Lock()
			connRefs = append(connRefs, localConnRefs...)
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}

	// Phase 4: per-connection fan-out — Repositories (2nd-gen).
	err = forEachItem(ctx, maxConcurrentCloudBuildFanout, connRefs, func(gctx context.Context, conn connRef) error {
		rerr := svc2.Projects.Locations.Connections.Repositories.List(conn.name).Pages(gctx, func(page *cloudbuildv2.ListRepositoriesResponse) error {
			batch := make([]*store.Resource, 0, len(page.Repositories))
			for _, r := range page.Repositories {
				if r == nil || r.Name == "" {
					continue
				}
				name := lastSegment(r.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudBuildRepository,
					NativeID:       r.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(r.Name)),
					CreatedAt:      strp(r.CreateTime),
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, conn.id)
			total += rt
			inserted += rn
			return rerr
		})
		if rerr != nil {
			if isPermissionDenied(rerr) {
				_ = skipIfDenied(st, "cloudbuild:repositories.list", p.ID, rerr)
				return nil
			}
			return rerr
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}

	// Phase 5: legacy GitHub Enterprise configs. No Pages/NextPageToken on
	// this endpoint (install counts per project are always small) — single
	// Do() call. GitHubEnterpriseConfig.Name is documented as
	// `projects/{p}/locations/{location}/githubEnterpriseConfigs/{id}`, a
	// location-partitioned resource like BuildTrigger — so this queries both
	// the legacy global parent (`projects/{p}`) AND every discovered
	// location's parent (`projects/{p}/locations/{loc}`); both paths hit the
	// identical flexible `{+parent}/githubEnterpriseConfigs` URL template, so
	// any config the global call already returns is upserted again by its
	// location call as a no-op (same NativeID, same content).
	upsertGHEConfigs := func(gctx context.Context, parent string) (int, int, error) {
		resp, err := svc.Projects.GithubEnterpriseConfigs.List(parent).Context(gctx).Do()
		if err != nil {
			return 0, 0, err
		}
		batch := make([]*store.Resource, 0, len(resp.Configs))
		for _, cfg := range resp.Configs {
			if cfg == nil || cfg.Name == "" {
				continue
			}
			name := cfg.DisplayName
			if name == "" {
				name = lastSegment(cfg.Name)
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCloudBuildGithubEnterpriseConfig,
				NativeID:       cfg.Name,
				Name:           &name,
				CreatedAt:      strp(cfg.CreateTime),
				AttributesJSON: mustJSON(cfg),
				DiscoveredBy:   scanID,
			})
		}
		return upsertWithProjClosure(p, st, batch)
	}

	gt, gn, gerr := upsertGHEConfigs(ctx, fmt.Sprintf("projects/%s", p.ID))
	total += gt
	inserted += gn
	if gerr != nil {
		if isPermissionDenied(gerr) {
			// Same phase-1-already-proved-enabled reasoning as phase 2/3/4:
			// never let this escalate to the whole-service disabled sentinel.
			_ = skipIfDenied(st, "cloudbuild:githubEnterpriseConfigs.list", p.ID, gerr)
		} else {
			return total, inserted, gerr
		}
	}

	err = forEachItem(ctx, maxConcurrentCloudBuildFanout, locations, func(gctx context.Context, loc string) error {
		lt, ln, lerr := upsertGHEConfigs(gctx, fmt.Sprintf("projects/%s/locations/%s", p.ID, loc))
		mu.Lock()
		total += lt
		inserted += ln
		mu.Unlock()
		if lerr != nil {
			if isPermissionDenied(lerr) {
				_ = skipIfDenied(st, "cloudbuild:githubEnterpriseConfigs.list", p.ID, lerr)
				return nil
			}
			return lerr
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
