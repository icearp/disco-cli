package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/datasync"
)

func init() {
	registerService(serviceEntry{
		name: "aws:datasync",
		fn:   scanDataSync,
		emits: []coverage.TypeDecl{
			{Service: "datasync", DiscoType: TypeDataSyncAgent},
			{Service: "datasync", DiscoType: TypeDataSyncTask},
			{Service: "datasync", DiscoType: TypeDataSyncLocationEFS},
			{Service: "datasync", DiscoType: TypeDataSyncLocationNFS},
			{Service: "datasync", DiscoType: TypeDataSyncLocationSMB},
			{Service: "datasync", DiscoType: TypeDataSyncLocationS3},
			{Service: "datasync", DiscoType: TypeDataSyncLocationHDFS},
			{Service: "datasync", DiscoType: TypeDataSyncLocationAzureBlob},
			{Service: "datasync", DiscoType: TypeDataSyncLocationObjectStorage},
			{Service: "datasync", DiscoType: TypeDataSyncLocationFSxLustre},
			{Service: "datasync", DiscoType: TypeDataSyncLocationFSxONTAP},
			{Service: "datasync", DiscoType: TypeDataSyncLocationFSxOpenZFS},
			{Service: "datasync", DiscoType: TypeDataSyncLocationFSxWindows},
		},
	})
}

type dataSyncAPI interface {
	ListAgents(context.Context, *datasync.ListAgentsInput, ...func(*datasync.Options)) (*datasync.ListAgentsOutput, error)
	ListTasks(context.Context, *datasync.ListTasksInput, ...func(*datasync.Options)) (*datasync.ListTasksOutput, error)
	ListLocations(context.Context, *datasync.ListLocationsInput, ...func(*datasync.Options)) (*datasync.ListLocationsOutput, error)
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataSyncAgent, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataSyncTask, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "datasync tasks")
}

// dataSyncLocationType routes a LocationUri scheme prefix to a disco type.
// URI shape: TYPE://GLOBAL_ID/SUBDIR — scheme distinguishes 11 location
// subtypes that CFN models as separate resources.
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
	var batch []*store.Resource
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dtype, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "datasync locations")
}
