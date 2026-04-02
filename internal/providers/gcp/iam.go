package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/iam/v1"
)

// scanIAMServiceAccounts discovers IAM service accounts for a project.
func scanIAMServiceAccounts(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("iam client: %w", err)
	}

	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	parent := fmt.Sprintf("projects/%s", p.ID)
	req := svc.Projects.ServiceAccounts.List(parent)
	return req.Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
		var batch []*store.Resource
		for _, sa := range page.Accounts {
			name := sa.DisplayName
			if name == "" {
				name = sa.Email
			}
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           "gcp:iam:service-account",
				NativeID:       sa.Name,
				Name:           &name,
				AttributesJSON: mustJSON(sa),
				ScanID:         scanID,
				ParentID:       &projParentID,
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert IAM service accounts: %w", err)
		}
		return nil
	})
}
