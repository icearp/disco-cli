package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	"golang.org/x/sync/semaphore"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for GCP. Upstream truth
// source = the public Discovery API. Coverage truth source = CollectEmits()
// (services.go), which unions every registerService / registerOrgService
// emits decl plus extraEmits from hierarchy_scanners + iampolicy_resolvers.
type coverageProvider struct{}

func (coverageProvider) Name() string { return "gcp" }

func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// Aliases overrides the algorithmic disco-type → upstream-key mapping for GCP
// types whose Discovery resource-collection name doesn't match the disco
// service segment 1:1 (e.g. cloudresourcemanager Discovery uses singular
// "Project" while disco's segment is "cloudresourcemanager").
//
// Discovery key shape: "<api>.googleapis.com/<Resource>" — the same shape
// produced by Fetch below. Empty map allowed; algorithmic fallback handles
// the rest.
func (coverageProvider) Aliases() map[string]string {
	return map[string]string{
		// Cloud Resource Manager hierarchy.
		TypeOrganization: "cloudresourcemanager.googleapis.com/Organization",
		TypeFolder:       "cloudresourcemanager.googleapis.com/Folder",
		TypeProject:      "cloudresourcemanager.googleapis.com/Project",
		// Compute Engine.
		TypeComputeInstance:         "compute.googleapis.com/Instance",
		TypeComputeNetwork:          "compute.googleapis.com/Network",
		TypeComputeSubnet:           "compute.googleapis.com/Subnetwork",
		TypeComputeFirewall:         "compute.googleapis.com/Firewall",
		TypeComputeForwardingRule:   "compute.googleapis.com/ForwardingRule",
		TypeComputeTargetHTTPProxy:  "compute.googleapis.com/TargetHttpProxy",
		TypeComputeTargetHTTPSProxy: "compute.googleapis.com/TargetHttpsProxy",
		TypeComputeURLMap:           "compute.googleapis.com/UrlMap",
		TypeComputeBackendService:   "compute.googleapis.com/BackendService",
		TypeComputeBackendBucket:    "compute.googleapis.com/BackendBucket",
		TypeComputeSecurityPolicy:   "compute.googleapis.com/SecurityPolicy",
		// Compute Engine — storage (Wave 1, docs/gcp-type-coverage.md).
		TypeComputeDisk:                       "compute.googleapis.com/Disk",
		TypeComputeRegionDisk:                 "compute.googleapis.com/RegionDisk",
		TypeComputeImage:                      "compute.googleapis.com/Image",
		TypeComputeMachineImage:               "compute.googleapis.com/MachineImage",
		TypeComputeSnapshot:                   "compute.googleapis.com/Snapshot",
		TypeComputeRegionSnapshot:             "compute.googleapis.com/RegionSnapshot",
		TypeComputeInstantSnapshot:            "compute.googleapis.com/InstantSnapshot",
		TypeComputeRegionInstantSnapshot:      "compute.googleapis.com/RegionInstantSnapshot",
		TypeComputeInstantSnapshotGroup:       "compute.googleapis.com/InstantSnapshotGroup",
		TypeComputeRegionInstantSnapshotGroup: "compute.googleapis.com/RegionInstantSnapshotGroup",
		TypeComputeStoragePool:                "compute.googleapis.com/StoragePool",
		// Certificate Manager.
		TypeCertManagerCertificate: "certificatemanager.googleapis.com/Certificate",
		TypeCertManagerMap:         "certificatemanager.googleapis.com/CertificateMap",
		TypeCertManagerMapEntry:    "certificatemanager.googleapis.com/CertificateMapEntry",
		TypeCertManagerDNSAuth:     "certificatemanager.googleapis.com/DnsAuthorization",
		// DNS.
		TypeDNSManagedZone: "dns.googleapis.com/ManagedZone",
		TypeDNSRecordSet:   "dns.googleapis.com/ResourceRecordSet",
		// Serverless.
		TypeCloudFunction: "cloudfunctions.googleapis.com/Function",
		TypeCloudRunSvc:   "run.googleapis.com/Service",
		TypeCloudRunJob:   "run.googleapis.com/Job",
		// Pub/Sub.
		TypePubSubTopic:        "pubsub.googleapis.com/Topic",
		TypePubSubSubscription: "pubsub.googleapis.com/Subscription",
		TypePubSubSchema:       "pubsub.googleapis.com/Schema",
		// BigQuery.
		TypeBQDataset:         "bigquery.googleapis.com/Dataset",
		TypeBQTable:           "bigquery.googleapis.com/Table",
		TypeBQModel:           "bigquery.googleapis.com/Model",
		TypeBQRoutine:         "bigquery.googleapis.com/Routine",
		TypeBQRowAccessPolicy: "bigquery.googleapis.com/RowAccessPolicy",
		// Bigtable / Firestore / Spanner.
		TypeBigtableInstance:         "bigtableadmin.googleapis.com/Instance",
		TypeBigtableCluster:          "bigtableadmin.googleapis.com/Cluster",
		TypeFirestoreDB:              "firestore.googleapis.com/Database",
		TypeFirestoreBackup:          "firestore.googleapis.com/Backup",
		TypeFirestoreBackupSchedule:  "firestore.googleapis.com/BackupSchedule",
		TypeFirestoreUserCred:        "firestore.googleapis.com/UserCred",
		TypeSpannerInstance:          "spanner.googleapis.com/Instance",
		TypeSpannerDatabase:          "spanner.googleapis.com/Database",
		TypeSpannerInstanceConfig:    "spanner.googleapis.com/InstanceConfig",
		TypeSpannerInstancePartition: "spanner.googleapis.com/InstancePartition",
		TypeSpannerBackup:            "spanner.googleapis.com/Backup",
		TypeSpannerBackupSchedule:    "spanner.googleapis.com/BackupSchedule",
		TypeSpannerDatabaseRole:      "spanner.googleapis.com/DatabaseRole",
		TypeBigtableBackup:           "bigtableadmin.googleapis.com/Backup",
		TypeBigtableAppProfile:       "bigtableadmin.googleapis.com/AppProfile",
		TypeBigtableTable:            "bigtableadmin.googleapis.com/Table",
		TypeBigtableAuthorizedView:   "bigtableadmin.googleapis.com/AuthorizedView",
		TypeBigtableLogicalView:      "bigtableadmin.googleapis.com/LogicalView",
		TypeBigtableMaterializedView: "bigtableadmin.googleapis.com/MaterializedView",
		TypeBigtableSchemaBundle:     "bigtableadmin.googleapis.com/SchemaBundle",
		TypeBigtableHotTablet:        "bigtableadmin.googleapis.com/HotTablet",
		TypeBigtableMemoryLayer:      "bigtableadmin.googleapis.com/MemoryLayer",
		// Composer.
		TypeComposerEnv: "composer.googleapis.com/Environment",
		// Artifact Registry.
		TypeArtifactRepository: "artifactregistry.googleapis.com/Repository",
		TypeArtifactPackage:    "artifactregistry.googleapis.com/Package",
		TypeArtifactTag:        "artifactregistry.googleapis.com/Tag",
		TypeArtifactRule:       "artifactregistry.googleapis.com/Rule",
		TypeArtifactAttachment: "artifactregistry.googleapis.com/Attachment",
		// Logging / Monitoring.
		TypeLoggingSink:                   "logging.googleapis.com/Sink",
		TypeLoggingBucket:                 "logging.googleapis.com/Bucket",
		TypeLoggingExclusion:              "logging.googleapis.com/Exclusion",
		TypeLoggingMetric:                 "logging.googleapis.com/Metric",
		TypeLoggingLink:                   "logging.googleapis.com/Link",
		TypeLoggingView:                   "logging.googleapis.com/View",
		TypeLoggingLogScope:               "logging.googleapis.com/LogScope",
		TypeLoggingSavedQuery:             "logging.googleapis.com/SavedQuery",
		TypeMonitoringAlertPol:            "monitoring.googleapis.com/AlertPolicy",
		TypeMonitoringDashboard:           "monitoring.googleapis.com/Dashboard",
		TypeMonitoringGroup:               "monitoring.googleapis.com/Group",
		TypeMonitoringNotificationChannel: "monitoring.googleapis.com/NotificationChannel",
		TypeMonitoringService:             "monitoring.googleapis.com/Service",
		TypeMonitoringSLO:                 "monitoring.googleapis.com/ServiceLevelObjective",
		TypeMonitoringSnooze:              "monitoring.googleapis.com/Snooze",
		TypeMonitoringUptimeCheckConfig:   "monitoring.googleapis.com/UptimeCheckConfig",
		// Cloud Build.
		TypeCloudBuildTrigger: "cloudbuild.googleapis.com/Trigger",
		// Binary Authorization.
		TypeBinAuthPolicy:   "binaryauthorization.googleapis.com/Policy",
		TypeBinAuthAttestor: "binaryauthorization.googleapis.com/Attestor",
		// Batch.
		TypeBatchJob: "batch.googleapis.com/Job",
		// GKE.
		TypeGKECluster: "container.googleapis.com/Cluster",
		// IAM.
		TypeIAMServiceAccount: "iam.googleapis.com/ServiceAccount",
		TypeIAMSAKey:          "iam.googleapis.com/Key",
		// Cloud KMS.
		TypeKMSKeyRing:   "cloudkms.googleapis.com/KeyRing",
		TypeKMSCryptoKey: "cloudkms.googleapis.com/CryptoKey",
		// Secret Manager.
		TypeSecret: "secretmanager.googleapis.com/Secret",
		// Cloud Storage.
		TypeStorageBucket:                     "storage.googleapis.com/Bucket",
		TypeStorageHmacKey:                    "storage.googleapis.com/HmacKey",
		TypeStorageNotification:               "storage.googleapis.com/Notification",
		TypeStorageManagedFolder:              "storage.googleapis.com/ManagedFolder",
		TypeStorageAnywhereCache:              "storage.googleapis.com/AnywhereCache",
		TypeStorageFolder:                     "storage.googleapis.com/Folder",
		TypeStorageBucketAccessControl:        "storage.googleapis.com/BucketAccessControl",
		TypeStorageDefaultObjectAccessControl: "storage.googleapis.com/DefaultObjectAccessControl",
		// Cloud SQL.
		TypeSQLInstance: "sqladmin.googleapis.com/Instance",
		// VPC Service Controls.
		TypeAccessPolicy:     "accesscontextmanager.googleapis.com/AccessPolicy",
		TypeServicePerimeter: "accesscontextmanager.googleapis.com/ServicePerimeter",
		// Dataproc + Dataflow.
		TypeDataprocCluster:           "dataproc.googleapis.com/Cluster",
		TypeDataprocAutoscalingPolicy: "dataproc.googleapis.com/AutoscalingPolicy",
		TypeDataprocBatch:             "dataproc.googleapis.com/Batch",
		TypeDataprocSession:           "dataproc.googleapis.com/Session",
		TypeDataprocSessionTemplate:   "dataproc.googleapis.com/SessionTemplate",
		TypeDataprocWorkflowTemplate:  "dataproc.googleapis.com/WorkflowTemplate",
		TypeDataprocJob:               "dataproc.googleapis.com/Job",
		TypeDataflowJob:               "dataflow.googleapis.com/Job",
		// Workspace + Cloud Identity.
		TypeWorkspaceUser:      "admin.googleapis.com/User",
		TypeCloudIdentityGroup: "cloudidentity.googleapis.com/Group",
	}
}

