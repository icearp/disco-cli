package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/icearp/disco-cli/store"
)

// scanSESExtended covers ContactList, CustomVerificationEmailTemplate,
// DedicatedIpPool, MultiRegionEndpoint, Template, Tenant, VdmAttributes,
// and ConfigurationSetEventDestination (per-config-set fan-out).
func scanSESExtended(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSESContactLists(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanSESCustomVerificationTemplates(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanSESDedicatedIPPools(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanSESMultiRegionEndpoints(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanSESTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSESTenants(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSESVdmAttributes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSESEventDestinations(ctx, client, acct, region, st, scanID) },
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

func sesARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:ses:%s:%s:%s/%s", region, acct, kind, id)
}

func scanSESContactLists(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListContactListsPaginator(client, &sesv2.ListContactListsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListContactLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListContactLists: %w", perr)
		}
		for _, c := range out.ContactLists {
			name := sv(c.ContactListName)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESContactList, NativeID: sesARN(region, acct.ID, "contact-list", name),
				Name: &n, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses contact-lists: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSESCustomVerificationTemplates(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListCustomVerificationEmailTemplatesPaginator(client, &sesv2.ListCustomVerificationEmailTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListCustomVerificationEmailTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListCustomVerificationEmailTemplates: %w", perr)
		}
		for _, t := range out.CustomVerificationEmailTemplates {
			name := sv(t.TemplateName)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESCustomVerificationEmailTemplate, NativeID: sesARN(region, acct.ID, "custom-verification-email-template", name),
				Name: &n, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses custom-verification-email-templates: %w", uerr)
	}
	return len(batch), n, nil
}

// scanSESDedicatedIPPools — ListDedicatedIpPools returns []string; no per-pool
// fan-out since GetDedicatedIpPool adds nothing useful.
func scanSESDedicatedIPPools(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListDedicatedIpPoolsPaginator(client, &sesv2.ListDedicatedIpPoolsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListDedicatedIpPools", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListDedicatedIpPools: %w", perr)
		}
		for _, name := range out.DedicatedIpPools {
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESDedicatedIPPool, NativeID: sesARN(region, acct.ID, "dedicated-ip-pool", name),
				Name: &n, Region: &region, AttributesJSON: mustJSON(map[string]string{"PoolName": name}), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses dedicated-ip-pools: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSESMultiRegionEndpoints(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListMultiRegionEndpointsPaginator(client, &sesv2.ListMultiRegionEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListMultiRegionEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListMultiRegionEndpoints: %w", perr)
		}
		for _, e := range out.MultiRegionEndpoints {
			id := sv(e.EndpointId)
			if id == "" {
				continue
			}
			label := sv(e.EndpointName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESMultiRegionEndpoint, NativeID: sesARN(region, acct.ID, "multi-region-endpoint", id),
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses multi-region-endpoints: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSESTemplates(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListEmailTemplatesPaginator(client, &sesv2.ListEmailTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListEmailTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListEmailTemplates: %w", perr)
		}
		for _, t := range out.TemplatesMetadata {
			name := sv(t.TemplateName)
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESTemplate, NativeID: sesARN(region, acct.ID, "template", name),
				Name: &n, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses templates: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSESTenants(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListTenantsPaginator(client, &sesv2.ListTenantsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListTenants", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListTenants: %w", perr)
		}
		for _, t := range out.Tenants {
			arn := sv(t.TenantArn)
			if arn == "" {
				continue
			}
			label := sv(t.TenantName)
			if label == "" {
				label = sv(t.TenantId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESTenant, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses tenants: %w", uerr)
	}
	return len(batch), n, nil
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
	arn := fmt.Sprintf("arn:aws:ses:%s:%s:vdm-attributes", region, acct.ID)
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

// scanSESEventDestinations re-lists configuration sets and fans out
// GetConfigurationSetEventDestinations per-set. One row per (set, dest).
func scanSESEventDestinations(ctx context.Context, client sesv2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sesv2.NewListConfigurationSetsPaginator(client, &sesv2.ListConfigurationSetsInput{})
	var setNames []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sesv2:ListConfigurationSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sesv2:ListConfigurationSets: %w", perr)
		}
		setNames = append(setNames, out.ConfigurationSets...)
	}
	if len(setNames) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, sname := range setNames {
		s := sname
		out, derr := client.GetConfigurationSetEventDestinations(ctx, &sesv2.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: &s})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("sesv2:GetConfigurationSetEventDestinations %s: %w", sname, derr)
		}
		for _, ed := range out.EventDestinations {
			edName := sv(ed.Name)
			if edName == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s/event-destination/%s", region, acct.ID, sname, edName)
			label := edName
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSESConfigurationSetEventDestination, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ed), DiscoveredBy: scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ses event-destinations: %w", uerr)
	}
	return len(batch), n, nil
}
