package aws

import (
	"cmp"
	"context"
	"errors"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/datasync"
	"github.com/aws/aws-sdk-go-v2/service/datasync/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataSyncAgent, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncTask, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationEFS, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationNFS, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationSMB, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationS3, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationHDFS, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationAzureBlob, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationObjectStorage, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationFSxLustre, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationFSxONTAP, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationFSxOpenZFS, Service: "datasync"})
	registerType(restype.Descriptor{Type: TypeDataSyncLocationFSxWindows, Service: "datasync"})
	registerService(serviceEntry{
		name: "aws:datasync",
		fn:   scanDataSync,
	})
}

type dataSyncAPI interface {
	ListAgents(context.Context, *datasync.ListAgentsInput, ...func(*datasync.Options)) (*datasync.ListAgentsOutput, error)
	ListTasks(context.Context, *datasync.ListTasksInput, ...func(*datasync.Options)) (*datasync.ListTasksOutput, error)
	ListLocations(context.Context, *datasync.ListLocationsInput, ...func(*datasync.Options)) (*datasync.ListLocationsOutput, error)
	ListTagsForResource(context.Context, *datasync.ListTagsForResourceInput, ...func(*datasync.Options)) (*datasync.ListTagsForResourceOutput, error)
	DescribeLocationS3(context.Context, *datasync.DescribeLocationS3Input, ...func(*datasync.Options)) (*datasync.DescribeLocationS3Output, error)
	DescribeLocationEfs(context.Context, *datasync.DescribeLocationEfsInput, ...func(*datasync.Options)) (*datasync.DescribeLocationEfsOutput, error)
	DescribeLocationNfs(context.Context, *datasync.DescribeLocationNfsInput, ...func(*datasync.Options)) (*datasync.DescribeLocationNfsOutput, error)
	DescribeLocationSmb(context.Context, *datasync.DescribeLocationSmbInput, ...func(*datasync.Options)) (*datasync.DescribeLocationSmbOutput, error)
	DescribeLocationHdfs(context.Context, *datasync.DescribeLocationHdfsInput, ...func(*datasync.Options)) (*datasync.DescribeLocationHdfsOutput, error)
	DescribeLocationAzureBlob(context.Context, *datasync.DescribeLocationAzureBlobInput, ...func(*datasync.Options)) (*datasync.DescribeLocationAzureBlobOutput, error)
	DescribeLocationObjectStorage(context.Context, *datasync.DescribeLocationObjectStorageInput, ...func(*datasync.Options)) (*datasync.DescribeLocationObjectStorageOutput, error)
	DescribeLocationFsxLustre(context.Context, *datasync.DescribeLocationFsxLustreInput, ...func(*datasync.Options)) (*datasync.DescribeLocationFsxLustreOutput, error)
	DescribeLocationFsxOntap(context.Context, *datasync.DescribeLocationFsxOntapInput, ...func(*datasync.Options)) (*datasync.DescribeLocationFsxOntapOutput, error)
	DescribeLocationFsxOpenZfs(context.Context, *datasync.DescribeLocationFsxOpenZfsInput, ...func(*datasync.Options)) (*datasync.DescribeLocationFsxOpenZfsOutput, error)
	DescribeLocationFsxWindows(context.Context, *datasync.DescribeLocationFsxWindowsInput, ...func(*datasync.Options)) (*datasync.DescribeLocationFsxWindowsOutput, error)
	DescribeAgent(context.Context, *datasync.DescribeAgentInput, ...func(*datasync.Options)) (*datasync.DescribeAgentOutput, error)
	DescribeTask(context.Context, *datasync.DescribeTaskInput, ...func(*datasync.Options)) (*datasync.DescribeTaskOutput, error)
}

// dataSyncPageCap is the MaxResults cap (0-100) the model documents for every
// DataSync listing, ListTagsForResource included.
const dataSyncPageCap int32 = 100

