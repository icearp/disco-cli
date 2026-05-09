package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/verifiedpermissions"
)

func init() {
	registerService(serviceEntry{
		name: "aws:verifiedpermissions",
		fn:   scanVerifiedPermissions,
		emits: []coverage.TypeDecl{
			{Service: "verifiedpermissions", DiscoType: TypeVerifiedPermissionsPolicyStore, Leaf: true},
			{Service: "verifiedpermissions", DiscoType: TypeVerifiedPermissionsPolicy},
			{Service: "verifiedpermissions", DiscoType: TypeVerifiedPermissionsPolicyTemplate},
			{Service: "verifiedpermissions", DiscoType: TypeVerifiedPermissionsIdentitySource},
		},
	})
}

type verifiedPermissionsAPI interface {
	ListPolicyStores(context.Context, *verifiedpermissions.ListPolicyStoresInput, ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListPolicyStoresOutput, error)
	ListPolicies(context.Context, *verifiedpermissions.ListPoliciesInput, ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListPoliciesOutput, error)
	ListPolicyTemplates(context.Context, *verifiedpermissions.ListPolicyTemplatesInput, ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListPolicyTemplatesOutput, error)
	ListIdentitySources(context.Context, *verifiedpermissions.ListIdentitySourcesInput, ...func(*verifiedpermissions.Options)) (*verifiedpermissions.ListIdentitySourcesOutput, error)
}

// scanVerifiedPermissions discovers Verified Permissions policy stores and
// per-store policies, policy templates, and identity sources. Children have
// no native ARN — synthesize off the parent store ARN.
func scanVerifiedPermissions(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := verifiedpermissions.NewFromConfig(acct.cfg, func(o *verifiedpermissions.Options) { o.Region = region })

	stores, t, i, ferr := scanVPPolicyStores(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, s := range stores {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanVPPolicies(ctx, client, acct, region, st, scanID, s) },
			func() (int, int, error) { return scanVPPolicyTemplates(ctx, client, acct, region, st, scanID, s) },
			func() (int, int, error) { return scanVPIdentitySources(ctx, client, acct, region, st, scanID, s) },
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

type vpStore struct {
	id, arn string
}

func scanVPPolicyStores(ctx context.Context, client verifiedPermissionsAPI, acct *account, region string, st *store.Store, scanID string) ([]vpStore, int, int, error) {
	pager := verifiedpermissions.NewListPolicyStoresPaginator(client, &verifiedpermissions.ListPolicyStoresInput{})
	var batch []*store.Resource
	var stores []vpStore
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "verifiedpermissions:ListPolicyStores", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("verifiedpermissions:ListPolicyStores: %w", err)
		}
		for _, p := range out.PolicyStores {
			arn := sv(p.Arn)
			id := sv(p.PolicyStoreId)
			if arn == "" || id == "" {
				continue
			}
			stores = append(stores, vpStore{id, arn})
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVerifiedPermissionsPolicyStore, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "verifiedpermissions policy-stores")
	return stores, t, i, err
}

func scanVPPolicies(ctx context.Context, client verifiedPermissionsAPI, acct *account, region string, st *store.Store, scanID string, s vpStore) (int, int, error) {
	psID := s.id
	pager := verifiedpermissions.NewListPoliciesPaginator(client, &verifiedpermissions.ListPoliciesInput{PolicyStoreId: &psID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "verifiedpermissions:ListPolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("verifiedpermissions:ListPolicies: %w", err)
		}
		for _, p := range out.Policies {
			pid := sv(p.PolicyId)
			if pid == "" {
				continue
			}
			arn := s.arn + "/policy/" + pid
			label := pid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVerifiedPermissionsPolicy, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "verifiedpermissions policies")
}

func scanVPPolicyTemplates(ctx context.Context, client verifiedPermissionsAPI, acct *account, region string, st *store.Store, scanID string, s vpStore) (int, int, error) {
	psID := s.id
	pager := verifiedpermissions.NewListPolicyTemplatesPaginator(client, &verifiedpermissions.ListPolicyTemplatesInput{PolicyStoreId: &psID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "verifiedpermissions:ListPolicyTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("verifiedpermissions:ListPolicyTemplates: %w", err)
		}
		for _, p := range out.PolicyTemplates {
			tid := sv(p.PolicyTemplateId)
			if tid == "" {
				continue
			}
			arn := s.arn + "/policy-template/" + tid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVerifiedPermissionsPolicyTemplate, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "verifiedpermissions policy-templates")
}

func scanVPIdentitySources(ctx context.Context, client verifiedPermissionsAPI, acct *account, region string, st *store.Store, scanID string, s vpStore) (int, int, error) {
	psID := s.id
	pager := verifiedpermissions.NewListIdentitySourcesPaginator(client, &verifiedpermissions.ListIdentitySourcesInput{PolicyStoreId: &psID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "verifiedpermissions:ListIdentitySources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("verifiedpermissions:ListIdentitySources: %w", err)
		}
		for _, isrc := range out.IdentitySources {
			isid := sv(isrc.IdentitySourceId)
			if isid == "" {
				continue
			}
			arn := s.arn + "/identity-source/" + isid
			label := isid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeVerifiedPermissionsIdentitySource, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(isrc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "verifiedpermissions identity-sources")
}
