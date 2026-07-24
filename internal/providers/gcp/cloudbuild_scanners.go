package gcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

// cloudBuildConnRef captures a 2nd-gen Connection's full resource name plus
// its computed resource ID, so phase 4 can fan out Repositories.List per
// connection and parent each repository under it.
type cloudBuildConnRef struct {
	name string
	id   string
}

// cloudBuildScan holds the shared state threaded through a single Cloud Build
// scan: the v1 and v2 SDK clients, store, project, scan ID, and the
// mutex-guarded running (total, inserted) upsert counters. The mutex guards
// the counters (and the phase-3 connRefs accumulation) across the bounded
// per-location / per-connection fan-out. Scoped to one
// scanCloudBuildWithClient call; not safe for concurrent use across scans.
type cloudBuildScan struct {
	svc    *cloudbuild.Service
	svc2   *cloudbuildv2.Service
	p      *project
	st     *store.Store
	scanID string

	mu       sync.Mutex
	total    int
	inserted int
}

// scanTriggers runs phase 1: project-scoped BuildTriggers.List. Counters are
// updated even on error to match the original's accumulate-then-check order.
func (s *cloudBuildScan) scanTriggers(ctx context.Context) error {
	t, n, err := runPaginated(ctx, s.st, s.p, "cloudbuild:triggers.list",
		s.svc.Projects.Triggers.List(s.p.ID),
		func(page *cloudbuild.ListBuildTriggersResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Triggers))
			for _, tr := range page.Triggers {
				name := tr.Name
				nativeID := tr.ResourceName
				if nativeID == "" {
					nativeID = fmt.Sprintf("projects/%s/triggers/%s", s.p.ID, tr.Id)
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      s.p.ID,
					AccountName:    &s.p.Name,
					Type:           TypeCloudBuildTrigger,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(tr.CreateTime),
					AttributesJSON: mustJSON(tr),
					DiscoveredBy:   s.scanID,
				})
			}
			return upsertWithProjClosure(s.p, s.st, batch)
		})
	s.total += t
	s.inserted += n
	return err
}

// discoverLocations runs phase 2: the v2 Locations.List catalog, reused for
// both the v1 WorkerPools and v2 Connections per-location fan-out. A
// permission denial is skip-logged (never escalated to the whole-service
// disabled sentinel) and yields an empty location list.
func (s *cloudBuildScan) discoverLocations(ctx context.Context) ([]string, error) {
	var locations []string
	if err := s.svc2.Projects.Locations.List(fmt.Sprintf("projects/%s", s.p.ID)).Pages(ctx, func(page *cloudbuildv2.ListLocationsResponse) error {
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
			_ = skipIfDenied(s.st, "cloudbuild:locations.list", s.p.ID, err)
		} else {
			return nil, err
		}
	}
	return locations, nil
}

// scanWorkerPools lists v1 WorkerPools for one location parent, accumulating
// upsert counts under the mutex. A permission denial is skip-logged and
// returns nil so the sibling Connections list still runs; any other error
// propagates.
func (s *cloudBuildScan) scanWorkerPools(ctx context.Context, parent, loc string) error {
	werr := s.svc.Projects.Locations.WorkerPools.List(parent).Pages(ctx, func(page *cloudbuild.ListWorkerPoolsResponse) error {
		batch := make([]*store.Resource, 0, len(page.WorkerPools))
		for _, wp := range page.WorkerPools {
			if wp == nil || wp.Name == "" {
				continue
			}
			name := lastSegment(wp.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      s.p.ID,
				AccountName:    &s.p.Name,
				Type:           TypeCloudBuildWorkerPool,
				NativeID:       wp.Name,
				Name:           &name,
				Region:         &loc,
				Status:         strp(wp.State),
				CreatedAt:      strp(wp.CreateTime),
				AttributesJSON: mustJSON(wp),
				DiscoveredBy:   s.scanID,
			})
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		wt, wn, werr := upsertWithProjClosure(s.p, s.st, batch)
		s.total += wt
		s.inserted += wn
		return werr
	})
	if werr != nil {
		if isPermissionDenied(werr) {
			// A per-location call nested after the project-scoped
			// Triggers.List (phase 1) already proved the cloudbuild API
			// enabled — never let a location's own denial escalate to
			// the whole-service disabled sentinel.
			_ = skipIfDenied(s.st, "cloudbuild:workerPools.list", s.p.ID, werr)
		} else {
			return werr
		}
	}
	return nil
}

