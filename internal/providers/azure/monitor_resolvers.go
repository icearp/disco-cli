package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerAPIResolver(apiResolverEntry{
		name: "azure:diagnostic-settings",
		fn:   resolveDiagnosticSettings,
	})
}

// diagnosableTypes is the allowlist of resource types known to support
// Microsoft.Insights/diagnosticSettings. Calling ListByResource on a
// non-diagnosable type returns a benign error per call; the allowlist keeps
// the API call count bounded and avoids per-resource error noise. Extend as
// new scanners land — Azure docs maintain a master list at
// learn.microsoft.com/azure/azure-monitor/essentials/resource-logs-categories.
var diagnosableTypes = []string{
	TypeKeyVaultVault,
	TypeStorageStorageAccount,
	TypeNetworkApplicationGateway,
	TypeNetworkSecurityGroup,
	TypeContainerServiceManagedCluster,
	TypeContainerRegistryRegistry,
	TypeAppServiceSite,
	TypeAppServiceSiteSlot,
	TypeAppServiceServerFarm,
	TypeServiceBusNamespace,
	TypeEventHubNamespace,
	TypeRedisCache,
	TypeCosmosDatabaseAccount,
	TypeSQLServer,
	TypeSQLDatabase,
	TypeOpInsightsWorkspace,
	TypeDatabricksWorkspace,
	TypeSynapseWorkspace,
}

// resolveDiagnosticSettings is a cross-service API resolver that walks every
// diagnosable resource in the subscription, reads its
// Microsoft.Insights/diagnosticSettings via armmonitor, and emits
// `routes-to` edges from the source resource to each configured destination
// (Log Analytics workspace, Storage Account, or Event Hub namespace).
//
// Edge attrs carry the diagnostic-setting name + destination kind so the rule
// engine can distinguish "logs to Storage" from "logs to Log Analytics".
//
// API cost: O(N) ListByResource calls where N = #diagnosable resources in the
// sub. Bounded by `maxConcurrentFanout` (50) to stay under ARM throttle. Per-
// resource errors are tolerated — first error per type counts toward an
// in-memory counter so we don't fan out a million warnings on a tenant
// without diag-settings permissions.
func resolveDiagnosticSettings(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store) (int, error) {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: diagnosableTypes,
		Limit: util.AllResources,
	})
	if err != nil {
		return 0, fmt.Errorf("list diagnosable resources: %w", err)
	}
	if len(resources) == 0 {
		return 0, nil
	}

	client, err := armmonitor.NewDiagnosticSettingsClient(cred, azClientOptions)
	if err != nil {
		return 0, fmt.Errorf("armmonitor:NewDiagnosticSettingsClient: %w", err)
	}

	// Pre-build target indexes: any destination must be a resource the store
	// already knows about so the FK on relationships.to_id holds. Index by
	// lowercased ARM ID — Azure stores IDs as-typed at create time.
	workspaceIdx, err := buildLowerIDIndex(st, sub.ID, TypeOpInsightsWorkspace)
	if err != nil {
		return 0, err
	}
	storageIdx, err := buildLowerIDIndex(st, sub.ID, TypeStorageStorageAccount)
	if err != nil {
		return 0, err
	}
	ehNamespaceIdx, err := buildLowerIDIndex(st, sub.ID, TypeEventHubNamespace)
	if err != nil {
		return 0, err
	}

	var (
		edgeCount    atomic.Int64
		denialCount  atomic.Int64
		warnedDenial atomic.Bool
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, r := range resources {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := client.NewListPager(r.NativeID, nil)
			for pager.More() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) || isDiagSettingsBenign(err) {
						denialCount.Add(1)
						return nil
					}
					return fmt.Errorf("monitor:DiagnosticSettings.list %s: %w", r.NativeID, err)
				}
				for _, ds := range page.Value {
					if ds == nil || ds.Properties == nil {
						continue
					}
					name := ""
					if ds.Name != nil {
						name = *ds.Name
					}
					emitDiagEdge := func(targetID, kind string) {
						attrs := mustJSON(map[string]string{
							"via":         "diagnostic-settings",
							"name":        name,
							"destination": kind,
						})
						if err := st.UpsertRelationship(r.ID, targetID, store.RelRoutesTo, "directed", &attrs); err == nil {
							edgeCount.Add(1)
						}
					}
					props := ds.Properties
					if id := strLower(props.WorkspaceID); id != "" {
						if tID, ok := workspaceIdx[id]; ok {
							emitDiagEdge(tID, "log-analytics")
						}
					}
					if id := strLower(props.StorageAccountID); id != "" {
						if tID, ok := storageIdx[id]; ok {
							emitDiagEdge(tID, "storage-account")
						}
					}
					if id := strLower(props.EventHubAuthorizationRuleID); id != "" {
						// Auth rule shape:
						//   /subscriptions/.../namespaces/{ns}/authorizationRules/{rule}
						// Trim back to the namespace ARM ID.
						if ns, ok := eventHubNamespaceFromAuthRule(id); ok {
							if tID, ok := ehNamespaceIdx[ns]; ok {
								emitDiagEdge(tID, "event-hub")
							}
						}
					}
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return int(edgeCount.Load()), err
	}
	if denialCount.Load() > 0 && !warnedDenial.Load() {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure",
			Service:  "azure:diagnostic-settings",
			Scope:    sub.ID,
			Message:  fmt.Sprintf("%d resources skipped (permission denied or unsupported)", denialCount.Load()),
		})
	}
	return int(edgeCount.Load()), nil
}

// buildLowerIDIndex returns a map[lower(NativeID)] → resource.ID for every
// resource of the given type in the subscription. Used by the diagnostic-
// settings resolver to FK-check destination ARNs (workspace/storage/eventhub).
func buildLowerIDIndex(st *store.Store, subID, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: subID,
		Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[strings.ToLower(r.NativeID)] = r.ID
	}
	return idx, nil
}

// strLower returns a lowercased value of the pointer, "" if nil/empty.
func strLower(p *string) string {
	if p == nil || *p == "" {
		return ""
	}
	return strings.ToLower(*p)
}

// eventHubNamespaceFromAuthRule extracts the namespace ARM ID from an auth-rule
// ID of shape `.../namespaces/{ns}/authorizationRules/{rule}`. Returns the
// trimmed ID + ok flag. ID is already lowercased by caller.
func eventHubNamespaceFromAuthRule(authRuleID string) (string, bool) {
	const sep = "/authorizationrules/"
	idx := strings.Index(authRuleID, sep)
	if idx < 0 {
		return "", false
	}
	return authRuleID[:idx], true
}

// isDiagSettingsBenign matches the typical "diagnostic settings not supported
// for this resource" responses Azure returns when called on a resource that
// (a) doesn't model diag-settings at all, or (b) the caller lacks the narrow
// `Microsoft.Insights/diagnosticSettings/read` permission on a resource where
// real RBAC (Reader/etc.) was granted. Both surface as 404/400 with a
// distinct error code — neither is fatal.
func isDiagSettingsBenign(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	switch respErr.StatusCode {
	case 404, 400, 405:
		return true
	}
	switch respErr.ErrorCode {
	case "ResourceNotSupported", "ResourceTypeNotSupported", "InvalidResourceType":
		return true
	}
	return false
}
