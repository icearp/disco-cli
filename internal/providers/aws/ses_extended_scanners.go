package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/store"
)

// Page caps the sesv2 model documents in each listing's PageSize text. The
// contact-list, dedicated-IP-pool and tenant listings document none, so
// they send none.
const (
	sesCustomVerificationPageCap int32 = 50
	sesTemplatePageCap           int32 = 100
	sesMultiRegionPageCap        int32 = 1000
)

const sesEventDestinationsOp = "sesv2:GetConfigurationSetEventDestinations"

func sesARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "ses", region, acct, kind+"/"+id)
}

// sesNamedRow is the row of a listed sesv2 item stored under its name's
// ARN of kind; a nameless item is not stored, and what names it in the
// warning.
func sesNamedRow(acct *account, region, scanID, typ, kind, what, name string, item any) (*store.Resource, string, string) {
	if name == "" {
		return nil, what + " have an empty name", "(an item with no name)"
	}
	return bacRowAt(acct, region, scanID, typ, sesARN(acct.partition(), region, acct.ID, kind, name), name, item), "", ""
}

func scanSESContactLists(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "sesv2:ListContactLists",
		func(ctx context.Context, token *string) ([]sesv2types.ContactList, *string, error) {
			out, err := client.ListContactLists(ctx, &sesv2.ListContactListsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ContactLists, out.NextToken, nil
		},
		func(c sesv2types.ContactList) (*store.Resource, string, string) {
			return sesNamedRow(acct, region, scanID, TypeSESContactList, "contact-list", "contact lists", sv(c.ContactListName), c)
		}, tags, p)
}

func scanSESCustomVerificationTemplates(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "sesv2:ListCustomVerificationEmailTemplates",
		func(ctx context.Context, token *string) ([]sesv2types.CustomVerificationEmailTemplateMetadata, *string, error) {
			out, err := client.ListCustomVerificationEmailTemplates(ctx, &sesv2.ListCustomVerificationEmailTemplatesInput{NextToken: token, PageSize: sdkaws.Int32(sesCustomVerificationPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CustomVerificationEmailTemplates, out.NextToken, nil
		},
		func(t sesv2types.CustomVerificationEmailTemplateMetadata) (*store.Resource, string, string) {
			return sesNamedRow(acct, region, scanID, TypeSESCustomVerificationEmailTemplate, "custom-verification-email-template",
				"custom verification email templates", sv(t.TemplateName), t)
		}, tags, p)
}

// scanSESDedicatedIPPools stores each listed pool; ListDedicatedIpPools
// returns names only, and GetDedicatedIpPool adds nothing useful, so the row
// holds the name under the pool's own PoolName key.
func scanSESDedicatedIPPools(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "sesv2:ListDedicatedIpPools",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListDedicatedIpPools(ctx, &sesv2.ListDedicatedIpPoolsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DedicatedIpPools, out.NextToken, nil
		},
		func(name string) (*store.Resource, string, string) {
			return sesNamedRow(acct, region, scanID, TypeSESDedicatedIPPool, "dedicated-ip-pool", "dedicated IP pools", name, map[string]string{"PoolName": name})
		}, tags, p)
}

// scanSESMultiRegionEndpoints stores the region's multi-region endpoints.
// Their tags are not read. A tag read is wired only where the tag op's model
// accepts the resource's ARN and the Service Reference lists the resource
// under TagResource. ListTagsForResource's ResourceArn accepts any string, but
// the Service Reference lists multi-region-endpoint under no tag op, and the
// element carries no ARN: the Service Reference's ARN format names the
// endpoint by EndpointName, while the NativeID here is built from
// EndpointId, so the ARN the op would need is unsettled.
func scanSESMultiRegionEndpoints(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "sesv2:ListMultiRegionEndpoints",
		func(ctx context.Context, token *string) ([]sesv2types.MultiRegionEndpoint, *string, error) {
			out, err := client.ListMultiRegionEndpoints(ctx, &sesv2.ListMultiRegionEndpointsInput{NextToken: token, PageSize: sdkaws.Int32(sesMultiRegionPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MultiRegionEndpoints, out.NextToken, nil
		},
		func(e sesv2types.MultiRegionEndpoint) (*store.Resource, string, string) {
			id := sv(e.EndpointId)
			if id == "" {
				return nil, "multi-region endpoints have an empty EndpointId", cmp.Or(sv(e.EndpointName), "(an endpoint with no name)")
			}
			return bacRowAt(acct, region, scanID, TypeSESMultiRegionEndpoint, sesARN(acct.partition(), region, acct.ID, "multi-region-endpoint", id), cmp.Or(sv(e.EndpointName), id), e), "", ""
		})
}

func scanSESTemplates(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "sesv2:ListEmailTemplates",
		func(ctx context.Context, token *string) ([]sesv2types.EmailTemplateMetadata, *string, error) {
			out, err := client.ListEmailTemplates(ctx, &sesv2.ListEmailTemplatesInput{NextToken: token, PageSize: sdkaws.Int32(sesTemplatePageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TemplatesMetadata, out.NextToken, nil
		},
		func(t sesv2types.EmailTemplateMetadata) (*store.Resource, string, string) {
			return sesNamedRow(acct, region, scanID, TypeSESTemplate, "template", "email templates", sv(t.TemplateName), t)
		}, tags, p)
}

func scanSESTenants(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "sesv2:ListTenants",
		func(ctx context.Context, token *string) ([]sesv2types.TenantInfo, *string, error) {
			out, err := client.ListTenants(ctx, &sesv2.ListTenantsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Tenants, out.NextToken, nil
		},
		func(t sesv2types.TenantInfo) (*store.Resource, string, string) {
			name := cmp.Or(sv(t.TenantName), sv(t.TenantId))
			return bacRow(acct, region, scanID, TypeSESTenant, "tenants", t.TenantArn, name, cmp.Or(name, "(a tenant with no name or id)"), t)
		}, tags, p)
}

// scanSESVdmAttributes — singleton per region. GetAccount returns the
// VdmAttributes block among other account-level fields; emit one row.
func scanSESVdmAttributes(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAccount(ctx, &sesv2.GetAccountInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "sesv2:GetAccount", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("sesv2:GetAccount: %w", err)
	}
	if out.VdmAttributes == nil {
		return 0, 0, nil
	}
	arn := arnFor(acct.partition(), "ses", region, acct.ID, "vdm-attributes")
	name := "vdm-attributes"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSESVdmAttributes, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(out.VdmAttributes), DiscoveredBy: scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses vdm-attributes: %w", uerr)
	}
	return 1, n, nil
}

// scanSESEventDestinations stores the event destinations of every
// configuration set the configuration-set phase stored, one row per (set,
// destination), the sets read concurrently. A set deleted since it was
// listed is skipped; the op answers with every destination at once (no page
// token).
func scanSESEventDestinations(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string, sets []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, sesEventDestinationsOp, sesNotFound, sets,
		func(ctx context.Context, set string, _ *string) ([]sesv2types.EventDestination, *string, error) {
			out, err := client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: &set})
			if err != nil {
				return nil, nil, err
			}
			return out.EventDestinations, nil, nil
		},
		func(set string, ed sesv2types.EventDestination) (*store.Resource, string, string) {
			name := sv(ed.Name)
			if name == "" {
				return nil, "event destinations have an empty Name", "(a destination of configuration set " + set + ")"
			}
			arn := sesConfigurationSetARN(acct.partition(), region, acct.ID, set) + "/event-destination/" + name
			return bacRowAt(acct, region, scanID, TypeSESConfigurationSetEventDestination, arn, name, ed), "", ""
		})
}
