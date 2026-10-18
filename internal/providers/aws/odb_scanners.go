package aws

import (
	"context"
	"fmt"

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
}

// scanODB discovers Oracle Database@AWS resources.
func scanODB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := odb.NewFromConfig(acct.cfg, func(o *odb.Options) { o.Region = region })

	// Autonomous VM clusters and Exadata infrastructures seed the per-parent
	// fan-outs that run after every other phase.
	var avmClusters, exaInfras []childParent
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			avmClusters, t, i, err = scanODBAutonomousVMClusters(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			exaInfras, t, i, err = scanODBExadataInfras(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanODBNetworks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBPeeringConnections(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Cloud VM clusters first — their ARNs seed the per-cluster ListDbNodes fan-out.
	vmClusters, t, i, ferr := scanODBVmClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanODBDbNodes(ctx, client, acct, region, st, scanID, vmClusters)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Autonomous databases first — their IDs seed the per-database backup fan-out.
	adbIDs, t, i, ferr := scanODBAutonomousDatabases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanODBAutonomousDatabaseBackups(ctx, client, acct, region, st, scanID, adbIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanODBDbServers(ctx, client, acct, region, st, scanID, exaInfras) },
		func() (int, int, error) {
			return scanODBAutonomousVirtualMachines(ctx, client, acct, region, st, scanID, avmClusters)
		},
		func() (int, int, error) { return scanODBExascaleDbStorageVaults(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBExadbVMClusters(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanODBAutonomousVMClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := odb.NewListCloudAutonomousVmClustersPaginator(client, &odb.ListCloudAutonomousVmClustersInput{})
	var batch []*store.Resource
	var parents []childParent
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListCloudAutonomousVmClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListCloudAutonomousVmClusters: %w", err)
		}
		for _, c := range out.CloudAutonomousVmClusters {
			arn := sv(c.CloudAutonomousVmClusterArn)
			if arn == "" {
				continue
			}
			if id := sv(c.CloudAutonomousVmClusterId); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudAutonomousVMCluster, NativeID: arn,
				Name: c.DisplayName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb cloud-autonomous-vm-clusters")
	return parents, t, i, err
}

func scanODBExadataInfras(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := odb.NewListCloudExadataInfrastructuresPaginator(client, &odb.ListCloudExadataInfrastructuresInput{})
	var batch []*store.Resource
	var parents []childParent
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListCloudExadataInfrastructures", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListCloudExadataInfrastructures: %w", err)
		}
		for _, c := range out.CloudExadataInfrastructures {
			arn := sv(c.CloudExadataInfrastructureArn)
			if arn == "" {
				continue
			}
			if id := sv(c.CloudExadataInfrastructureId); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudExadataInfrastructure, NativeID: arn,
				Name: c.DisplayName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb cloud-exadata-infrastructures")
	return parents, t, i, err
}

// isODBParentGone reports a parent deleted between its list call and the
// per-parent child list.
func isODBParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanODBDbServers lists the DB servers of each Exadata infrastructure. DB
// servers have no ARN (the Service Reference defines none), so NativeID is
// {infrastructureARN}/db-server/{dbServerId}.
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
// so NativeID is {clusterARN}/autonomous-virtual-machine/{autonomousVirtualMachineId}.
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

// odbVMCluster carries the (id, arn) pair the ListDbNodes fan-out needs: id
// satisfies the required CloudVmClusterId input; arn seeds the synthetic
// db-node NativeID so the resolver can recover the parent cluster.
type odbVMCluster struct {
	id  string
	arn string
}

func scanODBVmClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]odbVMCluster, int, int, error) {
	pager := odb.NewListCloudVmClustersPaginator(client, &odb.ListCloudVmClustersInput{})
	var batch []*store.Resource
	var clusters []odbVMCluster
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListCloudVmClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListCloudVmClusters: %w", err)
		}
		for _, c := range out.CloudVmClusters {
			arn := sv(c.CloudVmClusterArn)
			if arn == "" {
				continue
			}
			if id := sv(c.CloudVmClusterId); id != "" {
				clusters = append(clusters, odbVMCluster{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudVMCluster, NativeID: arn,
				Name: c.ClusterName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb cloud-vm-clusters")
	return clusters, t, i, err
}

// scanODBDbNodes fans out ListDbNodes per VM cluster (op requires a
// CloudVmClusterId). DB nodes carry a real ARN but no parent-cluster field, so
// NativeID is synthesised as {clusterARN}/db-node/{dbNodeId} for the resolver
// to recover the parent.
func scanODBDbNodes(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, clusters []odbVMCluster) (int, int, error) {
	var batch []*store.Resource
	for _, cl := range clusters {
		cid := cl.id
		pager := odb.NewListDbNodesPaginator(client, &odb.ListDbNodesInput{CloudVmClusterId: &cid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "odb:ListDbNodes", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("odb:ListDbNodes %s: %w", cid, err)
			}
			for _, n := range out.DbNodes {
				id := sv(n.DbNodeId)
				if id == "" {
					continue
				}
				name := sv(n.Hostname)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeODBDbNode, NativeID: cl.arn + "/db-node/" + id,
					Name: &name, Region: &region,
					AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "odb db-nodes")
}

// scanODBAutonomousDatabases lists Autonomous Databases account-wide and returns
// their ids for the per-database backup fan-out.
func scanODBAutonomousDatabases(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := odb.NewListAutonomousDatabasesPaginator(client, &odb.ListAutonomousDatabasesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListAutonomousDatabases", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListAutonomousDatabases: %w", err)
		}
		for _, d := range out.AutonomousDatabases {
			arn := sv(d.AutonomousDatabaseArn)
			if arn == "" {
				continue
			}
			if id := sv(d.AutonomousDatabaseId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBAutonomousDatabase, NativeID: arn,
				Name: d.DisplayName, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb autonomous-databases")
	return ids, t, i, err
}

// scanODBAutonomousDatabaseBackups fans out ListAutonomousDatabaseBackups per
// Autonomous Database (the op requires an AutonomousDatabaseId). Backups carry a
// real ARN; the resolver wires them to the parent via the AutonomousDatabaseId
// attribute.
func scanODBAutonomousDatabaseBackups(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, adbIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, id := range adbIDs {
		aid := id
		pager := odb.NewListAutonomousDatabaseBackupsPaginator(client, &odb.ListAutonomousDatabaseBackupsInput{AutonomousDatabaseId: &aid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "odb:ListAutonomousDatabaseBackups", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("odb:ListAutonomousDatabaseBackups %s: %w", aid, err)
			}
			for _, b := range out.AutonomousDatabaseBackups {
				arn := sv(b.AutonomousDatabaseBackupArn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeODBAutonomousDatabaseBackup, NativeID: arn,
					Name: b.DisplayName, Region: &region,
					AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "odb autonomous-database-backups")
}

func scanODBNetworks(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListOdbNetworksPaginator(client, &odb.ListOdbNetworksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListOdbNetworks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListOdbNetworks: %w", err)
		}
		for _, n := range out.OdbNetworks {
			arn := sv(n.OdbNetworkArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBOdbNetwork, NativeID: arn,
				Name: n.DisplayName, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb odb-networks")
}

func scanODBPeeringConnections(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListOdbPeeringConnectionsPaginator(client, &odb.ListOdbPeeringConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListOdbPeeringConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListOdbPeeringConnections: %w", err)
		}
		for _, p := range out.OdbPeeringConnections {
			arn := sv(p.OdbPeeringConnectionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBOdbPeeringConnection, NativeID: arn,
				Name: p.DisplayName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb odb-peering-connections")
}

// odbExascaleListCap is the maxResults range maximum the model sets on both
// Exascale list ops.
const odbExascaleListCap = 1000

// odbMissingARN counts listed elements skipped because they lack the ARN their
// NativeID is; the model marks the ARN optional on both Exascale summaries.
type odbMissingARN struct {
	count   int
	firstID string
}

func (m *odbMissingARN) add(id string) {
	if m.count == 0 {
		m.firstID = id
	}
	m.count++
}

func (m *odbMissingARN) warn(st *store.Store, op, kind string, acct *account, region string) {
	if m.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s skipped: listed without the ARN disco keys them by, first id %q", m.count, kind, m.firstID),
	})
}

// odbExascaleListErr classifies a failed page of a region-wide Exascale list
// op. The earlier ODB phases turn the not-onboarded state into the disabled
// sentinel before any row is written; reaching it here means onboarding changed
// mid-scan, after earlier phases stored rows, so it is skipped silently with the
// rows listed so far kept (a sentinel would discard those phases' counts, and a
// warning would read as an IAM gap). Exascale (ExaDB-XS) shipped after the rest of
// Oracle Database@AWS, so a region's endpoint may not route the op: on the
// first page that is a silent gap. A denial warns, and the rows already listed
// are kept (keep). Every other error is returned and the listing's rows are
// dropped.
func odbExascaleListErr(st *store.Store, op string, acct *account, region string, firstPage bool, err error) (keep bool, _ error) {
	if isOdbNotOnboarded(err) {
		return true, nil
	}
	if firstPage && isOpNotDeployed(err) {
		return false, nil
	}
	if isAccessDenied(err) {
		return true, skipIfAccessDenied(st, op, acct.ID, region, err)
	}
	return false, fmt.Errorf("%s: %w", op, err)
}

// scanODBExascaleDbStorageVaults lists Exascale storage vaults region-wide.
// TERMINATED marks a deleted vault and is dropped; TERMINATING is kept.
func scanODBExascaleDbStorageVaults(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "odb:ListExascaleDbStorageVaults"
	pager := odb.NewListExascaleDbStorageVaultsPaginator(client, &odb.ListExascaleDbStorageVaultsInput{},
		func(o *odb.ListExascaleDbStorageVaultsPaginatorOptions) {
			o.Limit = odbExascaleListCap
			o.StopOnDuplicateToken = true
		})
	var batch []*store.Resource
	var missing odbMissingARN
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			keep, lerr := odbExascaleListErr(st, op, acct, region, firstPage, err)
			if lerr != nil || !keep {
				return 0, 0, lerr
			}
			break
		}
		for _, v := range out.ExascaleDbStorageVaults {
			if v.Status == odbtypes.ResourceStatusTerminated {
				continue
			}
			arn := sv(v.ExascaleDbStorageVaultArn)
			if arn == "" {
				missing.add(sv(v.ExascaleDbStorageVaultId))
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBExascaleDbStorageVault, NativeID: arn,
				Name: v.DisplayName, Status: nonEmptyPtr(string(v.Status)), CreatedAt: tp(v.CreatedAt),
				Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	missing.warn(st, op, "exascale db storage vaults", acct, region)
	return upsertBatch(st, batch, "odb exascale-db-storage-vaults")
}

// scanODBExadbVMClusters lists Exascale VM clusters region-wide (the optional
// storage-vault filter is left unset). TERMINATED marks a deleted cluster and
// is dropped; TERMINATING is kept.
func scanODBExadbVMClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "odb:ListExadbVmClusters"
	pager := odb.NewListExadbVmClustersPaginator(client, &odb.ListExadbVmClustersInput{},
		func(o *odb.ListExadbVmClustersPaginatorOptions) {
			o.Limit = odbExascaleListCap
			o.StopOnDuplicateToken = true
		})
	var batch []*store.Resource
	var missing odbMissingARN
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			keep, lerr := odbExascaleListErr(st, op, acct, region, firstPage, err)
			if lerr != nil || !keep {
				return 0, 0, lerr
			}
			break
		}
		for _, c := range out.ExadbVmClusters {
			if c.Status == odbtypes.ResourceStatusTerminated {
				continue
			}
			arn := sv(c.ExadbVmClusterArn)
			if arn == "" {
				missing.add(sv(c.ExadbVmClusterId))
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBExadbVMCluster, NativeID: arn,
				Name: c.DisplayName, Status: nonEmptyPtr(string(c.Status)), CreatedAt: tp(c.CreatedAt),
				Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	missing.warn(st, op, "exadb vm clusters", acct, region)
	return upsertBatch(st, batch, "odb exadb-vm-clusters")
}