// Every DataSync op models only InvalidRequestException ("the client submits
// a malformed request") and InternalException, so none is read as not found
// (decision #14): a resource deleted between its listing and a Describe or
// tag read is kept, as its list element or without tags, and the error is
// returned.

// dataSyncTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the agent, task or location ARN the listing returned, whose kinds
// the op's TaggableResourceArn pattern accepts. The op pages its tags, so
// every page is read; a failed page fails the read.
func dataSyncTags(client dataSyncAPI) rowTagReader {
	return rowTagReader{
		op: "datasync:ListTagsForResource",
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]types.TagListEntry, *string, error) {
				out, err := client.ListTagsForResource(ctx, &datasync.ListTagsForResourceInput{
					ResourceArn: sdkaws.String(r.NativeID), NextToken: token, MaxResults: sdkaws.Int32(dataSyncPageCap),
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t types.TagListEntry) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

func scanDataSync(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := datasync.NewFromConfig(acct.cfg, func(o *datasync.Options) { o.Region = region })
	return scanDataSyncAll(ctx, client, acct, region, st, scanID)
}

// scanDataSyncAll discovers DataSync agents, tasks and locations in one
// region, running its phases one after another. A phase's error does not stop
// the later ones; a failed write does, since every later write would fail
// too, and so does ctx's end, reported as one error. The phases run one at a
// time, so readEachKey's own bound is the bound on the Describe and tag reads
// in flight. Every op calls the one DataSync endpoint, so an NXDOMAIN after
// rows were stored is a failure of this scan, not a region gap
// (resultLateNXDOMAIN).
func scanDataSyncAll(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p    phaseTotals
		tags = dataSyncTags(client)
	)
	phases := []func() bool{
		func() bool { return scanDSAgents(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanDSTasks(ctx, client, acct, region, st, scanID, tags, &p) },
		func() bool { return scanDSLocations(ctx, client, acct, region, st, scanID, tags, &p) },
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
	p.foldStop(ctx, "datasync", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

func dataSyncRow(acct *account, region, scanID, typ, arn, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// scanDSAgents stores the region's agents, each as its DescribeAgent body
// with its tags; an agent DescribeAgent fails for is stored as its list
// element (readRowDetails).
func scanDSAgents(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "datasync:ListAgents"
	d := &detailedRows{getOp: "datasync:DescribeAgent", what: "agent(s)", tags: tags}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		func(ctx context.Context, token *string) ([]types.AgentListEntry, *string, error) {
			out, err := client.ListAgents(ctx, &datasync.ListAgentsInput{NextToken: token, MaxResults: sdkaws.Int32(dataSyncPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Agents, out.NextToken, nil
		},
		func(a types.AgentListEntry) (*store.Resource, string, string) {
			arn := sv(a.AgentArn)
			if arn == "" {
				return nil, "agents have an empty AgentArn", cmp.Or(sv(a.Name), "(an agent with no name)")
			}
			return dataSyncRow(acct, region, scanID, TypeDataSyncAgent, arn, cmp.Or(sv(a.Name), arn), a), "", ""
		},
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			return client.DescribeAgent(ctx, &datasync.DescribeAgentInput{AgentArn: sdkaws.String(r.NativeID)})
		}))
	return d.addPhase(p, op, total, inserted, err)
}

// scanDSTasks stores the region's tasks, each as its DescribeTask body with
// its tags; a task DescribeTask fails for is stored as its list element
// (readRowDetails).
func scanDSTasks(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "datasync:ListTasks"
	d := &detailedRows{getOp: "datasync:DescribeTask", what: "task(s)", tags: tags}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		func(ctx context.Context, token *string) ([]types.TaskListEntry, *string, error) {
			out, err := client.ListTasks(ctx, &datasync.ListTasksInput{NextToken: token, MaxResults: sdkaws.Int32(dataSyncPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Tasks, out.NextToken, nil
		},
		func(t types.TaskListEntry) (*store.Resource, string, string) {
			arn := sv(t.TaskArn)
			if arn == "" {
				return nil, "tasks have an empty TaskArn", cmp.Or(sv(t.Name), "(a task with no name)")
			}
			return dataSyncRow(acct, region, scanID, TypeDataSyncTask, arn, cmp.Or(sv(t.Name), arn), t), "", ""
		},
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			return client.DescribeTask(ctx, &datasync.DescribeTaskInput{TaskArn: sdkaws.String(r.NativeID)})
		}))
	return d.addPhase(p, op, total, inserted, err)
}

// dsLocationKind is one kind of DataSync location: its type and the
// Describe op that reads it, sent the location ARN.
type dsLocationKind struct {
	typ, op  string
	describe func(ctx context.Context, arn string) (any, error)
}

// dsLocationKindOf routes a LocationUri (TYPE://GLOBAL_ID/SUBDIR) by its
// scheme to the location's kind; false for a scheme no kind maps.
func dsLocationKindOf(client dataSyncAPI, uri string) (dsLocationKind, bool) {
	scheme, _, ok := strings.Cut(strings.ToLower(uri), "://")
	if !ok {
		return dsLocationKind{}, false
	}
	switch scheme {
	case "efs":
		return dsLocationKind{TypeDataSyncLocationEFS, "datasync:DescribeLocationEfs", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationEfs(ctx, &datasync.DescribeLocationEfsInput{LocationArn: &arn})
		}}, true
	case "nfs":
		return dsLocationKind{TypeDataSyncLocationNFS, "datasync:DescribeLocationNfs", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationNfs(ctx, &datasync.DescribeLocationNfsInput{LocationArn: &arn})
		}}, true
	case "smb":
		return dsLocationKind{TypeDataSyncLocationSMB, "datasync:DescribeLocationSmb", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationSmb(ctx, &datasync.DescribeLocationSmbInput{LocationArn: &arn})
		}}, true
	case "s3":
		return dsLocationKind{TypeDataSyncLocationS3, "datasync:DescribeLocationS3", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationS3(ctx, &datasync.DescribeLocationS3Input{LocationArn: &arn})
		}}, true
	case "hdfs":
		return dsLocationKind{TypeDataSyncLocationHDFS, "datasync:DescribeLocationHdfs", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationHdfs(ctx, &datasync.DescribeLocationHdfsInput{LocationArn: &arn})
		}}, true
	case "azure-blob":
		return dsLocationKind{TypeDataSyncLocationAzureBlob, "datasync:DescribeLocationAzureBlob", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationAzureBlob(ctx, &datasync.DescribeLocationAzureBlobInput{LocationArn: &arn})
		}}, true
	case "object-storage":
		return dsLocationKind{TypeDataSyncLocationObjectStorage, "datasync:DescribeLocationObjectStorage", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationObjectStorage(ctx, &datasync.DescribeLocationObjectStorageInput{LocationArn: &arn})
		}}, true
	case "fsxl", "fsx-lustre":
		return dsLocationKind{TypeDataSyncLocationFSxLustre, "datasync:DescribeLocationFsxLustre", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationFsxLustre(ctx, &datasync.DescribeLocationFsxLustreInput{LocationArn: &arn})
		}}, true
	case "fsxn", "fsx-ontap":
		return dsLocationKind{TypeDataSyncLocationFSxONTAP, "datasync:DescribeLocationFsxOntap", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationFsxOntap(ctx, &datasync.DescribeLocationFsxOntapInput{LocationArn: &arn})
		}}, true
	case "fsxz", "fsx-openzfs":
		return dsLocationKind{TypeDataSyncLocationFSxOpenZFS, "datasync:DescribeLocationFsxOpenZfs", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationFsxOpenZfs(ctx, &datasync.DescribeLocationFsxOpenZfsInput{LocationArn: &arn})
		}}, true
	case "fsxw", "fsx-windows":
		return dsLocationKind{TypeDataSyncLocationFSxWindows, "datasync:DescribeLocationFsxWindows", func(ctx context.Context, arn string) (any, error) {
			return client.DescribeLocationFsxWindows(ctx, &datasync.DescribeLocationFsxWindowsInput{LocationArn: &arn})
		}}, true
	}
	return dsLocationKind{}, false
}

