package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlueCatalog, Service: "glue", Redact: glueIfSensitive("Parameters.*", "CatalogProperties.CustomProperties.*")})
	registerType(restype.Descriptor{Type: TypeGlueCustomEntityType, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueDataCatalogEncryptionSettings, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueDataQualityRuleset, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueIdentityCenterConfiguration, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueIntegration, Service: "glue", Redact: glueIfSensitive("IntegrationConfig.SourceProperties.*")})
	registerType(restype.Descriptor{Type: TypeGlueIntegrationResourceProperty, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueSecurityConfiguration, Service: "glue"})
	registerType(restype.Descriptor{Type: TypeGlueUsageProfile, Service: "glue"})
}

// errGlueCatalogNoARN fails the tag read of a catalog listed without the
// ResourceArn GetTags needs; the row is stored without tags.
var errGlueCatalogNoARN = errors.New("the catalog was listed without a ResourceArn, so its tags were not read; report it if it recurs")

// scanGlueCatalogs stores the account's catalogs with their tags. A catalog's
// NativeID is built from its CatalogId, which is not its ARN: the tag read is
// sent the ResourceArn its element carries (the documented
// catalog/{catalog name} form).
func scanGlueCatalogs(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	// Written while the rows are built, before the concurrent tag reads,
	// which only read it.
	arns := map[*store.Resource]string{}
	tags := glueTagsAt(client, func(r *store.Resource) (string, error) {
		if arn := arns[r]; arn != "" {
			return arn, nil
		}
		return "", errGlueCatalogNoARN
	})
	return storeTokenListTags(ctx, st, acct, region, "glue:GetCatalogs",
		func(ctx context.Context, token *string) ([]gluetypes.Catalog, *string, error) {
			out, err := client.GetCatalogs(ctx, &glue.GetCatalogsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.CatalogList, out.NextToken, nil
		},
		func(c gluetypes.Catalog) (*store.Resource, string, string) {
			id := sv(c.CatalogId)
			if id == "" {
				return nil, "catalogs have an empty CatalogId", cmp.Or(sv(c.Name), "(a catalog with no name)")
			}
			r := glueRow(acct, region, scanID, TypeGlueCatalog, glueResourceARN(region, acct.ID, "catalog", id), cmp.Or(sv(c.Name), id), c)
			arns[r] = sv(c.ResourceArn)
			return r, "", ""
		}, tags, p)
}

// scanGlueCustomEntityTypes stores the custom entity types with their tags.
// A type is identified by its name (GetCustomEntityType takes Name), which
// is the {CustomEntityTypeId} of the documented ARN format.
func scanGlueCustomEntityTypes(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:ListCustomEntityTypes",
		func(ctx context.Context, token *string) ([]gluetypes.CustomEntityType, *string, error) {
			out, err := client.ListCustomEntityTypes(ctx, &glue.ListCustomEntityTypesInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.CustomEntityTypes, out.NextToken, nil
		},
		func(c gluetypes.CustomEntityType) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueCustomEntityType, "customEntityType", "custom entity types", sv(c.Name), "", c)
		}, tags, p)
}

func scanGlueDataQualityRulesets(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:ListDataQualityRulesets",
		func(ctx context.Context, token *string) ([]gluetypes.DataQualityRulesetListDetails, *string, error) {
			out, err := client.ListDataQualityRulesets(ctx, &glue.ListDataQualityRulesetsInput{NextToken: token, MaxResults: sdkaws.Int32(gluePageSize)})
			if err != nil {
				return nil, nil, err
			}
			return out.Rulesets, out.NextToken, nil
		},
		func(r gluetypes.DataQualityRulesetListDetails) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueDataQualityRuleset, "dataQualityRuleset", "data quality rulesets", sv(r.Name), "", r)
		}, tags, p)
}