// scanConnections lists v2 Connections for one location parent, accumulating
// upsert counts under the mutex and returning the connection refs for phase 4.
// On permission denial the refs are DISCARDED (returns nil, nil) — the caller
// must only record refs from a fully-successful list, matching the original's
// append-only-in-the-success-branch behavior.
func (s *cloudBuildScan) scanConnections(ctx context.Context, parent, loc string) ([]cloudBuildConnRef, error) {
	var localConnRefs []cloudBuildConnRef
	cerr := s.svc2.Projects.Locations.Connections.List(parent).Pages(ctx, func(page *cloudbuildv2.ListConnectionsResponse) error {
		batch := make([]*store.Resource, 0, len(page.Connections))
		for _, c := range page.Connections {
			if c == nil || c.Name == "" {
				continue
			}
			localConnRefs = append(localConnRefs, cloudBuildConnRef{
				name: c.Name,
				id:   store.ResourceID("gcp", s.p.ID, c.Name),
			})
			name := lastSegment(c.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      s.p.ID,
				AccountName:    &s.p.Name,
				Type:           TypeCloudBuildConnection,
				NativeID:       c.Name,
				Name:           &name,
				Region:         &loc,
				CreatedAt:      strp(c.CreateTime),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   s.scanID,
			})
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		ct, cn, cerr := upsertWithProjClosure(s.p, s.st, batch)
		s.total += ct
		s.inserted += cn
		return cerr
	})
	if cerr != nil {
		if isPermissionDenied(cerr) {
			_ = skipIfDenied(s.st, "cloudbuild:connections.list", s.p.ID, cerr)
			return nil, nil
		}
		return nil, cerr
	}
	return localConnRefs, nil
}

// scanWorkerPoolsAndConnections runs phase 3: the per-location fan-out over
// WorkerPools (v1) then Connections (v2), collecting the connection refs (only
// from fully-successful Connections lists) for the phase-4 Repositories
// fan-out.
func (s *cloudBuildScan) scanWorkerPoolsAndConnections(ctx context.Context, locations []string) ([]cloudBuildConnRef, error) {
	var connRefs []cloudBuildConnRef
	err := forEachItem(ctx, maxConcurrentCloudBuildFanout, locations, func(gctx context.Context, loc string) error {
		parent := fmt.Sprintf("projects/%s/locations/%s", s.p.ID, loc)
		if err := s.scanWorkerPools(gctx, parent, loc); err != nil {
			return err
		}
		localConnRefs, err := s.scanConnections(gctx, parent, loc)
		if err != nil {
			return err
		}
		s.mu.Lock()
		connRefs = append(connRefs, localConnRefs...)
		s.mu.Unlock()
		return nil
	})
	return connRefs, err
}

// scanRepositories runs phase 4: the per-connection fan-out over 2nd-gen
// Repositories, each parented under its connection. A permission denial is
// skip-logged per connection; any other error aborts the fan-out.
func (s *cloudBuildScan) scanRepositories(ctx context.Context, connRefs []cloudBuildConnRef) error {
	return forEachItem(ctx, maxConcurrentCloudBuildFanout, connRefs, func(gctx context.Context, conn cloudBuildConnRef) error {
		rerr := s.svc2.Projects.Locations.Connections.Repositories.List(conn.name).Pages(gctx, func(page *cloudbuildv2.ListRepositoriesResponse) error {
			batch := make([]*store.Resource, 0, len(page.Repositories))
			for _, r := range page.Repositories {
				if r == nil || r.Name == "" {
					continue
				}
				name := lastSegment(r.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      s.p.ID,
					AccountName:    &s.p.Name,
					Type:           TypeCloudBuildRepository,
					NativeID:       r.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(r.Name)),
					CreatedAt:      strp(r.CreateTime),
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   s.scanID,
				})
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			rt, rn, rerr := upsertWithParent(s.st, batch, conn.id)
			s.total += rt
			s.inserted += rn
			return rerr
		})
		if rerr != nil {
			if isPermissionDenied(rerr) {
				_ = skipIfDenied(s.st, "cloudbuild:repositories.list", s.p.ID, rerr)
				return nil
			}
			return rerr
		}
		return nil
	})
}

