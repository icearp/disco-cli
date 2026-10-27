package aws

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/odb"
	odbtypes "github.com/aws/aws-sdk-go-v2/service/odb/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isOdbNotOnboarded disambiguates the "isn't onboarded to the service"
// not-subscribed state from a real IAM denial. Oracle Database@AWS requires
// a Marketplace subscription before any list op succeeds.
func isOdbNotOnboarded(err error) bool {
	return isAccessDeniedWithMessage(err, "isn't onboarded to the service")
}

func init() {
	registerType(restype.Descriptor{Type: TypeODBCloudAutonomousVMCluster, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBCloudExadataInfrastructure, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBCloudVMCluster, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBOdbNetwork, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBOdbPeeringConnection, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBAutonomousDatabase, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBAutonomousDatabaseBackup, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBDbNode, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBDbServer, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBAutonomousVirtualMachine, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBExascaleDbStorageVault, Service: "odb"})
	registerType(restype.Descriptor{Type: TypeODBExadbVMCluster, Service: "odb"})
	registerService(serviceEntry{
		name: "aws:odb",
		fn:   scanODB,
	})
}

type odbAPI interface {
	ListCloudAutonomousVmClusters(context.Context, *odb.ListCloudAutonomousVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudAutonomousVmClustersOutput, error)
	ListCloudExadataInfrastructures(context.Context, *odb.ListCloudExadataInfrastructuresInput, ...func(*odb.Options)) (*odb.ListCloudExadataInfrastructuresOutput, error)
	ListCloudVmClusters(context.Context, *odb.ListCloudVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudVmClustersOutput, error)
	ListOdbNetworks(context.Context, *odb.ListOdbNetworksInput, ...func(*odb.Options)) (*odb.ListOdbNetworksOutput, error)
	ListOdbPeeringConnections(context.Context, *odb.ListOdbPeeringConnectionsInput, ...func(*odb.Options)) (*odb.ListOdbPeeringConnectionsOutput, error)
	ListAutonomousDatabases(context.Context, *odb.ListAutonomousDatabasesInput, ...func(*odb.Options)) (*odb.ListAutonomousDatabasesOutput, error)
	ListAutonomousDatabaseBackups(context.Context, *odb.ListAutonomousDatabaseBackupsInput, ...func(*odb.Options)) (*odb.ListAutonomousDatabaseBackupsOutput, error)
	ListDbNodes(context.Context, *odb.ListDbNodesInput, ...func(*odb.Options)) (*odb.ListDbNodesOutput, error)
	ListDbServers(context.Context, *odb.ListDbServersInput, ...func(*odb.Options)) (*odb.ListDbServersOutput, error)
	ListAutonomousVirtualMachines(context.Context, *odb.ListAutonomousVirtualMachinesInput, ...func(*odb.Options)) (*odb.ListAutonomousVirtualMachinesOutput, error)
	ListExascaleDbStorageVaults(context.Context, *odb.ListExascaleDbStorageVaultsInput, ...func(*odb.Options)) (*odb.ListExascaleDbStorageVaultsOutput, error)
	ListExadbVmClusters(context.Context, *odb.ListExadbVmClustersInput, ...func(*odb.Options)) (*odb.ListExadbVmClustersOutput, error)
	ListTagsForResource(context.Context, *odb.ListTagsForResourceInput, ...func(*odb.Options)) (*odb.ListTagsForResourceOutput, error)
}

// odbPageCap is the maxResults maximum the model's range trait sets on every
// ODB list op.
const odbPageCap int32 = 1000

// odbPeeringPageCap is what ListOdbPeeringConnections is sent: its doc says
// 1 to 100 while its range trait says 1000, and 100 satisfies both.
const odbPeeringPageCap int32 = 100

// odbNotFound is the code a per-parent listing or ListTagsForResource answers
// for a resource deleted since it was listed; the model lists it for both.
var odbNotFound = []string{"ResourceNotFoundException"}

