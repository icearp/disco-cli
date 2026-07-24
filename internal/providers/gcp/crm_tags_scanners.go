package gcp

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/cloudresourcemanager/v3"
)

// Wave 8b of the GCP type-coverage buildout (docs/gcp-type-coverage.md):
// Cloud Resource Manager Tags + Liens.
//
// A TagKey/TagValue/TagHold tree can be parented by EITHER an organization OR
// a project directly (TagKey.Parent's doc comment), so it's scanned from two
// entry points: once org-wide via registerOrgService (catches every
// org-parented tree in one pass, regardless of which project/folder created
// it), and once per-project via registerService (catches trees parented
// directly by a project that has no organization at all, or whose creator
// lacked org-level tag permissions). Lien/TagBinding/EffectiveTag are
// project-scoped only — GCP has no org-level Lien/TagBinding endpoint.
func init() {
	registerType(restype.Descriptor{Type: TypeTagKey, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTagValue, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTagHold, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeLien, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTagBinding, Service: "cloudresourcemanager"})
	registerType(restype.Descriptor{Type: TypeEffectiveTag, Service: "cloudresourcemanager"})
	registerType(restype.Descriptor{Type: TypeTagKey, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTagValue, Service: "cloudresourcemanager", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTagHold, Service: "cloudresourcemanager", Leaf: true})
	registerOrgService(orgServiceEntry{
		name: "gcp:cloudresourcemanager-tags",
		fn:   scanCRMTags,
	})
	registerService(serviceEntry{
		name: "gcp:cloudresourcemanager-liens",
		fn:   scanCRMLiensAndBindings,
	})
}

