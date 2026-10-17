package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeApplicationAutoScalingScalableTarget, Service: "application-autoscaling"})
	registerType(restype.Descriptor{Type: TypeApplicationAutoScalingScalingPolicy, Service: "application-autoscaling"})
	registerType(restype.Descriptor{Type: TypeApplicationAutoScalingScheduledAction, Service: "application-autoscaling"})
	registerService(serviceEntry{
		name: "aws:application-autoscaling",
		fn:   scanApplicationAutoScaling,
	})
}

// applicationAutoScalingNamespaces enumerates every ServiceNamespace exposed
// by Application Auto Scaling. DescribeScalableTargets,
// DescribeScalingPolicies and DescribeScheduledActions require
// ServiceNamespace per call, so the scanner fans out across this list.
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
// uses.
type applicationAutoScalingAPI interface {
	DescribeScalableTargets(context.Context, *applicationautoscaling.DescribeScalableTargetsInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error)
	DescribeScalingPolicies(context.Context, *applicationautoscaling.DescribeScalingPoliciesInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error)
	DescribeScheduledActions(context.Context, *applicationautoscaling.DescribeScheduledActionsInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScheduledActionsOutput, error)
}

// applicationAutoScalingScalableTargetNativeID synthesizes the NativeID for
// a scalable target. The SDK exposes no ARN; uniqueness is the
// (namespace, ResourceId, ScalableDimension) triple.
func applicationAutoScalingScalableTargetNativeID(region, acct, namespace, resourceID, dimension string) string {
	return fmt.Sprintf("arn:aws:application-autoscaling:%s:%s:scalable-target/%s/%s/%s", region, acct, namespace, resourceID, dimension)
}

// scanApplicationAutoScaling discovers Application Auto Scaling scalable
// targets, scaling policies and scheduled actions in one region.
//
// The older phases (targets, then policies) stop at their first hard error.
// The scheduled action phase runs after them whatever they returned, so its
// failure costs no older phase its rows and theirs does not skip it. Each
// side's transient error is demoted to a warning, since one transient member
// would make the dispatcher read the whole join as transient; the counts are
// summed.
func scanApplicationAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := applicationautoscaling.NewFromConfig(acct.cfg, func(o *applicationautoscaling.Options) { o.Region = region })
	total, inserted, err := scanApplicationAutoScalingOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, serr := scanApplicationAutoScalingScheduledActions(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, "application-autoscaling", acct, region, err),
		demoteTransient(st, applicationAutoScalingScheduledActionsOp, acct, region, serr),
	)
}

func scanApplicationAutoScalingOlderPhases(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	tTotal, tInserted, err := scanApplicationAutoScalingScalableTargets(ctx, client, acct, region, st, scanID)
	if err != nil {
		return tTotal, tInserted, err
	}
	pTotal, pInserted, err := scanApplicationAutoScalingScalingPolicies(ctx, client, acct, region, st, scanID)
	return tTotal + pTotal, tInserted + pInserted, err
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

const applicationAutoScalingScheduledActionsOp = "application-autoscaling:DescribeScheduledActions"

// scanApplicationAutoScalingScheduledActions lists every namespace's scheduled
// actions a page at a time, storing each page as it arrives. A denial ends
// the phase with one warning: the Describe actions take no resource-level
// scoping, so a denial in one namespace is a denial in all of them.
func scanApplicationAutoScalingScheduledActions(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	const op = applicationAutoScalingScheduledActionsOp
	for _, ns := range applicationAutoScalingNamespaces {
		p := applicationautoscaling.NewDescribeScheduledActionsPaginator(client, &applicationautoscaling.DescribeScheduledActionsInput{
			ServiceNamespace: ns,
			MaxResults:       sdkaws.Int32(50),
		})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAPIErrorWithMessage(perr, "ValidationException", "Member must satisfy enum value set") {
					break
				}
				if isAccessDenied(perr) {
					return total, inserted, skipIfAccessDenied(st, op, acct.ID, region, perr)
				}
				return total, inserted, fmt.Errorf("%s %s: %w", op, ns, perr)
			}
			batch := make([]*store.Resource, 0, len(page.ScheduledActions))
			for _, sa := range page.ScheduledActions {
				arn := sv(sa.ScheduledActionARN)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeApplicationAutoScalingScheduledAction,
					NativeID:       arn,
					Name:           sa.ScheduledActionName,
					Region:         &region,
					CreatedAt:      tp(sa.CreationTime),
					AttributesJSON: mustJSON(sa),
					DiscoveredBy:   scanID,
				})
			}
			t, i, uerr := upsertBatch(st, batch, op)
			total += t
			inserted += i
			if uerr != nil {
				return total, inserted, uerr
			}
		}
	}
	return total, inserted, nil
}
