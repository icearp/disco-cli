package aws

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"golang.org/x/sync/errgroup"
)

func init() {
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
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		tt, nn, e := scanCloudFrontDistributions(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontMonitoringSubscriptions(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontStreamingDistributions(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontDistributionTenants(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontCachePolicies(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontOriginRequestPolicies(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontResponseHeadersPolicies(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontContinuousDeploymentPolicies(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanCloudFrontOAIs(gctx, acct, client, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanCloudFrontOriginAccessControls(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontFunctions(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontConnectionFunctions(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontKeyGroups(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontKeyValueStores(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontPublicKeys(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontRealtimeLogConfigs(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontTrustStores(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontConnectionGroups(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontAnycastIpLists(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanCloudFrontVpcOrigins(gctx, acct, client, st, scanID)
		add(tt, nn)
		return e
	})
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// cfMarkerScan abstracts Marker-based pagination for CloudFront list APIs that
// lack a dedicated SDK paginator. listFn returns (output, nextMarker, error);
// when nextMarker is nil there are no more pages. processFn returns (total, inserted, error).
func cfMarkerScan[Output any](
	ctx context.Context,
	opName string,
	listFn func(ctx context.Context, marker *string) (Output, *string, error),
	processFn func(Output) (int, int, error),
) (total, inserted int, err error) {
	var marker *string
	for {
		out, next, apiErr := listFn(ctx, marker)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return total, inserted, skipIfAccessDenied(opName, "", "global", apiErr)
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
func scanCloudFrontDistributions(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListDistributions", acct.ID, "global", err)
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
func scanCloudFrontMonitoringSubscriptions(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	// Collect all (id, arn) pairs from distributions.
	type distRef struct{ id, arn string }
	var refs []distRef
	pager := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("cloudfront:ListDistributions (monitoring)", acct.ID, "global", err)
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
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudFrontMonitoringSubscription,
				NativeID:       r.arn, // keyed to parent distribution ARN
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
func scanCloudFrontStreamingDistributions(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListStreamingDistributionsPaginator(client, &cloudfront.ListStreamingDistributionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListStreamingDistributions", acct.ID, "global", err)
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
func scanCloudFrontDistributionTenants(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListDistributionTenantsPaginator(client, &cloudfront.ListDistributionTenantsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListDistributionTenants", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListDistributionTenants: %w", err)
		}
		var batch []*store.Resource
		for _, t := range page.DistributionTenantList {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
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

// scanCloudFrontCachePolicies discovers customer-owned cache policies.
// No Type filter is set, which returns only CUSTOM (account-owned) policies.
func scanCloudFrontCachePolicies(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListCachePolicies",
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
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeCloudFrontCachePolicy,
					NativeID:       sv(cp.Id),
					Name:           name,
					CreatedAt:      tp(cp.LastModifiedTime),
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

// --- Origin Request Policies ---

// scanCloudFrontOriginRequestPolicies discovers customer-owned origin request policies.
func scanCloudFrontOriginRequestPolicies(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListOriginRequestPolicies",
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
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeCloudFrontOriginRequestPolicy,
					NativeID:       sv(p.Id),
					Name:           name,
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

// --- Response Headers Policies ---

// scanCloudFrontResponseHeadersPolicies discovers customer-owned response headers policies.
func scanCloudFrontResponseHeadersPolicies(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListResponseHeadersPolicies",
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
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeCloudFrontResponseHeadersPolicy,
					NativeID:       sv(p.Id),
					Name:           name,
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

// --- Continuous Deployment Policies ---

// scanCloudFrontContinuousDeploymentPolicies discovers continuous deployment policies.
func scanCloudFrontContinuousDeploymentPolicies(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListContinuousDeploymentPolicies",
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
func scanCloudFrontOAIs(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListCloudFrontOriginAccessIdentitiesPaginator(client, &cloudfront.ListCloudFrontOriginAccessIdentitiesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListCloudFrontOriginAccessIdentities", acct.ID, "global", err)
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
func scanCloudFrontOriginAccessControls(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListOriginAccessControlsPaginator(client, &cloudfront.ListOriginAccessControlsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListOriginAccessControls", acct.ID, "global", err)
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
func scanCloudFrontFunctions(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListFunctions",
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
func scanCloudFrontConnectionFunctions(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListConnectionFunctionsPaginator(client, &cloudfront.ListConnectionFunctionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListConnectionFunctions", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListConnectionFunctions: %w", err)
		}
		var batch []*store.Resource
		for _, f := range page.ConnectionFunctions {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
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
func scanCloudFrontKeyGroups(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListKeyGroups",
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
func scanCloudFrontKeyValueStores(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListKeyValueStoresPaginator(client, &cloudfront.ListKeyValueStoresInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListKeyValueStores", acct.ID, "global", err)
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
func scanCloudFrontPublicKeys(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListPublicKeysPaginator(client, &cloudfront.ListPublicKeysInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListPublicKeys", acct.ID, "global", err)
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
func scanCloudFrontRealtimeLogConfigs(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListRealtimeLogConfigs",
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
func scanCloudFrontTrustStores(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListTrustStoresPaginator(client, &cloudfront.ListTrustStoresInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListTrustStores", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListTrustStores: %w", err)
		}
		var batch []*store.Resource
		for _, t := range page.TrustStoreList {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
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
func scanCloudFrontConnectionGroups(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudfront.NewListConnectionGroupsPaginator(client, &cloudfront.ListConnectionGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("cloudfront:ListConnectionGroups", acct.ID, "global", err)
			}
			return total, inserted, fmt.Errorf("cloudfront:ListConnectionGroups: %w", err)
		}
		var batch []*store.Resource
		for _, g := range page.ConnectionGroups {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
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

// scanCloudFrontAnycastIpLists discovers Anycast static IP lists.
func scanCloudFrontAnycastIpLists(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListAnycastIpLists",
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
					Type:           TypeCloudFrontAnycastIpList,
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
func scanCloudFrontVpcOrigins(ctx context.Context, acct *account, client *cloudfront.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	return cfMarkerScan(ctx, "cloudfront:ListVpcOrigins",
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
