package aws

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// cloudfrontAPI is the narrow set of CloudFront operations called by the
// scanCloudFront families.
type cloudfrontAPI interface {
	ListDistributions(context.Context, *cloudfront.ListDistributionsInput, ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
	ListStreamingDistributions(context.Context, *cloudfront.ListStreamingDistributionsInput, ...func(*cloudfront.Options)) (*cloudfront.ListStreamingDistributionsOutput, error)
	ListDistributionTenants(context.Context, *cloudfront.ListDistributionTenantsInput, ...func(*cloudfront.Options)) (*cloudfront.ListDistributionTenantsOutput, error)
	ListCloudFrontOriginAccessIdentities(context.Context, *cloudfront.ListCloudFrontOriginAccessIdentitiesInput, ...func(*cloudfront.Options)) (*cloudfront.ListCloudFrontOriginAccessIdentitiesOutput, error)
	ListOriginAccessControls(context.Context, *cloudfront.ListOriginAccessControlsInput, ...func(*cloudfront.Options)) (*cloudfront.ListOriginAccessControlsOutput, error)
	ListConnectionFunctions(context.Context, *cloudfront.ListConnectionFunctionsInput, ...func(*cloudfront.Options)) (*cloudfront.ListConnectionFunctionsOutput, error)
	ListConnectionGroups(context.Context, *cloudfront.ListConnectionGroupsInput, ...func(*cloudfront.Options)) (*cloudfront.ListConnectionGroupsOutput, error)
	ListKeyValueStores(context.Context, *cloudfront.ListKeyValueStoresInput, ...func(*cloudfront.Options)) (*cloudfront.ListKeyValueStoresOutput, error)
	ListPublicKeys(context.Context, *cloudfront.ListPublicKeysInput, ...func(*cloudfront.Options)) (*cloudfront.ListPublicKeysOutput, error)
	ListTrustStores(context.Context, *cloudfront.ListTrustStoresInput, ...func(*cloudfront.Options)) (*cloudfront.ListTrustStoresOutput, error)
	ListAnycastIpLists(context.Context, *cloudfront.ListAnycastIpListsInput, ...func(*cloudfront.Options)) (*cloudfront.ListAnycastIpListsOutput, error)
	ListCachePolicies(context.Context, *cloudfront.ListCachePoliciesInput, ...func(*cloudfront.Options)) (*cloudfront.ListCachePoliciesOutput, error)
	ListContinuousDeploymentPolicies(context.Context, *cloudfront.ListContinuousDeploymentPoliciesInput, ...func(*cloudfront.Options)) (*cloudfront.ListContinuousDeploymentPoliciesOutput, error)
	ListFunctions(context.Context, *cloudfront.ListFunctionsInput, ...func(*cloudfront.Options)) (*cloudfront.ListFunctionsOutput, error)
	ListKeyGroups(context.Context, *cloudfront.ListKeyGroupsInput, ...func(*cloudfront.Options)) (*cloudfront.ListKeyGroupsOutput, error)
	ListOriginRequestPolicies(context.Context, *cloudfront.ListOriginRequestPoliciesInput, ...func(*cloudfront.Options)) (*cloudfront.ListOriginRequestPoliciesOutput, error)
	ListRealtimeLogConfigs(context.Context, *cloudfront.ListRealtimeLogConfigsInput, ...func(*cloudfront.Options)) (*cloudfront.ListRealtimeLogConfigsOutput, error)
	ListResponseHeadersPolicies(context.Context, *cloudfront.ListResponseHeadersPoliciesInput, ...func(*cloudfront.Options)) (*cloudfront.ListResponseHeadersPoliciesOutput, error)
	ListVpcOrigins(context.Context, *cloudfront.ListVpcOriginsInput, ...func(*cloudfront.Options)) (*cloudfront.ListVpcOriginsOutput, error)
	ListFieldLevelEncryptionConfigs(context.Context, *cloudfront.ListFieldLevelEncryptionConfigsInput, ...func(*cloudfront.Options)) (*cloudfront.ListFieldLevelEncryptionConfigsOutput, error)
	ListFieldLevelEncryptionProfiles(context.Context, *cloudfront.ListFieldLevelEncryptionProfilesInput, ...func(*cloudfront.Options)) (*cloudfront.ListFieldLevelEncryptionProfilesOutput, error)
	GetMonitoringSubscription(context.Context, *cloudfront.GetMonitoringSubscriptionInput, ...func(*cloudfront.Options)) (*cloudfront.GetMonitoringSubscriptionOutput, error)
	ListTagsForResource(context.Context, *cloudfront.ListTagsForResourceInput, ...func(*cloudfront.Options)) (*cloudfront.ListTagsForResourceOutput, error)
}

func init() {
	registerType(restype.Descriptor{Type: TypeCloudFrontDistribution, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontStreamingDistribution, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontDistributionTenant, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontOAI, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontOriginAccessControl, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontConnectionFunction, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontConnectionGroup, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontKeyValueStore, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontPublicKey, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontTrustStore, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontAnycastIPList, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontCachePolicy, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontContinuousDeploymentPolicy, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontFunction, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontKeyGroup, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontOriginRequestPolicy, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontRealtimeLogConfig, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontResponseHeadersPolicy, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontVpcOrigin, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontMonitoringSubscription, Service: "cloudfront"})
	registerType(restype.Descriptor{Type: TypeCloudFrontFieldLevelEncryptionConfig, Service: "cloudfront", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontFieldLevelEncryptionProfile, Service: "cloudfront", Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:cloudfront",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanCloudFront(ctx, acct, st, scanID)
		},
	})
}

