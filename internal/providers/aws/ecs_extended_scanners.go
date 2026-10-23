package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/icearp/disco-cli/store"
)

// isNoTaskSets reports whether err is the InvalidParameterException DescribeTaskSets
// returns for a default rolling-update (ECS) deployment controller service. Only
// EXTERNAL / CODE_DEPLOY services have task sets; others get "TaskSets cannot be
// empty" — a structural fact, not a scanner error, so skip it.
func isNoTaskSets(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidParameterException", "TaskSets cannot be empty")
}

type ecsExtAPI interface {
	DescribeCapacityProviders(context.Context, *ecs.DescribeCapacityProvidersInput, ...func(*ecs.Options)) (*ecs.DescribeCapacityProvidersOutput, error)
	DescribeClusters(context.Context, *ecs.DescribeClustersInput, ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error)
	ListServices(context.Context, *ecs.ListServicesInput, ...func(*ecs.Options)) (*ecs.ListServicesOutput, error)
	DescribeTaskSets(context.Context, *ecs.DescribeTaskSetsInput, ...func(*ecs.Options)) (*ecs.DescribeTaskSetsOutput, error)
}

const (
	ecsDescribeCapacityProvidersOp = "ecs:DescribeCapacityProviders"
	ecsClusterAssocOp              = "ecs:DescribeClusters(assoc)"
	ecsListServicesTaskSetsOp      = "ecs:ListServices(task-sets)"
	ecsDescribeTaskSetsOp          = "ecs:DescribeTaskSets"
	// ecsCapacityProvidersPageLimit is the model's maxResults cap (1-10).
	ecsCapacityProvidersPageLimit = 10
	// ecsListServicesPageLimit is the model's maxResults cap (1-100).
	ecsListServicesPageLimit = 100
)

