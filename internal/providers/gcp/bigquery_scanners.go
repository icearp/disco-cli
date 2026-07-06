package gcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
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
	registerService(serviceEntry{
		name: "gcp:bigquery",
		fn:   scanBigQuery,
		emits: []coverage.TypeDecl{
			{Service: "bigquery", DiscoType: TypeBQDataset},
			{Service: "bigquery", DiscoType: TypeBQTable},
		},
	})
}

// maxConcurrentBQDatasets caps per-project dataset fan-out for the
// per-dataset Get + Tables.List pair.
const maxConcurrentBQDatasets = 10

// scanBigQuery discovers BigQuery datasets and tables.
//   - Phase 1: `bigquery/v2` `Datasets.List` paginated — returns lightweight
//     stubs (no encryption config).
//   - Phase 2: per-dataset fan-out: `Datasets.Get` for the full proto
//     (needed for `defaultEncryptionConfiguration.kmsKeyName` → CMEK edge),
//     then `Tables.List` paginated. Routines + Models deferred per ROADMAP —
//     rarer, and dataset+table coverage already hits the bulk of "what's
//     the schema, which tables are CMEK".
//
// Tables stored as `Tables.List` stubs (no encryption / schema). Per-table
// `Tables.Get` is intentionally skipped — table counts can run into the
// thousands per dataset and pay for nothing without a rule-engine query.
func scanBigQuery(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("bigquery client: %w", err)
	}

	// Phase 1: list datasets (cheap stub) so we know which IDs to deep-get.
	type dsRef struct {
		datasetID string // BigQuery dataset ID (last segment)
		projectID string
	}
	var datasets []dsRef
	if _, _, err := runPaginated(ctx, st, p, "bigquery:datasets.list",
		svc.Datasets.List(p.ID),
		func(page *bigquery.DatasetList) (int, int, error) {
			for _, d := range page.Datasets {
				if d.DatasetReference == nil {
					continue
				}
				datasets = append(datasets, dsRef{
					datasetID: d.DatasetReference.DatasetId,
					projectID: d.DatasetReference.ProjectId,
				})
			}
			return 0, 0, nil
		}); err != nil {
		return 0, 0, err
	}

	// Phase 2: per-dataset Get + Tables.List.
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentBQDatasets, datasets, func(gctx context.Context, d dsRef) error {
		full, err := svc.Datasets.Get(d.projectID, d.datasetID).Context(gctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "bigquery:datasets.get", p.ID, err)
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
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeBQDataset,
			NativeID:       nativeID,
			Name:           &name,
			Region:         strp(region),
			CreatedAt:      msToRFC3339(full.CreationTime),
			AttributesJSON: mustJSON(full),
			DiscoveredBy:   scanID,
		}
		mu.Lock()
		tt, nn, uerr := upsertWithProjClosure(p, st, []*store.Resource{dsResource})
		total += tt
		inserted += nn
		mu.Unlock()
		if uerr != nil {
			return uerr
		}

		// Tables for the dataset.
		dsResourceID := store.ResourceID("gcp", p.ID, TypeBQDataset, nativeID)
		if err := svc.Tables.List(d.projectID, d.datasetID).Pages(gctx, func(page *bigquery.TableList) error {
			var batch []*store.Resource
			for _, tb := range page.Tables {
				tname := ""
				if tb.TableReference != nil {
					tname = tb.TableReference.TableId
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBQTable,
					NativeID:       tb.Id,
					Name:           &tname,
					Region:         strp(region),
					CreatedAt:      msToRFC3339(tb.CreationTime),
					AttributesJSON: mustJSON(tb),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			bt, bn, berr := upsertWithParent(st, batch, dsResourceID)
			total += bt
			inserted += bn
			return berr
		}); err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "bigquery:tables.list", p.ID, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