// dsLocationReads replaces each location row with its kind's Describe body
// (readRowDetails, one read per kind, so a failure is reported under the op
// that failed), then reads every row's tags (readRowTags), and holds both
// reads' errors until the write is done.
type dsLocationReads struct {
	kinds   map[*store.Resource]dsLocationKind
	tags    rowTagReader
	details []struct {
		op   string
		errs rowTagErrs
	}
	tagErrs rowTagErrs
}

// before is the listing's beforeWrite. Kinds are read in the order the
// listing first returned them.
func (d *dsLocationReads) before(ctx context.Context, st *store.Store, acct *account, region string) beforeWrite {
	return func(rows []*store.Resource) ([]*store.Resource, func()) {
		var (
			ops    []string
			byOp   = map[string][]*store.Resource{}
			kept   []*store.Resource
			warns  []func()
			detail = func(ctx context.Context, r *store.Resource) (any, error) {
				return d.kinds[r].describe(ctx, r.NativeID)
			}
		)
		for _, r := range rows {
			op := d.kinds[r].op
			if _, seen := byOp[op]; !seen {
				ops = append(ops, op)
			}
			byOp[op] = append(byOp[op], r)
		}
		// One kind at a time, so each failure is reported under its own op;
		// the cost is one fan-out per kind present.
		for _, op := range ops {
			got, warn, hard, transient := readRowDetails(ctx, st, acct, region, op, "location(s)", nil, byOp[op], detail)
			d.details = append(d.details, struct {
				op   string
				errs rowTagErrs
			}{op, rowTagErrs{hard, transient}})
			kept = append(kept, got...)
			warns = append(warns, warn)
		}
		kept, tagWarn := d.tags.hook(ctx, st, acct, region, &d.tagErrs)(kept)
		return kept, func() {
			for _, w := range warns {
				w()
			}
			tagWarn()
		}
	}
}

