package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveArtifactRegistryRelationships,
		EdgeDecl{TypeArtifactRepository, TypeKMSCryptoKey, store.RelUses},
	)
	registerResolver(resolveArtifactRuleRelationships,
		EdgeDecl{TypeArtifactRule, TypeArtifactPackage, store.RelUses},
	)
	registerResolver(resolveArtifactAttachmentRelationships,
		EdgeDecl{TypeArtifactAttachment, TypeArtifactPackage, store.RelUses},
	)
}

// resolveArtifactRegistryRelationships derives repository -[uses]-> cryptoKey
// CMEK edges via `kmsKeyName`. Reverse edges (GKE / Cloud Run → repository
// pull) deferred — they need image-ref parsing from container specs, which
// neither workload scanner exposes as structured fields today.
func resolveArtifactRegistryRelationships(p *project, st *store.Store) error {
	repos, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeArtifactRepository},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return nil
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}
	for _, r := range repos {
		var a struct {
			KmsKeyName string `json:"kmsKeyName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.KmsKeyName == "" {
			continue
		}
		keyID, ok := keyIDByNative[stripCryptoKeyVersion(a.KmsKeyName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert repo→cryptoKey: %w", err)
		}
	}
	return nil
}

// Resolver Wave R23 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Rule and Attachment, the 2 remaining artifactregistry
// orphans.
//
// Rule.packageId is documented as a bare package ID scoped to the rule's own
// owning repository ("if empty, this rule applies to all packages inside the
// repository") — when empty there's nothing more specific to wire than the
// repository-level scope the rule's own hierarchy-closure parent (set by the
// scanner's upsertWithParent) already expresses, so only the populated case
// gets an edge. The owning repository's resource-name prefix is derived from
// the Rule's own NativeID by trimming its "/rules/{id}" suffix (verified via
// `go doc`: Rule.Name is
// "projects/p1/locations/us-central1/repositories/repo1/rules/rule1"), then
// the bare packageId is appended to reconstruct the full Package NativeID —
// same reconstruct-from-own-NativeID technique as Dataproc's Job->Cluster
// resolver (R12/R19).
//
// Attachment.target is a heterogeneous oneof-by-path-shape — it can name a
// Version (unscanned by this provider, see the scanner's own header comment
// on cardinality), a Package, or the Attachment's own owning Repository
// (verified via `go doc`: all three example shapes share the same
// `.../repositories/{r}/...` prefix, differing only in what follows). Each
// Attachment row is classified per-entry by trailing path shape (mirrors
// Wave R16's LogScope per-entry classification): a target containing
// "/versions/" points at an unscanned Version — skipped; a target with no
// "/packages/" segment at all can only be the Attachment's own owning
// Repository (the only other shape in the doc's oneof), which is redundant
// with the hierarchy-closure parent edge the scanner already writes —
// skipped; only a target containing "/packages/" with no "/versions/"
// suffix is a genuine Package reference, wired by exact NativeID match.
func resolveArtifactRuleRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeArtifactRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedPkgs, err := scannedIDSet(p, st, TypeArtifactPackage)
	if err != nil {
		return err
	}
	if len(scannedPkgs) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			PackageId string `json:"packageId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PackageId == "" {
			continue
		}
		repoPrefix, _, ok := strings.Cut(r.NativeID, "/rules/")
		if !ok {
			continue
		}
		pkgNativeID := repoPrefix + "/packages/" + attrs.PackageId
		if err := upsertIfScanned(st, scannedPkgs, r.ID, "gcp", p.ID, TypeArtifactPackage, pkgNativeID, store.RelUses); err != nil {
			return fmt.Errorf("upsert rule→package: %w", err)
		}
	}
	return nil
}

func resolveArtifactAttachmentRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeArtifactAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedPkgs, err := scannedIDSet(p, st, TypeArtifactPackage)
	if err != nil {
		return err
	}
	if len(scannedPkgs) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Target == "" || strings.Contains(attrs.Target, "/versions/") {
			continue
		}
		if !strings.Contains(attrs.Target, "/packages/") {
			continue // targets its own owning Repository — already covered by the hierarchy parent
		}
		if err := upsertIfScanned(st, scannedPkgs, r.ID, "gcp", p.ID, TypeArtifactPackage, attrs.Target, store.RelUses); err != nil {
			return fmt.Errorf("upsert attachment→package: %w", err)
		}
	}
	return nil
}