// upsertGHEConfigs lists legacy GitHub Enterprise configs under one parent
// (single Do() — no pagination) and upserts them with a project-closure edge.
func (s *cloudBuildScan) upsertGHEConfigs(ctx context.Context, parent string) (int, int, error) {
	resp, err := s.svc.Projects.GithubEnterpriseConfigs.List(parent).Context(ctx).Do()
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
			AccountID:      s.p.ID,
			AccountName:    &s.p.Name,
			Type:           TypeCloudBuildGithubEnterpriseConfig,
			NativeID:       cfg.Name,
			Name:           &name,
			CreatedAt:      strp(cfg.CreateTime),
			AttributesJSON: mustJSON(cfg),
			DiscoveredBy:   s.scanID,
		})
	}
	return upsertWithProjClosure(s.p, s.st, batch)
}

// scanGitHubEnterpriseConfigs runs phase 5: the legacy global parent
// (`projects/{p}`) plus every discovered location's parent. GHEConfig.Name is
// location-partitioned, so both paths hit the identical flexible
// `{+parent}/githubEnterpriseConfigs` URL template — any config the global
// call returns is upserted again by its location call as a no-op (same
// NativeID, same content).
func (s *cloudBuildScan) scanGitHubEnterpriseConfigs(ctx context.Context, locations []string) error {
	gt, gn, gerr := s.upsertGHEConfigs(ctx, fmt.Sprintf("projects/%s", s.p.ID))
	s.total += gt
	s.inserted += gn
	if gerr != nil {
		if isPermissionDenied(gerr) {
			// Same phase-1-already-proved-enabled reasoning as phase 2/3/4:
			// never let this escalate to the whole-service disabled sentinel.
			_ = skipIfDenied(s.st, "cloudbuild:githubEnterpriseConfigs.list", s.p.ID, gerr)
		} else {
			return gerr
		}
	}

	return forEachItem(ctx, maxConcurrentCloudBuildFanout, locations, func(gctx context.Context, loc string) error {
		lt, ln, lerr := s.upsertGHEConfigs(gctx, fmt.Sprintf("projects/%s/locations/%s", s.p.ID, loc))
		s.mu.Lock()
		s.total += lt
		s.inserted += ln
		s.mu.Unlock()
		if lerr != nil {
			if isPermissionDenied(lerr) {
				_ = skipIfDenied(s.st, "cloudbuild:githubEnterpriseConfigs.list", s.p.ID, lerr)
				return nil
			}
			return lerr
		}
		return nil
	})
}

// scanCloudBuildWithClient is the test seam for scanCloudBuildTriggers —
// takes the pre-built v1 and v2 clients directly so tests can point both at
// a fake server.
func scanCloudBuildWithClient(ctx context.Context, svc *cloudbuild.Service, svc2 *cloudbuildv2.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	s := &cloudBuildScan{svc: svc, svc2: svc2, p: p, st: st, scanID: scanID}

	// Phase 1: triggers.
	if err := s.scanTriggers(ctx); err != nil {
		return s.total, s.inserted, err
	}

	// Phase 2: discover Cloud Build's regional footprint.
	locations, err := s.discoverLocations(ctx)
	if err != nil {
		return s.total, s.inserted, err
	}

	// Phase 3: per-location WorkerPools (v1) + Connections (v2).
	connRefs, err := s.scanWorkerPoolsAndConnections(ctx, locations)
	if err != nil {
		return s.total, s.inserted, err
	}

	// Phase 4: per-connection Repositories (2nd-gen).
	if err := s.scanRepositories(ctx, connRefs); err != nil {
		return s.total, s.inserted, err
	}

	// Phase 5: legacy GitHub Enterprise configs.
	if err := s.scanGitHubEnterpriseConfigs(ctx, locations); err != nil {
		return s.total, s.inserted, err
	}
	return s.total, s.inserted, nil
}
