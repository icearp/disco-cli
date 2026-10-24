package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueRegistry, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueSchema, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueSchemaVersion, Service: "glue"})
	// A metadata row's attributes are one key's MetadataInfo; the key itself
	// is the row's name, not in the attributes, so its values are judged by
	// shape alone (a credential shape is replaced, any other value kept).
	registerType(restype.Descriptor{Type: TypeGlueSchemaVersionMetadata, Service: "glue", Redact: []redact.Rule{
		{Path: "MetadataValue", Mode: redact.RedactIfSensitive},
		{Path: "OtherMetadataValueList[*].MetadataValue", Mode: redact.RedactIfSensitive},
	}})
}

// scanGlueRegistries stores the schema registries with their tags and
// returns the stored ones, keyed by ARN, the parents of the schema phase.
func scanGlueRegistries(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (registries []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "glue:ListRegistries", "registries", "RegistryArn",
		func(ctx context.Context, token *string) ([]gluetypes.RegistryListItem, *string, error) {
			out, err := client.ListRegistries(ctx, &glue.ListRegistriesInput{NextToken: token, MaxResults: sdkaws.Int32(glueMaxResultsNumber)})
			if err != nil {
				return nil, nil, err
			}
			return out.Registries, out.NextToken, nil
		},
		func(r gluetypes.RegistryListItem) (*store.Resource, string, string) {
			arn := sv(r.RegistryArn)
			if arn == "" {
				return nil, "registries have an empty RegistryArn", cmp.Or(sv(r.RegistryName), "(a registry with no name)")
			}
			return glueRow(acct, region, scanID, TypeGlueRegistry, arn, sv(r.RegistryName), r), "", ""
		},
		func(r gluetypes.RegistryListItem) string { return sv(r.RegistryArn) },
		tags, p)
}

// scanGlueSchemas lists the schemas of each registry the registry phase
// stored and stores them with their tags (storeEachParentTags). A registry
// ListSchemas no longer finds (EntityNotFoundException, deleted after it was
// listed) is skipped. It returns the ARNs of the schemas stored, the parents
// of the schema-version phase.
func scanGlueSchemas(ctx context.Context, client glueAPI, registries []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (schemaARNs []string, storeWriteFailed bool) {
	stored, stop := storeEachParentTags(ctx, st, acct, region, "glue:ListSchemas", []string{"EntityNotFoundException"}, registries,
		func(ctx context.Context, registry string, token *string) ([]gluetypes.SchemaListItem, *string, error) {
			out, err := client.ListSchemas(ctx, &glue.ListSchemasInput{
				RegistryId: &gluetypes.RegistryId{RegistryArn: &registry}, NextToken: token, MaxResults: sdkaws.Int32(glueMaxResultsNumber),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Schemas, out.NextToken, nil
		},
		func(registry string, s gluetypes.SchemaListItem) (*store.Resource, string, string) {
			arn := sv(s.SchemaArn)
			if arn == "" {
				return nil, "schemas have an empty SchemaArn", cmp.Or(sv(s.SchemaName), "(a schema with no name) of registry "+registry)
			}
			return glueRow(acct, region, scanID, TypeGlueSchema, arn, sv(s.SchemaName), s), "", ""
		}, tags, p)
	for _, r := range stored {
		schemaARNs = append(schemaARNs, r.NativeID)
	}
	return schemaARNs, stop
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