// scanECSExtended runs three independent phases, each whatever an earlier one
// returned, except after a failed store write:
//   - CapacityProviders (account-wide)
//   - ClusterCapacityProviderAssociations (synthesized per cluster from
//     DescribeClusters response)
//   - TaskSets (per service of each cluster)
func scanECSExtended(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (total, inserted int, err error) {
	var errs phaseErrs
	stop := func(op string, t, i int, ferr error) bool {
		total, inserted = total+t, inserted+i
		errs.add(op, ferr)
		return errors.Is(ferr, store.ErrStoreWrite)
	}
	t, i, ferr := scanECSCapacityProviders(ctx, client, acct, region, st, scanID)
	if stop(ecsDescribeCapacityProvidersOp, t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanECSClusterCapacityProviderAssociations(ctx, client, acct, region, st, scanID, clusterARNs)
	if stop(ecsClusterAssocOp, t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanECSTaskSets(ctx, client, acct, region, st, scanID, clusterARNs)
	stop(ecsDescribeTaskSetsOp, t, i, ferr)
	return total, inserted, errs.join(st, acct, region)
}

// ecsFargateCapacityProviders are the capacity providers ECS predefines for
// Fargate. CreateCapacityProvider rejects names prefixed with "fargate", so no
// customer-created provider carries either name. Cluster is not the test: only
// Managed Instances providers are bound to a cluster, so a customer's Auto
// Scaling group provider has none either.
var ecsFargateCapacityProviders = []string{"FARGATE", "FARGATE_SPOT"}

// scanECSCapacityProviders stores every capacity provider DescribeCapacityProviders
// returns, with its tags: the response carries them only for Include TAGS.
func scanECSCapacityProviders(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ecsDescribeCapacityProvidersOp,
		func(ctx context.Context, token *string) ([]ecstypes.CapacityProvider, *string, error) {
			out, err := client.DescribeCapacityProviders(ctx, &ecs.DescribeCapacityProvidersInput{
				Include:    []ecstypes.CapacityProviderField{ecstypes.CapacityProviderFieldTags},
				MaxResults: sdkaws.Int32(ecsCapacityProvidersPageLimit),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.CapacityProviders, out.NextToken, nil
		},
		func(cp ecstypes.CapacityProvider) (*store.Resource, string, string) {
			arn := sv(cp.CapacityProviderArn)
			if arn == "" {
				return nil, "capacity providers have an empty CapacityProviderArn", sv(cp.Name)
			}
			label := sv(cp.Name)
			if label == "" {
				label = arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSCapacityProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(cp), TagsJSON: awsTagsJSON(cp.Tags),
				DiscoveredBy:      scanID,
				ManagedByProvider: slices.Contains(ecsFargateCapacityProviders, sv(cp.Name)),
			}, "", ""
		})
}

func scanECSClusterCapacityProviderAssociations(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (int, int, error) {
	if len(clusterARNs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	const batchSize = 100
	for i := 0; i < len(clusterARNs); i += batchSize {
		end := i + batchSize
		if end > len(clusterARNs) {
			end = len(clusterARNs)
		}
		out, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: clusterARNs[i:end]})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, ecsClusterAssocOp, acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("%s: %w", ecsClusterAssocOp, err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			if arn == "" {
				continue
			}
			// Synthesized association resource: one per cluster, carries the cluster's
			// CapacityProviders list + DefaultCapacityProviderStrategy as attributes.
			synthArn := arn + "/capacity-provider-associations"
			label := sv(c.ClusterName)
			if label == "" {
				label = arn
			}
			payload := map[string]any{
				"ClusterArn":                      arn,
				"CapacityProviders":               c.CapacityProviders,
				"DefaultCapacityProviderStrategy": c.DefaultCapacityProviderStrategy,
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSClusterCapacityProviderAssociations, NativeID: synthArn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(payload), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "ecs cluster-capacity-provider-associations")
}

// scanECSTaskSets lists each cluster's services and stores the task sets
// DescribeTaskSets returns for each, with their tags: the response carries them
// only for Include TAGS. Clusters are listed one at a time and each page's
// services read concurrently, so at most fanoutMed calls are in flight. A
// cluster's failed listing is settled per storeTokenPerParent and a service's
// failed read per ecsTaskSetReads.describe. Every error is returned after the
// rows are stored.
func scanECSTaskSets(ctx context.Context, client ecsExtAPI, acct *account, region string, st *store.Store, scanID string, clusterARNs []string) (int, int, error) {
	var reads ecsTaskSetReads
	total, inserted, err := storeTokenPerParent(ctx, st, acct, region, ecsListServicesTaskSetsOp, []string{"ClusterNotFoundException"}, clusterARNs,
		func(ctx context.Context, cluster string, token *string) ([]ecstypes.TaskSet, *string, error) {
			out, err := client.ListServices(ctx, &ecs.ListServicesInput{
				Cluster: &cluster, MaxResults: sdkaws.Int32(ecsListServicesPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return reads.describe(ctx, client, cluster, out.ServiceArns), out.NextToken, nil
		},
		func(_ string, ts ecstypes.TaskSet) (*store.Resource, string, string) {
			arn := sv(ts.TaskSetArn)
			label := sv(ts.Id)
			if label == "" {
				label = arn
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeECSTaskSet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ts), TagsJSON: awsTagsJSON(ts.Tags),
				DiscoveredBy: scanID,
			}, "", ""
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	reads.warn(st, acct, region)
	return total, inserted, errors.Join(err, errors.Join(reads.errs...))
}

// ecsTaskSetReads collects, across one scan's DescribeTaskSets calls, the
// services and task sets skipped with a warning and the errors to return.
// describe is called for one page at a time, so it needs no lock.
type ecsTaskSetReads struct {
	denied, noARN skipTally
	errs          []error
}

// describe reads the task sets of a cluster's services concurrently. A service
// whose deployment controller keeps no task sets (isNoTaskSets), or that was
// deleted since it was listed, is skipped silently; a denied service and a
// task set without an ARN are tallied for warn; any other error is collected.
func (r *ecsTaskSetReads) describe(ctx context.Context, client ecsExtAPI, cluster string, services []string) []ecstypes.TaskSet {
	type read struct {
		sets []ecstypes.TaskSet
		err  error
	}
	reads, stopped := readEachKey(ctx, services, func(ctx context.Context, service string) read {
		out, err := client.DescribeTaskSets(ctx, &ecs.DescribeTaskSetsInput{
			Cluster: &cluster, Service: &service,
			Include: []ecstypes.TaskSetField{ecstypes.TaskSetFieldTags},
		})
		if err != nil {
			return read{err: err}
		}
		return read{sets: out.TaskSets}
	})
	var sets []ecstypes.TaskSet
	for i, got := range reads {
		service, err := services[i], got.err
		switch {
		case err == nil:
			for _, ts := range got.sets {
				if sv(ts.TaskSetArn) == "" {
					r.noARN.skipped(service+" "+sv(ts.Id), nil)
					continue
				}
				sets = append(sets, ts)
			}
		// ServiceNotActiveException is the same vanished parent as not-found:
		// the service was deleted, or is draining, since it was listed.
		case isNoTaskSets(err), isAPIErrorCode(err, "ServiceNotFoundException", "ServiceNotActiveException", "ClusterNotFoundException"):
		case isAccessDenied(err):
			r.denied.skipped(service, err)
		default:
			r.errs = append(r.errs, fmt.Errorf("%s %s: %w", ecsDescribeTaskSetsOp, service, err))
		}
	}
	if stopped != nil {
		r.errs = append(r.errs, fmt.Errorf("%s (cluster %s): %w", ecsDescribeTaskSetsOp, cluster, stopped))
	}
	return sets
}

// warn reports the denied services and the task sets without an ARN, one
// warning each; call it only after the rows were stored.
func (r *ecsTaskSetReads) warn(st *store.Store, acct *account, region string) {
	r.denied.warnDeniedIn(st, ecsDescribeTaskSetsOp, acct, region, "service(s)")
	warnNotStored(st, ecsDescribeTaskSetsOp, acct, region, &r.noARN, "task sets have an empty TaskSetArn")
}
