// Package azure implements cloud resource discovery for Microsoft Azure.
// It makes per-service API calls using the Azure SDK for Go (arm* packages)
// and follows the two-phase scan pattern.
package azure

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/sync/errgroup"
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for Azure.
type Scanner struct{}

func (s *Scanner) Name() string { return "azure" }

// Scan discovers all Azure resources across all configured subscriptions.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	subs, cred, err := loadSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("azure: load subscriptions: %w", err)
	}
	for i := range subs {
		if err := scanSubscription(ctx, &subs[i], cred, st, scanID); err != nil {
			return fmt.Errorf("azure subscription %s: %w", subs[i].ID, err)
		}
	}
	return nil
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	// Scan resource groups first (they are parents of all resources).
	if err := scanResourceGroups(ctx, sub, cred, st, scanID); err != nil {
		return err
	}

	// Scan all resource types in parallel.
	g, ctx := errgroup.WithContext(ctx)
	services := []struct {
		name string
		fn   func() error
	}{
		{"azure:compute", func() error { return scanCompute(ctx, sub, cred, st, scanID) }},
		{"azure:network", func() error { return scanNetwork(ctx, sub, cred, st, scanID) }},
		{"azure:storage", func() error { return scanStorage(ctx, sub, cred, st, scanID) }},
		{"azure:sql", func() error { return scanSQL(ctx, sub, cred, st, scanID) }},
		{"azure:aks", func() error { return scanAKS(ctx, sub, cred, st, scanID) }},
		{"azure:keyvault", func() error { return scanKeyVault(ctx, sub, cred, st, scanID) }},
	}
	for _, svc := range services {
		svc := svc
		g.Go(func() error {
			if err := svc.fn(); err != nil {
				return err
			}
			st.ReportService(svc.name)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	return resolveRelationships(ctx, sub, st)
}

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB.
func resolveRelationships(_ context.Context, sub *subscription, st *store.Store) error {
	if err := resolveVMRelationships(sub, st); err != nil {
		return err
	}
	if err := resolveSubnetVNetRelationships(sub, st); err != nil {
		return err
	}
	return resolveAKSRelationships(sub, st)
}

// — shared helpers —

// subscription holds a resolved Azure subscription.
type subscription struct {
	ID   string
	Name string
}

func mustJSON(v any) string { return util.MustJSON(v) }

// isAccessDenied reports whether err is an Azure 403/401 response error.
func isAccessDenied(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusForbidden ||
			respErr.StatusCode == http.StatusUnauthorized
	}
	return false
}

// skipIfAccessDenied logs the error and returns nil.
func skipIfAccessDenied(service, subID string, err error) error {
	log.Printf("warn: azure %s %s: %v (skipping)", service, subID, err)
	return nil
}

func sv(p *string) string { return util.Sv(p) }

// rgFromID extracts the resource group name from an Azure resource ID.
// e.g. /subscriptions/xxx/resourceGroups/myRG/... → "myRG"
func rgFromID(id string) string {
	parts := strings.Split(strings.ToLower(id), "/")
	for i, p := range parts {
		if p == "resourcegroups" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
