// Package azure implements cloud resource discovery for Microsoft Azure.
// It makes per-service API calls using the Azure SDK for Go (arm* packages)
// and follows the two-phase scan pattern.
package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
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
	g.Go(func() error { return scanCompute(ctx, sub, cred, st, scanID) })
	g.Go(func() error { return scanNetwork(ctx, sub, cred, st, scanID) })
	g.Go(func() error { return scanStorage(ctx, sub, cred, st, scanID) })
	g.Go(func() error { return scanSQL(ctx, sub, cred, st, scanID) })
	g.Go(func() error { return scanAKS(ctx, sub, cred, st, scanID) })
	g.Go(func() error { return scanKeyVault(ctx, sub, cred, st, scanID) })
	if err := g.Wait(); err != nil {
		return err
	}

	return resolveRelationships(ctx, sub, st)
}

// — shared helpers —

// subscription holds a resolved Azure subscription.
type subscription struct {
	ID   string
	Name string
}

// mustJSON marshals v to JSON, returning "{}" on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

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

// sv dereferences a string pointer, returning "" for nil.
func sv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

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
