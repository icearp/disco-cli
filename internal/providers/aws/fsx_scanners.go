package aws

import (
	"context"
	"math"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/fsx"
	fsxtypes "github.com/aws/aws-sdk-go-v2/service/fsx/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeFSxFileSystem, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxDataRepositoryAssociation, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxSnapshot, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxStorageVirtualMachine, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxVolume, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxS3AccessPointAttachment, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxBackup, Service: "fsx"})
	registerType(restype.Descriptor{Type: TypeFSxFileCache, Service: "fsx"})
	registerService(serviceEntry{
		name: "aws:fsx",
		fn:   scanFSx,
	})
}

type fsxAPI interface {
	DescribeFileSystems(context.Context, *fsx.DescribeFileSystemsInput, ...func(*fsx.Options)) (*fsx.DescribeFileSystemsOutput, error)
	DescribeDataRepositoryAssociations(context.Context, *fsx.DescribeDataRepositoryAssociationsInput, ...func(*fsx.Options)) (*fsx.DescribeDataRepositoryAssociationsOutput, error)
	DescribeSnapshots(context.Context, *fsx.DescribeSnapshotsInput, ...func(*fsx.Options)) (*fsx.DescribeSnapshotsOutput, error)
	DescribeStorageVirtualMachines(context.Context, *fsx.DescribeStorageVirtualMachinesInput, ...func(*fsx.Options)) (*fsx.DescribeStorageVirtualMachinesOutput, error)
	DescribeVolumes(context.Context, *fsx.DescribeVolumesInput, ...func(*fsx.Options)) (*fsx.DescribeVolumesOutput, error)
	DescribeS3AccessPointAttachments(context.Context, *fsx.DescribeS3AccessPointAttachmentsInput, ...func(*fsx.Options)) (*fsx.DescribeS3AccessPointAttachmentsOutput, error)
	DescribeBackups(context.Context, *fsx.DescribeBackupsInput, ...func(*fsx.Options)) (*fsx.DescribeBackupsOutput, error)
	DescribeFileCaches(context.Context, *fsx.DescribeFileCachesInput, ...func(*fsx.Options)) (*fsx.DescribeFileCachesOutput, error)
}

const (
	fsxDescribeFileSystemsOp                = "fsx:DescribeFileSystems"
	fsxDescribeDataRepositoryAssociationsOp = "fsx:DescribeDataRepositoryAssociations"
	fsxDescribeSnapshotsOp                  = "fsx:DescribeSnapshots"
	fsxDescribeStorageVirtualMachinesOp     = "fsx:DescribeStorageVirtualMachines"
	fsxDescribeVolumesOp                    = "fsx:DescribeVolumes"
	fsxDescribeS3AccessPointAttachmentsOp   = "fsx:DescribeS3AccessPointAttachments"
	fsxDescribeBackupsOp                    = "fsx:DescribeBackups"
	fsxDescribeFileCachesOp                 = "fsx:DescribeFileCaches"
)

// fsxMaxResults is the top of the MaxResults range (1-2147483647) the FSx
// model gives every Describe op scanFSxAll calls except
// DescribeDataRepositoryAssociations. For DescribeFileSystems and
// DescribeBackups the docs say the service returns the smaller of it and its
// own per-page maximum; the other ops document only the model range.
var fsxMaxResults = sdkaws.Int32(math.MaxInt32)

// fsxDRAMaxResults is the model's cap for DescribeDataRepositoryAssociations
// (LimitedMaxResults, 1-25).
var fsxDRAMaxResults = sdkaws.Int32(25)

func scanFSx(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := fsx.NewFromConfig(acct.cfg, func(o *fsx.Options) { o.Region = region })
	return scanFSxAll(ctx, client, acct, region, st, scanID)
}

