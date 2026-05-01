package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:glue",
		fn:   scanGlue,
		emits: []coverage.TypeDecl{
			{Service: "glue", DiscoType: TypeGlueDatabase},
			{Service: "glue", DiscoType: TypeGlueTable},
		},
	})
}

// glueAPI is the narrow set of Glue operations called by the scanGlue
// sub-phases.
type glueAPI interface {
	GetDatabases(context.Context, *glue.GetDatabasesInput, ...func(*glue.Options)) (*glue.GetDatabasesOutput, error)
	GetTables(context.Context, *glue.GetTablesInput, ...func(*glue.Options)) (*glue.GetTablesOutput, error)
	ListRegistries(context.Context, *glue.ListRegistriesInput, ...func(*glue.Options)) (*glue.ListRegistriesOutput, error)
	GetRegistry(context.Context, *glue.GetRegistryInput, ...func(*glue.Options)) (*glue.GetRegistryOutput, error)
	ListSchemas(context.Context, *glue.ListSchemasInput, ...func(*glue.Options)) (*glue.ListSchemasOutput, error)
	GetSchema(context.Context, *glue.GetSchemaInput, ...func(*glue.Options)) (*glue.GetSchemaOutput, error)
	ListSchemaVersions(context.Context, *glue.ListSchemaVersionsInput, ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error)
	GetSchemaVersion(context.Context, *glue.GetSchemaVersionInput, ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error)
	QuerySchemaVersionMetadata(context.Context, *glue.QuerySchemaVersionMetadataInput, ...func(*glue.Options)) (*glue.QuerySchemaVersionMetadataOutput, error)
	GetJobs(context.Context, *glue.GetJobsInput, ...func(*glue.Options)) (*glue.GetJobsOutput, error)
	GetTriggers(context.Context, *glue.GetTriggersInput, ...func(*glue.Options)) (*glue.GetTriggersOutput, error)
	ListWorkflows(context.Context, *glue.ListWorkflowsInput, ...func(*glue.Options)) (*glue.ListWorkflowsOutput, error)
	GetWorkflow(context.Context, *glue.GetWorkflowInput, ...func(*glue.Options)) (*glue.GetWorkflowOutput, error)
	GetMLTransforms(context.Context, *glue.GetMLTransformsInput, ...func(*glue.Options)) (*glue.GetMLTransformsOutput, error)
	GetDevEndpoints(context.Context, *glue.GetDevEndpointsInput, ...func(*glue.Options)) (*glue.GetDevEndpointsOutput, error)
	GetCrawlers(context.Context, *glue.GetCrawlersInput, ...func(*glue.Options)) (*glue.GetCrawlersOutput, error)
	GetConnections(context.Context, *glue.GetConnectionsInput, ...func(*glue.Options)) (*glue.GetConnectionsOutput, error)
	GetClassifiers(context.Context, *glue.GetClassifiersInput, ...func(*glue.Options)) (*glue.GetClassifiersOutput, error)
}

// scanGlue discovers Glue Data Catalog databases and tables in one region.
// Catalog itself is implicit (one per account+region) and not modeled. Two
// phases run sequentially: GetDatabases (paginator) → per-database GetTables
// fan-out (errgroup + fanoutMed). Crawlers, jobs, triggers, classifiers, and
// connections deferred — each adds its own sub-tree of edges (role refs,
// network refs) that warrant separate iterations.
func scanGlue(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := glue.NewFromConfig(acct.cfg, func(o *glue.Options) { o.Region = region })

	dbNames, t, i, ferr := scanGlueDatabases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if len(dbNames) == 0 {
		return total, inserted, nil
	}

	{
		t, i, ferr := scanGlueTables(ctx, client, acct, region, st, scanID, dbNames)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanGlueSchema(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanGlueJobs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanGlueCatalog(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func glueDatabaseARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", region, accountID, name)
}

func glueTableARN(region, accountID, dbName, tableName string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:table/%s/%s", region, accountID, dbName, tableName)
}

func scanGlueDatabases(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (dbNames []string, total, inserted int, err error) {
	pager := glue.NewGetDatabasesPaginator(client, &glue.GetDatabasesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetDatabases", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("glue:GetDatabases: %w", perr)
		}
		for _, d := range out.DatabaseList {
			name := sv(d.Name)
			if name == "" {
				continue
			}
			dbNames = append(dbNames, name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueDatabase,
				NativeID:       glueDatabaseARN(region, acct.ID, name),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert glue databases: %w", uerr)
	}
	return dbNames, len(batch), n, nil
}

func scanGlueTables(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, dbNames []string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, dbName := range dbNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pager := glue.NewGetTablesPaginator(client, &glue.GetTablesInput{DatabaseName: &dbName})
			parentID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, glueDatabaseARN(region, acct.ID, dbName))
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					return fmt.Errorf("glue:GetTables %s: %w", dbName, perr)
				}
				for _, tbl := range out.TableList {
					name := sv(tbl.Name)
					if name == "" {
						continue
					}
					arn := glueTableARN(region, acct.ID, dbName, name)
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeGlueTable,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						AttributesJSON: mustJSON(tbl),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					batch = append(batch, r)
					pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, TypeGlueTable, arn), parentID})
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
		return 0, 0, fmt.Errorf("upsert glue tables: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure glue tables: %w", err)
	}
	return len(batch), n, nil
}
