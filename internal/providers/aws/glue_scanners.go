package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueDatabase, Service: "glue", Redact: glueIfSensitive("Parameters.*")})
	registerType(restype.Descriptor{Type: TypeGlueTable, Service: "glue", Redact: glueTableRedact()})
	registerType(restype.Descriptor{Type: TypeGluePartition, Service: "glue", Redact: glueStorageRedact()})
	registerType(restype.Descriptor{Type: TypeGlueTableOptimizer, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueBlueprint, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueUserDefinedFunction, Service: "glue"})
	registerService(serviceEntry{
		name: "aws:glue",
		fn:   scanGlue,
	})
}

// glueAPI is the narrow set of Glue operations called by the scanGlue
// sub-phases.
type glueAPI interface {
	GetDatabases(context.Context, *glue.GetDatabasesInput, ...func(*glue.Options)) (*glue.GetDatabasesOutput, error)
	GetTables(context.Context, *glue.GetTablesInput, ...func(*glue.Options)) (*glue.GetTablesOutput, error)
	GetPartitions(context.Context, *glue.GetPartitionsInput, ...func(*glue.Options)) (*glue.GetPartitionsOutput, error)
	ListTableOptimizerRuns(context.Context, *glue.ListTableOptimizerRunsInput, ...func(*glue.Options)) (*glue.ListTableOptimizerRunsOutput, error)
	GetTableOptimizer(context.Context, *glue.GetTableOptimizerInput, ...func(*glue.Options)) (*glue.GetTableOptimizerOutput, error)
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
	GetCatalogs(context.Context, *glue.GetCatalogsInput, ...func(*glue.Options)) (*glue.GetCatalogsOutput, error)
	ListCustomEntityTypes(context.Context, *glue.ListCustomEntityTypesInput, ...func(*glue.Options)) (*glue.ListCustomEntityTypesOutput, error)
	GetDataCatalogEncryptionSettings(context.Context, *glue.GetDataCatalogEncryptionSettingsInput, ...func(*glue.Options)) (*glue.GetDataCatalogEncryptionSettingsOutput, error)
	ListDataQualityRulesets(context.Context, *glue.ListDataQualityRulesetsInput, ...func(*glue.Options)) (*glue.ListDataQualityRulesetsOutput, error)
	GetGlueIdentityCenterConfiguration(context.Context, *glue.GetGlueIdentityCenterConfigurationInput, ...func(*glue.Options)) (*glue.GetGlueIdentityCenterConfigurationOutput, error)
	DescribeIntegrations(context.Context, *glue.DescribeIntegrationsInput, ...func(*glue.Options)) (*glue.DescribeIntegrationsOutput, error)
	ListIntegrationResourceProperties(context.Context, *glue.ListIntegrationResourcePropertiesInput, ...func(*glue.Options)) (*glue.ListIntegrationResourcePropertiesOutput, error)
	GetSecurityConfigurations(context.Context, *glue.GetSecurityConfigurationsInput, ...func(*glue.Options)) (*glue.GetSecurityConfigurationsOutput, error)
	ListUsageProfiles(context.Context, *glue.ListUsageProfilesInput, ...func(*glue.Options)) (*glue.ListUsageProfilesOutput, error)
	ListBlueprints(context.Context, *glue.ListBlueprintsInput, ...func(*glue.Options)) (*glue.ListBlueprintsOutput, error)
	GetUserDefinedFunctions(context.Context, *glue.GetUserDefinedFunctionsInput, ...func(*glue.Options)) (*glue.GetUserDefinedFunctionsOutput, error)
	GetResourcePolicy(context.Context, *glue.GetResourcePolicyInput, ...func(*glue.Options)) (*glue.GetResourcePolicyOutput, error)
	ListAssetTypes(context.Context, *glue.ListAssetTypesInput, ...func(*glue.Options)) (*glue.ListAssetTypesOutput, error)
	ListGlossaries(context.Context, *glue.ListGlossariesInput, ...func(*glue.Options)) (*glue.ListGlossariesOutput, error)
	ListGlossaryTerms(context.Context, *glue.ListGlossaryTermsInput, ...func(*glue.Options)) (*glue.ListGlossaryTermsOutput, error)
	GetTags(context.Context, *glue.GetTagsInput, ...func(*glue.Options)) (*glue.GetTagsOutput, error)
}

const glueGetTagsOp = "glue:GetTags"

// Page-size caps (MaxResults) the model documents for the Glue listings,
// named after the model's page-size shapes.
const (
	gluePageSize                 int32 = 1000 // PageSize
	glueCatalogGetterPageSize    int32 = 100  // CatalogGetterPageSize: GetDatabases
	glueMaxResultsNumber         int32 = 100  // MaxResultsNumber: ListRegistries, ListSchemas
	glueOrchestrationPageSize25  int32 = 25   // ListWorkflows, ListBlueprints
	glueOrchestrationPageSize200 int32 = 200  // GetTriggers, ListUsageProfiles
)

