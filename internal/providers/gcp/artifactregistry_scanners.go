package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/artifactregistry/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:artifactregistry",
		fn:   scanArtifactRegistry,
	})
	registerType(restype.Descriptor{Type: TypeArtifactRepository, Service: "artifactregistry", Upstream: "artifactregistry.googleapis.com/Repository"})
	registerType(restype.Descriptor{Type: TypeArtifactPackage, Service: "artifactregistry", Upstream: "artifactregistry.googleapis.com/Package", Leaf: true})
	registerType(restype.Descriptor{Type: TypeArtifactTag, Service: "artifactregistry", Upstream: "artifactregistry.googleapis.com/Tag", Leaf: true})
	registerType(restype.Descriptor{Type: TypeArtifactRule, Service: "artifactregistry", Upstream: "artifactregistry.googleapis.com/Rule"})
	registerType(restype.Descriptor{Type: TypeArtifactAttachment, Service: "artifactregistry", Upstream: "artifactregistry.googleapis.com/Attachment"})
}

// maxConcurrentArtifactFanout caps the per-Repository (Packages/Rules/
// Attachments) and per-Package (Tags) fan-out phases.
const maxConcurrentArtifactFanout = 10

// scanArtifactRegistry discovers Artifact Registry repositories across every
// location via the `locations/-` wildcard, then fans out per repository for
// Packages, Rules, and Attachments, then per package for Tags.
//
// Deliberately NOT scanned (see docs/gcp-type-coverage.md Wave 11b for the
// full reasoning): Version, and the format-specific per-artifact views
// DockerImage/MavenArtifact/NpmPackage/PythonPackage, all share the same
// cardinality profile — one row per pushed image/artifact rather than per
// logical package, unbounded on busy/CI-fed registries with thousands of
// untagged builds. Tag already captures the graph/security-relevant named
// subset (a human- or pipeline-assigned reference), so scanning the raw
// per-push rows underneath it buys volume, not signal, for a provider that
// doesn't do package-content/dependency-graph analysis. (Note: only a
// handful of DockerImage's fields are actually mirrored into Version's
// Metadata per its own doc comment — MavenArtifact/NpmPackage/PythonPackage
// are NOT duplicates of anything else scanned, they're simply out of scope
// for the same cardinality reason as Version.) PrewarmedArtifact has no
// resource Name field and no List RPC at all — not independently
// addressable, and a cache-warming performance feature, not a security- or
// architecture-relevant resource. File (raw blob content, same cardinality
// class as S3 objects — never scanned for the same reason).
func scanArtifactRegistry(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := artifactregistry.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("artifactregistry client: %w", err)
	}
	return scanArtifactRegistryWithClient(ctx, svc, p, st, scanID)
}

