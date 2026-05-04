package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:opensearchserverless",
		fn:   scanOpenSearchServerless,
		emits: []coverage.TypeDecl{
			{Service: "opensearchserverless", DiscoType: TypeOSSAccessPolicy},
			{Service: "opensearchserverless", DiscoType: TypeOSSCollection},
			{Service: "opensearchserverless", DiscoType: TypeOSSCollectionGroup},
			{Service: "opensearchserverless", DiscoType: TypeOSSLifecyclePolicy},
			{Service: "opensearchserverless", DiscoType: TypeOSSSecurityConfig},
			{Service: "opensearchserverless", DiscoType: TypeOSSSecurityPolicy},
			{Service: "opensearchserverless", DiscoType: TypeOSSVpcEndpoint},
		},
	})
}

type ossAPI interface {
	ListAccessPolicies(context.Context, *opensearchserverless.ListAccessPoliciesInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListAccessPoliciesOutput, error)
	ListCollections(context.Context, *opensearchserverless.ListCollectionsInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error)
	ListCollectionGroups(context.Context, *opensearchserverless.ListCollectionGroupsInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionGroupsOutput, error)
	ListLifecyclePolicies(context.Context, *opensearchserverless.ListLifecyclePoliciesInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListLifecyclePoliciesOutput, error)
	ListSecurityConfigs(context.Context, *opensearchserverless.ListSecurityConfigsInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListSecurityConfigsOutput, error)
	ListSecurityPolicies(context.Context, *opensearchserverless.ListSecurityPoliciesInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListSecurityPoliciesOutput, error)
	ListVpcEndpoints(context.Context, *opensearchserverless.ListVpcEndpointsInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.ListVpcEndpointsOutput, error)
	BatchGetVpcEndpoint(context.Context, *opensearchserverless.BatchGetVpcEndpointInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetVpcEndpointOutput, error)
}

func ossARN(region, acct, kind, key string) string {
	return fmt.Sprintf("arn:aws:aoss:%s:%s:%s/%s", region, acct, kind, key)
}

func scanOpenSearchServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := opensearchserverless.NewFromConfig(acct.cfg, func(o *opensearchserverless.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOSSCollections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOSSCollectionGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOSSVpcEndpoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanOSSAccessPolicies(ctx, client, acct, region, st, scanID, types.AccessPolicyTypeData)
		},
		func() (int, int, error) {
			return scanOSSLifecyclePolicies(ctx, client, acct, region, st, scanID, types.LifecyclePolicyTypeRetention)
		},
		func() (int, int, error) {
			t1, i1, e := scanOSSSecurityPolicies(ctx, client, acct, region, st, scanID, types.SecurityPolicyTypeEncryption)
			if e != nil {
				return t1, i1, e
			}
			t2, i2, e := scanOSSSecurityPolicies(ctx, client, acct, region, st, scanID, types.SecurityPolicyTypeNetwork)
			return t1 + t2, i1 + i2, e
		},
		func() (int, int, error) {
			var t, i int
			for _, st_ := range types.SecurityConfigType("").Values() {
				tt, ii, e := scanOSSSecurityConfigs(ctx, client, acct, region, st, scanID, st_)
				if e != nil {
					return t, i, e
				}
				t += tt
				i += ii
			}
			return t, i, nil
		},
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

func scanOSSCollections(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := opensearchserverless.NewListCollectionsPaginator(client, &opensearchserverless.ListCollectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListCollections", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListCollections: %w", perr)
		}
		for _, c := range out.CollectionSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSCollection, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss collections")
}

