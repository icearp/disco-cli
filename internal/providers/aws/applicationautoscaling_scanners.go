package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeApplicationAutoScalingScalableTarget, Service: "application-autoscaling", Leaf: true})
	registerType(restype.Descriptor{Type: TypeApplicationAutoScalingScalingPolicy, Service: "application-autoscaling"})
	registerService(serviceEntry{
		name: "aws:application-autoscaling",
		fn:   scanApplicationAutoScaling,
	})
}

// applicationAutoScalingNamespaces enumerates every ServiceNamespace exposed
// by Application Auto Scaling. Both DescribeScalableTargets and
// DescribeScalingPolicies require ServiceNamespace per call, so the scanner
// fans out across this list.
var applicationAutoScalingNamespaces = []aastypes.ServiceNamespace{
	aastypes.ServiceNamespaceEcs,
	aastypes.ServiceNamespaceEmr,
	aastypes.ServiceNamespaceEc2,
	aastypes.ServiceNamespaceAppstream,
	aastypes.ServiceNamespaceDynamodb,
	aastypes.ServiceNamespaceRds,
	aastypes.ServiceNamespaceSagemaker,
	aastypes.ServiceNamespaceCustomResource,
	aastypes.ServiceNamespaceComprehend,
	aastypes.ServiceNamespaceLambda,
	aastypes.ServiceNamespaceCassandra,
	aastypes.ServiceNamespaceKafka,
	aastypes.ServiceNamespaceElasticache,
	aastypes.ServiceNamespaceNeptune,
	aastypes.ServiceNamespaceWorkspaces,
}

// applicationAutoScalingAPI is the narrow surface scanApplicationAutoScaling
// uses. DescribeScalableTargets has manual NextToken pagination (no SDK
// paginator helper); DescribeScalingPolicies has a paginator.
type applicationAutoScalingAPI interface {
	DescribeScalableTargets(context.Context, *applicationautoscaling.DescribeScalableTargetsInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error)
	DescribeScalingPolicies(context.Context, *applicationautoscaling.DescribeScalingPoliciesInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error)
}

// applicationAutoScalingScalableTargetNativeID synthesizes the NativeID for
// a scalable target. The SDK exposes no ARN; uniqueness is the
// (namespace, ResourceId, ScalableDimension) triple.
func applicationAutoScalingScalableTargetNativeID(region, acct, namespace, resourceID, dimension string) string {
	return fmt.Sprintf("arn:aws:application-autoscaling:%s:%s:scalable-target/%s/%s/%s", region, acct, namespace, resourceID, dimension)
}

func scanApplicationAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := applicationautoscaling.NewFromConfig(acct.cfg, func(o *applicationautoscaling.Options) { o.Region = region })
	tTotal, tInserted, err := scanApplicationAutoScalingScalableTargets(ctx, client, acct, region, st, scanID)
	if err != nil {
		return tTotal, tInserted, err
	}
	pTotal, pInserted, err := scanApplicationAutoScalingScalingPolicies(ctx, client, acct, region, st, scanID)
	if err != nil {
		return tTotal + pTotal, tInserted + pInserted, err
	}
	return tTotal + pTotal, tInserted + pInserted, nil
}

func scanApplicationAutoScalingScalableTargets(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, ns := range applicationAutoScalingNamespaces {
		var nextToken *string
		for {
			out, derr := client.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{ServiceNamespace: ns, NextToken: nextToken})
			if derr != nil {
				// Per-region namespace-enum gap: AWS rejects namespaces not
				// enumerated in this region's set with a ValidationException
				// listing the allowed members. Silent-skip the namespace.
				if isAPIErrorWithMessage(derr, "ValidationException", "Member must satisfy enum value set") {
					break
				}
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "application-autoscaling:DescribeScalableTargets", acct.ID, region, derr)
					break
				}
				return total, inserted, fmt.Errorf("application-autoscaling:DescribeScalableTargets %s: %w", ns, derr)
			}
			batch := make([]*store.Resource, 0, len(out.ScalableTargets))
			for _, t := range out.ScalableTargets {
				resourceID := sv(t.ResourceId)
				dimension := string(t.ScalableDimension)
				if resourceID == "" || dimension == "" {
					continue
				}
				nativeID := applicationAutoScalingScalableTargetNativeID(region, acct.ID, string(ns), resourceID, dimension)
				name := resourceID
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeApplicationAutoScalingScalableTarget,
					NativeID:       nativeID,
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(t.CreationTime),
					AttributesJSON: mustJSON(t),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, uerr := st.UpsertResources(batch)
				if uerr != nil {
					return total, inserted, fmt.Errorf("upsert application-autoscaling targets: %w", uerr)
				}
				total += len(batch)
				inserted += n
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return total, inserted, nil
}

func scanApplicationAutoScalingScalingPolicies(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, ns := range applicationAutoScalingNamespaces {
		p := applicationautoscaling.NewDescribeScalingPoliciesPaginator(client, &applicationautoscaling.DescribeScalingPoliciesInput{ServiceNamespace: ns})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAPIErrorWithMessage(perr, "ValidationException", "Member must satisfy enum value set") {
					break
				}
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "application-autoscaling:DescribeScalingPolicies", acct.ID, region, perr)
					break
				}
				return total, inserted, fmt.Errorf("application-autoscaling:DescribeScalingPolicies %s: %w", ns, perr)
			}
			batch := make([]*store.Resource, 0, len(page.ScalingPolicies))
			for _, sp := range page.ScalingPolicies {
				arn := sv(sp.PolicyARN)
				if arn == "" {
					continue
				}
				name := sv(sp.PolicyName)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeApplicationAutoScalingScalingPolicy,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(sp.CreationTime),
					AttributesJSON: mustJSON(sp),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, uerr := st.UpsertResources(batch)
				if uerr != nil {
					return total, inserted, fmt.Errorf("upsert application-autoscaling policies: %w", uerr)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return total, inserted, nil
}