// scanFSxAll runs every FSx listing and joins their errors. The listings are
// independent, so a failed one does not stop the rest; only a store write
// failure stops it.
func scanFSxAll(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	for _, phase := range []struct {
		op string
		fn func(context.Context, fsxAPI, *account, string, *store.Store, string) (int, int, error)
	}{
		{fsxDescribeFileSystemsOp, scanFSxFileSystems},
		{fsxDescribeDataRepositoryAssociationsOp, scanFSxDataRepositoryAssociations},
		{fsxDescribeSnapshotsOp, scanFSxSnapshots},
		{fsxDescribeStorageVirtualMachinesOp, scanFSxStorageVirtualMachines},
		{fsxDescribeVolumesOp, scanFSxVolumes},
		{fsxDescribeS3AccessPointAttachmentsOp, scanFSxS3AccessPointAttachments},
		{fsxDescribeBackupsOp, scanFSxBackups},
		{fsxDescribeFileCachesOp, scanFSxFileCaches},
	} {
		t, i, err := phase.fn(ctx, client, acct, region, st, scanID)
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// fsxTagKV reads an FSx tag for kvTagsJSON.
func fsxTagKV(t fsxtypes.Tag) (key, value *string) { return t.Key, t.Value }

// fsxElement is what fsxRow reads from a listed FSx element.
type fsxElement struct {
	arn, id   string // ResourceARN, the NativeID; the element's own id
	name      *string
	lifecycle string
	tags      []fsxtypes.Tag
}

// fsxRow builds the row of a listed FSx element keyed by its ResourceARN, or
// returns why it cannot be stored (kind names the elements, "file systems")
// with its id to name it in the warning.
func fsxRow(acct *account, region, scanID, rtype, kind string, e fsxElement, element any) (*store.Resource, string, string) {
	if e.arn == "" {
		return nil, kind + " have an empty ResourceARN", e.id
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: e.arn, Name: e.name, Region: &region, Status: nonEmptyPtr(e.lifecycle),
		AttributesJSON: mustJSON(element), TagsJSON: kvTagsJSON(e.tags, fsxTagKV), DiscoveredBy: scanID,
	}, "", ""
}

func scanFSxFileSystems(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeFileSystemsOp,
		func(ctx context.Context, token *string) ([]fsxtypes.FileSystem, *string, error) {
			out, err := client.DescribeFileSystems(ctx, &fsx.DescribeFileSystemsInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.FileSystems, out.NextToken, nil
		},
		func(f fsxtypes.FileSystem) (*store.Resource, string, string) {
			id := sv(f.FileSystemId)
			return fsxRow(acct, region, scanID, TypeFSxFileSystem, "file systems", fsxElement{
				arn: sv(f.ResourceARN), id: id, name: nonEmptyPtr(id), lifecycle: string(f.Lifecycle), tags: f.Tags,
			}, f)
		})
}

func scanFSxDataRepositoryAssociations(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeDataRepositoryAssociationsOp,
		func(ctx context.Context, token *string) ([]fsxtypes.DataRepositoryAssociation, *string, error) {
			out, err := client.DescribeDataRepositoryAssociations(ctx, &fsx.DescribeDataRepositoryAssociationsInput{NextToken: token, MaxResults: fsxDRAMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Associations, out.NextToken, nil
		},
		func(a fsxtypes.DataRepositoryAssociation) (*store.Resource, string, string) {
			id := sv(a.AssociationId)
			return fsxRow(acct, region, scanID, TypeFSxDataRepositoryAssociation, "data repository associations", fsxElement{
				arn: sv(a.ResourceARN), id: id, name: nonEmptyPtr(id), lifecycle: string(a.Lifecycle), tags: a.Tags,
			}, a)
		})
}

func scanFSxSnapshots(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeSnapshotsOp,
		func(ctx context.Context, token *string) ([]fsxtypes.Snapshot, *string, error) {
			out, err := client.DescribeSnapshots(ctx, &fsx.DescribeSnapshotsInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Snapshots, out.NextToken, nil
		},
		func(s fsxtypes.Snapshot) (*store.Resource, string, string) {
			return fsxRow(acct, region, scanID, TypeFSxSnapshot, "snapshots", fsxElement{
				arn: sv(s.ResourceARN), id: sv(s.SnapshotId), name: s.Name, lifecycle: string(s.Lifecycle), tags: s.Tags,
			}, s)
		})
}

func scanFSxStorageVirtualMachines(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeStorageVirtualMachinesOp,
		func(ctx context.Context, token *string) ([]fsxtypes.StorageVirtualMachine, *string, error) {
			out, err := client.DescribeStorageVirtualMachines(ctx, &fsx.DescribeStorageVirtualMachinesInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.StorageVirtualMachines, out.NextToken, nil
		},
		func(s fsxtypes.StorageVirtualMachine) (*store.Resource, string, string) {
			return fsxRow(acct, region, scanID, TypeFSxStorageVirtualMachine, "storage virtual machines", fsxElement{
				arn: sv(s.ResourceARN), id: sv(s.StorageVirtualMachineId), name: s.Name, lifecycle: string(s.Lifecycle), tags: s.Tags,
			}, s)
		})
}

func scanFSxVolumes(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeVolumesOp,
		func(ctx context.Context, token *string) ([]fsxtypes.Volume, *string, error) {
			out, err := client.DescribeVolumes(ctx, &fsx.DescribeVolumesInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Volumes, out.NextToken, nil
		},
		func(v fsxtypes.Volume) (*store.Resource, string, string) {
			return fsxRow(acct, region, scanID, TypeFSxVolume, "volumes", fsxElement{
				arn: sv(v.ResourceARN), id: sv(v.VolumeId), name: v.Name, lifecycle: string(v.Lifecycle), tags: v.Tags,
			}, v)
		})
}

// scanFSxS3AccessPointAttachments synthesizes ARNs from Name since the
// SDK type carries no ResourceARN field.
func scanFSxS3AccessPointAttachments(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeS3AccessPointAttachmentsOp,
		func(ctx context.Context, token *string) ([]fsxtypes.S3AccessPointAttachment, *string, error) {
			out, err := client.DescribeS3AccessPointAttachments(ctx, &fsx.DescribeS3AccessPointAttachmentsInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.S3AccessPointAttachments, out.NextToken, nil
		},
		func(a fsxtypes.S3AccessPointAttachment) (*store.Resource, string, string) {
			name := sv(a.Name)
			if name == "" {
				var accessPointARN string
				if a.S3AccessPoint != nil {
					accessPointARN = sv(a.S3AccessPoint.ResourceARN)
				}
				return nil, "S3 access point attachments have an empty Name (first names its access point ARN)", accessPointARN
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeFSxS3AccessPointAttachment,
				NativeID: arnFor(acct.partition(), "fsx", region, acct.ID, "s3-access-point-attachment/"+name),
				Name:     &name, Region: &region, Status: nonEmptyPtr(string(a.Lifecycle)),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
}
