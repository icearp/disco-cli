package gcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigquery/v2"
)

// msToRFC3339 converts a millisecond-since-epoch timestamp to an RFC3339
// pointer suitable for `Resource.CreatedAt`. Returns nil for ms ≤ 0 so that
// missing fields (BigQuery omits CreationTime in some list responses) don't
// surface as a 1970 timestamp.
func msToRFC3339(ms int64) *string {
	if ms <= 0 {
		return nil
	}
	s := time.UnixMilli(ms).UTC().Format(time.RFC3339)
	return &s
}

func init() {
	registerType(restype.Descriptor{Type: TypeBQDataset, Service: "bigquery", Upstream: "bigquery.googleapis.com/Dataset"})
	registerType(restype.Descriptor{Type: TypeBQTable, Service: "bigquery", Upstream: "bigquery.googleapis.com/Table", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBQModel, Service: "bigquery", Upstream: "bigquery.googleapis.com/Model", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBQRoutine, Service: "bigquery", Upstream: "bigquery.googleapis.com/Routine"})
	registerType(restype.Descriptor{Type: TypeBQRowAccessPolicy, Service: "bigquery", Upstream: "bigquery.googleapis.com/RowAccessPolicy"})
	registerService(serviceEntry{
		name: "gcp:bigquery",
		fn:   scanBigQuery,
	})
}

// maxConcurrentBQDatasets caps per-project dataset fan-out for the
// per-dataset Get + Tables.List pair.
const maxConcurrentBQDatasets = 10

// rowAccessPolicyAttrs embeds a row access policy's real IAM policy
// (fetched separately via GetIamPolicy, since List never populates
// `Grantees`) alongside the raw policy — named fields, not an anonymous
// embed, so the wrapper's own JSON shape isn't lost to RowAccessPolicy's
// custom MarshalJSON method.
type rowAccessPolicyAttrs struct {
	RowAccessPolicy *bigquery.RowAccessPolicy `json:"rowAccessPolicy"`
	IamPolicy       *bigquery.Policy          `json:"iamPolicy,omitempty"`
}

// scanBigQuery discovers BigQuery datasets, tables, models, routines, and
// row access policies.
//   - Phase 1: `bigquery/v2` `Datasets.List` paginated — returns lightweight
//     stubs (no encryption config).
//   - Phase 2: per-dataset fan-out: `Datasets.Get` for the full proto
//     (needed for `defaultEncryptionConfiguration.kmsKeyName` → CMEK edge),
//     then `Tables.List` paginated (plus, per table, `RowAccessPolicies.List`
//     — see cost note below), then `Models.List`/`Routines.List` paginated.
//
// Tables stored as `Tables.List` stubs (no encryption / schema). Per-table
// `Tables.Get` is intentionally skipped — table counts can run into the
// thousands per dataset and pay for nothing without a rule-engine query.
// RowAccessPolicies.List pays that same per-table cost deliberately: unlike
// Tables.Get's full schema/proto fetch, a row access policy list call is
// cheap and is the only way to discover a security-relevant resource type
// that has no independent enumeration path — accepted per the type-coverage
// buildout's audit (docs/gcp-type-coverage.md).
//
// Resolver Wave R27 confirmed via `go doc` that this same List-shape gap
// (internal/providers/CLAUDE.md "List-only summary scanners block resolver
// work") also covers Model and Routine, flagged `Leaf: true` alongside Table:
//   - `ListModelsResponse`'s own doc: "Only the following fields are
//     populated: model_reference, model_type, creation_time,
//     last_modified_time and labels" — `EncryptionConfiguration` (the only
//     resolvable field on Model) is never populated by `Models.List`.
//   - `ListRoutinesResponse`'s own doc: "Unless read_mask is set... only...
//     etag, project_id, dataset_id, routine_id, routine_type, creation_time,
//     last_modified_time, language, and remote_function_options" are
//     populated. `RemoteFunctionOptions.Connection` IS populated even
//     without a `ReadMask`, but points at a BigQuery Connections resource
//     disco doesn't scan yet (no scanner for that service exists), so even
//     that one populated field has no valid target today. This scanner now
//     sets a `ReadMask` (previously bare `Routines.List`) to additionally
//     populate `SparkOptions` — its `Connection` field is the same target
//     type, a second potential source once the Connections scanner lands.
//     `DefinitionBody`/`ImportedLibraries` remain informational only (SQL
//     text / `gs://` object paths, no matching scanned-resource type).
//
// Table/Model/Routine all stay `Leaf: true` for now — Table/Model would need
// a per-row `.Get` fan-out (Tables.Get / Models.Get) to become resolvable,
// same cost tradeoff as the Table note above (thousands of calls per
// dataset), deferred until rule-engine demand justifies it. Routine's only
// resolvable fields (`RemoteFunctionOptions.Connection`,
// `SparkOptions.Connection`) both target BigQuery Connections, which disco
// doesn't scan yet — dropping Routine's `Leaf` flag is a future wave's job,
// paired with adding that scanner.
//
// `RowAccessPolicy.Grantees` is doc'd "Optional. Input only." (go doc
// bigquery.RowAccessPolicy) — `RowAccessPolicies.List` never actually
// populates it. This scanner instead fetches real grantee data via a
// per-policy `RowAccessPolicies.GetIamPolicy` call (bounded: policies are
// few per table, same cost class already accepted for `RowAccessPolicies.List`
// itself) and embeds the returned IAM policy alongside the raw
// `RowAccessPolicy` under a wrapper (`rowAccessPolicyAttrs`) — per the
// "Embedding child data in parent attributes" convention
// (`internal/providers/CLAUDE.md`), since the IAM policy has no independent
// lifecycle apart from its row access policy.
func scanBigQuery(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("bigquery client: %w", err)
	}
	return scanBigQueryWithClient(ctx, svc, p, st, scanID)
}

