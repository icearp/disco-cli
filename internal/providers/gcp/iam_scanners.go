package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
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
	return runPaginated(ctx, st, p, "iam:serviceAccounts.list",
		svc.Projects.ServiceAccounts.List(parent),
		func(page *iam.ListServiceAccountsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Accounts))
			for _, sa := range page.Accounts {
				name := sa.DisplayName
				if name == "" {
					name = sa.Email
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeIAMServiceAccount,
					NativeID:       sa.Name,
					Name:           &name,
					AttributesJSON: mustJSON(sa),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert IAM service accounts: %w", e)
			}
			return len(batch), n, nil
		})
}
