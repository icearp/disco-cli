package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/s3files"
)

func init() {
	registerService(serviceEntry{
		name: "aws:s3files",
		fn:   scanS3Files,
		emits: []coverage.TypeDecl{
			{Service: "s3files", DiscoType: TypeS3FilesFileSystem},
			{Service: "s3files", DiscoType: TypeS3FilesAccessPoint},
			{Service: "s3files", DiscoType: TypeS3FilesMountTarget},
			{Service: "s3files", DiscoType: TypeS3FilesFileSystemPolicy},
		},
	})
}

type s3filesAPI interface {
	ListFileSystems(context.Context, *s3files.ListFileSystemsInput, ...func(*s3files.Options)) (*s3files.ListFileSystemsOutput, error)
	ListAccessPoints(context.Context, *s3files.ListAccessPointsInput, ...func(*s3files.Options)) (*s3files.ListAccessPointsOutput, error)
	ListMountTargets(context.Context, *s3files.ListMountTargetsInput, ...func(*s3files.Options)) (*s3files.ListMountTargetsOutput, error)
	GetFileSystemPolicy(context.Context, *s3files.GetFileSystemPolicyInput, ...func(*s3files.Options)) (*s3files.GetFileSystemPolicyOutput, error)
}

// scanS3Files discovers S3 Files file systems and per-FS access points,
// mount targets, and policies. MountTarget and FileSystemPolicy synthesize
// ARN since the SDK returns no native ARN for either.
func scanS3Files(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3files.NewFromConfig(acct.cfg, func(o *s3files.Options) { o.Region = region })

	type fsRef struct{ id, arn string }
	pager := s3files.NewListFileSystemsPaginator(client, &s3files.ListFileSystemsInput{})
	var batch []*store.Resource
	var systems []fsRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "s3files:ListFileSystems", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("s3files:ListFileSystems: %w", perr)
		}
		for _, f := range out.FileSystems {
			arn := sv(f.FileSystemArn)
			id := sv(f.FileSystemId)
			if arn == "" || id == "" {
				continue
			}
			systems = append(systems, fsRef{id, arn})
			status := string(f.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3FilesFileSystem, NativeID: arn,
				Name: f.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, ferr := upsertBatch(st, batch, "s3files file-systems")
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	for _, fs := range systems {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanS3FAccessPoints(ctx, client, acct, region, st, scanID, fs.id) },
			func() (int, int, error) {
				return scanS3FMountTargets(ctx, client, acct, region, st, scanID, fs.id, fs.arn)
			},
			func() (int, int, error) {
				return scanS3FFileSystemPolicy(ctx, client, acct, region, st, scanID, fs.id, fs.arn)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanS3FAccessPoints(ctx context.Context, client s3filesAPI, acct *account, region string, st *store.Store, scanID string, fsID string) (int, int, error) {
	id := fsID
	pager := s3files.NewListAccessPointsPaginator(client, &s3files.ListAccessPointsInput{FileSystemId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3files:ListAccessPoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3files:ListAccessPoints: %w", err)
		}
		for _, a := range out.AccessPoints {
			arn := sv(a.AccessPointArn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3FilesAccessPoint, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "s3files access-points")
}

func scanS3FMountTargets(ctx context.Context, client s3filesAPI, acct *account, region string, st *store.Store, scanID string, fsID, fsARN string) (int, int, error) {
	id := fsID
	pager := s3files.NewListMountTargetsPaginator(client, &s3files.ListMountTargetsInput{FileSystemId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3files:ListMountTargets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3files:ListMountTargets: %w", err)
		}
		for _, m := range out.MountTargets {
			mtid := sv(m.MountTargetId)
			if mtid == "" {
				continue
			}
			arn := fsARN + "/mount-target/" + mtid
			label := mtid
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3FilesMountTarget, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "s3files mount-targets")
}

func scanS3FFileSystemPolicy(ctx context.Context, client s3filesAPI, acct *account, region string, st *store.Store, scanID string, fsID, fsARN string) (int, int, error) {
	id := fsID
	out, err := client.GetFileSystemPolicy(ctx, &s3files.GetFileSystemPolicyInput{FileSystemId: &id})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "PolicyNotFound") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("s3files:GetFileSystemPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fsARN + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeS3FilesFileSystemPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "s3files file-system-policies")
}
