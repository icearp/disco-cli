package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/fsx"
)

func init() {
	registerService(serviceEntry{
		name: "aws:fsx",
		fn:   scanFSx,
		emits: []coverage.TypeDecl{
			{Service: "fsx", DiscoType: TypeFSxFileSystem},
			{Service: "fsx", DiscoType: TypeFSxDataRepositoryAssociation},
			{Service: "fsx", DiscoType: TypeFSxSnapshot},
			{Service: "fsx", DiscoType: TypeFSxStorageVirtualMachine},
			{Service: "fsx", DiscoType: TypeFSxVolume},
			{Service: "fsx", DiscoType: TypeFSxS3AccessPointAttachment},
		},
	})
}

type fsxAPI interface {
	DescribeFileSystems(context.Context, *fsx.DescribeFileSystemsInput, ...func(*fsx.Options)) (*fsx.DescribeFileSystemsOutput, error)
	DescribeDataRepositoryAssociations(context.Context, *fsx.DescribeDataRepositoryAssociationsInput, ...func(*fsx.Options)) (*fsx.DescribeDataRepositoryAssociationsOutput, error)
	DescribeSnapshots(context.Context, *fsx.DescribeSnapshotsInput, ...func(*fsx.Options)) (*fsx.DescribeSnapshotsOutput, error)
	DescribeStorageVirtualMachines(context.Context, *fsx.DescribeStorageVirtualMachinesInput, ...func(*fsx.Options)) (*fsx.DescribeStorageVirtualMachinesOutput, error)
	DescribeVolumes(context.Context, *fsx.DescribeVolumesInput, ...func(*fsx.Options)) (*fsx.DescribeVolumesOutput, error)
	DescribeS3AccessPointAttachments(context.Context, *fsx.DescribeS3AccessPointAttachmentsInput, ...func(*fsx.Options)) (*fsx.DescribeS3AccessPointAttachmentsOutput, error)
}

// scanFSx discovers six FSx resource types: file systems, data-repository
// associations, snapshots, storage virtual machines (ONTAP), volumes, and
// S3 access-point attachments. ResourceARN is native on all but
// S3AccessPointAttachment (synthesized from Name).
func scanFSx(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := fsx.NewFromConfig(acct.cfg, func(o *fsx.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanFSxFileSystems(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanFSxDataRepositoryAssociations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanFSxSnapshots(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFSxStorageVirtualMachines(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanFSxVolumes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanFSxS3AccessPointAttachments(ctx, client, acct, region, st, scanID)
		},
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

func scanFSxFileSystems(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeFileSystemsPaginator(client, &fsx.DescribeFileSystemsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeFileSystems", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeFileSystems: %w", err)
		}
		for _, f := range out.FileSystems {
			arn := sv(f.ResourceARN)
			if arn == "" {
				continue
			}
			label := sv(f.FileSystemId)
			lc := string(f.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxFileSystem, NativeID: arn,
				Name: &label, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx file-systems")
}

func scanFSxDataRepositoryAssociations(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeDataRepositoryAssociationsPaginator(client, &fsx.DescribeDataRepositoryAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeDataRepositoryAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeDataRepositoryAssociations: %w", err)
		}
		for _, a := range out.Associations {
			arn := sv(a.ResourceARN)
			if arn == "" {
				continue
			}
			label := sv(a.AssociationId)
			lc := string(a.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxDataRepositoryAssociation, NativeID: arn,
				Name: &label, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx data-repository-associations")
}

func scanFSxSnapshots(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeSnapshotsPaginator(client, &fsx.DescribeSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeSnapshots: %w", err)
		}
		for _, s := range out.Snapshots {
			arn := sv(s.ResourceARN)
			if arn == "" {
				continue
			}
			lc := string(s.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxSnapshot, NativeID: arn,
				Name: s.Name, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx snapshots")
}

func scanFSxStorageVirtualMachines(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeStorageVirtualMachinesPaginator(client, &fsx.DescribeStorageVirtualMachinesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeStorageVirtualMachines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeStorageVirtualMachines: %w", err)
		}
		for _, s := range out.StorageVirtualMachines {
			arn := sv(s.ResourceARN)
			if arn == "" {
				continue
			}
			lc := string(s.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxStorageVirtualMachine, NativeID: arn,
				Name: s.Name, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx storage-virtual-machines")
}

func scanFSxVolumes(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeVolumesPaginator(client, &fsx.DescribeVolumesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeVolumes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeVolumes: %w", err)
		}
		for _, v := range out.Volumes {
			arn := sv(v.ResourceARN)
			if arn == "" {
				continue
			}
			lc := string(v.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxVolume, NativeID: arn,
				Name: v.Name, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx volumes")
}

// scanFSxS3AccessPointAttachments synthesizes ARNs from Name since the
// SDK type carries no ResourceARN field.
func scanFSxS3AccessPointAttachments(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeS3AccessPointAttachmentsPaginator(client, &fsx.DescribeS3AccessPointAttachmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeS3AccessPointAttachments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeS3AccessPointAttachments: %w", err)
		}
		for _, a := range out.S3AccessPointAttachments {
			name := sv(a.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:fsx:%s:%s:s3-access-point-attachment/%s", region, acct.ID, name)
			lc := string(a.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxS3AccessPointAttachment, NativeID: arn,
				Name: &name, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx s3-access-point-attachments")
}
