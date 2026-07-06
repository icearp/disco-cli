package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// gluePartitionARN synthesizes a NativeID for a partition — the Glue API
// exposes no ARN; partitions are identified by (db, table, values[]). Values
// are joined with "/" after URL-style escaping so a value containing "/"
// doesn't collide.
func gluePartitionARN(region, acct, db, table string, values []string) string {
	escaped := make([]string, len(values))
	for i, v := range values {
		escaped[i] = strings.ReplaceAll(v, "/", "%2F")
	}
	return fmt.Sprintf("arn:aws:glue:%s:%s:partition/%s/%s/%s", region, acct, db, table, strings.Join(escaped, "/"))
}

// glueTableOptimizerARN synthesizes a NativeID per (db, table, optimizerType),
// mirroring CFN's AWS::Glue::TableOptimizer composite key (CatalogId, db,
// table, type).
func glueTableOptimizerARN(region, acct, db, table, optType string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:table-optimizer/%s/%s/%s", region, acct, db, table, optType)
}

// scanGluePartitions fans out GetPartitions per (db, table). Cardinality can
// explode on Hive-partitioned tables (millions of partitions); fanoutLow
// bounds concurrency, and per-table AccessDenied/EntityNotFoundException/
// ValidationException are tolerated without aborting siblings.
func scanGluePartitions(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, refs []glueTableRef) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutLow)
	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range refs {
		ref := ref
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			parentID := store.ResourceID("aws", acct.ID, TypeGlueTable, glueTableARN(region, acct.ID, ref.db, ref.table))
			pager := glue.NewGetPartitionsPaginator(client, &glue.GetPartitionsInput{
				DatabaseName: &ref.db,
				TableName:    &ref.table,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					if isAPIErrorCode(perr, "EntityNotFoundException", "ValidationException") {
						return nil
					}
					return fmt.Errorf("glue:GetPartitions %s/%s: %w", ref.db, ref.table, perr)
				}
				for _, p := range out.Partitions {
					arn := gluePartitionARN(region, acct.ID, ref.db, ref.table, p.Values)
					name := strings.Join(p.Values, "/")
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeGluePartition,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						AttributesJSON: mustJSON(p),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					batch = append(batch, r)
					pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, TypeGluePartition, arn), parentID})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue partitions: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure glue partitions: %w", err)
	}
	return len(batch), n, nil
}

// glueOptimizerTypes lists the SDK's TableOptimizerType values. Iceberg-only
// feature; non-Iceberg tables are filtered out upstream.
var glueOptimizerTypes = []gluetypes.TableOptimizerType{
	gluetypes.TableOptimizerTypeCompaction,
	gluetypes.TableOptimizerTypeRetention,
	gluetypes.TableOptimizerTypeOrphanFileDeletion,
}

// scanGlueTableOptimizers fans out GetTableOptimizer per (db, table, type).
// Only Iceberg tables are eligible; non-Iceberg tables skip the call to avoid
// wasted RPCs. EntityNotFoundException is expected when an optimizer type
// isn't configured for a given Iceberg table — skip silently.
func scanGlueTableOptimizers(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, refs []glueTableRef) (total, inserted int, err error) {
	// Filter to Iceberg tables.
	icebergRefs := refs[:0:0]
	for _, r := range refs {
		if r.isIceberg {
			icebergRefs = append(icebergRefs, r)
		}
	}
	if len(icebergRefs) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range icebergRefs {
		ref := ref
		for _, optType := range glueOptimizerTypes {
			optType := optType
			if err := sem.Acquire(gctx, 1); err != nil {
				return 0, 0, err
			}
			g.Go(func() error {
				defer sem.Release(1)
				catID := ref.catalogID
				if catID == "" {
					catID = acct.ID
				}
				out, gerr := client.GetTableOptimizer(gctx, &glue.GetTableOptimizerInput{
					CatalogId:    &catID,
					DatabaseName: &ref.db,
					TableName:    &ref.table,
					Type:         optType,
				})
				if gerr != nil {
					if isAccessDenied(gerr) {
						return nil
					}
					if isAPIErrorCode(gerr, "EntityNotFoundException", "ValidationException") {
						return nil
					}
					return fmt.Errorf("glue:GetTableOptimizer %s/%s/%s: %w", ref.db, ref.table, optType, gerr)
				}
				if out.TableOptimizer == nil {
					return nil
				}
				arn := glueTableOptimizerARN(region, acct.ID, ref.db, ref.table, string(optType))
				name := fmt.Sprintf("%s/%s/%s", ref.db, ref.table, optType)
				parentID := store.ResourceID("aws", acct.ID, TypeGlueTable, glueTableARN(region, acct.ID, ref.db, ref.table))
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeGlueTableOptimizer,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(out),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, TypeGlueTableOptimizer, arn), parentID})
				mu.Unlock()
				return nil
			})
		}
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue table-optimizers: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure glue table-optimizers: %w", err)
	}
	return len(batch), n, nil
}