// bqDatasetRef is a phase-1 dataset stub: the dataset ID (last segment) and
// its owning project, enough to drive the phase-2 per-dataset deep get + child
// list fan-out.
type bqDatasetRef struct {
	datasetID string // BigQuery dataset ID (last segment)
	projectID string
}

// bigQueryScan holds the shared state threaded through a single BigQuery scan:
// the SDK client, project, store, scan ID, and the mutex-guarded running
// (total, inserted) upsert counters. The mutex guards the counters across the
// bounded per-dataset fan-out. Scoped to one scanBigQueryWithClient call; not
// safe for concurrent use across scans.
type bigQueryScan struct {
	svc    *bigquery.Service
	p      *project
	st     *store.Store
	scanID string

	mu       sync.Mutex
	total    int
	inserted int
}

// scanDataset runs the per-dataset phase-2 work: Datasets.Get for the full
// proto, upsert the dataset, then its tables (with row access policies),
// models, and routines.
func (s *bigQueryScan) scanDataset(ctx context.Context, d bqDatasetRef) error {
	full, err := s.svc.Datasets.Get(d.projectID, d.datasetID).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied(s.st, "bigquery:datasets.get", s.p.ID, err)
		}
		return err
	}
	// Native ID: opaque "{project}:{dataset}" ID from List/Get — matches
	// BigQuery's own canonical reference.
	nativeID := full.Id
	name := d.datasetID
	region := full.Location
	dsResource := &store.Resource{
		Provider:       "gcp",
		AccountID:      s.p.ID,
		AccountName:    &s.p.Name,
		Type:           TypeBQDataset,
		NativeID:       nativeID,
		Name:           &name,
		Region:         strp(region),
		CreatedAt:      msToRFC3339(full.CreationTime),
		AttributesJSON: mustJSON(full),
		DiscoveredBy:   s.scanID,
	}
	s.mu.Lock()
	tt, nn, uerr := upsertWithProjClosure(s.p, s.st, []*store.Resource{dsResource})
	s.total += tt
	s.inserted += nn
	s.mu.Unlock()
	if uerr != nil {
		return uerr
	}

	dsResourceID := store.ResourceID("gcp", s.p.ID, nativeID)
	if err := s.scanTables(ctx, d, region, dsResourceID); err != nil {
		return err
	}
	if err := s.scanModels(ctx, d, region, dsResourceID); err != nil {
		return err
	}
	return s.scanRoutines(ctx, d, region, dsResourceID)
}