// scanArtifactRegistryWithClient is the test seam for scanArtifactRegistry —
// takes the pre-built client directly so tests can point it at a fake
// server.
func scanArtifactRegistryWithClient(ctx context.Context, svc *artifactregistry.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: repositories — capture (name, resourceID) pairs for the
	// per-repository fan-out below.
	type repoRef struct {
		name string // full resource name, e.g. projects/p/locations/l/repositories/r
		id   string
	}
	var repoRefs []repoRef
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	t, n, err := runPaginated(ctx, st, p, "artifactregistry:repositories.list",
		svc.Projects.Locations.Repositories.List(parent),
		func(page *artifactregistry.ListRepositoriesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Repositories))
			for _, r := range page.Repositories {
				if r == nil || r.Name == "" {
					continue
				}
				repoRefs = append(repoRefs, repoRef{
					name: r.Name,
					id:   store.ResourceID("gcp", p.ID, r.Name),
				})
				name := lastSegment(r.Name)
				region := locationFromResourceName(r.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeArtifactRepository,
					NativeID:       r.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(r.CreateTime),
					AttributesJSON: mustJSON(r),
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

	// Phase 2: per-repository fan-out — Packages (capturing refs for the
	// per-package Tags fan-out in phase 3), Rules, Attachments.
	type pkgRef struct {
		name string
		id   string
	}
	var mu sync.Mutex
	var pkgRefs []pkgRef
	err = forEachItem(ctx, maxConcurrentArtifactFanout, repoRefs, func(gctx context.Context, repo repoRef) error {
		var localPkgRefs []pkgRef
		perr := svc.Projects.Locations.Repositories.Packages.List(repo.name).Pages(gctx, func(page *artifactregistry.ListPackagesResponse) error {
			batch := make([]*store.Resource, 0, len(page.Packages))
			for _, pkg := range page.Packages {
				if pkg == nil || pkg.Name == "" {
					continue
				}
				localPkgRefs = append(localPkgRefs, pkgRef{
					name: pkg.Name,
					id:   store.ResourceID("gcp", p.ID, pkg.Name),
				})
				name := lastSegment(pkg.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeArtifactPackage,
					NativeID:       pkg.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(pkg.Name)),
					CreatedAt:      strp(pkg.CreateTime),
					AttributesJSON: mustJSON(pkg),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			pt, pn, perr := upsertWithParent(st, batch, repo.id)
			total += pt
			inserted += pn
			return perr
		})
		if perr != nil {
			if isPermissionDenied(perr) {
				// Packages/Rules/Attachments/Tags are all nested under a
				// per-repository or per-package fan-out that only runs after
				// Repositories.List (phase 1) already proved the
				// artifactregistry API enabled for this project. Never let a
				// nested call's isAPINotEnabled-shaped error escalate to the
				// whole-service disabled sentinel — always warn and move on.
				_ = skipIfDenied(st, "artifactregistry:packages.list", p.ID, perr)
			} else {
				return perr
			}
		} else {
			mu.Lock()
			pkgRefs = append(pkgRefs, localPkgRefs...)
			mu.Unlock()
		}

		rerr := svc.Projects.Locations.Repositories.Rules.List(repo.name).Pages(gctx, func(page *artifactregistry.ListRulesResponse) error {
			batch := make([]*store.Resource, 0, len(page.Rules))
			for _, rule := range page.Rules {
				if rule == nil || rule.Name == "" {
					continue
				}
				name := lastSegment(rule.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeArtifactRule,
					NativeID:       rule.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(rule.Name)),
					AttributesJSON: mustJSON(rule),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, repo.id)
			total += rt
			inserted += rn
			return rerr
		})
		if rerr != nil {
			if isPermissionDenied(rerr) {
				_ = skipIfDenied(st, "artifactregistry:rules.list", p.ID, rerr)
			} else {
				return rerr
			}
		}

		aerr := svc.Projects.Locations.Repositories.Attachments.List(repo.name).Pages(gctx, func(page *artifactregistry.ListAttachmentsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Attachments))
			for _, att := range page.Attachments {
				if att == nil || att.Name == "" {
					continue
				}
				name := lastSegment(att.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeArtifactAttachment,
					NativeID:       att.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(att.Name)),
					CreatedAt:      strp(att.CreateTime),
					AttributesJSON: mustJSON(att),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			at, an, aerr := upsertWithParent(st, batch, repo.id)
			total += at
			inserted += an
			return aerr
		})
		if aerr != nil {
			if isPermissionDenied(aerr) {
				_ = skipIfDenied(st, "artifactregistry:attachments.list", p.ID, aerr)
			} else {
				return aerr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}

	// Phase 3: per-package fan-out — Tags.
	err = forEachItem(ctx, maxConcurrentArtifactFanout, pkgRefs, func(gctx context.Context, pkg pkgRef) error {
		terr := svc.Projects.Locations.Repositories.Packages.Tags.List(pkg.name).Pages(gctx, func(page *artifactregistry.ListTagsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Tags))
			for _, tag := range page.Tags {
				if tag == nil || tag.Name == "" {
					continue
				}
				name := lastSegment(tag.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeArtifactTag,
					NativeID:       tag.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(tag.Name)),
					AttributesJSON: mustJSON(tag),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			tt, tn, terr := upsertWithParent(st, batch, pkg.id)
			total += tt
			inserted += tn
			return terr
		})
		if terr != nil {
			if isPermissionDenied(terr) {
				_ = skipIfDenied(st, "artifactregistry:tags.list", p.ID, terr)
			} else {
				return terr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
