package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"golang.org/x/sync/errgroup"
)

// cloudfrontAPI is the narrow set of CloudFront operations called by the
// scanCloudFront sub-phases. CloudFront has the broadest fan-out in the
// codebase (~21 resource types from one service); the iface mirrors that.
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
	registerType(restype.Descriptor{Type: TypeCloudFrontDistribution, Service: "cloudfront", Upstream: "AWS::CloudFront::Distribution"})
	registerType(restype.Descriptor{Type: TypeCloudFrontStreamingDistribution, Service: "cloudfront", Upstream: "AWS::CloudFront::StreamingDistribution"})
	registerType(restype.Descriptor{Type: TypeCloudFrontDistributionTenant, Service: "cloudfront", Upstream: "AWS::CloudFront::DistributionTenant"})
	registerType(restype.Descriptor{Type: TypeCloudFrontOAI, Service: "cloudfront", Upstream: "AWS::CloudFront::CloudFrontOriginAccessIdentity", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontOriginAccessControl, Service: "cloudfront", Upstream: "AWS::CloudFront::OriginAccessControl", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontConnectionFunction, Service: "cloudfront", Upstream: "AWS::CloudFront::ConnectionFunction", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontConnectionGroup, Service: "cloudfront", Upstream: "AWS::CloudFront::ConnectionGroup"})
	registerType(restype.Descriptor{Type: TypeCloudFrontKeyValueStore, Service: "cloudfront", Upstream: "AWS::CloudFront::KeyValueStore", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontPublicKey, Service: "cloudfront", Upstream: "AWS::CloudFront::PublicKey", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontTrustStore, Service: "cloudfront", Upstream: "AWS::CloudFront::TrustStore", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontAnycastIPList, Service: "cloudfront", Upstream: "AWS::CloudFront::AnycastIpList", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontCachePolicy, Service: "cloudfront", Upstream: "AWS::CloudFront::CachePolicy", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontContinuousDeploymentPolicy, Service: "cloudfront", Upstream: "AWS::CloudFront::ContinuousDeploymentPolicy", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontFunction, Service: "cloudfront", Upstream: "AWS::CloudFront::Function", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontKeyGroup, Service: "cloudfront", Upstream: "AWS::CloudFront::KeyGroup"})
	registerType(restype.Descriptor{Type: TypeCloudFrontOriginRequestPolicy, Service: "cloudfront", Upstream: "AWS::CloudFront::OriginRequestPolicy", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontRealtimeLogConfig, Service: "cloudfront", Upstream: "AWS::CloudFront::RealtimeLogConfig"})
	registerType(restype.Descriptor{Type: TypeCloudFrontResponseHeadersPolicy, Service: "cloudfront", Upstream: "AWS::CloudFront::ResponseHeadersPolicy", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFrontVpcOrigin, Service: "cloudfront", Upstream: "AWS::CloudFront::VpcOrigin"})
	registerType(restype.Descriptor{Type: TypeCloudFrontMonitoringSubscription, Service: "cloudfront", Upstream: "AWS::CloudFront::MonitoringSubscription"})
	registerType(restype.Descriptor{Type: TypeCloudFrontFieldLevelEncryptionConfig, Service: "cloudfront", Upstream: "AWS::cloudfront::field-level-encryption-config"})
	registerType(restype.Descriptor{Type: TypeCloudFrontFieldLevelEncryptionProfile, Service: "cloudfront", Upstream: "AWS::cloudfront::field-level-encryption-profile"})
	registerService(serviceEntry{
		name:   "aws:cloudfront",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanCloudFront(ctx, acct, st, scanID)
		},
	})
}

