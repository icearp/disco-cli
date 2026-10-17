package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// sqTemplateRegion is the endpoint the template ops are called on. The
// template is one per organization and each entry names the region it applies
// to (AwsRegion), so a single unfiltered list from one endpoint returns every
// region's entries; calling it per scanned region would list the same entries
// again. us-east-1 is assumed from the AWS user guide, not observed live: a
// TemplatesNotAvailableInRegionException here means the assumption is wrong,
// so it is returned rather than skipped.
const sqTemplateRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate, Service: "servicequotas"})
	// Separate from aws:servicequotas because that one is per-region and
	// this is one org-level list. Opt-in like it (--include-service-quotas
	// or --services aws:servicequotas-template) because the template is a
	// management-account feature: a member account's call is expected to be
	// denied, and no existing phase tells disco cheaply whether this account
	// is the management account, so a default scan would warn on every member
	// account forever.
	registerService(serviceEntry{
		name:   "aws:servicequotas-template",
		global: true,
		optIn:  true,
		fn:     scanServiceQuotasTemplate,
	})
}

type sqTemplateAPI interface {
	ListServiceQuotaIncreaseRequestsInTemplate(context.Context, *servicequotas.ListServiceQuotaIncreaseRequestsInTemplateInput, ...func(*servicequotas.Options)) (*servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput, error)
}

func scanServiceQuotasTemplate(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	client := servicequotas.NewFromConfig(acct.cfg, func(o *servicequotas.Options) { o.Region = sqTemplateRegion })
	return scanSQTemplateEntries(ctx, client, acct, st, scanID)
}

// scanSQTemplateEntries lists the organization's quota request template: the
// quota increases Service Quotas requests automatically for each new account
// created in the organization.
//
// A row's Region is the "global" sentinel, not the entry's AwsRegion, because
// the entry lives in the one organization-wide template; AwsRegion stays in
// the NativeID and the attributes.
func scanSQTemplateEntries(ctx context.Context, client sqTemplateAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	const op = "servicequotas:ListServiceQuotaIncreaseRequestsInTemplate"
	pager := servicequotas.NewListServiceQuotaIncreaseRequestsInTemplatePaginator(client,
		&servicequotas.ListServiceQuotaIncreaseRequestsInTemplateInput{},
		func(o *servicequotas.ListServiceQuotaIncreaseRequestsInTemplatePaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// The model documents these as "the account making this call is
			// not a member of an organization" and "not allowed unless Service
			// Access with Service Quotas is enabled in your organization": the
			// default states of an account or organization that does not use
			// the template, so there are no entries to list.
			if isAPIErrorCode(err, "NoAvailableOrganizationException", "AWSServiceAccessNotEnabledException") {
				return 0, 0, nil
			}
			// Scope "" because the template is organization-wide, not regional.
			if isAccessDenied(err) || isAPIErrorCode(err, "DependencyAccessDeniedException") {
				return 0, 0, skipIfAccessDenied(st, op, acct.ID, "", err)
			}
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, e := range out.ServiceQuotaIncreaseRequestInTemplateList {
			awsRegion, serviceCode, quotaCode := sv(e.AwsRegion), sv(e.ServiceCode), sv(e.QuotaCode)
			if awsRegion == "" || serviceCode == "" || quotaCode == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate,
				NativeID: sqTemplateEntryNativeID(awsRegion, acct.ID, serviceCode, quotaCode),
				Name:     e.QuotaName, Region: regionGlobal,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "servicequotas template entries")
}

// sqTemplateEntryNativeID builds the identity of one template entry. AWS issues
// no ARN for it; the template holds at most one entry per (region, service
// code, quota code), so it extends the quota's own ARN
// (arn:aws:servicequotas:{region}:{account}:{serviceCode}/{quotaCode}, Service
// Reference) with a /request-template suffix. None of the parts can contain
// "/" (model patterns ^[a-zA-Z][a-zA-Z0-9-]...).
func sqTemplateEntryNativeID(awsRegion, accountID, serviceCode, quotaCode string) string {
	return "arn:aws:servicequotas:" + awsRegion + ":" + accountID + ":" + serviceCode + "/" + quotaCode + "/request-template"
}
