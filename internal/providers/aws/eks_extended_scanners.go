package aws

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/icearp/disco-cli/store"
)

// eksExtAPI lists the ops of the seven per-cluster listings and of the reads
// of their rows' tags.
type eksExtAPI interface {
	ListAccessEntries(context.Context, *eks.ListAccessEntriesInput, ...func(*eks.Options)) (*eks.ListAccessEntriesOutput, error)
	ListAddons(context.Context, *eks.ListAddonsInput, ...func(*eks.Options)) (*eks.ListAddonsOutput, error)
	ListCapabilities(context.Context, *eks.ListCapabilitiesInput, ...func(*eks.Options)) (*eks.ListCapabilitiesOutput, error)
	ListFargateProfiles(context.Context, *eks.ListFargateProfilesInput, ...func(*eks.Options)) (*eks.ListFargateProfilesOutput, error)
	ListIdentityProviderConfigs(context.Context, *eks.ListIdentityProviderConfigsInput, ...func(*eks.Options)) (*eks.ListIdentityProviderConfigsOutput, error)
	ListNodegroups(context.Context, *eks.ListNodegroupsInput, ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error)
	ListPodIdentityAssociations(context.Context, *eks.ListPodIdentityAssociationsInput, ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error)
	DescribeAccessEntry(context.Context, *eks.DescribeAccessEntryInput, ...func(*eks.Options)) (*eks.DescribeAccessEntryOutput, error)
	DescribeAddon(context.Context, *eks.DescribeAddonInput, ...func(*eks.Options)) (*eks.DescribeAddonOutput, error)
	DescribeFargateProfile(context.Context, *eks.DescribeFargateProfileInput, ...func(*eks.Options)) (*eks.DescribeFargateProfileOutput, error)
	DescribeIdentityProviderConfig(context.Context, *eks.DescribeIdentityProviderConfigInput, ...func(*eks.Options)) (*eks.DescribeIdentityProviderConfigOutput, error)
	DescribeNodegroup(context.Context, *eks.DescribeNodegroupInput, ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error)
	DescribePodIdentityAssociation(context.Context, *eks.DescribePodIdentityAssociationInput, ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error)
	ListTagsForResource(context.Context, *eks.ListTagsForResourceInput, ...func(*eks.Options)) (*eks.ListTagsForResourceOutput, error)
}

// eksNotFound is the code every per-cluster listing but ListCapabilities, and
// every Describe read here, models for a cluster or resource that does not
// exist.
var eksNotFound = []string{"ResourceNotFoundException"}

// errEKSNoBody fails a tag read whose Describe answered without the resource,
// so that the row is not read as untagged.
var errEKSNoBody = errors.New("the response carried no resource")

// eksChildARN is the NativeID of a resource its per-cluster listing returns
// by name alone: {kind}/{cluster}/{key} after the cluster ARN's prefix. It is
// not the resource's own ARN, which ends in an id only a Describe returns.
func eksChildARN(partition, region, acct, kind, cluster, key string) string {
	return arnFor(partition, "eks", region, acct, kind+"/"+cluster+"/"+key)
}

// eksChildKey returns the cluster and key of a NativeID eksChildARN built
// with kind. A cluster name holds no "/" (CreateCluster's name pattern), so
// the first "/" after the kind ends it and the key is the rest, which may
// hold "/" and ":" (an IAM principal ARN). Any other string gives empty parts:
// the SDK refuses to send an empty cluster name, so the read fails visibly.
func eksChildKey(nativeID, kind string) (cluster, key string) {
	a, ok := parseARN(nativeID)
	if !ok {
		return "", ""
	}
	rest, ok := strings.CutPrefix(a.Resource, kind+"/")
	if !ok {
		return "", ""
	}
	cluster, key, _ = strings.Cut(rest, "/")
	return cluster, key
}

func eksClusterARN(partition, region, acct, name string) string {
	return arnFor(partition, "eks", region, acct, "cluster/"+name)
}

