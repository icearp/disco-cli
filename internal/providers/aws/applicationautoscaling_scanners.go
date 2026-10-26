package aws

import (
	"context"
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
func applicationAutoScalingScalableTargetNativeID(partition, region, acct, namespace, resourceID, dimension string) string {
	return arnFor(partition, "application-autoscaling", region, acct, "scalable-target/"+namespace+"/"+resourceID+"/"+dimension)
}

// scanApplicationAutoScaling discovers Application Auto Scaling scalable
// targets, scaling policies and scheduled actions in one region.
//
// Policies and scheduled actions are listed only in namespaces not proven
// empty of targets: PutScalingPolicy and PutScheduledAction reject a resource
// that is not a registered scalable target, and DeregisterScalableTarget
// deletes the target's policies and scheduled actions. A namespace whose
// target listing failed or never ran is never read as "no targets": every
// later phase that runs still queries it. A hard target error still skips
// the policies phase (below), so only the scheduled action phase does.
//
// The older phases (targets, then policies) stop at their first hard error. The
// scheduled action phase runs after them whatever they returned, so its failure
// costs no older phase its rows and theirs does not skip it. Each side's
// transient error is demoted to a warning only beside a hard error: joined, it
// would be buried in the hard error instead of reported under its op; the
// counts are summed.
func scanApplicationAutoScaling(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := applicationautoscaling.NewFromConfig(acct.cfg, func(o *applicationautoscaling.Options) { o.Region = region })
	total, inserted, noTargets, err := scanApplicationAutoScalingOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, serr := scanApplicationAutoScalingScheduledActions(ctx, client, acct, region, st, scanID, noTargets)
	var errs phaseErrs
	errs.add("application-autoscaling", err)
	errs.add(applicationAutoScalingScheduledActionsOp, serr)
	return total + t, inserted + i, errs.join(st, acct, region)
}

func scanApplicationAutoScalingOlderPhases(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, noTargets map[aastypes.ServiceNamespace]bool, err error) {
	tTotal, tInserted, noTargets, err := scanApplicationAutoScalingScalableTargets(ctx, client, acct, region, st, scanID)
	if err != nil {
		return tTotal, tInserted, noTargets, err
	}
	pTotal, pInserted, err := scanApplicationAutoScalingScalingPolicies(ctx, client, acct, region, st, scanID, noTargets)
	return tTotal + pTotal, tInserted + pInserted, noTargets, err
}

// scanApplicationAutoScalingScalableTargets lists every namespace's scalable
// targets. noTargets holds only the namespaces the listing proved empty; one
// that was denied, outside the region's enum set, or not reached before a
// hard error is absent.
func scanApplicationAutoScalingScalableTargets(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, noTargets map[aastypes.ServiceNamespace]bool, err error) {
	noTargets = map[aastypes.ServiceNamespace]bool{}
	for _, ns := range applicationAutoScalingNamespaces {
		empty, lTotal, lInserted, lerr := listApplicationAutoScalingScalableTargets(ctx, client, acct, region, st, scanID, ns)
		total += lTotal
		inserted += lInserted
		if lerr != nil {
			return total, inserted, noTargets, lerr
		}
		if empty {
			noTargets[ns] = true
		}
	}
	return total, inserted, noTargets, nil
}

// listApplicationAutoScalingScalableTargets stores one namespace's scalable
// targets. empty is true only when the listing completed and returned no
// target, counting targets skipped for a missing id.
func listApplicationAutoScalingScalableTargets(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string, ns aastypes.ServiceNamespace) (empty bool, total, inserted int, err error) {
	var found int
	var nextToken *string
	for {
		out, derr := client.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{ServiceNamespace: ns, NextToken: nextToken, MaxResults: sdkaws.Int32(50)})
		if derr != nil {
			// Per-region namespace-enum gap: AWS rejects namespaces not
			// enumerated in this region's set with a ValidationException
			// listing the allowed members. Silent-skip the namespace.
			if isAPIErrorWithMessage(derr, "ValidationException", "Member must satisfy enum value set") {
				return false, total, inserted, nil
			}
			if isAccessDenied(derr) {
				return false, total, inserted, skipIfAccessDenied(st, "application-autoscaling:DescribeScalableTargets", acct.ID, region, derr)
			}
			return false, total, inserted, fmt.Errorf("application-autoscaling:DescribeScalableTargets %s: %w", ns, derr)
		}
		found += len(out.ScalableTargets)
		batch := make([]*store.Resource, 0, len(out.ScalableTargets))
		for _, t := range out.ScalableTargets {
			resourceID := sv(t.ResourceId)
			dimension := string(t.ScalableDimension)
			if resourceID == "" || dimension == "" {
				continue
			}
			nativeID := applicationAutoScalingScalableTargetNativeID(acct.partition(), region, acct.ID, string(ns), resourceID, dimension)
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
				return false, total, inserted, fmt.Errorf("upsert application-autoscaling targets: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
		next := sv(out.NextToken)
		if next == "" {
			return found == 0, total, inserted, nil
		}
		// A repeated token would loop forever; the listing is then incomplete,
		// so it fails instead of reporting the namespace as listed.
		if next == sv(nextToken) {
			return false, total, inserted, fmt.Errorf("application-autoscaling:DescribeScalableTargets %s: NextToken %q: %w", ns, next, errRepeatedNextToken)
		}
		nextToken = out.NextToken
	}
}

func scanApplicationAutoScalingScalingPolicies(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string, noTargets map[aastypes.ServiceNamespace]bool) (total, inserted int, err error) {
	for _, ns := range applicationAutoScalingNamespaces {
		if noTargets[ns] {
			continue
		}
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

// scanApplicationAutoScalingScheduledActions lists the scheduled actions of
// every namespace not in noTargets a page at a time, storing each page as it
// arrives. A denial ends the phase with one warning: the Describe actions
// take no resource-level scoping, so a denial in one namespace is a denial in
// all of them.
func scanApplicationAutoScalingScheduledActions(ctx context.Context, client applicationAutoScalingAPI, acct *account, region string, st *store.Store, scanID string, noTargets map[aastypes.ServiceNamespace]bool) (total, inserted int, err error) {
	const op = applicationAutoScalingScheduledActionsOp
	for _, ns := range applicationAutoScalingNamespaces {
		if noTargets[ns] {
			continue
		}
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
