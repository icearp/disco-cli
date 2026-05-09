package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueCatalog},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueCustomEntityType, Leaf: true},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueDataCatalogEncryptionSettings},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueDataQualityRuleset},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueIdentityCenterConfiguration},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueIntegration},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueIntegrationResourceProperty, Leaf: true},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueSecurityConfiguration},
		coverage.TypeDecl{Service: "glue", DiscoType: TypeGlueUsageProfile, Leaf: true},
	)
}

func scanGlueMisc(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGlueCatalogs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueCustomEntityTypes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueDataCatalogEncryption(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueDataQualityRulesets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueIdentityCenter(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueIntegrations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanGlueIntegrationResourceProps(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanGlueSecurityConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGlueUsageProfiles(ctx, client, acct, region, st, scanID) },
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

// scanGlueCatalogs — manual NextToken loop, no paginator on GetCatalogs.
func scanGlueCatalogs(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetCatalogs(ctx, &glue.GetCatalogsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glue:GetCatalogs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glue:GetCatalogs: %w", err)
		}
		for _, c := range out.CatalogList {
			id := sv(c.CatalogId)
			if id == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueCatalog,
				NativeID:       glueResourceARN(region, acct.ID, "catalog", id),
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue catalogs: %w", uerr)
	}
	return len(batch), n, nil
}

func scanGlueCustomEntityTypes(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewListCustomEntityTypesPaginator(client, &glue.ListCustomEntityTypesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:ListCustomEntityTypes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:ListCustomEntityTypes: %w", perr)
		}
		for _, c := range out.CustomEntityTypes {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueCustomEntityType,
				NativeID:       glueResourceARN(region, acct.ID, "customEntityType", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue custom-entity-types: %w", uerr)
	}
	return len(batch), n, nil
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

func scanGlueDataQualityRulesets(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewListDataQualityRulesetsPaginator(client, &glue.ListDataQualityRulesetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:ListDataQualityRulesets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:ListDataQualityRulesets: %w", perr)
		}
		for _, r := range out.Rulesets {
			name := sv(r.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueDataQualityRuleset,
				NativeID:       glueResourceARN(region, acct.ID, "dataQualityRuleset", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue data-quality-rulesets: %w", uerr)
	}
	return len(batch), n, nil
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

// scanGlueIntegrations — DescribeIntegrations uses Marker pagination, no
// paginator constructor.
func scanGlueIntegrations(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var marker *string
	for {
		out, err := client.DescribeIntegrations(ctx, &glue.DescribeIntegrationsInput{Marker: marker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glue:DescribeIntegrations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glue:DescribeIntegrations: %w", err)
		}
		for _, i := range out.Integrations {
			arn := sv(i.IntegrationArn)
			if arn == "" {
				continue
			}
			label := sv(i.IntegrationName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueIntegration,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(i),
				DiscoveredBy:   scanID,
			})
		}
		if out.Marker == nil || *out.Marker == "" {
			break
		}
		marker = out.Marker
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue integrations: %w", uerr)
	}
	return len(batch), n, nil
}

// scanGlueIntegrationResourceProps — Marker pagination, no paginator.
func scanGlueIntegrationResourceProps(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var marker *string
	for {
		out, err := client.ListIntegrationResourceProperties(ctx, &glue.ListIntegrationResourcePropertiesInput{Marker: marker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glue:ListIntegrationResourceProperties", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glue:ListIntegrationResourceProperties: %w", err)
		}
		for _, p := range out.IntegrationResourcePropertyList {
			arn := sv(p.ResourcePropertyArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueIntegrationResourceProperty,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if out.Marker == nil || *out.Marker == "" {
			break
		}
		marker = out.Marker
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue integration-resource-properties: %w", uerr)
	}
	return len(batch), n, nil
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

func scanGlueUsageProfiles(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewListUsageProfilesPaginator(client, &glue.ListUsageProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "glue:ListUsageProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("glue:ListUsageProfiles: %w", perr)
		}
		for _, p := range out.Profiles {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeGlueUsageProfile,
				NativeID:       glueResourceARN(region, acct.ID, "usageProfile", name),
				Name:           &n,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert glue usage-profiles: %w", uerr)
	}
	return len(batch), n, nil
}
