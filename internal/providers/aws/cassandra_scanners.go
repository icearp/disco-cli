package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	keyspacestypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"github.com/aws/aws-sdk-go-v2/service/keyspacesstreams"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCassandraKeyspace, Service: "cassandra"})
	registerType(restype.Descriptor{Type: TypeCassandraStream, Service: "cassandra"})
	registerType(restype.Descriptor{Type: TypeCassandraTable, Service: "cassandra"})
	registerType(restype.Descriptor{Type: TypeCassandraType, Service: "cassandra"})
	registerService(serviceEntry{
		name: "aws:cassandra",
		fn:   scanCassandra,
	})
}

// cassandraSystemKeyspaces lists AWS-managed system keyspaces shipped in
// every Amazon Keyspaces region. Their tables/types are AWS-owned catalogue
// rows — AWS-managed encryption (no useful KMS edges), so they qualify for
// ManagedByProvider=true. Skip GetTable enrichment on them; the per-region
// GetTable fan-out is dominated by these ~40+ tables.
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

// cassandraStreamsAPI is the narrow set of Keyspaces CDC stream operations,
// served by a separate SDK package and endpoint from cassandraAPI.
type cassandraStreamsAPI interface {
	ListStreams(context.Context, *keyspacesstreams.ListStreamsInput, ...func(*keyspacesstreams.Options)) (*keyspacesstreams.ListStreamsOutput, error)
}

// cassandraTableKey names a table within one region's keyspaces.
type cassandraTableKey struct {
	keyspace, table string
}

// scanCassandra discovers Amazon Keyspaces (Apache Cassandra-compatible)
// keyspaces, tables (per keyspace), user-defined types (per keyspace) and CDC
// streams.
func scanCassandra(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := keyspaces.NewFromConfig(acct.cfg, func(o *keyspaces.Options) { o.Region = region })
	streamsClient := keyspacesstreams.NewFromConfig(acct.cfg, func(o *keyspacesstreams.Options) { o.Region = region })
	return scanCassandraAll(ctx, client, streamsClient, acct, region, st, scanID)
}

// scanCassandraAll runs every phase even when an earlier one fails, so one
// phase's error costs no other phase its rows. Tables and types fan out over
// the keyspaces listed first (none when that listing failed); streams list
// the whole region and use the tables only for their contains pairs.
func scanCassandraAll(ctx context.Context, client cassandraAPI, streamsClient cassandraStreamsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	keyspaceNames, kt, ki, kerr := scanCassandraKeyspaces(ctx, client, acct, region, st, scanID)
	tableARNs, tt, ti, terr := scanCassandraTables(ctx, client, keyspaceNames, acct, region, st, scanID)
	yt, yi, yerr := scanCassandraTypes(ctx, client, keyspaceNames, acct, region, st, scanID)
	stt, sti, serr := scanCassandraStreams(ctx, streamsClient, tableARNs, acct, region, st, scanID)
	return kt + tt + yt + stt, ki + ti + yi + sti, errors.Join(kerr, terr, yerr, serr)
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

// scanCassandraTables returns the stored tables' ARNs by keyspace and table
// name, or nil with its error.
func scanCassandraTables(ctx context.Context, client cassandraAPI, ksNames []string, acct *account, region string, st *store.Store, scanID string) (map[cassandraTableKey]string, int, int, error) {
	// Phase 1: collect all (keyspace, table-summary) pairs from ListTables.
	// AWS Keyspaces ships ~40+ system tables per region, so serial per-table
	// GetTable enrichment dominates wall time.
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
				return nil, 0, 0, fmt.Errorf("cassandra:ListTables ks=%s: %w", ksName, err)
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
			return nil, 0, 0, err
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
		return nil, 0, 0, err
	}
	t, i, err := upsertBatch(st, batch, "cassandra tables")
	if err != nil {
		return nil, 0, 0, err
	}
	arns := make(map[cassandraTableKey]string, len(pairs))
	for _, p := range pairs {
		arns[cassandraTableKey{p.ks, sv(p.tbl.TableName)}] = sv(p.tbl.ResourceArn)
	}
	return arns, t, i, nil
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

// scanCassandraStreams discovers the region's CDC streams with one unfiltered
// ListStreams, and records each under the table it captures when the tables
// phase stored that table. The summary carries no status, so every listed
// stream is stored. On the first call, the shapes of an endpoint that does not
// serve the op (isOpNotDeployed) are a silent feature gap. None has been
// observed for this endpoint; the SDK builds it for every region with no
// per-region list, so a region that does not serve it would show only on the
// first call.
// A network failure on any page warns once and keeps the rows listed so far.
// Once a page has succeeded, any other error but a denial is returned after the
// rows listed so far are stored.
func scanCassandraStreams(ctx context.Context, client cassandraStreamsAPI, tableARNs map[cassandraTableKey]string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "cassandra:ListStreams"
	pager := keyspacesstreams.NewListStreamsPaginator(client, &keyspacesstreams.ListStreamsInput{},
		func(o *keyspacesstreams.ListStreamsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var parentARNs []string
	var listErr error
	listed := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			// Unlike every other Keyspaces call, this endpoint is on the
			// dual-stack domain (api.aws in the aws partition), which egress
			// filtering can block. Returned, a network error joined with the
			// other phases' errors would make the dispatcher reclassify the
			// whole service: NXDOMAIN as region-unavailable, which zeroes the
			// counts of the rows already stored, and any other network error
			// as transient, which reports the other phases' hard errors as
			// transient ones.
			case isTransientNetworkError(perr):
				_ = skipIfTransient(st, op, acct.ID, region, fmt.Errorf("dual-stack endpoint cassandra-streams.%s unreachable: %w", region, perr))
			case !listed && isOpNotDeployed(perr):
				return 0, 0, nil
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, s := range out.Streams {
			arn := sv(s.StreamArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCassandraStream, NativeID: arn,
				Name: s.StreamLabel, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
			parentARNs = append(parentARNs, tableARNs[cassandraTableKey{sv(s.KeyspaceName), sv(s.TableName)}])
		}
	}
	t, i, uerr := upsertBatch(st, batch, "cassandra streams")
	if uerr != nil {
		return 0, 0, errors.Join(listErr, uerr)
	}
	var pairs [][2]string
	for n, r := range batch {
		if parentARNs[n] != "" {
			pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, parentARNs[n])})
		}
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return t, i, errors.Join(listErr, fmt.Errorf("closure cassandra streams: %w", herr))
	}
	return t, i, listErr
}
