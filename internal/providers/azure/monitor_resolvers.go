package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	// R3.25 allowlist extension — newer/secondary diagnosable types.
	TypePostgreSQLFlexibleServer,
	TypeMySQLFlexibleServer,
	TypeContainerInstanceContainerGroup,
	TypeEventGridTopic,
	TypeEventGridSystemTopic,
	TypeEventGridDomain,
	TypeDataFactoryFactory,
	TypeLogicWorkflow,
	TypeAPIManagementService,
	TypeNetworkTrafficManagerProfile,
	TypeCDNProfile,
	TypeSQLManagedInstance,
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
func resolveDiagnosticSettings(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store) (int, error) {
	resources, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
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

	idx := diagTargetIndexes{workspace: workspaceIdx, storage: storageIdx, eventHubNS: ehNamespaceIdx}
	var (
		edgeCount   atomic.Int64
		denialCount atomic.Int64
		failCount   atomic.Int64
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, r := range resources {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			return scanDiagnosticSettingsForResource(gctx, client, st, r, idx, &edgeCount, &denialCount, &failCount)
		})
	}
	if err := g.Wait(); err != nil {
		return int(edgeCount.Load()), err
	}
	if denialCount.Load() > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure",
			Service:  "azure:diagnostic-settings",
			Scope:    sub.ID,
			Message:  fmt.Sprintf("%d resources skipped (permission denied or unsupported)", denialCount.Load()),
		})
	}
	if failCount.Load() > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure",
			Service:  "azure:diagnostic-settings",
			Scope:    sub.ID,
			Message:  fmt.Sprintf("%d diagnostic-settings edges failed to persist", failCount.Load()),
		})
	}
	return int(edgeCount.Load()), nil
}

// diagTargetIndexes bundles the destination ARM-ID → resource-ID lookup tables
// so the per-resource helper takes one struct rather than three maps.
type diagTargetIndexes struct {
	workspace  map[string]string
	storage    map[string]string
	eventHubNS map[string]string
}

// scanDiagnosticSettingsForResource pages the diagnostic-settings list for a
// single source resource and emits one routes-to edge per matched destination.
// AccessDenied / unsupported-type failures bump denialCount and return nil so
// siblings keep going.
func scanDiagnosticSettingsForResource(ctx context.Context, client *armmonitor.DiagnosticSettingsClient, st *store.Store, r store.Resource, idx diagTargetIndexes, edgeCount, denialCount, failCount *atomic.Int64) error {
	pager := client.NewListPager(r.NativeID, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isDiagSettingsBenign(err) {
				denialCount.Add(1)
				return nil
			}
			return fmt.Errorf("monitor:DiagnosticSettings.list %s: %w", r.NativeID, err)
		}
		for _, ds := range page.Value {
			emitDiagSettingEdges(st, r.ID, ds, idx, edgeCount, failCount)
		}
	}
	return nil
}

// emitDiagSettingEdges fans the three known destination kinds (workspace,
// storage account, event-hub namespace) into routes-to edges. Per-edge upsert
// failures are tolerated (partial-edge progress beats failing the whole
// resolver) but counted into failCount so the caller can surface them rather
// than letting edge loss vanish silently.
func emitDiagSettingEdges(st *store.Store, fromID string, ds *armmonitor.DiagnosticSettingsResource, idx diagTargetIndexes, edgeCount, failCount *atomic.Int64) {
	if ds == nil || ds.Properties == nil {
		return
	}
	name := ""
	if ds.Name != nil {
		name = *ds.Name
	}
	emit := func(targetID, kind string) {
		attrs := mustJSON(map[string]string{
			"via":         "diagnostic-settings",
			"name":        name,
			"destination": kind,
		})
		if err := st.UpsertRelationship(fromID, targetID, store.RelRoutesTo, "directed", &attrs); err == nil {
			edgeCount.Add(1)
		} else {
			failCount.Add(1)
		}
	}
	props := ds.Properties
	if id := strLower(props.WorkspaceID); id != "" {
		if tID, ok := idx.workspace[id]; ok {
			emit(tID, "log-analytics")
		}
	}
	if id := strLower(props.StorageAccountID); id != "" {
		if tID, ok := idx.storage[id]; ok {
			emit(tID, "storage-account")
		}
	}
	if id := strLower(props.EventHubAuthorizationRuleID); id != "" {
		// Auth rule shape:
		//   /subscriptions/.../namespaces/{ns}/authorizationRules/{rule}
		// Trim back to the namespace ARM ID.
		if ns, ok := eventHubNamespaceFromAuthRule(id); ok {
			if tID, ok := idx.eventHubNS[ns]; ok {
				emit(tID, "event-hub")
			}
		}
	}
}

// buildLowerIDIndex returns a map[lower(NativeID)] → resource.ID for every
// resource of the given type in the subscription. Used by the diagnostic-
// settings resolver to FK-check destination ARNs (workspace/storage/eventhub).
func buildLowerIDIndex(st *store.Store, subID, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: subID,
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