// scanCloudFront discovers every CloudFront resource type. CloudFront is
// account-wide: the client calls the partition's home region (homeRegion),
// and rows and warnings carry the region "global". The listings run as
// concurrent families (runFamilies): distributions then their monitoring
// subscriptions, and each other listing on its own. A family's error, a
// failed write included, does not stop the others; ctx's end is reported
// once. The types ListTagsForResource accepts have their tags read (cfTags),
// fanoutMed reads at a time across every family, and every tag denial of the
// scan is one warning.
func scanCloudFront(ctx context.Context, acct *account, st *store.Store, scanID string) (int, int, error) {
	home, err := acct.homeRegion("cloudfront")
	if err != nil {
		return 0, 0, err
	}
	client := cloudfront.NewFromConfig(acct.cfg, func(o *cloudfront.Options) { o.Region = home })
	region := sv(regionGlobal)
	tags := cfTags(client).boundedBy(make(chan struct{}, fanoutMed)).sharingDenials()
	p := runFamilies(ctx, "cloudfront",
		phaseFamily(func(p *phaseTotals) {
			dists := scanCloudFrontDistributions(ctx, client, acct, region, st, scanID, tags, p)
			total, inserted, err := scanCloudFrontMonitoringSubscriptions(ctx, client, acct, region, st, scanID, dists)
			p.addPhase(cfMonitoringOp, total, inserted, err)
		}),
		countedFamily("cloudfront:ListStreamingDistributions", func() (int, int, error) {
			return scanCloudFrontStreamingDistributions(ctx, acct, client, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) {
			scanCloudFrontDistributionTenants(ctx, client, acct, region, st, scanID, tags, p)
		}),
		countedFamily("cloudfront:ListCachePolicies", func() (int, int, error) {
			return scanCloudFrontCachePolicies(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListOriginRequestPolicies", func() (int, int, error) {
			return scanCloudFrontOriginRequestPolicies(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListResponseHeadersPolicies", func() (int, int, error) {
			return scanCloudFrontResponseHeadersPolicies(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListContinuousDeploymentPolicies", func() (int, int, error) {
			return scanCloudFrontContinuousDeploymentPolicies(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListCloudFrontOriginAccessIdentities", func() (int, int, error) {
			return scanCloudFrontOAIs(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListOriginAccessControls", func() (int, int, error) {
			return scanCloudFrontOriginAccessControls(ctx, acct, client, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontFunctions(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) {
			scanCloudFrontConnectionFunctions(ctx, client, acct, region, st, scanID, tags, p)
		}),
		countedFamily("cloudfront:ListKeyGroups", func() (int, int, error) {
			return scanCloudFrontKeyGroups(ctx, acct, client, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontKeyValueStores(ctx, client, acct, region, st, scanID, tags, p) }),
		countedFamily("cloudfront:ListPublicKeys", func() (int, int, error) {
			return scanCloudFrontPublicKeys(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListRealtimeLogConfigs", func() (int, int, error) {
			return scanCloudFrontRealtimeLogConfigs(ctx, acct, client, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontTrustStores(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontConnectionGroups(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontAnycastIPLists(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanCloudFrontVpcOrigins(ctx, client, acct, region, st, scanID, tags, p) }),
		countedFamily("cloudfront:ListFieldLevelEncryptionConfigs", func() (int, int, error) {
			return scanCloudFrontFieldLevelEncryptionConfigs(ctx, acct, client, st, scanID)
		}),
		countedFamily("cloudfront:ListFieldLevelEncryptionProfiles", func() (int, int, error) {
			return scanCloudFrontFieldLevelEncryptionProfiles(ctx, acct, client, st, scanID)
		}),
	)
	tags.warnDenials(st, acct, region)
	p.foldStop(ctx, "cloudfront", 0)
	// Every family calls the one CloudFront host, so once any stored rows an
	// NXDOMAIN is a failure of this scan, not a missing endpoint.
	return p.resultLateNXDOMAIN(st, acct, region)
}

// cfMarkerScan abstracts Marker-based pagination for CloudFront list APIs that
// lack a dedicated SDK paginator. listFn returns (output, nextMarker, error);
// when nextMarker is nil there are no more pages. processFn returns (total, inserted, error).
func cfMarkerScan[Output any](
	ctx context.Context,
	opName string,
	st *store.Store,
	listFn func(ctx context.Context, marker *string) (Output, *string, error),
	processFn func(Output) (int, int, error),
) (total, inserted int, err error) {
	var marker *string
	for {
		out, next, apiErr := listFn(ctx, marker)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return total, inserted, skipIfAccessDenied(st, opName, "", "global", apiErr)
			}
			return total, inserted, fmt.Errorf("%s: %w", opName, apiErr)
		}
		tt, nn, pErr := processFn(out)
		if pErr != nil {
			return total, inserted, pErr
		}
		total += tt
		inserted += nn
		if next == nil {
			return total, inserted, nil
		}
		marker = next
	}
}

// cfMonitoringOp reads one distribution's monitoring subscription.
const cfMonitoringOp = "cloudfront:GetMonitoringSubscription"

// cfMonitoringSkip holds the codes GetMonitoringSubscription models that skip
// a distribution silently: NoSuchMonitoringSubscription is a distribution
// without one, the usual state; NoSuchDistribution is one deleted since it
// was listed; UnsupportedOperation is one whose kind or partition offers no
// real-time metrics (which kinds, unverified live).
var cfMonitoringSkip = []string{"NoSuchMonitoringSubscription", "NoSuchDistribution", "UnsupportedOperation"}

// scanCloudFrontMonitoringSubscriptions stores the real-time metrics
// subscription of each distribution the distribution phase stored this scan
// (dists), fanoutMed at a time (storeTokenEachParent); there is no list op. A
// distribution in cfMonitoringSkip is skipped silently; a denial warns once
// for the op; any other error is returned after the others' rows are stored.
// A subscription without a RealtimeMetricsSubscriptionConfig is not stored.
// The row's NativeID is the distribution's ARN plus a suffix, which keeps it
// apart from the distribution while the parent stays derivable; dists holds
// only distributions stored with an ARN and an id.
func scanCloudFrontMonitoringSubscriptions(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, dists []childParent) (int, int, error) {
	arns := make(map[string]string, len(dists))
	for _, d := range dists {
		arns[d.id] = d.arn
	}
	return storeTokenEachParent(ctx, st, acct, region, cfMonitoringOp, cfMonitoringSkip, childParentIDs(dists),
		func(ctx context.Context, id string, _ *string) ([]cftypes.MonitoringSubscription, *string, error) {
			out, err := client.GetMonitoringSubscription(ctx, &cloudfront.GetMonitoringSubscriptionInput{DistributionId: &id})
			if err != nil {
				return nil, nil, err
			}
			if out.MonitoringSubscription == nil || out.MonitoringSubscription.RealtimeMetricsSubscriptionConfig == nil {
				return nil, nil, nil
			}
			return []cftypes.MonitoringSubscription{*out.MonitoringSubscription}, nil, nil
		},
		func(id string, it cftypes.MonitoringSubscription) (*store.Resource, string, string) {
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name, Region: regionGlobal,
				Type: TypeCloudFrontMonitoringSubscription, NativeID: arns[id] + "/monitoring-subscription",
				Name: sp(id), AttributesJSON: mustJSON(it), DiscoveredBy: scanID,
			}, "", ""
		})
}

// --- Streaming Distributions ---

// scanCloudFrontStreamingDistributions discovers RTMP streaming distributions.
func scanCloudFrontStreamingDistributions(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListStreamingDistributionsPaginator(client, &cloudfront.ListStreamingDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListStreamingDistributions", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListStreamingDistributions: %w", err)
		}
		if page.StreamingDistributionList == nil {
			continue
		}
		var batch []*store.Resource
		for _, d := range page.StreamingDistributionList.Items {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontStreamingDistribution,
				NativeID:       sv(d.ARN),
				Name:           d.Id,
				CreatedAt:      tp(d.LastModifiedTime),
				Status:         d.Status,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront streaming distributions: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Cache Policies ---

// scanCloudFrontCachePolicies discovers cache policies (custom + AWS-managed
// catalogue). Each summary carries `Type=managed|custom`; managed entries
// flag ManagedByProvider=true so they're hidden from default list/graph
// output but available for explicit lookup + edge resolution.
func scanCloudFrontCachePolicies(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListCachePolicies", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListCachePoliciesOutput, *string, error) {
			out, err := client.ListCachePolicies(ctx, &cloudfront.ListCachePoliciesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.CachePolicyList == nil {
				return out, nil, nil
			}
			return out, out.CachePolicyList.NextMarker, nil
		},
		func(out *cloudfront.ListCachePoliciesOutput) (int, int, error) {
			if out.CachePolicyList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.CachePolicyList.Items {
				if s.CachePolicy == nil {
					continue
				}
				cp := s.CachePolicy
				var name *string
				if cp.CachePolicyConfig != nil {
					name = cp.CachePolicyConfig.Name
				}
				batch = append(batch, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Region:            regionGlobal,
					Type:              TypeCloudFrontCachePolicy,
					NativeID:          sv(cp.Id),
					Name:              name,
					CreatedAt:         tp(cp.LastModifiedTime),
					AttributesJSON:    mustJSON(s),
					DiscoveredBy:      scanID,
					ManagedByProvider: s.Type == cftypes.CachePolicyTypeManaged,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Origin Request Policies ---

// scanCloudFrontOriginRequestPolicies discovers origin-request policies
// (custom + AWS-managed catalogue). Managed entries flag
// ManagedByProvider=true via the per-summary Type field.
func scanCloudFrontOriginRequestPolicies(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListOriginRequestPolicies", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListOriginRequestPoliciesOutput, *string, error) {
			out, err := client.ListOriginRequestPolicies(ctx, &cloudfront.ListOriginRequestPoliciesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.OriginRequestPolicyList == nil {
				return out, nil, nil
			}
			return out, out.OriginRequestPolicyList.NextMarker, nil
		},
		func(out *cloudfront.ListOriginRequestPoliciesOutput) (int, int, error) {
			if out.OriginRequestPolicyList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.OriginRequestPolicyList.Items {
				if s.OriginRequestPolicy == nil {
					continue
				}
				p := s.OriginRequestPolicy
				var name *string
				if p.OriginRequestPolicyConfig != nil {
					name = p.OriginRequestPolicyConfig.Name
				}
				batch = append(batch, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Region:            regionGlobal,
					Type:              TypeCloudFrontOriginRequestPolicy,
					NativeID:          sv(p.Id),
					Name:              name,
					CreatedAt:         tp(p.LastModifiedTime),
					AttributesJSON:    mustJSON(s),
					DiscoveredBy:      scanID,
					ManagedByProvider: s.Type == cftypes.OriginRequestPolicyTypeManaged,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Response Headers Policies ---

// scanCloudFrontResponseHeadersPolicies discovers response-headers policies
// (custom + AWS-managed catalogue). Managed entries flag
// ManagedByProvider=true via the per-summary Type field.
func scanCloudFrontResponseHeadersPolicies(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListResponseHeadersPolicies", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListResponseHeadersPoliciesOutput, *string, error) {
			out, err := client.ListResponseHeadersPolicies(ctx, &cloudfront.ListResponseHeadersPoliciesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.ResponseHeadersPolicyList == nil {
				return out, nil, nil
			}
			return out, out.ResponseHeadersPolicyList.NextMarker, nil
		},
		func(out *cloudfront.ListResponseHeadersPoliciesOutput) (int, int, error) {
			if out.ResponseHeadersPolicyList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.ResponseHeadersPolicyList.Items {
				if s.ResponseHeadersPolicy == nil {
					continue
				}
				p := s.ResponseHeadersPolicy
				var name *string
				if p.ResponseHeadersPolicyConfig != nil {
					name = p.ResponseHeadersPolicyConfig.Name
				}
				batch = append(batch, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Region:            regionGlobal,
					Type:              TypeCloudFrontResponseHeadersPolicy,
					NativeID:          sv(p.Id),
					Name:              name,
					CreatedAt:         tp(p.LastModifiedTime),
					AttributesJSON:    mustJSON(s),
					DiscoveredBy:      scanID,
					ManagedByProvider: s.Type == cftypes.ResponseHeadersPolicyTypeManaged,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Continuous Deployment Policies ---

// scanCloudFrontContinuousDeploymentPolicies discovers continuous deployment policies.
func scanCloudFrontContinuousDeploymentPolicies(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListContinuousDeploymentPolicies", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListContinuousDeploymentPoliciesOutput, *string, error) {
			out, err := client.ListContinuousDeploymentPolicies(ctx, &cloudfront.ListContinuousDeploymentPoliciesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.ContinuousDeploymentPolicyList == nil {
				return out, nil, nil
			}
			return out, out.ContinuousDeploymentPolicyList.NextMarker, nil
		},
		func(out *cloudfront.ListContinuousDeploymentPoliciesOutput) (int, int, error) {
			if out.ContinuousDeploymentPolicyList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.ContinuousDeploymentPolicyList.Items {
				if s.ContinuousDeploymentPolicy == nil {
					continue
				}
				p := s.ContinuousDeploymentPolicy
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontContinuousDeploymentPolicy,
					NativeID:       sv(p.Id),
					CreatedAt:      tp(p.LastModifiedTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Origin Access Identities (legacy) ---

// scanCloudFrontOAIs discovers CloudFront origin access identities (legacy OAI).
func scanCloudFrontOAIs(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListCloudFrontOriginAccessIdentitiesPaginator(client, &cloudfront.ListCloudFrontOriginAccessIdentitiesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListCloudFrontOriginAccessIdentities", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListCloudFrontOriginAccessIdentities: %w", err)
		}
		if page.CloudFrontOriginAccessIdentityList == nil {
			continue
		}
		var batch []*store.Resource
		for _, o := range page.CloudFrontOriginAccessIdentityList.Items {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontOAI,
				NativeID:       sv(o.Id),
				AttributesJSON: mustJSON(o),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront OAIs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Origin Access Controls ---

// scanCloudFrontOriginAccessControls discovers origin access controls (modern OAI replacement).
func scanCloudFrontOriginAccessControls(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListOriginAccessControlsPaginator(client, &cloudfront.ListOriginAccessControlsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListOriginAccessControls", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListOriginAccessControls: %w", err)
		}
		if page.OriginAccessControlList == nil {
			continue
		}
		var batch []*store.Resource
		for _, o := range page.OriginAccessControlList.Items {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontOriginAccessControl,
				NativeID:       sv(o.Id),
				Name:           o.Name,
				AttributesJSON: mustJSON(o),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront origin access controls: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Key Groups ---

// scanCloudFrontKeyGroups discovers key groups used to sign URLs/cookies.
func scanCloudFrontKeyGroups(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListKeyGroups", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListKeyGroupsOutput, *string, error) {
			out, err := client.ListKeyGroups(ctx, &cloudfront.ListKeyGroupsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.KeyGroupList == nil {
				return out, nil, nil
			}
			return out, out.KeyGroupList.NextMarker, nil
		},
		func(out *cloudfront.ListKeyGroupsOutput) (int, int, error) {
			if out.KeyGroupList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.KeyGroupList.Items {
				if s.KeyGroup == nil {
					continue
				}
				kg := s.KeyGroup
				var name *string
				if kg.KeyGroupConfig != nil {
					name = kg.KeyGroupConfig.Name
				}
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontKeyGroup,
					NativeID:       sv(kg.Id),
					Name:           name,
					CreatedAt:      tp(kg.LastModifiedTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Public Keys ---

// scanCloudFrontPublicKeys discovers public keys used with signed URLs/cookies.
func scanCloudFrontPublicKeys(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListPublicKeysPaginator(client, &cloudfront.ListPublicKeysInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListPublicKeys", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListPublicKeys: %w", err)
		}
		if page.PublicKeyList == nil {
			continue
		}
		var batch []*store.Resource
		for _, k := range page.PublicKeyList.Items {
			// PublicKeySummary.Name is the canonical name field.
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontPublicKey,
				NativeID:       sv(k.Id),
				Name:           k.Name,
				CreatedAt:      tp(k.CreatedTime),
				AttributesJSON: mustJSON(k),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront public keys: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Real-time Log Configs ---

// scanCloudFrontRealtimeLogConfigs discovers real-time log configurations.
func scanCloudFrontRealtimeLogConfigs(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListRealtimeLogConfigs", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListRealtimeLogConfigsOutput, *string, error) {
			out, err := client.ListRealtimeLogConfigs(ctx, &cloudfront.ListRealtimeLogConfigsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.RealtimeLogConfigs == nil {
				return out, nil, nil
			}
			return out, out.RealtimeLogConfigs.NextMarker, nil
		},
		func(out *cloudfront.ListRealtimeLogConfigsOutput) (int, int, error) {
			if out.RealtimeLogConfigs == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, c := range out.RealtimeLogConfigs.Items {
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontRealtimeLogConfig,
					NativeID:       sv(c.ARN),
					Name:           c.Name,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Field-Level Encryption ---

// scanCloudFrontFieldLevelEncryptionConfigs discovers FLE configs. The summary
// carries the profile references (ContentTypeProfileConfig / QueryArgProfileConfig)
// the resolver wires to FLE profiles.
func scanCloudFrontFieldLevelEncryptionConfigs(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListFieldLevelEncryptionConfigs", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListFieldLevelEncryptionConfigsOutput, *string, error) {
			out, err := client.ListFieldLevelEncryptionConfigs(ctx, &cloudfront.ListFieldLevelEncryptionConfigsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.FieldLevelEncryptionList == nil {
				return out, nil, nil
			}
			return out, out.FieldLevelEncryptionList.NextMarker, nil
		},
		func(out *cloudfront.ListFieldLevelEncryptionConfigsOutput) (int, int, error) {
			if out.FieldLevelEncryptionList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.FieldLevelEncryptionList.Items {
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontFieldLevelEncryptionConfig,
					NativeID:       sv(s.Id),
					CreatedAt:      tp(s.LastModifiedTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// scanCloudFrontFieldLevelEncryptionProfiles discovers FLE profiles (the public-
// key/field-pattern mapping configs FLE configs reference).
func scanCloudFrontFieldLevelEncryptionProfiles(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListFieldLevelEncryptionProfiles", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListFieldLevelEncryptionProfilesOutput, *string, error) {
			out, err := client.ListFieldLevelEncryptionProfiles(ctx, &cloudfront.ListFieldLevelEncryptionProfilesInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.FieldLevelEncryptionProfileList == nil {
				return out, nil, nil
			}
			return out, out.FieldLevelEncryptionProfileList.NextMarker, nil
		},
		func(out *cloudfront.ListFieldLevelEncryptionProfilesOutput) (int, int, error) {
			if out.FieldLevelEncryptionProfileList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, s := range out.FieldLevelEncryptionProfileList.Items {
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontFieldLevelEncryptionProfile,
					NativeID:       sv(s.Id),
					Name:           s.Name,
					CreatedAt:      tp(s.LastModifiedTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return 0, 0, err
				}
				return len(batch), n, nil
			}
			return 0, 0, nil
		},
	)
}

// --- Types whose tags are read ---
//
// The Service Reference lists distribution, distribution-tenant, function,
// connection-function, key-value-store, trust-store, connection-group,
// anycast-ip-list and vpcorigin as ListTagsForResource resources, and no list
// element of these types carries tags. Streaming distributions are not read:
// the Service Reference lists streaming-distribution for TagResource but not
// for ListTagsForResource.
//
// No listing is sent MaxItems: the model gives the member no range, and no
// list op's documentation names a maximum.

// cfTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned, so the partition is the
// one CloudFront answered with. The op does not page; NoSuchResource is its
// modeled not-found.
func cfTags(client cloudfrontAPI) rowTagReader {
	return rowTagReader{
		op:       "cloudfront:ListTagsForResource",
		notFound: []string{"NoSuchResource"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &cloudfront.ListTagsForResourceInput{Resource: &arn})
			if err != nil {
				return nil, err
			}
			if out.Tags == nil {
				return nil, nil
			}
			return awsTagsJSON(out.Tags.Items), nil
		},
	}
}

// cfResource is the row of a listed element whose NativeID is arn, the ARN
// its listing returned.
func cfResource(acct *account, scanID, typ string, arn *string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name, Region: regionGlobal,
		Type: typ, NativeID: sv(arn), AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// cfRow returns r to store, or, when its listing returned no ARN, why it is
// not and the key naming it in the warning: key (an id or name), or a fixed
// text when key is empty. what names the type's elements.
func cfRow(r *store.Resource, what, key string) (*store.Resource, string, string) {
	if r.NativeID == "" {
		return nil, what + " have no ARN", cmp.Or(key, "(an element with no identifier)")
	}
	return r, "", ""
}

// scanCloudFrontDistributions stores the distributions with their tags and
// returns the stored ones, keyed by Id, the parents of the monitoring
// subscription phase. ListDistributions returns DistributionSummary items
// with the full configuration, so no GetDistribution call is needed.
func scanCloudFrontDistributions(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) []childParent {
	// A failed write is already in p's errors and returns no parents, so the
	// monitoring phase after it reads nothing.
	parents, _ := storeParentListTags(ctx, st, acct, region, "cloudfront:ListDistributions", "distributions", "Id",
		func(ctx context.Context, marker *string) ([]cftypes.DistributionSummary, *string, error) {
			out, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.DistributionList == nil {
				return nil, nil, nil
			}
			return out.DistributionList.Items, out.DistributionList.NextMarker, nil
		},
		func(it cftypes.DistributionSummary) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontDistribution, it.ARN, it)
			r.Name, r.CreatedAt, r.Status = it.Id, tp(it.LastModifiedTime), it.Status
			return cfRow(r, "distributions", sv(it.Id))
		},
		func(it cftypes.DistributionSummary) string { return sv(it.Id) },
		tags, p)
	return parents
}

// scanCloudFrontDistributionTenants stores the SaaS Manager distribution
// tenants with their tags.
func scanCloudFrontDistributionTenants(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListDistributionTenants",
		func(ctx context.Context, marker *string) ([]cftypes.DistributionTenantSummary, *string, error) {
			out, err := client.ListDistributionTenants(ctx, &cloudfront.ListDistributionTenantsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DistributionTenantList, out.NextMarker, nil
		},
		func(it cftypes.DistributionTenantSummary) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontDistributionTenant, it.Arn, it)
			r.Name, r.CreatedAt = it.Id, tp(it.CreatedTime)
			return cfRow(r, "distribution tenants", sv(it.Id))
		}, tags, p)
}

// cfFunctionAttrs is a function's stored attributes: its LIVE summary (or its
// only listed one) and, under DevelopmentStage, the DEVELOPMENT summary listed
// under the same ARN (cfStoreOneRowPerARN).
type cfFunctionAttrs struct {
	cftypes.FunctionSummary
	DevelopmentStage *cftypes.FunctionSummary `json:"DevelopmentStage,omitempty"`
}

// scanCloudFrontFunctions stores the CloudFront Functions with their tags,
// one row per ARN (cfStoreOneRowPerARN). A function's ARN and stage are in
// its FunctionMetadata.
func scanCloudFrontFunctions(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return cfStoreOneRowPerARN(ctx, st, acct, region, "cloudfront:ListFunctions",
		func(ctx context.Context, marker *string) ([]cftypes.FunctionSummary, *string, error) {
			out, err := client.ListFunctions(ctx, &cloudfront.ListFunctionsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.FunctionList == nil {
				return nil, nil, nil
			}
			return out.FunctionList.Items, out.FunctionList.NextMarker, nil
		},
		func(s cfStaged[cftypes.FunctionSummary]) (*store.Resource, string, string) {
			it := s.item
			var meta cftypes.FunctionMetadata
			if it.FunctionMetadata != nil {
				meta = *it.FunctionMetadata
			}
			r := cfResource(acct, scanID, TypeCloudFrontFunction, meta.FunctionARN, cfFunctionAttrs{it, s.development})
			r.Name, r.CreatedAt = it.Name, tp(meta.LastModifiedTime)
			return cfRow(r, "functions", sv(it.Name))
		},
		func(it cftypes.FunctionSummary) (string, cftypes.FunctionStage) {
			if it.FunctionMetadata == nil {
				return "", ""
			}
			return sv(it.FunctionMetadata.FunctionARN), it.FunctionMetadata.Stage
		}, tags, p)
}

// cfConnectionFunctionAttrs is cfFunctionAttrs for a connection function.
type cfConnectionFunctionAttrs struct {
	cftypes.ConnectionFunctionSummary
	DevelopmentStage *cftypes.ConnectionFunctionSummary `json:"DevelopmentStage,omitempty"`
}

// scanCloudFrontConnectionFunctions stores the connection functions with
// their tags, one row per ARN (cfStoreOneRowPerARN).
func scanCloudFrontConnectionFunctions(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return cfStoreOneRowPerARN(ctx, st, acct, region, "cloudfront:ListConnectionFunctions",
		func(ctx context.Context, marker *string) ([]cftypes.ConnectionFunctionSummary, *string, error) {
			out, err := client.ListConnectionFunctions(ctx, &cloudfront.ListConnectionFunctionsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectionFunctions, out.NextMarker, nil
		},
		func(s cfStaged[cftypes.ConnectionFunctionSummary]) (*store.Resource, string, string) {
			it := s.item
			r := cfResource(acct, scanID, TypeCloudFrontConnectionFunction, it.ConnectionFunctionArn, cfConnectionFunctionAttrs{it, s.development})
			r.Name, r.CreatedAt = it.Name, tp(it.CreatedTime)
			return cfRow(r, "connection functions", cmp.Or(sv(it.Name), sv(it.Id)))
		},
		func(it cftypes.ConnectionFunctionSummary) (string, cftypes.FunctionStage) {
			return sv(it.ConnectionFunctionArn), it.Stage
		}, tags, p)
}

// scanCloudFrontKeyValueStores stores the KeyValueStores with their tags.
func scanCloudFrontKeyValueStores(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListKeyValueStores",
		func(ctx context.Context, marker *string) ([]cftypes.KeyValueStore, *string, error) {
			out, err := client.ListKeyValueStores(ctx, &cloudfront.ListKeyValueStoresInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.KeyValueStoreList == nil {
				return nil, nil, nil
			}
			return out.KeyValueStoreList.Items, out.KeyValueStoreList.NextMarker, nil
		},
		func(it cftypes.KeyValueStore) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontKeyValueStore, it.ARN, it)
			r.Name = it.Name
			return cfRow(r, "key value stores", cmp.Or(sv(it.Name), sv(it.Id)))
		}, tags, p)
}

// scanCloudFrontTrustStores stores the mutual TLS trust stores with their
// tags.
func scanCloudFrontTrustStores(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListTrustStores",
		func(ctx context.Context, marker *string) ([]cftypes.TrustStoreSummary, *string, error) {
			out, err := client.ListTrustStores(ctx, &cloudfront.ListTrustStoresInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.TrustStoreList, out.NextMarker, nil
		},
		func(it cftypes.TrustStoreSummary) (*store.Resource, string, string) {
			return cfRow(cfResource(acct, scanID, TypeCloudFrontTrustStore, it.Arn, it), "trust stores", cmp.Or(sv(it.Name), sv(it.Id)))
		}, tags, p)
}

// scanCloudFrontConnectionGroups stores the connection groups with their
// tags.
func scanCloudFrontConnectionGroups(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListConnectionGroups",
		func(ctx context.Context, marker *string) ([]cftypes.ConnectionGroupSummary, *string, error) {
			out, err := client.ListConnectionGroups(ctx, &cloudfront.ListConnectionGroupsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectionGroups, out.NextMarker, nil
		},
		func(it cftypes.ConnectionGroupSummary) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontConnectionGroup, it.Arn, it)
			r.Name, r.CreatedAt = it.Name, tp(it.CreatedTime)
			return cfRow(r, "connection groups", cmp.Or(sv(it.Name), sv(it.Id)))
		}, tags, p)
}

// scanCloudFrontAnycastIPLists stores the Anycast static IP lists with their
// tags.
func scanCloudFrontAnycastIPLists(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListAnycastIpLists",
		func(ctx context.Context, marker *string) ([]cftypes.AnycastIpListSummary, *string, error) {
			out, err := client.ListAnycastIpLists(ctx, &cloudfront.ListAnycastIpListsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.AnycastIpLists == nil {
				return nil, nil, nil
			}
			return out.AnycastIpLists.Items, out.AnycastIpLists.NextMarker, nil
		},
		func(it cftypes.AnycastIpListSummary) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontAnycastIPList, it.Arn, it)
			r.Name = it.Name
			return cfRow(r, "Anycast IP lists", cmp.Or(sv(it.Name), sv(it.Id)))
		}, tags, p)
}

// scanCloudFrontVpcOrigins stores the VPC origins with their tags. A VPC
// origin can be shared with other accounts through RAM, and its summary names
// its owner (AccountId), so a listed VPC origin may be another account's;
// whether ListVpcOrigins returns shared ones is unverified. Such a row is read
// under otherAccountGap (rowTagReader.untaggedIfOtherAccount).
func scanCloudFrontVpcOrigins(ctx context.Context, client cloudfrontAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "cloudfront:ListVpcOrigins",
		func(ctx context.Context, marker *string) ([]cftypes.VpcOriginSummary, *string, error) {
			out, err := client.ListVpcOrigins(ctx, &cloudfront.ListVpcOriginsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.VpcOriginList == nil {
				return nil, nil, nil
			}
			return out.VpcOriginList.Items, out.VpcOriginList.NextMarker, nil
		},
		func(it cftypes.VpcOriginSummary) (*store.Resource, string, string) {
			r := cfResource(acct, scanID, TypeCloudFrontVpcOrigin, it.Arn, it)
			r.Name = it.Name
			return cfRow(r, "VPC origins", cmp.Or(sv(it.Name), sv(it.Id)))
		}, tags.untaggedIfOtherAccount(acct.ID), p)
}

// cfStoreOneRowPerARN is storeTokenListTags for ListFunctions and
// ListConnectionFunctions. Neither is sent a Stage, so a published function
// is listed twice, its DEVELOPMENT and LIVE stages under one ARN. A NativeID
// holds one resource, so each ARN is one row, counted and tag-read once,
// built from cfOneRowPerARN's pairing. (Before, both summaries were written
// and the later one overwrote the other.) arnStage returns an item's ARN and
// stage; items without an ARN are left for row to warn about.
func cfStoreOneRowPerARN[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, marker *string) ([]Item, *string, error),
	row func(cfStaged[Item]) (*store.Resource, string, string),
	arnStage func(Item) (string, cftypes.FunctionStage),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var skips notStoredSkips
	items, err := readTokenPages(ctx, page)
	rows := buildListedRows(cfOneRowPerARN(items, arnStage, &skips), row, nil, &skips)
	return storeRowsTags(ctx, st, acct, region, op, rows, err, func() { skips.warn(st, op, acct, region) }, tags, p)
}

// cfStaged is the entry a function's row is built from and the DEVELOPMENT
// entry listed under the same ARN beside a LIVE one (nil when none).
type cfStaged[Item any] struct {
	item        Item
	development *Item
}

// cfUnpairedStage is why cfOneRowPerARN does not store an entry.
const cfUnpairedStage = "function entries under an ARN already listed that are neither a LIVE and DEVELOPMENT pair nor a repeat of a stage kept"

// cfOneRowPerARN pairs, in listing order, the entries of each ARN. The row's
// entry is the LIVE one when listed, else the first, so the stored summary
// does not flip with listing order; beside a LIVE entry the DEVELOPMENT one
// is kept as development, so neither stage's summary is lost. A further
// entry in a stage already kept is the same listing data again and is
// dropped silently. Any other further entry (an empty or unknown stage, or
// one a LIVE entry displaces) is not stored and is added to skips under
// cfUnpairedStage, keyed by its ARN, so it is warned about. Each entry
// without an ARN is its own row, so each is warned about.
func cfOneRowPerARN[Item any](items []Item, arnStage func(Item) (string, cftypes.FunctionStage), skips *notStoredSkips) []cfStaged[Item] {
	rows := make([]cfStaged[Item], 0, len(items))
	at := map[string]int{}
	for _, it := range items {
		arn, stage := arnStage(it)
		i, seen := at[arn]
		if !seen {
			if arn != "" {
				at[arn] = len(rows)
			}
			rows = append(rows, cfStaged[Item]{item: it})
			continue
		}
		r := &rows[i]
		_, kept := arnStage(r.item)
		switch {
		case stage == cftypes.FunctionStageLive && kept != cftypes.FunctionStageLive:
			if kept == cftypes.FunctionStageDevelopment {
				development := r.item
				r.development = &development
			} else {
				skips.skipped(cfUnpairedStage, arn)
			}
			r.item = it
		case stage == cftypes.FunctionStageDevelopment && kept == cftypes.FunctionStageLive && r.development == nil:
			r.development = &it
		case stage == kept, stage == cftypes.FunctionStageDevelopment && r.development != nil:
			// A repeat of a kept stage: nothing new to store.
		default:
			skips.skipped(cfUnpairedStage, arn)
		}
	}
	return rows
}