// scanCloudFront is the orchestrator for all CloudFront resource types.
// All CloudFront resources are global; the client is always created against us-east-1.
func scanCloudFront(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudfront.NewFromConfig(acct.cfg, func(o *cloudfront.Options) { o.Region = "us-east-1" })
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontDistributions(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontMonitoringSubscriptions(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontStreamingDistributions(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontDistributionTenants(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontCachePolicies(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontOriginRequestPolicies(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontResponseHeadersPolicies(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontContinuousDeploymentPolicies(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) { return scanCloudFrontOAIs(ctx, acct, client, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontOriginAccessControls(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontFunctions(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontConnectionFunctions(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontKeyGroups(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontKeyValueStores(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontPublicKeys(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontRealtimeLogConfigs(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontTrustStores(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontConnectionGroups(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontAnycastIPLists(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontVpcOrigins(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontFieldLevelEncryptionConfigs(ctx, acct, client, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCloudFrontFieldLevelEncryptionProfiles(ctx, acct, client, st, scanID)
		},
	)
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

// --- Distributions ---

// scanCloudFrontDistributions discovers CloudFront distributions.
// ListDistributions returns DistributionSummary items with full configuration —
// no separate GetDistribution call is needed. Tags are fetched concurrently.
func scanCloudFrontDistributions(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListDistributions", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListDistributions: %w", err)
		}
		if page.DistributionList == nil {
			continue
		}

		// Fetch tags for all distributions on this page concurrently.
		var mu sync.Mutex
		tagsByARN := make(map[string]*string, len(page.DistributionList.Items))
		g, gctx := errgroup.WithContext(ctx)
		for _, d := range page.DistributionList.Items {
			arn := sv(d.ARN)
			g.Go(func() error {
				out, err := client.ListTagsForResource(gctx, &cloudfront.ListTagsForResourceInput{Resource: &arn})
				if err != nil || out.Tags == nil {
					return nil // tags are best-effort
				}
				if t := awsTagsJSON(out.Tags.Items); t != nil {
					mu.Lock()
					tagsByARN[arn] = t
					mu.Unlock()
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return total, inserted, err
		}

		var batch []*store.Resource
		for _, d := range page.DistributionList.Items {
			arn := sv(d.ARN)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontDistribution,
				NativeID:       arn,
				Name:           d.Id,
				CreatedAt:      tp(d.LastModifiedTime),
				Status:         d.Status,
				AttributesJSON: mustJSON(d),
				TagsJSON:       tagsByARN[arn],
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront distributions: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanCloudFrontMonitoringSubscriptions fetches the CloudWatch monitoring
// subscription (if any) for each distribution. GetMonitoringSubscription is the
// only API — there is no list endpoint. A missing subscription is not an error.
func scanCloudFrontMonitoringSubscriptions(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	// Collect all (id, arn) pairs from distributions.
	type distRef struct{ id, arn string }
	var refs []distRef
	pager := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudfront:ListDistributions (monitoring)", acct.ID, "global", err)
			}
			return 0, 0, fmt.Errorf("cloudfront:ListDistributions (monitoring): %w", err)
		}
		if page.DistributionList == nil {
			continue
		}
		for _, d := range page.DistributionList.Items {
			if d.Id != nil {
				refs = append(refs, distRef{id: sv(d.Id), arn: sv(d.ARN)})
			}
		}
	}
	if len(refs) == 0 {
		return
	}

	// Fetch monitoring subscription for each distribution concurrently.
	var mu sync.Mutex
	var batch []*store.Resource
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range refs {
		r := ref
		g.Go(func() error {
			out, err := client.GetMonitoringSubscription(gctx, &cloudfront.GetMonitoringSubscriptionInput{
				DistributionId: &r.id,
			})
			if err != nil {
				// NoSuchMonitoringSubscription is expected; treat all errors as benign.
				return nil
			}
			if out.MonitoringSubscription == nil || out.MonitoringSubscription.RealtimeMetricsSubscriptionConfig == nil {
				return nil
			}
			res := &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Region:      regionGlobal,
				Type:        TypeCloudFrontMonitoringSubscription,
				// Suffix keeps this distinct from the distribution (same ARN) while
				// staying parent-derivable.
				NativeID:       r.arn + "/monitoring-subscription",
				Name:           sp(r.id),
				AttributesJSON: mustJSON(out.MonitoringSubscription),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, res)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert CloudFront monitoring subscriptions: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
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

// --- Distribution Tenants ---

// scanCloudFrontDistributionTenants discovers SaaS Manager distribution tenants.
func scanCloudFrontDistributionTenants(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListDistributionTenantsPaginator(client, &cloudfront.ListDistributionTenantsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListDistributionTenants", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListDistributionTenants: %w", err)
		}
		var batch []*store.Resource
		for _, t := range page.DistributionTenantList {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontDistributionTenant,
				NativeID:       sv(t.Arn),
				Name:           t.Id,
				CreatedAt:      tp(t.CreatedTime),
				AttributesJSON: mustJSON(t),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront distribution tenants: %w", err)
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

// --- Functions ---

// scanCloudFrontFunctions discovers CloudFront Functions (lightweight edge compute).
func scanCloudFrontFunctions(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListFunctions", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListFunctionsOutput, *string, error) {
			out, err := client.ListFunctions(ctx, &cloudfront.ListFunctionsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.FunctionList == nil {
				return out, nil, nil
			}
			return out, out.FunctionList.NextMarker, nil
		},
		func(out *cloudfront.ListFunctionsOutput) (int, int, error) {
			if out.FunctionList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, f := range out.FunctionList.Items {
				if f.FunctionMetadata == nil {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontFunction,
					NativeID:       sv(f.FunctionMetadata.FunctionARN),
					Name:           f.Name,
					CreatedAt:      tp(f.FunctionMetadata.LastModifiedTime),
					AttributesJSON: mustJSON(f),
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

// --- Connection Functions ---

// scanCloudFrontConnectionFunctions discovers connection functions.
func scanCloudFrontConnectionFunctions(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListConnectionFunctionsPaginator(client, &cloudfront.ListConnectionFunctionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListConnectionFunctions", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListConnectionFunctions: %w", err)
		}
		var batch []*store.Resource
		for _, f := range page.ConnectionFunctions {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontConnectionFunction,
				NativeID:       sv(f.ConnectionFunctionArn),
				Name:           f.Name,
				CreatedAt:      tp(f.CreatedTime),
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront connection functions: %w", err)
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

// --- Key Value Stores ---

// scanCloudFrontKeyValueStores discovers CloudFront KeyValueStore resources.
func scanCloudFrontKeyValueStores(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListKeyValueStoresPaginator(client, &cloudfront.ListKeyValueStoresInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListKeyValueStores", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListKeyValueStores: %w", err)
		}
		if page.KeyValueStoreList == nil {
			continue
		}
		var batch []*store.Resource
		for _, k := range page.KeyValueStoreList.Items {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontKeyValueStore,
				NativeID:       sv(k.ARN),
				Name:           k.Name,
				AttributesJSON: mustJSON(k),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront key value stores: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
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

// --- Trust Stores ---

// scanCloudFrontTrustStores discovers mutual TLS trust stores.
func scanCloudFrontTrustStores(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListTrustStoresPaginator(client, &cloudfront.ListTrustStoresInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListTrustStores", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListTrustStores: %w", err)
		}
		var batch []*store.Resource
		for _, t := range page.TrustStoreList {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontTrustStore,
				NativeID:       sv(t.Arn),
				AttributesJSON: mustJSON(t),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront trust stores: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Connection Groups ---

// scanCloudFrontConnectionGroups discovers connection groups (VPC origins routing).
func scanCloudFrontConnectionGroups(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListConnectionGroupsPaginator(client, &cloudfront.ListConnectionGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "cloudfront:ListConnectionGroups", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListConnectionGroups: %w", err)
		}
		var batch []*store.Resource
		for _, g := range page.ConnectionGroups {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
				Type:           TypeCloudFrontConnectionGroup,
				NativeID:       sv(g.Arn),
				Name:           g.Name,
				CreatedAt:      tp(g.CreatedTime),
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert CloudFront connection groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// --- Anycast IP Lists ---

// scanCloudFrontAnycastIPLists discovers Anycast static IP lists.
func scanCloudFrontAnycastIPLists(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListAnycastIpLists", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListAnycastIpListsOutput, *string, error) {
			out, err := client.ListAnycastIpLists(ctx, &cloudfront.ListAnycastIpListsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.AnycastIpLists == nil {
				return out, nil, nil
			}
			return out, out.AnycastIpLists.NextMarker, nil
		},
		func(out *cloudfront.ListAnycastIpListsOutput) (int, int, error) {
			if out.AnycastIpLists == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, a := range out.AnycastIpLists.Items {
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontAnycastIPList,
					NativeID:       sv(a.Arn),
					Name:           a.Name,
					AttributesJSON: mustJSON(a),
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

// --- VPC Origins ---

// scanCloudFrontVpcOrigins discovers VPC origin configurations.
func scanCloudFrontVpcOrigins(ctx context.Context, acct *account, client cloudfrontAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(
		ctx, "cloudfront:ListVpcOrigins", st,
		func(ctx context.Context, marker *string) (*cloudfront.ListVpcOriginsOutput, *string, error) {
			out, err := client.ListVpcOrigins(ctx, &cloudfront.ListVpcOriginsInput{Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			if out.VpcOriginList == nil {
				return out, nil, nil
			}
			return out, out.VpcOriginList.NextMarker, nil
		},
		func(out *cloudfront.ListVpcOriginsOutput) (int, int, error) {
			if out.VpcOriginList == nil {
				return 0, 0, nil
			}
			var batch []*store.Resource
			for _, v := range out.VpcOriginList.Items {
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Region:         regionGlobal,
					Type:           TypeCloudFrontVpcOrigin,
					NativeID:       sv(v.Arn),
					Name:           v.Name,
					AttributesJSON: mustJSON(v),
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
