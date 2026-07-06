package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/observabilityadmin"
)

func init() {
	registerService(serviceEntry{
		name: "aws:observabilityadmin",
		fn:   scanObservabilityAdmin,
		emits: []coverage.TypeDecl{
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminOrganizationCentralizationRule, Leaf: true},
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminOrganizationTelemetryRule, Leaf: true},
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminS3TableIntegration, Leaf: true},
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminTelemetryEnrichment, Leaf: true},
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminTelemetryPipelines, Leaf: true},
			{Service: "observabilityadmin", DiscoType: TypeObservabilityAdminTelemetryRule, Leaf: true},
		},
	})
}

type obsAdminAPI interface {
	ListCentralizationRulesForOrganization(context.Context, *observabilityadmin.ListCentralizationRulesForOrganizationInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListCentralizationRulesForOrganizationOutput, error)
	ListTelemetryRulesForOrganization(context.Context, *observabilityadmin.ListTelemetryRulesForOrganizationInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListTelemetryRulesForOrganizationOutput, error)
	ListTelemetryRules(context.Context, *observabilityadmin.ListTelemetryRulesInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListTelemetryRulesOutput, error)
	ListS3TableIntegrations(context.Context, *observabilityadmin.ListS3TableIntegrationsInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListS3TableIntegrationsOutput, error)
	ListTelemetryPipelines(context.Context, *observabilityadmin.ListTelemetryPipelinesInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.ListTelemetryPipelinesOutput, error)
	GetTelemetryEnrichmentStatus(context.Context, *observabilityadmin.GetTelemetryEnrichmentStatusInput, ...func(*observabilityadmin.Options)) (*observabilityadmin.GetTelemetryEnrichmentStatusOutput, error)
}

// scanObservabilityAdmin discovers six CloudWatch Observability Admin
// resource types. Organization-scoped lists tolerate AccessDenied for
// non-management accounts. TelemetryEnrichment is a singleton per
// (acct, region) with a synthesized ARN.
func scanObservabilityAdmin(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := observabilityadmin.NewFromConfig(acct.cfg, func(o *observabilityadmin.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanObsCentralizationRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanObsOrgTelemetryRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanObsTelemetryRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanObsS3TableIntegrations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanObsTelemetryPipelines(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanObsTelemetryEnrichment(ctx, client, acct, region, st, scanID) },
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

func scanObsCentralizationRules(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := observabilityadmin.NewListCentralizationRulesForOrganizationPaginator(client, &observabilityadmin.ListCentralizationRulesForOrganizationInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// Org-only API: silent no-op from non-org-management or non-org
			// accounts. Real IAM denies still warn via isAccessDenied.
			if isAPIErrorCode(err, "AWSOrganizationsNotInUseException", "UnauthorizedException") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "observabilityadmin:ListCentralizationRulesForOrganization", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("observabilityadmin:ListCentralizationRulesForOrganization: %w", err)
		}
		for _, r := range out.CentralizationRuleSummaries {
			arn := sv(r.RuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeObservabilityAdminOrganizationCentralizationRule, NativeID: arn,
				Name: r.RuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "observabilityadmin organization-centralization-rules")
}

func scanObsOrgTelemetryRules(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := observabilityadmin.NewListTelemetryRulesForOrganizationPaginator(client, &observabilityadmin.ListTelemetryRulesForOrganizationInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// Org-only API: silent no-op from non-org-management or non-org
			// accounts. Real IAM denies still warn via isAccessDenied.
			if isAPIErrorCode(err, "AWSOrganizationsNotInUseException", "UnauthorizedException") {
				return 0, 0, nil
			}
			// Telemetry evaluation not enabled at org level — feature gate, not error.
			if isAPIErrorWithMessage(err, "ValidationException", "Telemetry evaluation is not enabled") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "observabilityadmin:ListTelemetryRulesForOrganization", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("observabilityadmin:ListTelemetryRulesForOrganization: %w", err)
		}
		for _, r := range out.TelemetryRuleSummaries {
			arn := sv(r.RuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeObservabilityAdminOrganizationTelemetryRule, NativeID: arn,
				Name: r.RuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "observabilityadmin organization-telemetry-rules")
}

func scanObsTelemetryRules(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := observabilityadmin.NewListTelemetryRulesPaginator(client, &observabilityadmin.ListTelemetryRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "observabilityadmin:ListTelemetryRules", acct.ID, region, err)
			}
			// ValidationException "Telemetry evaluation is not enabled" =
			// account not opted into observabilityadmin.
			if isAPIErrorWithMessage(err, "ValidationException", "Telemetry evaluation is not enabled") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("observabilityadmin:ListTelemetryRules: %w", err)
		}
		for _, r := range out.TelemetryRuleSummaries {
			arn := sv(r.RuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeObservabilityAdminTelemetryRule, NativeID: arn,
				Name: r.RuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "observabilityadmin telemetry-rules")
}

func scanObsS3TableIntegrations(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := observabilityadmin.NewListS3TableIntegrationsPaginator(client, &observabilityadmin.ListS3TableIntegrationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "observabilityadmin:ListS3TableIntegrations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("observabilityadmin:ListS3TableIntegrations: %w", err)
		}
		for _, s := range out.IntegrationSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeObservabilityAdminS3TableIntegration, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "observabilityadmin s3-table-integrations")
}

func scanObsTelemetryPipelines(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := observabilityadmin.NewListTelemetryPipelinesPaginator(client, &observabilityadmin.ListTelemetryPipelinesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "observabilityadmin:ListTelemetryPipelines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("observabilityadmin:ListTelemetryPipelines: %w", err)
		}
		for _, p := range out.PipelineSummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeObservabilityAdminTelemetryPipelines, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "observabilityadmin telemetry-pipelines")
}

// scanObsTelemetryEnrichment captures the per-(acct, region) singleton
// telemetry-enrichment status; ARN synthesized since the API returns none.
func scanObsTelemetryEnrichment(ctx context.Context, client obsAdminAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetTelemetryEnrichmentStatus(ctx, &observabilityadmin.GetTelemetryEnrichmentStatusInput{})
	if err != nil {
		// ResourceNotFoundException = enrichment not configured (default state). Silent.
		if isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "observabilityadmin:GetTelemetryEnrichmentStatus", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("observabilityadmin:GetTelemetryEnrichmentStatus: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:observabilityadmin:%s:%s:telemetry-enrichment", region, acct.ID)
	label := "telemetry-enrichment"
	status := string(out.Status)
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeObservabilityAdminTelemetryEnrichment, NativeID: arn,
		Name: &label, Region: &region, Status: &status,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "observabilityadmin telemetry-enrichment")
}
