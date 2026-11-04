package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	keyspacestypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"github.com/aws/aws-sdk-go-v2/service/keyspacesstreams"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspacesstreams/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

const (
	cassandraTagsOp     = "cassandra:ListTagsForResource"
	cassandraGetTableOp = "cassandra:GetTable"
	// cassandraPageCap is the maxResults maximum of ListKeyspaces, ListTables
	// and ListTagsForResource.
	cassandraPageCap = 1000
	// cassandraStreamsPageCap is the maxResults maximum of ListStreams.
	cassandraStreamsPageCap = 100
)

// cassandraGone is the not-found code ListTables, GetTable and
// ListTagsForResource model: a keyspace, table or stream deleted since it
// was listed.
var cassandraGone = []string{"ResourceNotFoundException"}

type cassandraAPI interface {
	ListKeyspaces(context.Context, *keyspaces.ListKeyspacesInput, ...func(*keyspaces.Options)) (*keyspaces.ListKeyspacesOutput, error)
	ListTables(context.Context, *keyspaces.ListTablesInput, ...func(*keyspaces.Options)) (*keyspaces.ListTablesOutput, error)
	ListTypes(context.Context, *keyspaces.ListTypesInput, ...func(*keyspaces.Options)) (*keyspaces.ListTypesOutput, error)
	GetTable(context.Context, *keyspaces.GetTableInput, ...func(*keyspaces.Options)) (*keyspaces.GetTableOutput, error)
	ListTagsForResource(context.Context, *keyspaces.ListTagsForResourceInput, ...func(*keyspaces.Options)) (*keyspaces.ListTagsForResourceOutput, error)
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

// scanCassandraAll runs the phases one at a time (runHostPhases): a phase's
// error does not stop the later ones; a failed write and ctx's end do. Tables
// and types fan out over the keyspaces stored first; streams list the whole
// region and use the stored tables only for their contains pairs. Keyspaces,
// tables and streams have their tags read per row (cassandraTags), whose
// denials are one warning for the whole scan.
func scanCassandraAll(ctx context.Context, client cassandraAPI, streamsClient cassandraStreamsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const keyspacesHost, streamsHost = 0, 1
	tags := cassandraTags(client).sharingDenials()
	var (
		keyspaceParents []childParent
		tableARNs       map[cassandraTableKey]string
		noKeyspaces     bool
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "cassandra", keyspacesHost, []hostPhase{
		{op: "cassandra:ListKeyspaces", host: keyspacesHost, run: func(p *phaseTotals) (failed bool) {
			total, errs := p.total, len(p.errs)
			keyspaceParents, failed = scanCassandraKeyspaces(ctx, client, acct, region, st, scanID, tags, p)
			noKeyspaces = hostAbsentAfter(p, total, errs)
			return failed
		}},
		{op: "cassandra:ListTables", host: keyspacesHost, run: func(p *phaseTotals) (failed bool) {
			tableARNs, failed = scanCassandraTables(ctx, client, keyspaceParents, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(keyspacesHost, "cassandra:ListTypes", func() (int, int, error) {
			return scanCassandraTypes(ctx, client, childParentIDs(keyspaceParents), acct, region, st, scanID)
		}),
		{op: "cassandra:ListStreams", host: streamsHost, run: func(p *phaseTotals) bool {
			// Keyspaces itself not resolving means the region has no
			// Keyspaces; probing the streams endpoint would only add a
			// warning every scan.
			if noKeyspaces {
				return false
			}
			return scanCassandraStreams(ctx, streamsClient, tableARNs, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// cassandraTags reads a row's tags with keyspaces ListTagsForResource, sent
// the row's NativeID: the ARN its listing returned. The Service Reference
// gives keyspace, table and stream the aws:ResourceTag condition key and
// lists all three under TagResource; the op's model takes any cassandra ARN.
// That it answers for a stream's ARN is unverified live: no response read
// here carries a stream's tags, and keyspacesstreams has no tag op.
// The op pages its tags; every page is read, and a repeated nextToken fails
// the read. ResourceNotFoundException, its modeled not-found, drops a
// resource deleted since it was listed.
//
// A ManagedByProvider row (a system keyspace or one of its tables) is not
// read and keeps no tags: AWS creates them, and whether the op accepts them
// is unverified live.
func cassandraTags(client cassandraAPI) rowTagReader {
	return rowTagReader{
		op:       cassandraTagsOp,
		notFound: cassandraGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return nil, nil
			}
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]keyspacestypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &keyspaces.ListTagsForResourceInput{ResourceArn: &arn, NextToken: token, MaxResults: sdkaws.Int32(cassandraPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t keyspacestypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// cassandraRow is the row of a listed element stored under its ARN; what
// names the elements in the warning about one without an ARN.
func cassandraRow(acct *account, region, scanID, typ, what string, arn, name *string, managed bool, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: nonEmptyPtr(sv(name)), Region: &region,
		AttributesJSON:    mustJSON(element),
		ManagedByProvider: managed,
		DiscoveredBy:      scanID,
	})
}

// scanCassandraKeyspaces stores the keyspaces with their tags, a page at a
// time, and returns the stored ones keyed by KeyspaceName, the parents of the
// table and type phases.
func scanCassandraKeyspaces(ctx context.Context, client cassandraAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "cassandra:ListKeyspaces", "keyspace(s)", "KeyspaceName",
		func(ctx context.Context, token *string) ([]keyspacestypes.KeyspaceSummary, *string, error) {
			out, err := client.ListKeyspaces(ctx, &keyspaces.ListKeyspacesInput{NextToken: token, MaxResults: sdkaws.Int32(cassandraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Keyspaces, out.NextToken, nil
		},
		func(k keyspacestypes.KeyspaceSummary) (*store.Resource, string, string) {
			return cassandraRow(acct, region, scanID, TypeCassandraKeyspace, "keyspaces", k.ResourceArn, k.KeyspaceName, cassandraSystemKeyspaces[sv(k.KeyspaceName)], k)
		},
		func(k keyspacestypes.KeyspaceSummary) string { return sv(k.KeyspaceName) },
		tags, p)
}

// scanCassandraTables lists the tables of each stored keyspace, the keyspaces
// concurrently (readEachKey), and stores each table as its GetTable body,
// which adds the encryption key and schema to the summary, then with its
// tags. A keyspace ListTables no longer finds was deleted since it was
// listed and is skipped. A table GetTable no longer finds is not stored; any
// other GetTable failure stores the summary (readRowDetails). A system
// keyspace's table gets no GetTable. It returns the stored tables' ARNs by
// keyspace and table name, or storeWriteFailed (and none) when the write
// failed.
func scanCassandraTables(ctx context.Context, client cassandraAPI, keyspaceParents []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (map[cassandraTableKey]string, bool) {
	const op = "cassandra:ListTables"
	names := childParentIDs(keyspaceParents)
	reads, stopped := readEachKey(ctx, names, func(ctx context.Context, ks string) parentItems[keyspacestypes.TableSummary] {
		return readParentPages(ctx, ks, func(ctx context.Context, ks string, token *string) ([]keyspacestypes.TableSummary, *string, error) {
			out, err := client.ListTables(ctx, &keyspaces.ListTablesInput{KeyspaceName: &ks, NextToken: token, MaxResults: sdkaws.Int32(cassandraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Tables, out.NextToken, nil
		})
	})
	out := parentOutcomes{op: op, notFound: cassandraGone}
	keys := map[*store.Resource]cassandraTableKey{}
	for i, read := range reads {
		addParentRows(&out, names[i], read, func(ks string, t keyspacestypes.TableSummary) (*store.Resource, string, string) {
			r, why, key := cassandraRow(acct, region, scanID, TypeCassandraTable, "tables", t.ResourceArn, t.TableName, cassandraSystemKeyspaces[ks], t)
			if r != nil {
				keys[r] = cassandraTableKey{ks, sv(t.TableName)}
			}
			return r, why, key
		})
	}
	out.stopped(stopped)
	d := &detailedRows{getOp: cassandraGetTableOp, what: "table(s)", notFound: cassandraGone, tags: tags}
	var warn func()
	out.rows, warn = d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
		k := keys[r]
		if r.ManagedByProvider || k.table == "" {
			return nil, nil
		}
		got, err := client.GetTable(ctx, &keyspaces.GetTableInput{KeyspaceName: &k.keyspace, TableName: &k.table})
		if err != nil {
			return nil, err
		}
		return got, nil
	})(out.rows)
	total, inserted, err := out.store(st, acct, region)
	if d.addPhase(p, op, total, inserted, err) {
		return nil, true
	}
	warn()
	arns := make(map[cassandraTableKey]string, len(out.rows))
	for _, r := range out.rows {
		arns[keys[r]] = r.NativeID
	}
	return arns, false
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
				arn := arnFor(acct.partition(), "cassandra", region, acct.ID, "keyspace/"+ksName+"/type/"+typeName)
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

// scanCassandraStreams stores the region's CDC streams with their tags, from
// one unfiltered ListStreams written a page at a time, and records each under
// the table it captures when the tables phase stored that table. The summary
// carries no status, so every listed stream with an ARN is stored unless its
// tag read finds it deleted (cassandraTags). On the first call,
// the shapes of an endpoint that does not serve the op (isOpNotDeployed) are
// a silent feature gap. None has been observed for this endpoint; the SDK
// builds it for every region with no per-region list, so a region that does
// not serve it would show only on the first call.
// An endpoint that does not resolve on the first call warns and stores nothing:
// unlike every other Keyspaces call it is on the dual-stack domain (api.aws in
// the aws partition), which egress filtering can block, so returning the
// NXDOMAIN would report the whole service unavailable in this region and drop
// the keyspace phases' rows. Any other error but a denial, a transient one
// included, is returned after the rows listed so far are stored. Counts and
// errors go to p; it reports whether a write failed.
func scanCassandraStreams(ctx context.Context, client cassandraStreamsAPI, tableARNs map[cassandraTableKey]string, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "cassandra:ListStreams"
	var unresolved error
	page := func(ctx context.Context, token *string) ([]kstypes.Stream, *string, error) {
		out, err := client.ListStreams(ctx, &keyspacesstreams.ListStreamsInput{NextToken: token, MaxResults: sdkaws.Int32(cassandraStreamsPageCap)})
		switch {
		case err == nil:
			return out.Streams, out.NextToken, nil
		case token == nil && isDNSNotFound(err):
			unresolved = err
			return nil, nil, nil
		case token == nil && isOpNotDeployed(err):
			return nil, nil, nil
		case isDNSNotFound(err):
			return nil, nil, lateNXDOMAIN("a later page", err)
		}
		return nil, nil, err
	}
	tableOf := map[*store.Resource]string{}
	var pairs [][2]string
	row := func(s kstypes.Stream) (*store.Resource, string, string) {
		r, why, key := cassandraRow(acct, region, scanID, TypeCassandraStream, "streams", s.StreamArn, s.StreamLabel, false, s)
		if r != nil {
			tableOf[r] = tableARNs[cassandraTableKey{sv(s.KeyspaceName), sv(s.TableName)}]
		}
		return r, why, key
	}
	stored := func(rows []*store.Resource) {
		for _, r := range rows {
			if t := tableOf[r]; t != "" {
				pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, t)})
			}
		}
		clear(tableOf)
	}
	if storeTokenPagesTagsWith(ctx, st, acct, region, op, page, row, tokenPageSteps{stored: stored}, tags, p) {
		return true
	}
	if unresolved != nil {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("CDC streams not scanned: the dual-stack endpoint cassandra-streams.%s does not resolve; DNS or egress filtering may block it, or the region may not offer it: %v", region, unresolved),
		})
	}
	return recordHierarchyPairs(st, op, pairs, p)
}