// addPhase adds the listing's counts and err to p under op, and the reads'
// errors under their ops, as detailedRows.addPhase does. It reports whether
// the write failed, after which the scanner must stop.
func (d *dsLocationReads) addPhase(p *phaseTotals, op string, total, inserted int, err error) (storeWriteFailed bool) {
	for _, det := range d.details {
		if errors.Is(err, store.ErrStoreWrite) {
			err = errors.Join(err, det.errs.hard, det.errs.transient)
			continue
		}
		p.errs.add(det.op, det.errs.hard)
		p.errs.add(det.op, det.errs.transient)
	}
	return d.tagErrs.addPhase(p, d.tags.op, op, total, inserted, err)
}

// scanDSLocations stores the region's locations, each typed by its URI
// scheme and stored as its kind's Describe body with its tags; a location
// whose Describe fails is stored as its list element (readRowDetails). A
// location whose scheme maps to no type is warned about, not stored.
func scanDSLocations(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "datasync:ListLocations"
	d := &dsLocationReads{kinds: map[*store.Resource]dsLocationKind{}, tags: tags}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		func(ctx context.Context, token *string) ([]types.LocationListEntry, *string, error) {
			out, err := client.ListLocations(ctx, &datasync.ListLocationsInput{NextToken: token, MaxResults: sdkaws.Int32(dataSyncPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Locations, out.NextToken, nil
		},
		func(l types.LocationListEntry) (*store.Resource, string, string) {
			arn, uri := sv(l.LocationArn), sv(l.LocationUri)
			if arn == "" {
				return nil, "locations have an empty LocationArn", cmp.Or(uri, "(a location with no URI)")
			}
			kind, ok := dsLocationKindOf(client, uri)
			if !ok {
				return nil, "locations have a LocationUri scheme no location type maps", arn
			}
			r := dataSyncRow(acct, region, scanID, kind.typ, arn, uri, l)
			d.kinds[r] = kind
			return r, "", ""
		},
		d.before(ctx, st, acct, region))
	return d.addPhase(p, op, total, inserted, err)
}