// odbGate records an "isn't onboarded" answer. Met in any listing before any
// row was stored, it makes the scan report the service disabled (disabled),
// and no later listing runs. Met after rows were stored, onboarding changed
// mid-scan, so the listing ends there, keeping its rows, without an error:
// the sentinel would discard the stored phases' counts, and a warning would
// read as an IAM gap.
type odbGate struct {
	err      error
	disabled bool
}

// odbListPage reads one page with read and ends the listing, without an
// error, on an "isn't onboarded" answer, recorded in gate. With firstPageGap
// set, an op the region's endpoint does not route (isOpNotDeployed) is a
// silent gap on the first page, for the Exascale (ExaDB-XS) ops, which
// shipped after the rest of Oracle Database@AWS.
func odbListPage[Item any](gate *odbGate, firstPageGap bool, read func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := read(ctx, token)
		switch {
		case err == nil:
			return items, next, nil
		case isOdbNotOnboarded(err):
			if gate.err == nil {
				gate.err = err
			}
			return nil, nil, nil
		case firstPageGap && token == nil && isOpNotDeployed(err):
			return nil, nil, nil
		}
		return nil, nil, err
	}
}

// odbTags reads a row's tags with ListTagsForResource, sent the ARN arnOf
// returns for the row; a row it returns "" for is not read. The op returns
// every tag in one response. A denied read of a resource another account
// owns (its ARN's account is not acct's) is expected and keeps the row
// without tags and without a warning. The Service Reference gives
// cloud-exadata-infrastructure, exascale-db-storage-vault and odb-network
// resource-policy and grant-share actions (Get/PutResourcePolicy,
// Create/Update/DeleteGrantShare), the shape of resources AWS RAM shares, so
// those three can be listed from an account that does not own them.
func odbTags(client odbAPI, acct *account, arnOf func(*store.Resource) string) rowTagReader {
	return rowTagReader{
		op:       "odb:ListTagsForResource",
		notFound: odbNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := arnOf(r)
			if arn == "" {
				return nil, nil
			}
			out, err := client.ListTagsForResource(ctx, &odb.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				if a, ok := parseARN(arn); ok && a.AccountID != acct.ID && isAccessDenied(err) {
					return nil, nil
				}
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// odbNativeARN is the arnOf of the types whose NativeID is their ARN.
func odbNativeARN(r *store.Resource) string { return r.NativeID }

// scanODB discovers Oracle Database@AWS resources.
func scanODB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := odb.NewFromConfig(acct.cfg, func(o *odb.Options) { o.Region = region })
	return scanODBAll(ctx, client, acct, region, st, scanID)
}

// scanODBAll runs every ODB listing in turn, each per-parent listing after
// the listing of its parents, which hands it the parents it stored. A
// phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The phases run one at a time, so readEachKey's own
// bound is the bound on the reads in flight. An account not onboarded is
// reported disabled when a listing says so before any row was stored.
func scanODBAll(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		gate                               odbGate
		tags                               = odbTags(client, acct, odbNativeARN)
		avmClusters, exaInfras, vmClusters []childParent
		autonomousDBs                      []childParent
		storeWriteFailed                   bool
		phase                              = func(op string, run func(p *phaseTotals) bool) hostPhase {
			return hostPhase{op: op, run: func(p *phaseTotals) bool {
				if gate.disabled {
					return false
				}
				storeWriteFailed := run(p)
				gate.disabled = gate.err != nil && p.total == 0
				return storeWriteFailed
			}}
		}
	)
	phases := []hostPhase{
		phase("odb:ListCloudAutonomousVmClusters", func(p *phaseTotals) bool {
			avmClusters, storeWriteFailed = scanODBAutonomousVMClusters(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("odb:ListCloudExadataInfrastructures", func(p *phaseTotals) bool {
			exaInfras, storeWriteFailed = scanODBExadataInfras(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("odb:ListOdbNetworks", func(p *phaseTotals) bool {
			return scanODBNetworks(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("odb:ListOdbPeeringConnections", func(p *phaseTotals) bool {
			return scanODBPeeringConnections(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("odb:ListCloudVmClusters", func(p *phaseTotals) bool {
			vmClusters, storeWriteFailed = scanODBVmClusters(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("odb:ListDbNodes", func(p *phaseTotals) bool {
			return scanODBDbNodes(ctx, client, acct, region, st, scanID, vmClusters, p)
		}),
		phase("odb:ListAutonomousDatabases", func(p *phaseTotals) bool {
			autonomousDBs, storeWriteFailed = scanODBAutonomousDatabases(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("odb:ListAutonomousDatabaseBackups", func(p *phaseTotals) bool {
			return scanODBAutonomousDatabaseBackups(ctx, client, acct, region, st, scanID, autonomousDBs, tags, p)
		}),
		phase("odb:ListDbServers", func(p *phaseTotals) bool {
			t, i, err := scanODBDbServers(ctx, client, acct, region, st, scanID, exaInfras)
			return p.addPhase("odb:ListDbServers", t, i, err)
		}),
		phase("odb:ListAutonomousVirtualMachines", func(p *phaseTotals) bool {
			t, i, err := scanODBAutonomousVirtualMachines(ctx, client, acct, region, st, scanID, avmClusters)
			return p.addPhase("odb:ListAutonomousVirtualMachines", t, i, err)
		}),
		phase("odb:ListExascaleDbStorageVaults", func(p *phaseTotals) bool {
			return scanODBExascaleDbStorageVaults(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("odb:ListExadbVmClusters", func(p *phaseTotals) bool {
			return scanODBExadbVMClusters(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "odb", 0, phases)
	if gate.disabled {
		return total, inserted, markServiceDisabled(gate.err)
	}
	return total, inserted, err
}

func odbRow(acct *account, region, scanID, typ, nativeID string, name *string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// odbNoARN is the reason and key of a listed element not stored because it
// lacks the ARN its NativeID is; the model marks every summary's ARN optional.
func odbNoARN(what, id string) (*store.Resource, string, string) {
	return nil, what + " have no ARN", cmp.Or(id, "(an element with no id)")
}

func scanODBAutonomousVMClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "odb:ListCloudAutonomousVmClusters", "cloud autonomous VM clusters", "CloudAutonomousVmClusterId",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.CloudAutonomousVmClusterSummary, *string, error) {
			out, err := client.ListCloudAutonomousVmClusters(ctx, &odb.ListCloudAutonomousVmClustersInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudAutonomousVmClusters, out.NextToken, nil
		}),
		func(c odbtypes.CloudAutonomousVmClusterSummary) (*store.Resource, string, string) {
			arn := sv(c.CloudAutonomousVmClusterArn)
			if arn == "" {
				return odbNoARN("cloud autonomous VM clusters", sv(c.CloudAutonomousVmClusterId))
			}
			return odbRow(acct, region, scanID, TypeODBCloudAutonomousVMCluster, arn, c.DisplayName, c), "", ""
		},
		func(c odbtypes.CloudAutonomousVmClusterSummary) string { return sv(c.CloudAutonomousVmClusterId) },
		tags, p)
}

func scanODBExadataInfras(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "odb:ListCloudExadataInfrastructures", "cloud Exadata infrastructures", "CloudExadataInfrastructureId",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.CloudExadataInfrastructureSummary, *string, error) {
			out, err := client.ListCloudExadataInfrastructures(ctx, &odb.ListCloudExadataInfrastructuresInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudExadataInfrastructures, out.NextToken, nil
		}),
		func(c odbtypes.CloudExadataInfrastructureSummary) (*store.Resource, string, string) {
			arn := sv(c.CloudExadataInfrastructureArn)
			if arn == "" {
				return odbNoARN("cloud Exadata infrastructures", sv(c.CloudExadataInfrastructureId))
			}
			return odbRow(acct, region, scanID, TypeODBCloudExadataInfrastructure, arn, c.DisplayName, c), "", ""
		},
		func(c odbtypes.CloudExadataInfrastructureSummary) string { return sv(c.CloudExadataInfrastructureId) },
		tags, p)
}

// isODBParentGone reports a parent deleted between its list call and the
// per-parent child list.
func isODBParentGone(err error) bool {
	return isAPIErrorCode(err, odbNotFound...)
}

// scanODBDbServers lists the DB servers of each Exadata infrastructure. DB
// servers have no ARN (the Service Reference defines none), so NativeID is
// {infrastructureARN}/db-server/{dbServerId}, and ListTagsForResource, whose
// ARN pattern needs one, cannot read their tags.
func scanODBDbServers(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, infras []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "odb:ListDbServers", infras, isODBParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := odb.NewListDbServersPaginator(client, &odb.ListDbServersInput{CloudExadataInfrastructureId: &p.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.DbServers {
					id := sv(s.DbServerId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeODBDbServer, NativeID: p.arn + "/db-server/" + id,
						Name: s.DisplayName, Status: nonEmptyPtr(string(s.Status)), CreatedAt: tp(s.CreatedAt),
						Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanODBAutonomousVirtualMachines lists the virtual machines of each
// Autonomous VM cluster. They have no ARN (the Service Reference defines none),
// so NativeID is {clusterARN}/autonomous-virtual-machine/{autonomousVirtualMachineId},
// and ListTagsForResource, whose ARN pattern needs one, cannot read their tags.
func scanODBAutonomousVirtualMachines(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "odb:ListAutonomousVirtualMachines", clusters, isODBParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := odb.NewListAutonomousVirtualMachinesPaginator(client, &odb.ListAutonomousVirtualMachinesInput{CloudAutonomousVmClusterId: &p.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, vm := range out.AutonomousVirtualMachines {
					id := sv(vm.AutonomousVirtualMachineId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeODBAutonomousVirtualMachine, NativeID: p.arn + "/autonomous-virtual-machine/" + id,
						Name: vm.VmName, Status: nonEmptyPtr(string(vm.Status)),
						Region: &region, AttributesJSON: mustJSON(vm), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanODBVmClusters lists the cloud VM clusters and returns those stored,
// whose ids key the ListDbNodes fan-out.
func scanODBVmClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "odb:ListCloudVmClusters", "cloud VM clusters", "CloudVmClusterId",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.CloudVmClusterSummary, *string, error) {
			out, err := client.ListCloudVmClusters(ctx, &odb.ListCloudVmClustersInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CloudVmClusters, out.NextToken, nil
		}),
		func(c odbtypes.CloudVmClusterSummary) (*store.Resource, string, string) {
			arn := sv(c.CloudVmClusterArn)
			if arn == "" {
				return odbNoARN("cloud VM clusters", sv(c.CloudVmClusterId))
			}
			return odbRow(acct, region, scanID, TypeODBCloudVMCluster, arn, c.ClusterName, c), "", ""
		},
		func(c odbtypes.CloudVmClusterSummary) string { return sv(c.CloudVmClusterId) },
		tags, p)
}

// scanODBDbNodes lists the DB nodes of each cloud VM cluster stored this scan
// (the op requires a CloudVmClusterId). DB nodes carry no parent-cluster
// field, so NativeID is {clusterARN}/db-node/{dbNodeId} for the resolver to
// recover the parent, and the tag read is sent the node's own DbNodeArn. A
// node listed without one is stored without tags, with one warning.
func scanODBDbNodes(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) (storeWriteFailed bool) {
	const op = "odb:ListDbNodes"
	clusterARN := make(map[string]string, len(clusters))
	for _, c := range clusters {
		clusterARN[c.id] = c.arn
	}
	// Filled while the rows are built, before the tag reads start, and only
	// read by them.
	nodeARN := map[*store.Resource]string{}
	var noARN skipTally
	_, storeWriteFailed = storeEachParentTags(ctx, st, acct, region, op, odbNotFound, clusters,
		func(ctx context.Context, clusterID string, token *string) ([]odbtypes.DbNodeSummary, *string, error) {
			out, err := client.ListDbNodes(ctx, &odb.ListDbNodesInput{CloudVmClusterId: &clusterID, NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.DbNodes, out.NextToken, nil
		},
		func(clusterID string, n odbtypes.DbNodeSummary) (*store.Resource, string, string) {
			id := sv(n.DbNodeId)
			if id == "" {
				return nil, "DB nodes have no DbNodeId", cmp.Or(sv(n.DbNodeArn), sv(n.Hostname), "(a node with no ARN or hostname) of "+clusterID)
			}
			r := odbRow(acct, region, scanID, TypeODBDbNode, clusterARN[clusterID]+"/db-node/"+id, sdkaws.String(sv(n.Hostname)), n)
			if nodeARN[r] = sv(n.DbNodeArn); nodeARN[r] == "" {
				noARN.skipped(r.NativeID, nil)
			}
			return r, "", ""
		},
		odbTags(client, acct, func(r *store.Resource) string { return nodeARN[r] }), p)
	if !storeWriteFailed && noARN.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "odb:ListTagsForResource", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d DB node(s) stored without tags: listed without the DbNodeArn the tag read is sent; first %s", noARN.count, noARN.first),
		})
	}
	return storeWriteFailed
}

// scanODBAutonomousDatabases lists Autonomous Databases account-wide and returns
// those stored, whose ids key the backup fan-out.
func scanODBAutonomousDatabases(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "odb:ListAutonomousDatabases", "autonomous databases", "AutonomousDatabaseId",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.AutonomousDatabaseSummary, *string, error) {
			out, err := client.ListAutonomousDatabases(ctx, &odb.ListAutonomousDatabasesInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AutonomousDatabases, out.NextToken, nil
		}),
		func(d odbtypes.AutonomousDatabaseSummary) (*store.Resource, string, string) {
			arn := sv(d.AutonomousDatabaseArn)
			if arn == "" {
				return odbNoARN("autonomous databases", sv(d.AutonomousDatabaseId))
			}
			return odbRow(acct, region, scanID, TypeODBAutonomousDatabase, arn, d.DisplayName, d), "", ""
		},
		func(d odbtypes.AutonomousDatabaseSummary) string { return sv(d.AutonomousDatabaseId) },
		tags, p)
}

// scanODBAutonomousDatabaseBackups lists the backups of each Autonomous
// Database stored this scan (the op requires an AutonomousDatabaseId), one
// tag read per backup. Backups carry a real ARN; the resolver wires them to
// the parent via the AutonomousDatabaseId attribute.
func scanODBAutonomousDatabaseBackups(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, databases []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, storeWriteFailed = storeEachParentTags(ctx, st, acct, region, "odb:ListAutonomousDatabaseBackups", odbNotFound, databases,
		func(ctx context.Context, databaseID string, token *string) ([]odbtypes.AutonomousDatabaseBackupSummary, *string, error) {
			out, err := client.ListAutonomousDatabaseBackups(ctx, &odb.ListAutonomousDatabaseBackupsInput{AutonomousDatabaseId: &databaseID, NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AutonomousDatabaseBackups, out.NextToken, nil
		},
		func(_ string, b odbtypes.AutonomousDatabaseBackupSummary) (*store.Resource, string, string) {
			arn := sv(b.AutonomousDatabaseBackupArn)
			if arn == "" {
				return odbNoARN("autonomous database backups", sv(b.AutonomousDatabaseBackupId))
			}
			return odbRow(acct, region, scanID, TypeODBAutonomousDatabaseBackup, arn, b.DisplayName, b), "", ""
		},
		tags, p)
	return storeWriteFailed
}

func scanODBNetworks(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "odb:ListOdbNetworks",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.OdbNetworkSummary, *string, error) {
			out, err := client.ListOdbNetworks(ctx, &odb.ListOdbNetworksInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.OdbNetworks, out.NextToken, nil
		}),
		func(n odbtypes.OdbNetworkSummary) (*store.Resource, string, string) {
			arn := sv(n.OdbNetworkArn)
			if arn == "" {
				return odbNoARN("ODB networks", sv(n.OdbNetworkId))
			}
			return odbRow(acct, region, scanID, TypeODBOdbNetwork, arn, n.DisplayName, n), "", ""
		},
		tags, p)
}

func scanODBPeeringConnections(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "odb:ListOdbPeeringConnections",
		odbListPage(gate, false, func(ctx context.Context, token *string) ([]odbtypes.OdbPeeringConnectionSummary, *string, error) {
			out, err := client.ListOdbPeeringConnections(ctx, &odb.ListOdbPeeringConnectionsInput{NextToken: token, MaxResults: sdkaws.Int32(odbPeeringPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.OdbPeeringConnections, out.NextToken, nil
		}),
		func(c odbtypes.OdbPeeringConnectionSummary) (*store.Resource, string, string) {
			arn := sv(c.OdbPeeringConnectionArn)
			if arn == "" {
				return odbNoARN("ODB peering connections", sv(c.OdbPeeringConnectionId))
			}
			return odbRow(acct, region, scanID, TypeODBOdbPeeringConnection, arn, c.DisplayName, c), "", ""
		},
		tags, p)
}

// odbTerminated reports an Exascale element whose TERMINATED status marks it
// deleted; TERMINATING is kept.
func odbTerminated(s odbtypes.ResourceStatus) bool { return s == odbtypes.ResourceStatusTerminated }

// scanODBExascaleDbStorageVaults lists Exascale storage vaults region-wide,
// dropping the terminated ones.
func scanODBExascaleDbStorageVaults(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "odb:ListExascaleDbStorageVaults",
		odbListPage(gate, true, func(ctx context.Context, token *string) ([]odbtypes.ExascaleDbStorageVaultSummary, *string, error) {
			out, err := client.ListExascaleDbStorageVaults(ctx, &odb.ListExascaleDbStorageVaultsInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(out.ExascaleDbStorageVaults, func(v odbtypes.ExascaleDbStorageVaultSummary) bool { return odbTerminated(v.Status) }), out.NextToken, nil
		}),
		func(v odbtypes.ExascaleDbStorageVaultSummary) (*store.Resource, string, string) {
			arn := sv(v.ExascaleDbStorageVaultArn)
			if arn == "" {
				return odbNoARN("Exascale DB storage vaults", sv(v.ExascaleDbStorageVaultId))
			}
			r := odbRow(acct, region, scanID, TypeODBExascaleDbStorageVault, arn, v.DisplayName, v)
			r.Status, r.CreatedAt = nonEmptyPtr(string(v.Status)), tp(v.CreatedAt)
			return r, "", ""
		},
		tags, p)
}

// scanODBExadbVMClusters lists Exascale VM clusters region-wide (the optional
// storage-vault filter is left unset), dropping the terminated ones.
func scanODBExadbVMClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, gate *odbGate, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "odb:ListExadbVmClusters",
		odbListPage(gate, true, func(ctx context.Context, token *string) ([]odbtypes.ExadbVmClusterSummary, *string, error) {
			out, err := client.ListExadbVmClusters(ctx, &odb.ListExadbVmClustersInput{NextToken: token, MaxResults: sdkaws.Int32(odbPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(out.ExadbVmClusters, func(c odbtypes.ExadbVmClusterSummary) bool { return odbTerminated(c.Status) }), out.NextToken, nil
		}),
		func(c odbtypes.ExadbVmClusterSummary) (*store.Resource, string, string) {
			arn := sv(c.ExadbVmClusterArn)
			if arn == "" {
				return odbNoARN("Exascale VM clusters", sv(c.ExadbVmClusterId))
			}
			r := odbRow(acct, region, scanID, TypeODBExadbVMCluster, arn, c.DisplayName, c)
			r.Status, r.CreatedAt = nonEmptyPtr(string(c.Status)), tp(c.CreatedAt)
			return r, "", ""
		},
		tags, p)
}
