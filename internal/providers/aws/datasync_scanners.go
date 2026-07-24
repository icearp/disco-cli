package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/datasync"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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

// dsDescribeLocation dispatches DescribeLocation* per disco type and returns
// the marshalled enriched response, or empty string on miss/error.
func dsDescribeLocation(ctx context.Context, client dataSyncAPI, dtype, arn string) (string, error) {
	a := arn
	switch dtype {
	case TypeDataSyncLocationS3:
		out, err := client.DescribeLocationS3(ctx, &datasync.DescribeLocationS3Input{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationEFS:
		out, err := client.DescribeLocationEfs(ctx, &datasync.DescribeLocationEfsInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationNFS:
		out, err := client.DescribeLocationNfs(ctx, &datasync.DescribeLocationNfsInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationSMB:
		out, err := client.DescribeLocationSmb(ctx, &datasync.DescribeLocationSmbInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationHDFS:
		out, err := client.DescribeLocationHdfs(ctx, &datasync.DescribeLocationHdfsInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationAzureBlob:
		out, err := client.DescribeLocationAzureBlob(ctx, &datasync.DescribeLocationAzureBlobInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationObjectStorage:
		out, err := client.DescribeLocationObjectStorage(ctx, &datasync.DescribeLocationObjectStorageInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationFSxLustre:
		out, err := client.DescribeLocationFsxLustre(ctx, &datasync.DescribeLocationFsxLustreInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationFSxONTAP:
		out, err := client.DescribeLocationFsxOntap(ctx, &datasync.DescribeLocationFsxOntapInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationFSxOpenZFS:
		out, err := client.DescribeLocationFsxOpenZfs(ctx, &datasync.DescribeLocationFsxOpenZfsInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	case TypeDataSyncLocationFSxWindows:
		out, err := client.DescribeLocationFsxWindows(ctx, &datasync.DescribeLocationFsxWindowsInput{LocationArn: &a})
		if err != nil {
			return "", err
		}
		return mustJSON(out), nil
	}
	return "", nil
}

func scanDataSync(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := datasync.NewFromConfig(acct.cfg, func(o *datasync.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDSAgents(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDSTasks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDSLocations(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanDSAgents(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := datasync.NewListAgentsPaginator(client, &datasync.ListAgentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "datasync:ListAgents", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("datasync:ListAgents: %w", perr)
		}
		for _, a := range out.Agents {
			arn := sv(a.AgentArn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = arn
			}
			attrsJSON := mustJSON(a)
			arnLocal := arn
			if dout, derr := client.DescribeAgent(ctx, &datasync.DescribeAgentInput{AgentArn: &arnLocal}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataSyncAgent, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "datasync agents")
}

func scanDSTasks(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := datasync.NewListTasksPaginator(client, &datasync.ListTasksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "datasync:ListTasks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("datasync:ListTasks: %w", perr)
		}
		for _, t := range out.Tasks {
			arn := sv(t.TaskArn)
			if arn == "" {
				continue
			}
			label := sv(t.Name)
			if label == "" {
				label = arn
			}
			attrsJSON := mustJSON(t)
			arnLocal := arn
			if dout, derr := client.DescribeTask(ctx, &datasync.DescribeTaskInput{TaskArn: &arnLocal}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataSyncTask, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "datasync tasks")
}

// dataSyncLocationType routes a LocationUri scheme prefix to a disco type.
// URI shape TYPE://GLOBAL_ID/SUBDIR; scheme distinguishes the 11 location
// subtypes CFN models as separate resources.
func dataSyncLocationType(uri string) string {
	scheme, _, ok := strings.Cut(strings.ToLower(uri), "://")
	if !ok {
		return ""
	}
	switch scheme {
	case "efs":
		return TypeDataSyncLocationEFS
	case "nfs":
		return TypeDataSyncLocationNFS
	case "smb":
		return TypeDataSyncLocationSMB
	case "s3":
		return TypeDataSyncLocationS3
	case "hdfs":
		return TypeDataSyncLocationHDFS
	case "azure-blob":
		return TypeDataSyncLocationAzureBlob
	case "object-storage":
		return TypeDataSyncLocationObjectStorage
	case "fsxl", "fsx-lustre":
		return TypeDataSyncLocationFSxLustre
	case "fsxn", "fsx-ontap":
		return TypeDataSyncLocationFSxONTAP
	case "fsxz", "fsx-openzfs":
		return TypeDataSyncLocationFSxOpenZFS
	case "fsxw", "fsx-windows":
		return TypeDataSyncLocationFSxWindows
	}
	return ""
}

func scanDSLocations(ctx context.Context, client dataSyncAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := datasync.NewListLocationsPaginator(client, &datasync.ListLocationsInput{})
	type locRef struct {
		arn, uri, dtype, label string
	}
	var refs []locRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "datasync:ListLocations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("datasync:ListLocations: %w", perr)
		}
		for _, l := range out.Locations {
			arn := sv(l.LocationArn)
			if arn == "" {
				continue
			}
			uri := sv(l.LocationUri)
			dtype := dataSyncLocationType(uri)
			if dtype == "" {
				continue
			}
			label := uri
			if label == "" {
				label = arn
			}
			refs = append(refs, locRef{arn: arn, uri: uri, dtype: dtype, label: label})
		}
	}
	// Per-row Describe enrichment under fanoutMed concurrency. Per-row
	// AccessDenied / not-found falls back to summary attrs (LocationListEntry).
	type result struct {
		ref   locRef
		attrs string
	}
	results := make([]result, len(refs))
	g, gctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(fanoutMed))
	for i, ref := range refs {
		i, ref := i, ref
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			attrs, derr := dsDescribeLocation(gctx, client, ref.dtype, ref.arn)
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "datasync:DescribeLocation*", acct.ID, region, derr)
					attrs = ""
				} else {
					return derr
				}
			}
			if attrs == "" {
				attrs = mustJSON(map[string]string{"LocationArn": ref.arn, "LocationUri": ref.uri})
			}
			results[i] = result{ref: ref, attrs: attrs}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, fmt.Errorf("datasync:DescribeLocation*: %w", err)
	}
	batch := make([]*store.Resource, 0, len(results))
	for _, r := range results {
		if r.ref.arn == "" {
			continue
		}
		label := r.ref.label
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: r.ref.dtype, NativeID: r.ref.arn,
			Name: &label, Region: &region, AttributesJSON: r.attrs, DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "datasync locations")
}