func scanOSSCollectionGroups(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := opensearchserverless.NewListCollectionGroupsPaginator(client, &opensearchserverless.ListCollectionGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListCollectionGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListCollectionGroups: %w", perr)
		}
		for _, g := range out.CollectionGroupSummaries {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSCollectionGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss collection-groups")
}

func scanOSSVpcEndpoints(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := opensearchserverless.NewListVpcEndpointsPaginator(client, &opensearchserverless.ListVpcEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListVpcEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListVpcEndpoints: %w", perr)
		}
		// Collect IDs first then BatchGetVpcEndpoint (chunks of 5 — AWS limit)
		// to expose VpcId/SubnetIds/SecurityGroupIds for the resolver.
		var ids []string
		for _, v := range out.VpcEndpointSummaries {
			if id := sv(v.Id); id != "" {
				ids = append(ids, id)
			}
		}
		bodies := make(map[string]any, len(ids))
		for i := 0; i < len(ids); i += 5 {
			end := i + 5
			if end > len(ids) {
				end = len(ids)
			}
			bout, berr := client.BatchGetVpcEndpoint(ctx, &opensearchserverless.BatchGetVpcEndpointInput{Ids: ids[i:end]})
			if berr != nil {
				continue
			}
			for _, d := range bout.VpcEndpointDetails {
				bodies[sv(d.Id)] = d
			}
		}
		for _, v := range out.VpcEndpointSummaries {
			id := sv(v.Id)
			if id == "" {
				continue
			}
			arn := ossARN(region, acct.ID, "vpce", id)
			label := sv(v.Name)
			if label == "" {
				label = id
			}
			attrsJSON := mustJSON(v)
			if body, ok := bodies[id]; ok {
				attrsJSON = mustJSON(body)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSVpcEndpoint, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss vpc-endpoints")
}

func scanOSSAccessPolicies(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string, ptype types.AccessPolicyType) (int, int, error) {
	pager := opensearchserverless.NewListAccessPoliciesPaginator(client, &opensearchserverless.ListAccessPoliciesInput{Type: ptype})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListAccessPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListAccessPolicies: %w", perr)
		}
		for _, p := range out.AccessPolicySummaries {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			arn := ossARN(region, acct.ID, "access-policy", fmt.Sprintf("%s/%s", string(ptype), name))
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSAccessPolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss access-policies")
}

func scanOSSLifecyclePolicies(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string, ptype types.LifecyclePolicyType) (int, int, error) {
	pager := opensearchserverless.NewListLifecyclePoliciesPaginator(client, &opensearchserverless.ListLifecyclePoliciesInput{Type: ptype})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListLifecyclePolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListLifecyclePolicies: %w", perr)
		}
		for _, p := range out.LifecyclePolicySummaries {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			arn := ossARN(region, acct.ID, "lifecycle-policy", fmt.Sprintf("%s/%s", string(ptype), name))
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSLifecyclePolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss lifecycle-policies")
}

func scanOSSSecurityPolicies(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string, ptype types.SecurityPolicyType) (int, int, error) {
	pager := opensearchserverless.NewListSecurityPoliciesPaginator(client, &opensearchserverless.ListSecurityPoliciesInput{Type: ptype})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListSecurityPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListSecurityPolicies: %w", perr)
		}
		for _, p := range out.SecurityPolicySummaries {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			arn := ossARN(region, acct.ID, "security-policy", fmt.Sprintf("%s/%s", string(ptype), name))
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSSecurityPolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss security-policies")
}

func scanOSSSecurityConfigs(ctx context.Context, client ossAPI, acct *account, region string, st *store.Store, scanID string, ctype types.SecurityConfigType) (int, int, error) {
	pager := opensearchserverless.NewListSecurityConfigsPaginator(client, &opensearchserverless.ListSecurityConfigsInput{Type: ctype})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "aoss:ListSecurityConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("aoss:ListSecurityConfigs: %w", perr)
		}
		for _, c := range out.SecurityConfigSummaries {
			id := sv(c.Id)
			if id == "" {
				continue
			}
			arn := ossARN(region, acct.ID, "security-config", fmt.Sprintf("%s/%s", string(ctype), id))
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOSSSecurityConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "aoss security-configs")
}