// eksTags reads a capability row's tags with ListTagsForResource, sent the
// row's NativeID, the ARN its list element carries; the Service Reference
// lists capability under that op. NotFoundException, the op's modeled
// not-found, drops a resource deleted after it was listed.
func eksTags(client eksExtAPI) rowTagReader {
	return rowTagReader{
		op:       "eks:ListTagsForResource",
		notFound: []string{"NotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &eks.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// eksDescribeTags reads the tags of a row whose listing returned the resource
// by name alone, so the row carries no ARN for ListTagsForResource: describe,
// whose op is op, is sent the cluster and key of the row's NativeID
// (eksChildKey with kind) and returns the resource's Tags. The Service
// Reference authorizes each of these Describes on the resource itself.
// ResourceNotFoundException, which each models, drops a resource deleted
// after it was listed.
func eksDescribeTags(op, kind string, describe func(ctx context.Context, cluster, key string) (map[string]string, error)) rowTagReader {
	return rowTagReader{
		op:       op,
		notFound: eksNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			cluster, key := eksChildKey(r.NativeID, kind)
			tags, err := describe(ctx, cluster, key)
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(tags), nil
		},
	}
}

// eksNamedRow is the row of a resource its per-cluster listing returns by
// name alone: NativeID eksChildARN(kind, cluster, name), named name, its
// attributes the cluster's name and the name under nameKey. A resource listed
// with an empty name is not stored; blank says so in that warning.
func eksNamedRow(acct *account, region, scanID, typ, kind, nameKey, blank, cluster, name string) (*store.Resource, string, string) {
	if name == "" {
		return nil, blank, "(no name) in cluster " + cluster
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: eksChildARN(acct.partition(), region, acct.ID, kind, cluster, name),
		Name: &name, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterName": cluster, nameKey: name}), DiscoveredBy: scanID,
	}, "", ""
}

// scanEKSAccessEntries stores each cluster's access entries with the tags
// DescribeAccessEntry returns, each page as soon as its tags are read, and
// returns the entries stored, the parents of the associated access policies.
// One cluster's failure costs the others none of theirs.
func scanEKSAccessEntries(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) ([]eksAccessEntryRef, bool) {
	var entries []eksAccessEntryRef
	failed := storeEachParentPagesTagsWith(ctx, st, acct, region, "eks:ListAccessEntries", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]string, *string, error) {
			out, err := client.ListAccessEntries(ctx, &eks.ListAccessEntriesInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessEntries, out.NextToken, nil
		},
		func(cluster, principal string) (*store.Resource, string, string) {
			return eksNamedRow(acct, region, scanID, TypeEKSAccessEntry, "access-entry", "PrincipalArn", "access entries have an empty principal ARN", cluster, principal)
		},
		nil,
		func(rows []*store.Resource) {
			for _, r := range rows {
				cluster, principal := eksChildKey(r.NativeID, "access-entry")
				entries = append(entries, eksAccessEntryRef{cluster: cluster, principalARN: principal, nativeID: r.NativeID})
			}
		},
		eksDescribeTags("eks:DescribeAccessEntry", "access-entry", func(ctx context.Context, cluster, principal string) (map[string]string, error) {
			out, err := client.DescribeAccessEntry(ctx, &eks.DescribeAccessEntryInput{ClusterName: &cluster, PrincipalArn: &principal})
			if err != nil {
				return nil, err
			}
			if out.AccessEntry == nil {
				return nil, errEKSNoBody
			}
			return out.AccessEntry.Tags, nil
		}), p)
	return entries, failed
}

// scanEKSAddons stores each cluster's add-ons with the tags DescribeAddon
// returns, each page as soon as its tags are read; one cluster's failure
// costs the others none of theirs.
func scanEKSAddons(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListAddons", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]string, *string, error) {
			out, err := client.ListAddons(ctx, &eks.ListAddonsInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Addons, out.NextToken, nil
		},
		func(cluster, name string) (*store.Resource, string, string) {
			return eksNamedRow(acct, region, scanID, TypeEKSAddon, "addon", "AddonName", "add-ons have an empty name", cluster, name)
		},
		eksDescribeTags("eks:DescribeAddon", "addon", func(ctx context.Context, cluster, name string) (map[string]string, error) {
			out, err := client.DescribeAddon(ctx, &eks.DescribeAddonInput{ClusterName: &cluster, AddonName: &name})
			if err != nil {
				return nil, err
			}
			if out.Addon == nil {
				return nil, errEKSNoBody
			}
			return out.Addon.Tags, nil
		}), p)
}

