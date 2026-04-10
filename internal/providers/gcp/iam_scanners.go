package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/iam/v1"
)

func init() { registerService(serviceEntry{name: "gcp:iam", fn: scanIAMServiceAccounts}) }

// scanIAMServiceAccounts discovers IAM service accounts for a project.
func scanIAMServiceAccounts(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("iam client: %w", err)
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
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		n, e := st.UpsertResources(batch)
		if e != nil {
			return fmt.Errorf("upsert IAM service accounts: %w", e)
		}
		total += len(batch)
		inserted += n
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied("iam:serviceAccounts.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
