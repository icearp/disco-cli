package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueRegistry, Service: "glue", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGlueSchema, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueSchemaVersion, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueSchemaVersionMetadata, Service: "glue", Leaf: true})
}

// scanGlueSchema runs all Schema-family phases — Glue Schema Registry
// resources (registry → schema → version → version-metadata).
func scanGlueSchema(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	registries, t1, i1, e1 := scanGlueRegistries(ctx, client, acct, region, st, scanID)
	if e1 != nil {
		return 0, 0, e1
	}
	total += t1
	inserted += i1

	schemas, t2, i2, e2 := scanGlueSchemas(ctx, client, registries, acct, region, st, scanID)
	if e2 != nil {
		return total, inserted, e2
	}
	total += t2
	inserted += i2

	versions, t3, i3, e3 := scanGlueSchemaVersions(ctx, client, schemas, acct, region, st, scanID)
	if e3 != nil {
		return total, inserted, e3
	}
	total += t3
	inserted += i3

	t4, i4, e4 := scanGlueSchemaVersionMetadata(ctx, client, versions, acct, region, st, scanID)
	if e4 != nil {
		return total, inserted, e4
	}
	total += t4
	inserted += i4
	return total, inserted, nil
}

// scanGlueRegistries returns the registry ARN list so downstream phases can
// scope ListSchemas per-registry.
func scanGlueRegistries(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (regArns []string, total, inserted int, err error) {
	pager := glue.NewListRegistriesPaginator(client, &glue.ListRegistriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:ListRegistries", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("glue:ListRegistries: %w", perr)
		}
		for _, r := range out.Registries {
			arn := sv(r.RegistryArn)
			if arn == "" {
				continue
			}
			regArns = append(regArns, arn)
			rname := sv(r.RegistryName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueRegistry,
				NativeID:       arn,
				Name:           &rname,
				Region:         &region,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert glue registries: %w", uerr)
	}
	return regArns, len(batch), n, nil
}

// scanGlueSchemas lists schemas per registry. Returns schema ARNs for
// downstream version enumeration.
func scanGlueSchemas(ctx context.Context, client glueAPI, regArns []string, acct *account, region string, st *store.Store, scanID string) (schemaArns []string, total, inserted int, err error) {
	if len(regArns) == 0 {
		return nil, 0, 0, nil
	}
	var batch []*store.Resource
	for _, regARN := range regArns {
		regID := gluetypes.RegistryId{RegistryArn: &regARN}
		pager := glue.NewListSchemasPaginator(client, &glue.ListSchemasInput{RegistryId: &regID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("glue:ListSchemas %s: %w", regARN, perr)
			}
			for _, s := range out.Schemas {
				arn := sv(s.SchemaArn)
				if arn == "" {
					continue
				}
				schemaArns = append(schemaArns, arn)
				sname := sv(s.SchemaName)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeGlueSchema,
					NativeID:       arn,
					Name:           &sname,
					Region:         &region,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert glue schemas: %w", uerr)
	}
	return schemaArns, len(batch), n, nil
}

// scanGlueSchemaVersions enumerates versions per schema. Returns version
// IDs for downstream metadata enumeration.
func scanGlueSchemaVersions(ctx context.Context, client glueAPI, schemaArns []string, acct *account, region string, st *store.Store, scanID string) (versionIDs []string, total, inserted int, err error) {
	if len(schemaArns) == 0 {
		return nil, 0, 0, nil
	}
	var batch []*store.Resource
	for _, schARN := range schemaArns {
		sid := gluetypes.SchemaId{SchemaArn: &schARN}
		pager := glue.NewListSchemaVersionsPaginator(client, &glue.ListSchemaVersionsInput{SchemaId: &sid})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("glue:ListSchemaVersions %s: %w", schARN, perr)
			}
			for _, v := range out.Schemas {
				vid := sv(v.SchemaVersionId)
				if vid == "" {
					continue
				}
				versionIDs = append(versionIDs, vid)
				vlabel := vid
				if v.VersionNumber != nil {
					vlabel = fmt.Sprintf("v%d:%s", *v.VersionNumber, vid)
				}
				arn := fmt.Sprintf("%s/version/%s", schARN, vid)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeGlueSchemaVersion,
					NativeID:       arn,
					Name:           &vlabel,
					Region:         &region,
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert glue schema versions: %w", uerr)
	}
	return versionIDs, len(batch), n, nil
}

// scanGlueSchemaVersionMetadata queries metadata key-value pairs per
// schema version. Each returned entry becomes a separate row.
func scanGlueSchemaVersionMetadata(ctx context.Context, client glueAPI, versionIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(versionIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, vid := range versionIDs {
		var token *string
		for {
			out, perr := client.QuerySchemaVersionMetadata(ctx, &glue.QuerySchemaVersionMetadataInput{SchemaVersionId: &vid, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("glue:QuerySchemaVersionMetadata %s: %w", vid, perr)
			}
			for key, info := range out.MetadataInfoMap {
				k := key
				arn := fmt.Sprintf("arn:aws:glue:%s:%s:schema-version/%s/metadata/%s", region, acct.ID, vid, k)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeGlueSchemaVersionMetadata,
					NativeID:       arn,
					Name:           &k,
					Region:         &region,
					AttributesJSON: mustJSON(info),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue schema-version-metadata: %w", uerr)
	}
	return len(batch), n, nil
}