// scanEKSCapabilities stores each cluster's capabilities with their tags
// (eksTags), each page as soon as its tags are read; one cluster's failure
// costs the others none of theirs. ListCapabilities models no not-found, so a
// cluster deleted since it was listed fails its listing.
func scanEKSCapabilities(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListCapabilities", nil, clusters,
		func(ctx context.Context, cluster string, token *string) ([]types.CapabilitySummary, *string, error) {
			out, err := client.ListCapabilities(ctx, &eks.ListCapabilitiesInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Capabilities, out.NextToken, nil
		},
		func(cluster string, c types.CapabilitySummary) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return nil, "capabilities have an empty ARN", cmp.Or(sv(c.CapabilityName), "(no name)") + " in cluster " + cluster
			}
			label := sv(c.CapabilityName)
			if label == "" {
				label = arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSCapability, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		}, eksTags(client), p)
}

// scanEKSFargateProfiles stores each cluster's Fargate profiles with the tags
// DescribeFargateProfile returns, each page as soon as its tags are read; one
// cluster's failure costs the others none of theirs.
func scanEKSFargateProfiles(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListFargateProfiles", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]string, *string, error) {
			out, err := client.ListFargateProfiles(ctx, &eks.ListFargateProfilesInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FargateProfileNames, out.NextToken, nil
		},
		func(cluster, name string) (*store.Resource, string, string) {
			return eksNamedRow(acct, region, scanID, TypeEKSFargateProfile, "fargateprofile", "FargateProfileName", "Fargate profiles have an empty name", cluster, name)
		},
		eksDescribeTags("eks:DescribeFargateProfile", "fargateprofile", func(ctx context.Context, cluster, name string) (map[string]string, error) {
			out, err := client.DescribeFargateProfile(ctx, &eks.DescribeFargateProfileInput{ClusterName: &cluster, FargateProfileName: &name})
			if err != nil {
				return nil, err
			}
			if out.FargateProfile == nil {
				return nil, errEKSNoBody
			}
			return out.FargateProfile.Tags, nil
		}), p)
}

// scanEKSIdentityProviderConfigs stores each cluster's identity provider
// configs with the tags DescribeIdentityProviderConfig returns, each page as
// soon as its tags are read; one cluster's failure costs the others none of
// theirs. A config's key is {type}/{name}; its type names the
// IdentityProviderConfigResponse member that carries the tags, and oidc is
// the only member the model defines.
func scanEKSIdentityProviderConfigs(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	const kind = "identityproviderconfig"
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListIdentityProviderConfigs", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]types.IdentityProviderConfig, *string, error) {
			out, err := client.ListIdentityProviderConfigs(ctx, &eks.ListIdentityProviderConfigsInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IdentityProviderConfigs, out.NextToken, nil
		},
		func(cluster string, c types.IdentityProviderConfig) (*store.Resource, string, string) {
			name := sv(c.Name)
			if name == "" {
				return nil, "identity provider configs have an empty name", "(no name) in cluster " + cluster
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSIdentityProviderConfig, NativeID: eksChildARN(acct.partition(), region, acct.ID, kind, cluster, sv(c.Type)+"/"+name),
				Name: &name, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		},
		eksDescribeTags("eks:DescribeIdentityProviderConfig", kind, func(ctx context.Context, cluster, key string) (map[string]string, error) {
			typ, name, _ := strings.Cut(key, "/")
			out, err := client.DescribeIdentityProviderConfig(ctx, &eks.DescribeIdentityProviderConfigInput{
				ClusterName: &cluster, IdentityProviderConfig: &types.IdentityProviderConfig{Type: &typ, Name: &name},
			})
			if err != nil {
				return nil, err
			}
			if out.IdentityProviderConfig == nil || out.IdentityProviderConfig.Oidc == nil {
				return nil, errEKSNoBody
			}
			return out.IdentityProviderConfig.Oidc.Tags, nil
		}), p)
}

