package gcp

import (
	"context"
	"fmt"
	"strings"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/cloudresourcemanager/v3"
)

// scanHierarchy discovers the GCP org → folder → project tree and populates
// the hierarchy_closure table. It must run before any project-scoped resources
// are upserted, since projects are used as parent_id for those resources.
func scanHierarchy(ctx context.Context, projects []project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	crmSvc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("cloudresourcemanager client: %w", err)
	}

	// Collect all distinct parent references from the project list.
	seen := map[string]bool{}
	var orgs, folders []string
	for _, p := range projects {
		proj, err := crmSvc.Projects.Get(fmt.Sprintf("projects/%s", p.ID)).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return fmt.Errorf("cloudresourcemanager:GetProject %s: %w", p.ID, err)
		}
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
			return fmt.Errorf("cloudresourcemanager:GetOrganization %s: %w", orgName, err)
		}
		id := org.Name // "organizations/123"
		name := org.DisplayName
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      id,
			Type:           "gcp:cloudresourcemanager:organization",
			NativeID:       id,
			Name:           &name,
			AttributesJSON: mustJSON(org),
			ScanID:         scanID,
		}
		if err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return err
		}
		// Organizations are roots; add self-entry to closure table.
		orgResourceID := store.ResourceID("gcp", id, "gcp:cloudresourcemanager:organization", id)
		if err := st.AddToHierarchyClosure(orgResourceID, orgResourceID); err != nil {
			return err
		}
	}

	// Upsert folders.
	for _, folderName := range folders {
		folder, err := crmSvc.Folders.Get(folderName).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return fmt.Errorf("cloudresourcemanager:GetFolder %s: %w", folderName, err)
		}
		id := folder.Name // "folders/123"
		displayName := folder.DisplayName
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      id,
			Type:           "gcp:cloudresourcemanager:folder",
			NativeID:       id,
			Name:           &displayName,
			AttributesJSON: mustJSON(folder),
			ScanID:         scanID,
		}
		if err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return err
		}
		folderResourceID := store.ResourceID("gcp", id, "gcp:cloudresourcemanager:folder", id)
		if folder.Parent != "" {
			parentResourceID := gcpParentResourceID(folder.Parent)
			if err := st.AddToHierarchyClosure(folderResourceID, parentResourceID); err != nil {
				return err
			}
		}
	}

	// Upsert projects and link them into the hierarchy.
	for i := range projects {
		p := &projects[i]
		proj, err := crmSvc.Projects.Get(fmt.Sprintf("projects/%s", p.ID)).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				continue
			}
			return fmt.Errorf("cloudresourcemanager:GetProject %s: %w", p.ID, err)
		}
		p.Name = proj.DisplayName
		p.Number = projectNumber(proj.Name)

		projResourceID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			Type:           "gcp:cloudresourcemanager:project",
			NativeID:       proj.Name,
			Name:           &p.Name,
			AttributesJSON: mustJSON(proj),
			ScanID:         scanID,
		}
		if proj.Parent != "" {
			parentResourceID := gcpParentResourceID(proj.Parent)
			r.ParentID = &parentResourceID
			p.ParentID = parentResourceID
		}
		if err := st.UpsertResources([]*store.Resource{r}); err != nil {
			return err
		}
		if proj.Parent != "" {
			if err := st.AddToHierarchyClosure(projResourceID, p.ParentID); err != nil {
				return err
			}
		}
	}
	return nil
}

// gcpParentResourceID converts a GCP parent name ("folders/123" or
// "organizations/456") to a disco resource ID.
func gcpParentResourceID(parent string) string {
	if strings.HasPrefix(parent, "organizations/") {
		return store.ResourceID("gcp", parent, "gcp:cloudresourcemanager:organization", parent)
	}
	if strings.HasPrefix(parent, "folders/") {
		return store.ResourceID("gcp", parent, "gcp:cloudresourcemanager:folder", parent)
	}
	return ""
}