// scanGlueIntegrations stores the zero-ETL integrations, each with the Tags
// its element carries. The model documents no MaxRecords range, so none is
// sent and the service's default page size applies.
func scanGlueIntegrations(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "glue:DescribeIntegrations",
		func(ctx context.Context, marker *string) ([]gluetypes.Integration, *string, error) {
			out, err := client.DescribeIntegrations(ctx, &glue.DescribeIntegrationsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.Integrations, out.Marker, nil
		},
		func(i gluetypes.Integration) (*store.Resource, string, string) {
			arn := sv(i.IntegrationArn)
			if arn == "" {
				return nil, "integrations have an empty IntegrationArn", cmp.Or(sv(i.IntegrationName), "(an integration with no name)")
			}
			r := glueRow(acct, region, scanID, TypeGlueIntegration, arn, cmp.Or(sv(i.IntegrationName), arn), i)
			r.TagsJSON = kvTagsJSON(i.Tags, func(t gluetypes.Tag) (*string, *string) { return t.Key, t.Value })
			return r, "", ""
		})
}

// scanGlueIntegrationResourceProps stores the integration resource
// properties with their tags, read with the ResourcePropertyArn each element
// carries. The model documents no MaxRecords range, so none is sent.
func scanGlueIntegrationResourceProps(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:ListIntegrationResourceProperties",
		func(ctx context.Context, marker *string) ([]gluetypes.IntegrationResourceProperty, *string, error) {
			out, err := client.ListIntegrationResourceProperties(ctx, &glue.ListIntegrationResourcePropertiesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.IntegrationResourcePropertyList, out.Marker, nil
		},
		func(rp gluetypes.IntegrationResourceProperty) (*store.Resource, string, string) {
			arn := sv(rp.ResourcePropertyArn)
			if arn == "" {
				return nil, "integration resource properties have an empty ResourcePropertyArn", cmp.Or(sv(rp.ResourceArn), "(a property with no resource ARN)")
			}
			return glueRow(acct, region, scanID, TypeGlueIntegrationResourceProperty, arn, arn, rp), "", ""
		}, tags, p)
}

// scanGlueUsageProfiles stores the usage profiles with their tags. A profile
// is identified by its name (GetUsageProfile takes Name), which is the
// {UsageProfileId} of the documented ARN format.
func scanGlueUsageProfiles(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:ListUsageProfiles",
		func(ctx context.Context, token *string) ([]gluetypes.UsageProfileDefinition, *string, error) {
			out, err := client.ListUsageProfiles(ctx, &glue.ListUsageProfilesInput{NextToken: token, MaxResults: sdkaws.Int32(glueOrchestrationPageSize200)})
			if err != nil {
				return nil, nil, err
			}
			return out.Profiles, out.NextToken, nil
		},
		func(u gluetypes.UsageProfileDefinition) (*store.Resource, string, string) {
			return glueNamedRow(acct, region, scanID, TypeGlueUsageProfile, "usageProfile", "usage profiles", sv(u.Name), "", u)
		}, tags, p)
}

// scanGlueDataCatalogEncryption emits one row per region — singleton config.
func scanGlueDataCatalogEncryption(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetDataCatalogEncryptionSettings(ctx, &glue.GetDataCatalogEncryptionSettingsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "glue:GetDataCatalogEncryptionSettings", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("glue:GetDataCatalogEncryptionSettings: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:glue:%s:%s:data-catalog-encryption-settings", region, acct.ID)
	name := "data-catalog-encryption-settings"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeGlueDataCatalogEncryptionSettings,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue data-catalog-encryption: %w", uerr)
	}
	return 1, n, nil
}

// scanGlueIdentityCenter emits one row per region (singleton). Returns
// nothing if Identity Center is not configured (EntityNotFoundException).
func scanGlueIdentityCenter(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetGlueIdentityCenterConfiguration(ctx, &glue.GetGlueIdentityCenterConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "glue:GetGlueIdentityCenterConfiguration", acct.ID, region, err)
		}
		if isAPIErrorCode(err, "EntityNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("glue:GetGlueIdentityCenterConfiguration: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:glue:%s:%s:identity-center-configuration", region, acct.ID)
	name := "identity-center-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeGlueIdentityCenterConfiguration,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue identity-center: %w", uerr)
	}
	return 1, n, nil
}

func scanGlueSecurityConfigurations(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewGetSecurityConfigurationsPaginator(client, &glue.GetSecurityConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:GetSecurityConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:GetSecurityConfigurations: %w", perr)
		}
		for _, s := range out.SecurityConfigurations {
			name := sv(s.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueSecurityConfiguration,
				NativeID:       glueResourceARN(region, acct.ID, "securityConfiguration", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue security-configurations: %w", uerr)
	}
	return len(batch), n, nil
}