// AlgorithmicKey converts a disco type to a best-effort Discovery key shape.
// Fallback when Aliases() has no entry — keeps the matrix render sensible
// for entries not yet audited against a live Discovery dump. Format:
// "<service>.googleapis.com/<Pascal>".
func (coverageProvider) AlgorithmicKey(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) != 3 {
		return discoType
	}
	svc, kind := parts[1], parts[2]
	// Convert kebab-case kind to Pascal: "alert-policy" → "AlertPolicy".
	segs := strings.Split(kind, "-")
	for i, s := range segs {
		if s == "" {
			continue
		}
		segs[i] = strings.ToUpper(s[:1]) + s[1:]
	}
	return svc + ".googleapis.com/" + strings.Join(segs, "")
}

// Discovery API endpoints. Public, unauthenticated; rate-limited but generous
// for a one-shot enumeration. discoveryListURL is a var so tests can point the
// fetch at an httptest server (the per-API doc URLs come from the list response).
var discoveryListURL = "https://www.googleapis.com/discovery/v1/apis"

const discoveryFetchLimit = 8

// Fetch enumerates first-party Google APIs via the Discovery API and returns
// every resource collection encountered as an UpstreamType. Filtering is
// applied to the union of:
//   - registered scanner names (project-scope + org-scope service entries),
//   - parent / sibling APIs that don't have their own scanner but supply
//     resource shapes scanners depend on (cloudresourcemanager, iam, run, …).
//
// The filter is *post-fetch* — we still paginate the full Discovery list once
// but skip per-API doc HTTP calls for APIs disco doesn't need. Keeps the
// matrix focused; trims runtime ~10× vs scanning every Google API.
func (coverageProvider) Fetch(ctx context.Context, _ coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	allow := relevantAPISet()

	apis, err := fetchDiscoveryAPIList(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("discovery list: %w", err)
	}

	type apiRef struct{ name, url string }
	var todo []apiRef
	seenURL := map[string]bool{}
	for _, a := range apis {
		if a.Name == "" || a.DiscoveryRestURL == "" || seenURL[a.DiscoveryRestURL] {
			continue
		}
		seenURL[a.DiscoveryRestURL] = true
		if !allow[a.Name] {
			continue
		}
		todo = append(todo, apiRef{name: a.Name, url: a.DiscoveryRestURL})
	}

	var (
		mu       sync.Mutex
		out      []coverage.UpstreamType
		firstErr error // guarded by mu
		sem      = semaphore.NewWeighted(discoveryFetchLimit)
		wg       sync.WaitGroup
	)
	for _, ref := range todo {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		wg.Go(func() {
			defer sem.Release(1)
			doc, err := fetchDiscoveryDoc(ctx, client, ref.url)
			if err != nil {
				// A per-API doc failure would leave that API's types absent
				// from the upstream set, falsely bucketing them upstream-missing.
				// Record and propagate rather than silently degrade — cmd turns
				// this into a fatal "registry unreachable" (exit 2).
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("discovery doc %s: %w", ref.name, err)
				}
				mu.Unlock()
				return
			}
			types := walkResourceCollections(ref.name, doc)
			mu.Lock()
			out = append(out, types...)
			mu.Unlock()
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	// Dedupe across versions by full upstream key — same API across v1/v2
	// often reports the same resource collection twice. Service segment of
	// the first occurrence wins.
	seen := make(map[string]bool, len(out))
	deduped := out[:0]
	for _, u := range out {
		if seen[u.Key] {
			continue
		}
		seen[u.Key] = true
		deduped = append(deduped, u)
	}
	return deduped, nil
}

// relevantAPISet is the union of APIs disco's registered scanners (project +
// org) cover, plus parent APIs whose Discovery docs supply resource shapes.
// Drives the post-fetch filter in Fetch.
func relevantAPISet() map[string]bool {
	emits := CollectEmits()
	out := make(map[string]bool, len(emits)+8)
	for _, e := range emits {
		out[e.Service] = true
	}
	// APIs that always belong (parent surfaces, common shared shapes).
	for _, a := range []string{
		"cloudresourcemanager", "iam", "compute", "dns", "pubsub",
		"bigquery", "bigtableadmin", "firestore", "spanner", "composer",
		"artifactregistry", "logging", "monitoring", "cloudbuild",
		"binaryauthorization", "batch", "container", "cloudkms",
		"secretmanager", "storage", "sqladmin", "accesscontextmanager",
		"dataproc", "dataflow", "admin", "cloudidentity", "run",
		"cloudfunctions", "certificatemanager",
	} {
		out[a] = true
	}
	return out
}

// discoveryAPI is the relevant subset of one entry in the Discovery list.
type discoveryAPI struct {
	Name             string `json:"name"`
	DiscoveryRestURL string `json:"discoveryRestUrl"`
	Preferred        bool   `json:"preferred"`
}

func fetchDiscoveryAPIList(ctx context.Context, client *http.Client) ([]discoveryAPI, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryListURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery list status %d", resp.StatusCode)
	}
	var body struct {
		Items []discoveryAPI `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	// Return every version of every API. Different versions expose different
	// resource collections (e.g. cloudbuild v1 has Triggers, v2 has
	// Connection/Repository). Fetcher dedupes the union by full upstream key
	// post-walk.
	return body.Items, nil
}

// discoveryDoc carries the recursive resource-collection tree of one API's
// Discovery document. We only care about resource names and their methods —
// the presence of a `get` or `list` method indicates a fetchable collection.
type discoveryDoc struct {
	Resources map[string]discoveryResource `json:"resources"`
}

type discoveryResource struct {
	Methods   map[string]json.RawMessage   `json:"methods"`
	Resources map[string]discoveryResource `json:"resources"`
}

func fetchDiscoveryDoc(ctx context.Context, client *http.Client, url string) (*discoveryDoc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery doc %s status %d", url, resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// walkResourceCollections recursively walks a Discovery doc's resources tree
// and emits one UpstreamType per fetchable collection — any node carrying a
// `get` or `list` method, matching GCP's notion of a resource type in
// tooling like gcloud + asset inventory.
//
// Resource name conversion: Discovery uses lowerCamel collection names
// ("forwardingRules" → singular "ForwardingRule"). Strip a trailing 's' and
// PascalCase. Imperfect but consistent — alias-map overrides cover edge cases.
func walkResourceCollections(api string, doc *discoveryDoc) []coverage.UpstreamType {
	if doc == nil {
		return nil
	}
	var out []coverage.UpstreamType
	var walk func(name string, r discoveryResource)
	walk = func(name string, r discoveryResource) {
		if hasFetchMethod(r.Methods) {
			singular := singularize(name)
			out = append(out, coverage.UpstreamType{
				Key:     api + ".googleapis.com/" + pascalCase(singular),
				Service: api,
			})
		}
		for childName, child := range r.Resources {
			walk(childName, child)
		}
	}
	for name, r := range doc.Resources {
		walk(name, r)
	}
	return out
}

func hasFetchMethod(methods map[string]json.RawMessage) bool {
	if len(methods) == 0 {
		return false
	}
	for k := range methods {
		switch strings.ToLower(k) {
		case "get", "list", "aggregatedlist":
			return true
		}
	}
	return false
}

// singularizeExceptions handles plurals no suffix-only rule can resolve.
// "databases" and "aliases" both end in identical "-ases", but the true
// singular of one ends in a silent "e" (non-sibilant stem, "+s" plural) and
// the other in a genuine sibilant "s" (sibilant stem, "+es" plural) — the
// suffix alone can't tell them apart. Extend this map, not the heuristic
// below, when a new word hits the same ambiguity (surfaces as an
// "upstream-missing" row in `disco coverage services` for a type that's
// actually scanned).
var singularizeExceptions = map[string]string{
	"databases":      "database",
	"snoozes":        "snooze",
	"anywhereCaches": "anywhereCache",
}

// singularize strips a trailing plural marker from a lowerCamel collection
// name. Heuristic only — alias-map handles cases this gets wrong (e.g.
// "indexes" → "Index" handled here via the sibilant-stem rule, "policies" →
// "Policy" via the -ies rule, but genuinely irregular plurals go in
// singularizeExceptions above).
func singularize(s string) string {
	if exc, ok := singularizeExceptions[s]; ok {
		return exc
	}
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "es") && len(s) > 2 && hasSibilantStem(s[:len(s)-2]):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}

// hasSibilantStem reports whether stem ends in a sound that pluralizes with
// "-es" rather than a bare "-s" (s, x, z, ch, sh) — e.g. "address"/"alias"/
// "box"/"branch"/"dish", distinguishing "addresses" → "address" from
// "instances" → "instance".
func hasSibilantStem(stem string) bool {
	switch {
	case strings.HasSuffix(stem, "s"), strings.HasSuffix(stem, "x"), strings.HasSuffix(stem, "z"):
		return true
	case strings.HasSuffix(stem, "ch"), strings.HasSuffix(stem, "sh"):
		return true
	}
	return false
}

func pascalCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// FetchRegions calls compute.Regions.List for the first accessible project
// and returns the region-name list. Reuses gcpRegions(ctx, *project) so
// scanner-side and coverage-side fetch paths share the same shape, including
// silent-skip on permission denied / API not enabled (yields empty slice —
// DiffRegions then marks every static entry "stale", a clear signal the
// caller needs broader creds).
func (coverageProvider) FetchRegions(ctx context.Context, _ coverage.FetchOptions) ([]string, error) {
	// Coverage tooling uses ambient/config credentials; no per-scan override.
	projects, err := loadProjects(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no accessible GCP projects; coverage --regions needs at least one")
	}
	return gcpRegions(ctx, &projects[0])
}
