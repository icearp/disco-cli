package gcp

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudresourcemanager/v3"
)

func init() {
	// scanHierarchy runs direct from gcp.go (not via registerService) — it
	// fires once before per-project fan-out and emits the project/folder/
	// organization rows resolvers anchor against. Declared via
	// registerExtraEmits so the coverage matrix still picks up its emits.
	registerExtraEmits(
		coverage.TypeDecl{Service: "cloudresourcemanager", DiscoType: TypeOrganization, Leaf: true},
		// Folder's only outbound reference is `Parent`, already wired via
		// RecordHierarchy above (a hierarchy_closure/contains edge, not a
		// registerResolver edge) — mirrors Organization/Project's Leaf flag.
		// `ManagementProject` (rare app-management-capability field,
		// referencing an arbitrary project outside any predictable scope)
		// is deliberately not resolved — not worth the cross-project
		// placeholder machinery for a niche, often-empty field.
		coverage.TypeDecl{Service: "cloudresourcemanager", DiscoType: TypeFolder, Leaf: true},
		coverage.TypeDecl{Service: "cloudresourcemanager", DiscoType: TypeProject, Leaf: true},
	)
}

// scanHierarchy discovers the GCP org → folder → project tree and populates
// the hierarchy_closure table. It must run before any project-scoped resources
// are upserted, since projects are used as parent_id for those resources.
func scanHierarchy(ctx context.Context, projects []project, st *store.Store, scanID string) ([]orgScope, error) {
	opts := clientOptions(ctx, providerCfg{})
	crmSvc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cloudresourcemanager client: %w", err)
	}

	// Fetch each project once and cache — reused below when upserting project
	// resources, avoiding a second round of API calls.
	projCache := make(map[string]*cloudresourcemanager.Project, len(projects))
	seen := map[string]bool{}
	var orgs, folders []string
	var scopes []orgScope // org+folder scopes seen at least once; consumed by org-services
	for _, p := range projects {
		proj, err := crmSvc.Projects.Get(fmt.Sprintf("projects/%s", p.ID)).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return nil, fmt.Errorf("cloudresourcemanager:GetProject %s: %w", p.ID, err)
		}
		projCache[p.ID] = proj
		parent := proj.Parent // e.g. "folders/123" or "organizations/456"
		if parent != "" && !seen[parent] {
			seen[parent] = true
			if strings.HasPrefix(parent, "organizations/") {
				orgs = append(orgs, parent)
			} else if strings.HasPrefix(parent, "folders/") {
				folders = append(folders, parent)
			}
		}
	}

	// Upsert organizations.
	for _, orgName := range orgs {
		org, err := crmSvc.Organizations.Get(orgName).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return nil, fmt.Errorf("cloudresourcemanager:GetOrganization %s: %w", orgName, err)
		}
		id := org.Name // "organizations/123"
		name := org.DisplayName
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      id,
			Type:           TypeOrganization,
			NativeID:       id,
			Name:           &name,
			AttributesJSON: mustJSON(org),
			DiscoveredBy:   scanID,
		}
		if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return nil, err
		}
		// Organizations are roots; add self-entry to closure table.
		orgResourceID := store.ResourceID("gcp", id, TypeOrganization, id)
		if err := st.RecordHierarchy(orgResourceID, orgResourceID); err != nil {
			return nil, err
		}
		scopes = append(scopes, orgScope{Kind: "organization", Name: id, Resource: orgResourceID})
	}

	// Upsert folders.
	for _, folderName := range folders {
		folder, err := crmSvc.Folders.Get(folderName).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return nil, fmt.Errorf("cloudresourcemanager:GetFolder %s: %w", folderName, err)
		}
		id := folder.Name // "folders/123"
		displayName := folder.DisplayName
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      id,
			Type:           TypeFolder,
			NativeID:       id,
			Name:           &displayName,
			AttributesJSON: mustJSON(folder),
			DiscoveredBy:   scanID,
		}
		if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return nil, err
		}
		folderResourceID := store.ResourceID("gcp", id, TypeFolder, id)
		if folder.Parent != "" {
			parentResourceID := gcpParentResourceID(folder.Parent)
			if err := st.RecordHierarchy(folderResourceID, parentResourceID); err != nil {
				return nil, err
			}
		}
		scopes = append(scopes, orgScope{Kind: "folder", Name: id, Resource: folderResourceID})
	}

	// Upsert projects and link them into the hierarchy.
	// proj data comes from projCache populated above — no second API call needed.
	for i := range projects {
		p := &projects[i]
		proj, ok := projCache[p.ID]
		if !ok {
			continue // was permission-denied during fetch; skip
		}
		p.Name = proj.DisplayName
		p.Number = projectNumber(proj.Name)

		projResourceID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			Type:           TypeProject,
			NativeID:       p.ID, // project ID (e.g. "my-project-123"); proj.Name is in attributes
			Name:           &p.Name,
			AttributesJSON: mustJSON(proj),
			DiscoveredBy:   scanID,
		}
		if proj.Parent != "" {
			p.ParentID = gcpParentResourceID(proj.Parent)
		}
		if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return nil, err
		}
		if proj.Parent != "" {
			if err := st.RecordHierarchy(projResourceID, p.ParentID); err != nil {
				return nil, err
			}
		}
	}
	return scopes, nil
}

// gcpParentResourceID converts a GCP parent name ("folders/123" or
// "organizations/456") to a disco resource ID.
func gcpParentResourceID(parent string) string {
	if strings.HasPrefix(parent, "organizations/") {
		return store.ResourceID("gcp", parent, TypeOrganization, parent)
	}
	if strings.HasPrefix(parent, "folders/") {
		return store.ResourceID("gcp", parent, TypeFolder, parent)
	}
	return ""
}
