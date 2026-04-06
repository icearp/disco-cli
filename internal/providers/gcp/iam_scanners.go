package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/iam/v1"
)

func init() { registerService(serviceEntry{name: "gcp:iam", fn: scanIAMServiceAccounts}) }

// scanIAMServiceAccounts discovers IAM service accounts for a project.
func scanIAMServiceAccounts(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("iam client: %w", err)
	}

	parent := fmt.Sprintf("projects/%s", p.ID)
	req := svc.Projects.ServiceAccounts.List(parent)
	if err := req.Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
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
				Type:           TypeIAMServiceAccount,
				NativeID:       sa.Name,
				Name:           &name,
				AttributesJSON: mustJSON(sa),
				DiscoveredBy:         scanID,
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
	}); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("iam:serviceAccounts.list", p.ID, err)
		}
		return err
	}
	return nil
}
