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
	"sync/atomic"
	"time"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentServices caps the number of service scanners running in parallel
	// per subscription to avoid hitting Azure API rate limits.
	maxConcurrentServices = 10
	// serviceTimeout is the per-service hard deadline. A misbehaving API endpoint
	// won't stall the entire scan beyond this duration.
	serviceTimeout = 5 * time.Minute
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for Azure.
type Scanner struct {
	serviceFilter []string // nil = scan all registered services
}

func (s *Scanner) Name() string { return "azure" }

// SetServiceFilter restricts the scan to the named services (e.g. "azure:compute", "azure:network").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all Azure resources across all configured subscriptions.
// Subscriptions are scanned in parallel, bounded by maxConcurrentServices.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	subs, cred, err := loadSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("azure: load subscriptions: %w", err)
	}
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g, gctx := errgroup.WithContext(ctx)
	for i := range subs {
		sub := &subs[i]
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			if err := scanSubscription(gctx, sub, cred, s.serviceFilter, st, scanID); err != nil {
				return fmt.Errorf("azure subscription %s: %w", sub.ID, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, services []string, st *store.Store, scanID string) error {
	// Scan resource groups first (they are parents of all resources).
	if err := scanResourceGroups(ctx, sub, cred, st, scanID); err != nil {
		return err
	}

	// Scan all registered service types in parallel, bounded by maxConcurrentServices.
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g, gctx := errgroup.WithContext(ctx)
	for _, svc := range filteredServices(services) {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(gctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, sub, cred, st, scanID)
			if err != nil {
				return err
			}
			st.ReportService(svc.name, total, inserted)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	st.ReportResolveStart("azure")
	var counter atomic.Int64
	err := resolveRelationships(ctx, sub, st.WithRelCounter(&counter))
	st.ReportResolveComplete("azure", int(counter.Load()))
	return err
}

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB.
func resolveRelationships(_ context.Context, sub *subscription, st *store.Store) error {
	for _, r := range registeredResolvers {
		if err := r.fn(sub, st); err != nil {
			return err
		}
	}
	return nil
}

// filteredServices returns the services to run. When filter is non-empty, only
// services whose name appears in filter are returned.
func filteredServices(filter []string) []serviceEntry {
	if len(filter) == 0 {
		return registeredServices
	}
	allowed := make(map[string]bool, len(filter))
	for _, name := range filter {
		allowed[name] = true
	}
	var out []serviceEntry
	for _, svc := range registeredServices {
		if allowed[svc.name] {
			out = append(out, svc)
		}
	}
	return out
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

func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }

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
