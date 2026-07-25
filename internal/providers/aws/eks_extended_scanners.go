package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/icearp/disco-cli/store"
)

// eksExtAPI lists ops for the seven per-cluster EKS resource types. ARNs
// synthesized as arn:aws:eks:{r}:{a}:{kind}/{cluster}/{key} since List*
// responses return only names/IDs.
type eksExtAPI interface {
	ListClusters(context.Context, *eks.ListClustersInput, ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	ListAccessEntries(context.Context, *eks.ListAccessEntriesInput, ...func(*eks.Options)) (*eks.ListAccessEntriesOutput, error)
	ListAddons(context.Context, *eks.ListAddonsInput, ...func(*eks.Options)) (*eks.ListAddonsOutput, error)
	ListCapabilities(context.Context, *eks.ListCapabilitiesInput, ...func(*eks.Options)) (*eks.ListCapabilitiesOutput, error)
	ListFargateProfiles(context.Context, *eks.ListFargateProfilesInput, ...func(*eks.Options)) (*eks.ListFargateProfilesOutput, error)
	ListIdentityProviderConfigs(context.Context, *eks.ListIdentityProviderConfigsInput, ...func(*eks.Options)) (*eks.ListIdentityProviderConfigsOutput, error)
	ListNodegroups(context.Context, *eks.ListNodegroupsInput, ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error)
	ListPodIdentityAssociations(context.Context, *eks.ListPodIdentityAssociationsInput, ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error)
}

func eksChildARN(region, acct, kind, cluster, key string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:%s/%s/%s", region, acct, kind, cluster, key)
}

func scanEKSExtended(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var clusterNames []string
	pager := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("eks:ListClusters(ext): %w", perr)
		}
		clusterNames = append(clusterNames, out.Clusters...)
	}

	for _, c := range clusterNames {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanEKSAccessEntries(ctx, client, acct, region, st, scanID, c) },
			func() (int, int, error) { return scanEKSAddons(ctx, client, acct, region, st, scanID, c) },
			func() (int, int, error) { return scanEKSCapabilities(ctx, client, acct, region, st, scanID, c) },
			func() (int, int, error) { return scanEKSFargateProfiles(ctx, client, acct, region, st, scanID, c) },
			func() (int, int, error) {
				return scanEKSIdentityProviderConfigs(ctx, client, acct, region, st, scanID, c)
			},
			func() (int, int, error) { return scanEKSNodegroups(ctx, client, acct, region, st, scanID, c) },
			func() (int, int, error) {
				return scanEKSPodIdentityAssociations(ctx, client, acct, region, st, scanID, c)
			},
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

func scanEKSAccessEntries(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListAccessEntriesPaginator(client, &eks.ListAccessEntriesInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListAccessEntries", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListAccessEntries: %w", perr)
		}
		for _, principalARN := range out.AccessEntries {
			if principalARN == "" {
				continue
			}
			arn := eksChildARN(region, acct.ID, "access-entry", cluster, principalARN)
			label := principalARN
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSAccessEntry, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterName": cluster, "PrincipalArn": principalARN}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks access-entries")
}

func scanEKSAddons(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListAddonsPaginator(client, &eks.ListAddonsInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListAddons", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListAddons: %w", perr)
		}
		for _, name := range out.Addons {
			if name == "" {
				continue
			}
			arn := eksChildARN(region, acct.ID, "addon", cluster, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSAddon, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterName": cluster, "AddonName": name}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks addons")
}

func scanEKSCapabilities(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListCapabilitiesPaginator(client, &eks.ListCapabilitiesInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListCapabilities", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListCapabilities: %w", perr)
		}
		for _, c := range out.Capabilities {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.CapabilityName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSCapability, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks capabilities")
}

func scanEKSFargateProfiles(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListFargateProfilesPaginator(client, &eks.ListFargateProfilesInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListFargateProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListFargateProfiles: %w", perr)
		}
		for _, name := range out.FargateProfileNames {
			if name == "" {
				continue
			}
			arn := eksChildARN(region, acct.ID, "fargateprofile", cluster, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSFargateProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterName": cluster, "FargateProfileName": name}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks fargate-profiles")
}

func scanEKSIdentityProviderConfigs(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListIdentityProviderConfigsPaginator(client, &eks.ListIdentityProviderConfigsInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListIdentityProviderConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListIdentityProviderConfigs: %w", perr)
		}
		for _, c := range out.IdentityProviderConfigs {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			ipcType := sv(c.Type)
			arn := eksChildARN(region, acct.ID, "identityproviderconfig", cluster, ipcType+"/"+name)
			label := name
			rec := types.IdentityProviderConfig{Name: &name, Type: c.Type}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSIdentityProviderConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(rec), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks identity-provider-configs")
}

func scanEKSNodegroups(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListNodegroupsPaginator(client, &eks.ListNodegroupsInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListNodegroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListNodegroups: %w", perr)
		}
		for _, name := range out.Nodegroups {
			if name == "" {
				continue
			}
			arn := eksChildARN(region, acct.ID, "nodegroup", cluster, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSNodegroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterName": cluster, "NodegroupName": name}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks nodegroups")
}

func scanEKSPodIdentityAssociations(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID, cluster string) (int, int, error) {
	cn := cluster
	pager := eks.NewListPodIdentityAssociationsPaginator(client, &eks.ListPodIdentityAssociationsInput{ClusterName: &cn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "eks:ListPodIdentityAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("eks:ListPodIdentityAssociations: %w", perr)
		}
		for _, a := range out.Associations {
			arn := sv(a.AssociationArn)
			if arn == "" {
				continue
			}
			label := sv(a.AssociationId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSPodIdentityAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks pod-identity-associations")
}