// scanTables lists a dataset's tables (parented under the dataset) and then
// their row access policies. A permission denial on the list is skip-logged
// (never escalated to the whole-service disabled sentinel).
func (s *bigQueryScan) scanTables(ctx context.Context, d bqDatasetRef, region, dsResourceID string) error {
	if err := s.svc.Tables.List(d.projectID, d.datasetID).Pages(ctx, func(page *bigquery.TableList) error {
		var batch []*store.Resource
		for _, tb := range page.Tables {
			tname := ""
			if tb.TableReference != nil {
				tname = tb.TableReference.TableId
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      s.p.ID,
				AccountName:    &s.p.Name,
				Type:           TypeBQTable,
				NativeID:       tb.Id,
				Name:           &tname,
				Region:         strp(region),
				CreatedAt:      msToRFC3339(tb.CreationTime),
				AttributesJSON: mustJSON(tb),
				DiscoveredBy:   s.scanID,
			})
		}
		s.mu.Lock()
		bt, bn, berr := upsertWithParent(s.st, batch, dsResourceID)
		s.total += bt
		s.inserted += bn
		s.mu.Unlock()
		if berr != nil {
			return berr
		}
		// Row access policies must be fetched after the table row itself is
		// committed above — upsertWithParent's closure write silently no-ops if
		// the parent doesn't already exist.
		return s.scanRowAccessPolicies(ctx, d, page.Tables)
	}); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied(s.st, "bigquery:tables.list", s.p.ID, err)
		}
		return err
	}
	return nil
}

