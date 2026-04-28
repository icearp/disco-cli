package gcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codeberg.org/icearp/disco/internal/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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

func init() { registerService(serviceEntry{name: "gcp:bigquery", fn: scanBigQuery}) }

// maxConcurrentBQDatasets caps per-project dataset fan-out for the
// per-dataset Get + Tables.List pair.
const maxConcurrentBQDatasets = 10

// scanBigQuery discovers BigQuery datasets and tables.
//   - Phase 1: `bigquery/v2` `Datasets.List` paginated — returns lightweight
//     stubs (no encryption config).
//   - Phase 2: per-dataset fan-out: `Datasets.Get` for the full proto
//     (needed for `defaultEncryptionConfiguration.kmsKeyName` → CMEK edge),
//     then `Tables.List` paginated. Routines + Models deferred — per
//     ROADMAP they're queued but rarer; the dataset+table coverage here
//     hits the bulk of "what's the schema, which tables are CMEK".
//
// Tables stored as `Tables.List` stubs (no encryption / schema). The deeper
// `Tables.Get` per table is intentionally skipped — table counts can run
// into the thousands per dataset and per-table Get pays for nothing without
// a corresponding rule-engine query.
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
	if err := svc.Datasets.List(p.ID).Pages(ctx, func(page *bigquery.DatasetList) error {
		for _, d := range page.Datasets {
			if d.DatasetReference == nil {
				continue
			}
			datasets = append(datasets, dsRef{
				datasetID: d.DatasetReference.DatasetId,
				projectID: d.DatasetReference.ProjectId,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "bigquery:datasets.list", p.ID, err)
		}
		return 0, 0, err
	}

	// Phase 2: per-dataset Get + Tables.List.
	sem := semaphore.NewWeighted(maxConcurrentBQDatasets)
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for _, d := range datasets {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			full, err := svc.Datasets.Get(d.projectID, d.datasetID).Context(gctx).Do()
			if err != nil {
				if isPermissionDenied(err) {
					return skipIfDenied(st, "bigquery:datasets.get", p.ID, err)
				}
				return err
			}
			// Native ID for dataset: the full opaque "{project}:{dataset}" ID
			// returned from List/Get; matches BigQuery's own canonical reference.
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
				if len(batch) == 0 {
					return nil
				}
				mu.Lock()
				defer mu.Unlock()
				bn, berr := st.UpsertResources(batch)
				if berr != nil {
					return berr
				}
				total += len(batch)
				inserted += bn
				// Closure: table → dataset.
				pairs := make([][2]string, 0, len(batch))
				for _, r := range batch {
					pairs = append(pairs, [2]string{
						store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
						dsResourceID,
					})
				}
				return st.BatchAddToHierarchyClosure(pairs)
			}); err != nil {
				if isPermissionDenied(err) {
					return skipIfDenied(st, "bigquery:tables.list", p.ID, err)
				}
				return err
			}
			return nil
		})
	}
	return total, inserted, g.Wait()
}
