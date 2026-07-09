package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/clouddirectory"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudDirectoryDirectory, Service: "clouddirectory", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudDirectoryDevelopmentSchema, Service: "clouddirectory", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudDirectoryPublishedSchema, Service: "clouddirectory", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudDirectoryAppliedSchema, Service: "clouddirectory"})
	registerService(serviceEntry{
		name: "aws:clouddirectory",
		fn:   scanCloudDirectory,
	})
}

type cloudDirectoryAPI interface {
	ListDirectories(context.Context, *clouddirectory.ListDirectoriesInput, ...func(*clouddirectory.Options)) (*clouddirectory.ListDirectoriesOutput, error)
	ListDevelopmentSchemaArns(context.Context, *clouddirectory.ListDevelopmentSchemaArnsInput, ...func(*clouddirectory.Options)) (*clouddirectory.ListDevelopmentSchemaArnsOutput, error)
	ListPublishedSchemaArns(context.Context, *clouddirectory.ListPublishedSchemaArnsInput, ...func(*clouddirectory.Options)) (*clouddirectory.ListPublishedSchemaArnsOutput, error)
	ListAppliedSchemaArns(context.Context, *clouddirectory.ListAppliedSchemaArnsInput, ...func(*clouddirectory.Options)) (*clouddirectory.ListAppliedSchemaArnsOutput, error)
}

// scanCloudDirectory discovers directories, account-level development / published
// schemas, and the schemas applied to each directory (per-directory fan-out).
func scanCloudDirectory(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := clouddirectory.NewFromConfig(acct.cfg, func(o *clouddirectory.Options) { o.Region = region })

	dirARNs, t, i, ferr := scanCDDirectories(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCDDevelopmentSchemas(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCDPublishedSchemas(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCDAppliedSchemas(ctx, client, dirARNs, acct, region, st, scanID) },
	} {
		t, i, ferr = phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanCDDirectories lists directories and returns their ARNs for the applied-
// schema fan-out.
func scanCDDirectories(ctx context.Context, client cloudDirectoryAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var arns []string
	var batch []*store.Resource
	pager := clouddirectory.NewListDirectoriesPaginator(client, &clouddirectory.ListDirectoriesInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return arns, 0, 0, skipIfAccessDenied(st, "clouddirectory:ListDirectories", acct.ID, region, perr)
			}
			return arns, 0, 0, fmt.Errorf("clouddirectory:ListDirectories: %w", perr)
		}
		for _, d := range out.Directories {
			arn := sv(d.DirectoryArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			status := string(d.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudDirectoryDirectory, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), CreatedAt: tp(d.CreationDateTime), DiscoveredBy: scanID,
			})
		}
	}
	t, i, e := upsertBatch(st, batch, "clouddirectory directories")
	return arns, t, i, e
}

// schemaNameFromARN returns the trailing path segment of a schema ARN for a
// human-readable Name.
func schemaNameFromARN(arn string) string {
	if i := strings.LastIndexByte(arn, '/'); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func scanCDDevelopmentSchemas(ctx context.Context, client cloudDirectoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := clouddirectory.NewListDevelopmentSchemaArnsPaginator(client, &clouddirectory.ListDevelopmentSchemaArnsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "clouddirectory:ListDevelopmentSchemaArns", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("clouddirectory:ListDevelopmentSchemaArns: %w", perr)
		}
		for _, arn := range out.SchemaArns {
			batch = append(batch, cdSchemaResource(TypeCloudDirectoryDevelopmentSchema, arn, "", acct, region, scanID))
		}
	}
	return upsertBatch(st, batch, "clouddirectory development-schemas")
}

func scanCDPublishedSchemas(ctx context.Context, client cloudDirectoryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := clouddirectory.NewListPublishedSchemaArnsPaginator(client, &clouddirectory.ListPublishedSchemaArnsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "clouddirectory:ListPublishedSchemaArns", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("clouddirectory:ListPublishedSchemaArns: %w", perr)
		}
		for _, arn := range out.SchemaArns {
			batch = append(batch, cdSchemaResource(TypeCloudDirectoryPublishedSchema, arn, "", acct, region, scanID))
		}
	}
	return upsertBatch(st, batch, "clouddirectory published-schemas")
}

// scanCDAppliedSchemas fans out per directory — ListAppliedSchemaArns requires a
// DirectoryArn. Each applied schema carries the owning directory ARN so the
// resolver can wire it back.
func scanCDAppliedSchemas(ctx context.Context, client cloudDirectoryAPI, dirARNs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(dirARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, dirARN := range dirARNs {
		d := dirARN
		pager := clouddirectory.NewListAppliedSchemaArnsPaginator(client, &clouddirectory.ListAppliedSchemaArnsInput{DirectoryArn: &d})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "clouddirectory:ListAppliedSchemaArns", acct.ID, region, perr)
					break
				}
				if isAPIErrorCode(perr, "ResourceNotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("clouddirectory:ListAppliedSchemaArns d=%s: %w", d, perr)
			}
			for _, arn := range out.SchemaArns {
				batch = append(batch, cdSchemaResource(TypeCloudDirectoryAppliedSchema, arn, d, acct, region, scanID))
			}
		}
	}
	return upsertBatch(st, batch, "clouddirectory applied-schemas")
}

// cdSchemaResource builds a schema row. The List*SchemaArns ops return bare ARN
// strings (no SDK struct), so attrs is a minimal container; applied schemas also
// carry the owning DirectoryArn.
func cdSchemaResource(rtype, arn, dirARN string, acct *account, region, scanID string) *store.Resource {
	name := schemaNameFromARN(arn)
	attrs := map[string]string{"SchemaArn": arn}
	if dirARN != "" {
		attrs["DirectoryArn"] = dirARN
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: arn,
		Name: &name, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}