func scanGlue(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := glue.NewFromConfig(acct.cfg, func(o *glue.Options) { o.Region = region })
	return scanGlueAll(ctx, client, acct, region, st, scanID)
}

// scanGlueAll discovers Glue resources in one region, running its phases one
// after another. A phase's error does not stop the later ones; a failed
// write does, since every later write would fail too, and so does ctx's end,
// reported as one error. A child phase gets the parents its parent phase
// stored: tables and user-defined functions the databases, partitions and
// table optimizers the tables, schemas the registries, and so on down the
// schema registry. The phases run one at a time, so readRowTags's own bound
// is the bound on the GetTags reads in flight. Every phase calls the one
// Glue endpoint, so an NXDOMAIN after rows were stored is a failure of this
// scan, not a region gap (resultLateNXDOMAIN).
func scanGlueAll(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p               phaseTotals
		tags            = glueTags(client)
		databases, regs []childParent
		tableRefs       []glueTableRef
		schemaARNs      []string
		versionIDs      []string
	)
	counted := func(op string, run func() (int, int, error)) func() bool {
		return func() bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}
	}
	phases := []func() bool{
		func() (stop bool) {
			databases, stop = scanGlueDatabases(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool {
			t, i, refs, err := scanGlueTables(ctx, client, acct, region, st, scanID, childParentIDs(databases))
			tableRefs = refs
			return p.addPhase("glue:GetTables", t, i, err)
		},
		counted("glue:GetPartitions", func() (int, int, error) {
			return scanGluePartitions(ctx, client, acct, region, st, scanID, tableRefs)
		}),
		counted("glue:GetTableOptimizer", func() (int, int, error) {
			return scanGlueTableOptimizers(ctx, client, acct, region, st, scanID, tableRefs)
		}),
		func() (stop bool) {
			regs, stop = scanGlueRegistries(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() (stop bool) {
			schemaARNs, stop = scanGlueSchemas(ctx, client, regs, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool {
			ids, t, i, err := scanGlueSchemaVersions(ctx, client, schemaARNs, acct, region, st, scanID)
			versionIDs = ids
			return p.addPhase("glue:ListSchemaVersions", t, i, err)
		},
		counted("glue:QuerySchemaVersionMetadata", func() (int, int, error) {
			return scanGlueSchemaVersionMetadata(ctx, client, versionIDs, acct, region, st, scanID)
		}),
		func() bool { return scanGlueJobsPhase(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueTriggers(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueWorkflows(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueMLTransforms(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueDevEndpoints(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueCatalogs(ctx, client, acct, region, st, scanID, &p) },
		func() bool { return scanGlueCustomEntityTypes(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("glue:GetDataCatalogEncryptionSettings", func() (int, int, error) {
			return scanGlueDataCatalogEncryption(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanGlueDataQualityRulesets(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("glue:GetGlueIdentityCenterConfiguration", func() (int, int, error) {
			return scanGlueIdentityCenter(ctx, client, acct, region, st, scanID)
		}),
		counted("glue:DescribeIntegrations", func() (int, int, error) {
			return scanGlueIntegrations(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanGlueIntegrationResourceProps(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("glue:GetSecurityConfigurations", func() (int, int, error) {
			return scanGlueSecurityConfigurations(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanGlueUsageProfiles(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueCrawlers(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanGlueConnections(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("glue:GetClassifiers", func() (int, int, error) {
			return scanGlueClassifiers(ctx, client, acct, region, st, scanID)
		}),
		func() bool { return scanGlueBlueprints(ctx, client, acct, region, st, scanID, tags, &p) },
		counted("glue:GetUserDefinedFunctions", func() (int, int, error) {
			return scanGlueUserDefinedFunctions(ctx, client, acct, region, st, scanID, childParentIDs(databases))
		}),
		counted("glue:GetResourcePolicy", func() (int, int, error) {
			return scanGlueMetadata(ctx, client, acct, region, st, scanID)
		}),
	}
	notRun := 0
	for i, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - i
			break
		}
		if run() {
			break
		}
	}
	p.foldStop(ctx, "glue", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// glueStorageRedact judges a partition's parameter maps, where a
// connector's table, serde or column properties can carry a password;
// glueTableRedact adds the maps only a table has.
func glueStorageRedact() []redact.Rule {
	return glueIfSensitive(
		"Parameters.*",
		"StorageDescriptor.Parameters.*",
		"StorageDescriptor.SerdeInfo.Parameters.*",
		"StorageDescriptor.Columns[*].Parameters.*",
	)
}

func glueTableRedact() []redact.Rule {
	return append(glueStorageRedact(), glueIfSensitive("PartitionKeys[*].Parameters.*", "IcebergTableMetadata.Properties.*")...)
}

// glueTags reads a row's tags with GetTags, sent the row's NativeID, which
// for every type read through it is the resource's ARN: the one its list
// element carries (registry, schema, integration resource property), or one
// built in the ARN format the Service Reference documents for the type
// (glueResourceARN, glueDatabaseARN). Glue's tagging guide lists connections,
// databases, crawlers, dev endpoints, jobs, triggers, workflows, blueprints,
// ML transforms, data quality rulesets, stream schemas and schema
// registries as taggable; the Service Reference's GetTags action adds the
// catalogs, custom entity types and usage profiles, and its TagResource
// action the integration resource properties, whose Create ops all take
// Tags. Tables and user-defined functions are not read: neither appears in
// any of those lists, and their Create ops take no Tags. GetTags answers
// with every tag at once (no page token); EntityNotFoundException is the
// only not-found code its model lists.
func glueTags(client glueAPI) rowTagReader {
	return glueTagsAt(client, func(r *store.Resource) (string, error) { return r.NativeID, nil })
}

// glueTagsAt is glueTags sent the ARN arnOf gives for a row, for a type whose
// NativeID is not the ARN GetTags takes.
func glueTagsAt(client glueAPI, arnOf func(*store.Resource) (string, error)) rowTagReader {
	return rowTagReader{
		op:       glueGetTagsOp,
		notFound: []string{"EntityNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn, err := arnOf(r)
			if err != nil {
				return nil, err
			}
			out, err := client.GetTags(ctx, &glue.GetTagsInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// glueRow is the row of a listed Glue item stored under nativeID, item
// verbatim as the attributes.
func glueRow(acct *account, region, scanID, typ, nativeID, name string, item any) *store.Resource {
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           typ,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(item),
		DiscoveredBy:   scanID,
	}
}

// glueNamedRow is glueRow for an item whose ARN is built from its name in
// kind's ARN format (glueResourceARN). An item without a name gives no row:
// the reason names the items (what), and key, or a fixed text when key is
// empty, names the item in the warning about them.
func glueNamedRow(acct *account, region, scanID, typ, kind, what, name, key string, item any) (*store.Resource, string, string) {
	if name == "" {
		return nil, what + " have an empty name", cmp.Or(key, "(an item with no id)")
	}
	return glueRow(acct, region, scanID, typ, glueResourceARN(region, acct.ID, kind, name), name, item), "", ""
}

// glueResourceARN builds the ARN in the commercial partition only: account
// carries no partition, and every AWS scanner hardcodes arn:aws, so in aws-cn
// and aws-us-gov the NativeID is not the resource's ARN and GetTags fails
// for each row. Fixing it is a package-wide change, not a Glue one.
func glueResourceARN(region, accountID, kind, id string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:%s/%s", region, accountID, kind, id)
}

func glueDatabaseARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", region, accountID, name)
}

func glueTableARN(region, accountID, dbName, tableName string) string {
	return fmt.Sprintf("arn:aws:glue:%s:%s:table/%s/%s", region, accountID, dbName, tableName)
}

// scanGlueDatabases stores the account's databases with their tags and
// returns the stored ones, keyed by name, the parents of the table and
// user-defined-function phases. A database's element carries no ARN; its
// NativeID is built in the documented database ARN format.
func scanGlueDatabases(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (databases []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "glue:GetDatabases", "databases", "Name",
		func(ctx context.Context, token *string) ([]gluetypes.Database, *string, error) {
			out, err := client.GetDatabases(ctx, &glue.GetDatabasesInput{NextToken: token, MaxResults: sdkaws.Int32(glueCatalogGetterPageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.DatabaseList, out.NextToken, nil
		},
		func(d gluetypes.Database) (*store.Resource, string, string) {
			name := sv(d.Name)
			if name == "" {
				return nil, "databases have an empty name", cmp.Or(sv(d.CatalogId), "(a database with no catalog id)")
			}
			return glueRow(acct, region, scanID, TypeGlueDatabase, glueDatabaseARN(region, acct.ID, name), name, d), "", ""
		},
		func(d gluetypes.Database) string { return sv(d.Name) },
		tags, p)
}

// glueTableRef identifies a Glue table by (db, table, catalogId, isIceberg).
// catalogId is empty for the default catalog; isIceberg is true when
// Parameters carries table_type=ICEBERG — gates the table-optimizer fan-out
// (Iceberg-only).
type glueTableRef struct {
	db        string
	table     string
	catalogID string
	isIceberg bool
}

func scanGlueTables(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, dbNames []string) (total, inserted int, refs []glueTableRef, err error) {
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, dbName := range dbNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, nil, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pager := glue.NewGetTablesPaginator(client, &glue.GetTablesInput{DatabaseName: &dbName})
			parentID := store.ResourceID("aws", acct.ID, glueDatabaseARN(region, acct.ID, dbName))
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
					ref := glueTableRef{
						db:        dbName,
						table:     name,
						catalogID: sv(tbl.CatalogId),
						isIceberg: tbl.Parameters["table_type"] == "ICEBERG",
					}
					mu.Lock()
					batch = append(batch, r)
					pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, arn), parentID})
					refs = append(refs, ref)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, nil, werr
	}
	if len(batch) == 0 {
		return 0, 0, refs, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert glue tables: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, nil, fmt.Errorf("closure glue tables: %w", err)
	}
	return len(batch), n, refs, nil
}
