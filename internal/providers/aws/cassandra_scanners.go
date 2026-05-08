package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	keyspacestypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cassandra",
		fn:   scanCassandra,
		emits: []coverage.TypeDecl{
			{Service: "cassandra", DiscoType: TypeCassandraKeyspace, Leaf: true},
			{Service: "cassandra", DiscoType: TypeCassandraTable},
			{Service: "cassandra", DiscoType: TypeCassandraType, Leaf: true},
		},
	})
}

// cassandraSystemKeyspaces lists the AWS-managed system keyspaces shipped
// with every Amazon Keyspaces region. Their tables/types are catalogue
// rows owned by AWS — they use AWS-managed encryption (no useful KMS
// edges) and qualify for ManagedByProvider=true. Skip GetTable
// enrichment on them; the per-region GetTable fan-out is dominated by
// these ~40+ tables.
var cassandraSystemKeyspaces = map[string]bool{
	"system":                  true,
	"system_schema":           true,
	"system_schema_mcs":       true,
	"system_multiregion_info": true,
}

type cassandraAPI interface {
	ListKeyspaces(context.Context, *keyspaces.ListKeyspacesInput, ...func(*keyspaces.Options)) (*keyspaces.ListKeyspacesOutput, error)
	ListTables(context.Context, *keyspaces.ListTablesInput, ...func(*keyspaces.Options)) (*keyspaces.ListTablesOutput, error)
	ListTypes(context.Context, *keyspaces.ListTypesInput, ...func(*keyspaces.Options)) (*keyspaces.ListTypesOutput, error)
	GetTable(context.Context, *keyspaces.GetTableInput, ...func(*keyspaces.Options)) (*keyspaces.GetTableOutput, error)
}

// scanCassandra discovers Amazon Keyspaces (Apache Cassandra-compatible)
// keyspaces, tables (per keyspace), and user-defined types (per keyspace).
func scanCassandra(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := keyspaces.NewFromConfig(acct.cfg, func(o *keyspaces.Options) { o.Region = region })

	keyspaceNames, t, i, ferr := scanCassandraKeyspaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCassandraTables(ctx, client, keyspaceNames, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCassandraTypes(ctx, client, keyspaceNames, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCassandraKeyspaces(ctx context.Context, client cassandraAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var names []string
	var nextToken *string
	for {
		out, err := client.ListKeyspaces(ctx, &keyspaces.ListKeyspacesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "cassandra:ListKeyspaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("cassandra:ListKeyspaces: %w", err)
		}
		for _, k := range out.Keyspaces {
			arn := sv(k.ResourceArn)
			name := sv(k.KeyspaceName)
			if arn == "" || name == "" {
				continue
			}
			names = append(names, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCassandraKeyspace, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON:    mustJSON(k),
				ManagedByProvider: cassandraSystemKeyspaces[name],
				DiscoveredBy:      scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "cassandra keyspaces")
	return names, t, i, err
}

func scanCassandraTables(ctx context.Context, client cassandraAPI, ksNames []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// Phase 1: collect all (keyspace, table-summary) pairs from ListTables.
	// AWS Keyspaces ships ~40+ system tables per region, so the per-table
	// GetTable enrichment dominates wall time when run serially.
	type pair struct {
		ks  string
		tbl keyspacestypes.TableSummary
	}
	var pairs []pair
	for _, ks := range ksNames {
		ksName := ks
		var nextToken *string
		for {
			out, err := client.ListTables(ctx, &keyspaces.ListTablesInput{
				KeyspaceName: &ksName,
				NextToken:    nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "cassandra:ListTables", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("cassandra:ListTables ks=%s: %w", ksName, err)
			}
			for _, t := range out.Tables {
				if sv(t.ResourceArn) == "" {
					continue
				}
				pairs = append(pairs, pair{ks: ksName, tbl: t})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}

	// Phase 2: parallel GetTable enrichment. EncryptionSpecification.KmsKeyIdentifier
	// is only on the GetTable body. Fan-out at fanoutMed; fall back to summary
	// attrs on per-row failure.
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, p := range pairs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			arn := sv(p.tbl.ResourceArn)
			attrs := mustJSON(p.tbl)
			managed := cassandraSystemKeyspaces[p.ks]
			ksn := p.ks
			tn := sv(p.tbl.TableName)
			// Skip GetTable on AWS-managed system keyspaces — they use
			// AWS-managed encryption and yield no useful edges.
			if !managed && tn != "" {
				gout, gerr := client.GetTable(gctx, &keyspaces.GetTableInput{KeyspaceName: &ksn, TableName: &tn})
				if gerr != nil {
					if isAccessDenied(gerr) {
						_ = skipIfAccessDenied(st, "cassandra:GetTable", acct.ID, region, gerr)
					}
				} else if gout != nil {
					attrs = mustJSON(gout)
				}
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCassandraTable, NativeID: arn,
				Name: p.tbl.TableName, Region: &region,
				AttributesJSON:    attrs,
				ManagedByProvider: managed,
				DiscoveredBy:      scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "cassandra tables")
}

func scanCassandraTypes(ctx context.Context, client cassandraAPI, ksNames []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, ks := range ksNames {
		ksName := ks
		var nextToken *string
		for {
			out, err := client.ListTypes(ctx, &keyspaces.ListTypesInput{
				KeyspaceName: &ksName,
				NextToken:    nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "cassandra:ListTypes", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("cassandra:ListTypes ks=%s: %w", ksName, err)
			}
			for _, name := range out.Types {
				if name == "" {
					continue
				}
				typeName := name
				arn := fmt.Sprintf("arn:aws:cassandra:%s:%s:keyspace/%s/type/%s", region, acct.ID, ksName, typeName)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCassandraType, NativeID: arn,
					Name: &typeName, Region: &region,
					AttributesJSON:    mustJSON(map[string]string{"KeyspaceName": ksName, "TypeName": typeName}),
					ManagedByProvider: cassandraSystemKeyspaces[ksName],
					DiscoveredBy:      scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "cassandra types")
}