// scanRowAccessPolicies fetches each table's row access policies, embedding
// per-policy real IAM policy data. A per-table list denial is skip-logged and
// the loop continues to the next table (a single table's denial must never
// abort the dataset's remaining tables); a GetIamPolicy denial is skip-logged
// and leaves that policy's iamPolicy nil. Any other error aborts.
func (s *bigQueryScan) scanRowAccessPolicies(ctx context.Context, d bqDatasetRef, tables []*bigquery.TableListTables) error {
	for _, tb := range tables {
		if tb.TableReference == nil || tb.TableReference.TableId == "" {
			continue
		}
		tableID := tb.TableReference.TableId
		tableResID := store.ResourceID("gcp", s.p.ID, tb.Id)
		rerr := s.svc.RowAccessPolicies.List(d.projectID, d.datasetID, tableID).Pages(ctx, func(rpage *bigquery.ListRowAccessPoliciesResponse) error {
			var rbatch []*store.Resource
			for _, rap := range rpage.RowAccessPolicies {
				if rap == nil || rap.RowAccessPolicyReference == nil || rap.RowAccessPolicyReference.PolicyId == "" {
					continue
				}
				ref := rap.RowAccessPolicyReference
				rapNative := fmt.Sprintf("projects/%s/datasets/%s/tables/%s/rowAccessPolicies/%s", ref.ProjectId, ref.DatasetId, ref.TableId, ref.PolicyId)
				var iamPolicy *bigquery.Policy
				pol, perr := s.svc.RowAccessPolicies.GetIamPolicy(rapNative, &bigquery.GetIamPolicyRequest{}).Context(ctx).Do()
				if perr != nil {
					if isPermissionDenied(perr) {
						_ = skipIfDenied(s.st, "bigquery:rowAccessPolicies.getIamPolicy", s.p.ID, perr)
					} else {
						return perr
					}
				} else {
					iamPolicy = pol
				}
				rbatch = append(rbatch, &store.Resource{
					Provider:    "gcp",
					AccountID:   s.p.ID,
					AccountName: &s.p.Name,
					Type:        TypeBQRowAccessPolicy,
					NativeID:    rapNative,
					Name:        &ref.PolicyId,
					AttributesJSON: mustJSON(rowAccessPolicyAttrs{
						RowAccessPolicy: rap,
						IamPolicy:       iamPolicy,
					}),
					DiscoveredBy: s.scanID,
				})
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			rt, rn, rerr := upsertWithParent(s.st, rbatch, tableResID)
			s.total += rt
			s.inserted += rn
			return rerr
		})
		if rerr != nil {
			if isPermissionDenied(rerr) {
				// Row access policies are a per-table security feature, not a
				// service-enablement signal — Datasets.List (phase 1) already
				// owns "is BigQuery enabled at all" detection and would have
				// aborted before reaching here if not. Never let a single
				// table's denial escalate to the whole-service disabled
				// sentinel; always warn and move on to the next table.
				_ = skipIfDenied(s.st, "bigquery:rowAccessPolicies.list", s.p.ID, rerr)
				continue
			}
			return rerr
		}
	}
	return nil
}

// scanModels lists a dataset's ML models, parented under the dataset.
func (s *bigQueryScan) scanModels(ctx context.Context, d bqDatasetRef, region, dsResourceID string) error {
	if _, _, err := runPaginated(ctx, s.st, s.p, "bigquery:models.list",
		s.svc.Models.List(d.projectID, d.datasetID),
		func(page *bigquery.ListModelsResponse) (int, int, error) {
			var batch []*store.Resource
			for _, m := range page.Models {
				if m == nil || m.ModelReference == nil || m.ModelReference.ModelId == "" {
					continue
				}
				ref := m.ModelReference
				mNative := fmt.Sprintf("projects/%s/datasets/%s/models/%s", ref.ProjectId, ref.DatasetId, ref.ModelId)
				mRegion := m.Location
				if mRegion == "" {
					mRegion = region
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      s.p.ID,
					AccountName:    &s.p.Name,
					Type:           TypeBQModel,
					NativeID:       mNative,
					Name:           &ref.ModelId,
					Region:         strp(mRegion),
					CreatedAt:      msToRFC3339(m.CreationTime),
					AttributesJSON: mustJSON(m),
					DiscoveredBy:   s.scanID,
				})
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			mt, mn, merr := upsertWithParent(s.st, batch, dsResourceID)
			s.total += mt
			s.inserted += mn
			return mt, mn, merr
		}); err != nil {
		return err
	}
	return nil
}

// scanRoutines lists a dataset's routines, parented under the dataset.
// ReadMask additionally populates SparkOptions (bare List omits it) — see the
// SparkOptions.Connection resolver note above.
func (s *bigQueryScan) scanRoutines(ctx context.Context, d bqDatasetRef, region, dsResourceID string) error {
	if _, _, err := runPaginated(ctx, s.st, s.p, "bigquery:routines.list",
		s.svc.Routines.List(d.projectID, d.datasetID).ReadMask(
			"etag,routineReference,routineType,creationTime,lastModifiedTime,language,definitionBody,importedLibraries,sparkOptions,remoteFunctionOptions"),
		func(page *bigquery.ListRoutinesResponse) (int, int, error) {
			var batch []*store.Resource
			for _, rt := range page.Routines {
				if rt == nil || rt.RoutineReference == nil || rt.RoutineReference.RoutineId == "" {
					continue
				}
				ref := rt.RoutineReference
				rtNative := fmt.Sprintf("projects/%s/datasets/%s/routines/%s", ref.ProjectId, ref.DatasetId, ref.RoutineId)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      s.p.ID,
					AccountName:    &s.p.Name,
					Type:           TypeBQRoutine,
					NativeID:       rtNative,
					Name:           &ref.RoutineId,
					Region:         strp(region),
					CreatedAt:      msToRFC3339(rt.CreationTime),
					AttributesJSON: mustJSON(rt),
					DiscoveredBy:   s.scanID,
				})
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			rtt, rtn, rterr := upsertWithParent(s.st, batch, dsResourceID)
			s.total += rtt
			s.inserted += rtn
			return rtt, rtn, rterr
		}); err != nil {
		return err
	}
	return nil
}

// scanBigQueryWithClient is the test seam for scanBigQuery — takes the
// pre-built client directly so tests can point it at a fake server.
func scanBigQueryWithClient(ctx context.Context, svc *bigquery.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: list datasets (cheap stub) so we know which IDs to deep-get.
	var datasets []bqDatasetRef
	if _, _, err := runPaginated(ctx, st, p, "bigquery:datasets.list",
		svc.Datasets.List(p.ID),
		func(page *bigquery.DatasetList) (int, int, error) {
			for _, d := range page.Datasets {
				if d.DatasetReference == nil {
					continue
				}
				datasets = append(datasets, bqDatasetRef{
					datasetID: d.DatasetReference.DatasetId,
					projectID: d.DatasetReference.ProjectId,
				})
			}
			return 0, 0, nil
		}); err != nil {
		return 0, 0, err
	}

	// Phase 2: per-dataset Get + Tables.List, bounded fan-out.
	s := &bigQueryScan{svc: svc, p: p, st: st, scanID: scanID}
	if err := forEachItem(ctx, maxConcurrentBQDatasets, datasets, s.scanDataset); err != nil {
		return s.total, s.inserted, err
	}
	return s.total, s.inserted, nil
}