// scanCRMTags discovers the org-wide TagKey → TagValue → TagHold tree.
// Folder scopes are skipped — TagKeys.List(parent=organizations/{id}) already
// returns every org-parented TagKey regardless of which folder/project it
// was created under, so a folder-scoped re-list would just duplicate rows.
// Project-parented trees (a TagKey can be parented directly by a project,
// bypassing the org entirely) are covered separately by
// scanCRMLiensAndBindings, which runs per-project.
func scanCRMTags(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudresourcemanager client: %w", err)
	}
	for _, sc := range scopes {
		if sc.Kind != "organization" {
			continue
		}
		t, n, perr := scanCRMTagsUnder(ctx, svc, sc.Name, sc.Name, sc.Resource, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}

// scanCRMTagsUnder lists TagKeys parented by listParent ("organizations/{id}"
// or "projects/{id}") and fans out into TagValues/TagHolds beneath each.
// accountID scopes the resulting resource rows (org name or bare project ID,
// matching each entry point's own AccountID convention); closureParentID is
// the disco resource ID each TagKey attaches to in hierarchy_closure.
func scanCRMTagsUnder(ctx context.Context, svc *cloudresourcemanager.Service, accountID, listParent, closureParentID string, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.TagKeys.List().Parent(listParent).Pages(ctx, func(page *cloudresourcemanager.ListTagKeysResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, tk := range page.TagKeys {
			name := tk.ShortName
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: accountID,
				Type: TypeTagKey, NativeID: tk.Name, Name: &name,
				CreatedAt: strp(tk.CreateTime), AttributesJSON: mustJSON(tk),
				DiscoveredBy: scanID,
			})
			tkID := store.ResourceID("gcp", accountID, tk.Name)
			pairs = append(pairs, [2]string{tkID, closureParentID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert tag keys: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure tag keys: %w", cErr)
		}
		for _, tk := range page.TagKeys {
			t, n, tvErr := scanCRMTagValuesUnder(ctx, svc, accountID, tk, st, scanID)
			total += t
			inserted += n
			if tvErr != nil {
				return tvErr
			}
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:tagKeys.list", listParent, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

func scanCRMTagValuesUnder(ctx context.Context, svc *cloudresourcemanager.Service, accountID string, tk *cloudresourcemanager.TagKey, st *store.Store, scanID string) (total, inserted int, err error) {
	tkID := store.ResourceID("gcp", accountID, tk.Name)
	err = svc.TagValues.List().Parent(tk.Name).Pages(ctx, func(page *cloudresourcemanager.ListTagValuesResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, tv := range page.TagValues {
			name := tv.ShortName
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: accountID,
				Type: TypeTagValue, NativeID: tv.Name, Name: &name,
				CreatedAt: strp(tv.CreateTime), AttributesJSON: mustJSON(tv),
				DiscoveredBy: scanID,
			})
			tvID := store.ResourceID("gcp", accountID, tv.Name)
			pairs = append(pairs, [2]string{tvID, tkID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert tag values: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure tag values: %w", cErr)
		}
		for _, tv := range page.TagValues {
			t, n, thErr := scanCRMTagHoldsUnder(ctx, svc, accountID, tv, st, scanID)
			total += t
			inserted += n
			if thErr != nil {
				return thErr
			}
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:tagValues.list", tk.Name, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

func scanCRMTagHoldsUnder(ctx context.Context, svc *cloudresourcemanager.Service, accountID string, tv *cloudresourcemanager.TagValue, st *store.Store, scanID string) (total, inserted int, err error) {
	tvID := store.ResourceID("gcp", accountID, tv.Name)
	err = svc.TagValues.TagHolds.List(tv.Name).Pages(ctx, func(page *cloudresourcemanager.ListTagHoldsResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, th := range page.TagHolds {
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: accountID,
				Type: TypeTagHold, NativeID: th.Name,
				CreatedAt: strp(th.CreateTime), AttributesJSON: mustJSON(th),
				DiscoveredBy: scanID,
			})
			thID := store.ResourceID("gcp", accountID, th.Name)
			pairs = append(pairs, [2]string{thID, tvID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert tag holds: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure tag holds: %w", cErr)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:tagValues.tagHolds.list", tv.Name, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// crmProjectFullResourceName builds the full-resource-name form of a project
// (https://cloud.google.com/apis/design/resource_names#full_resource_name)
// required by TagBindings.List/EffectiveTags.List's `parent` param. Only the
// project NUMBER (not the ID) can build it — ok is false when projectNumber
// is empty, signaling the caller to skip rather than send a malformed parent.
func crmProjectFullResourceName(projectNumber string) (name string, ok bool) {
	if projectNumber == "" {
		return "", false
	}
	return "//cloudresourcemanager.googleapis.com/projects/" + projectNumber, true
}

// scanCRMLiensAndBindings discovers project-scoped Liens, any TagKey tree
// parented directly by this project (see package doc comment), and the
// TagBinding/EffectiveTag sets attached directly to the project resource.
//
// TagBindings/EffectiveTags formally support any Google Cloud resource as
// `parent` (the ledger's original "fan-out per resource" note), but
// enumerating every scanned resource of every type in a project to check for
// tag bindings would multiply the API-call count across the entire scan for
// a feature most commonly used at the project/folder/org level for
// cost-allocation and IAM-condition tagging. Scoped down to the project
// resource itself — same judgment call as Wave 7's ReservationSlot
// deferral (see docs/gcp-type-coverage.md).
func scanCRMLiensAndBindings(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudresourcemanager client: %w", err)
	}
	projParentID := store.ResourceID("gcp", p.ID, p.ID)

	t, n, err := scanCRMLiens(ctx, svc, p, projParentID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanCRMTagsUnder(ctx, svc, p.ID, "projects/"+p.ID, projParentID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	fullResourceName, ok := crmProjectFullResourceName(p.Number)
	if !ok {
		// p.Number is populated by scanHierarchy from a successful
		// Projects.Get call; it stays "" when that call was
		// permission-denied for this project (the project is still
		// scanned — CRM access and per-service scan access are
		// independent grants). Skip rather than send a malformed
		// parent that would 400.
		return total, inserted, nil
	}

	t, n, err = scanCRMTagBindings(ctx, svc, p, fullResourceName, projParentID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanCRMEffectiveTags(ctx, svc, p, fullResourceName, projParentID, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

func scanCRMLiens(ctx context.Context, svc *cloudresourcemanager.Service, p *project, projParentID string, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.Liens.List().Parent("projects/"+p.ID).Pages(ctx, func(page *cloudresourcemanager.ListLiensResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, l := range page.Liens {
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: p.ID, AccountName: &p.Name,
				Type: TypeLien, NativeID: l.Name,
				CreatedAt: strp(l.CreateTime), AttributesJSON: mustJSON(l),
				DiscoveredBy: scanID,
			})
			lID := store.ResourceID("gcp", p.ID, l.Name)
			pairs = append(pairs, [2]string{lID, projParentID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert liens: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure liens: %w", cErr)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:liens.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

func scanCRMTagBindings(ctx context.Context, svc *cloudresourcemanager.Service, p *project, fullResourceName, projParentID string, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.TagBindings.List().Parent(fullResourceName).Pages(ctx, func(page *cloudresourcemanager.ListTagBindingsResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, tb := range page.TagBindings {
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: p.ID, AccountName: &p.Name,
				Type: TypeTagBinding, NativeID: tb.Name,
				AttributesJSON: mustJSON(tb),
				DiscoveredBy:   scanID,
			})
			tbID := store.ResourceID("gcp", p.ID, tb.Name)
			pairs = append(pairs, [2]string{tbID, projParentID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert tag bindings: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure tag bindings: %w", cErr)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:tagBindings.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanCRMEffectiveTags synthesizes a NativeID from the parent resource's full
// resource name + the effective TagValue — EffectiveTag has no name/self-link
// of its own (it's a computed view, not a stored resource), same synthetic-
// NativeID shape as gcp:dns:resource-record-set / gcp:iam:policy. Exactly one
// EffectiveTag exists per (project, TagValue) pair, so the pair is a stable,
// collision-free natural key.
func scanCRMEffectiveTags(ctx context.Context, svc *cloudresourcemanager.Service, p *project, fullResourceName, projParentID string, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.EffectiveTags.List().Parent(fullResourceName).Pages(ctx, func(page *cloudresourcemanager.ListEffectiveTagsResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, et := range page.EffectiveTags {
			nativeID := fullResourceName + "/effectiveTags/" + lastSegment(et.TagValue)
			name := et.NamespacedTagValue
			batch = append(batch, &store.Resource{
				Provider: "gcp", Region: regionGlobal, AccountID: p.ID, AccountName: &p.Name,
				Type: TypeEffectiveTag, NativeID: nativeID, Name: &name,
				AttributesJSON: mustJSON(et),
				DiscoveredBy:   scanID,
			})
			etID := store.ResourceID("gcp", p.ID, nativeID)
			pairs = append(pairs, [2]string{etID, projParentID})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert effective tags: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure effective tags: %w", cErr)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "cloudresourcemanager:effectiveTags.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}