// scanEKSNodegroups stores each cluster's managed node groups with the tags
// DescribeNodegroup returns, each page as soon as its tags are read; one
// cluster's failure costs the others none of theirs.
func scanEKSNodegroups(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListNodegroups", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]string, *string, error) {
			out, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Nodegroups, out.NextToken, nil
		},
		func(cluster, name string) (*store.Resource, string, string) {
			return eksNamedRow(acct, region, scanID, TypeEKSNodegroup, "nodegroup", "NodegroupName", "node groups have an empty name", cluster, name)
		},
		eksDescribeTags("eks:DescribeNodegroup", "nodegroup", func(ctx context.Context, cluster, name string) (map[string]string, error) {
			out, err := client.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{ClusterName: &cluster, NodegroupName: &name})
			if err != nil {
				return nil, err
			}
			if out.Nodegroup == nil {
				return nil, errEKSNoBody
			}
			return out.Nodegroup.Tags, nil
		}), p)
}

// eksPodIdentityKeys keeps beside each pod identity association row the two
// values DescribePodIdentityAssociation is sent: the cluster the row was
// listed under and the element's AssociationId. Neither is read back from the
// association's ARN: the Service Reference names its last segment a UUID,
// and nothing says that it is the AssociationId. The rows are built on the
// clusters' concurrent reads, hence mu; a tag read takes its row's entry, so
// the map does not grow with the whole listing.
type eksPodIdentityKeys struct {
	mu   sync.Mutex
	keys map[*store.Resource]eksPodIdentityKey
}

type eksPodIdentityKey struct{ cluster, id string }

func (k *eksPodIdentityKeys) keep(r *store.Resource, key eksPodIdentityKey) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.keys == nil {
		k.keys = map[*store.Resource]eksPodIdentityKey{}
	}
	k.keys[r] = key
}

func (k *eksPodIdentityKeys) take(r *store.Resource) eksPodIdentityKey {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := k.keys[r]
	delete(k.keys, r)
	return key
}

// tags reads a pod identity association row's tags with
// DescribePodIdentityAssociation, sent the row's kept cluster and
// AssociationId. The Service Reference authorizes that op on
// podidentityassociation but leaves the type out of ListTagsForResource.
// ResourceNotFoundException, which the op models, drops an association
// deleted after it was listed. An element listed without an AssociationId
// sends an empty one, which the SDK's serializer refuses, so the read fails
// visibly.
func (k *eksPodIdentityKeys) tags(client eksExtAPI) rowTagReader {
	return rowTagReader{
		op:       "eks:DescribePodIdentityAssociation",
		notFound: eksNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			key := k.take(r)
			out, err := client.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{ClusterName: &key.cluster, AssociationId: &key.id})
			if err != nil {
				return nil, err
			}
			if out.Association == nil {
				return nil, errEKSNoBody
			}
			return mapTagsJSON(out.Association.Tags), nil
		},
	}
}

// scanEKSPodIdentityAssociations stores each cluster's pod identity
// associations with their tags (eksPodIdentityKeys.tags), each page as soon
// as its tags are read; one cluster's failure costs the others none of
// theirs.
func scanEKSPodIdentityAssociations(ctx context.Context, client eksExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, p *phaseTotals) bool {
	var keys eksPodIdentityKeys
	return storeEachParentPagesTags(ctx, st, acct, region, "eks:ListPodIdentityAssociations", eksNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]types.PodIdentityAssociationSummary, *string, error) {
			out, err := client.ListPodIdentityAssociations(ctx, &eks.ListPodIdentityAssociationsInput{ClusterName: &cluster, NextToken: token, MaxResults: sdkaws.Int32(eksPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Associations, out.NextToken, nil
		},
		func(cluster string, a types.PodIdentityAssociationSummary) (*store.Resource, string, string) {
			arn := sv(a.AssociationArn)
			if arn == "" {
				return nil, "pod identity associations have an empty ARN", cmp.Or(sv(a.AssociationId), "(no id)") + " in cluster " + cluster
			}
			label := sv(a.AssociationId)
			if label == "" {
				label = arn
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSPodIdentityAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
			keys.keep(r, eksPodIdentityKey{cluster: cluster, id: sv(a.AssociationId)})
			return r, "", ""
		}, keys.tags(client), p)
}
