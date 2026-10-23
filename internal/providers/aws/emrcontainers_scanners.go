package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/emrcontainers"
	emrctypes "github.com/aws/aws-sdk-go-v2/service/emrcontainers/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEMRContainersVirtualCluster, Service: "emr-containers"})
	registerType(restype.Descriptor{Type: TypeEMRContainersEndpoint, Service: "emr-containers"})
	registerType(restype.Descriptor{Type: TypeEMRContainersSecurityConfig, Service: "emr-containers"})
	registerType(restype.Descriptor{Type: TypeEMRContainersJobTemplate, Service: "emr-containers"})
	registerService(serviceEntry{
		name: "aws:emr-containers",
		fn:   scanEMRContainers,
	})
}

type emrContainersAPI interface {
	ListVirtualClusters(context.Context, *emrcontainers.ListVirtualClustersInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListVirtualClustersOutput, error)
	ListManagedEndpoints(context.Context, *emrcontainers.ListManagedEndpointsInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListManagedEndpointsOutput, error)
	ListSecurityConfigurations(context.Context, *emrcontainers.ListSecurityConfigurationsInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListSecurityConfigurationsOutput, error)
	ListJobTemplates(context.Context, *emrcontainers.ListJobTemplatesInput, ...func(*emrcontainers.Options)) (*emrcontainers.ListJobTemplatesOutput, error)
}

// The list ops' MaxResults carries no range in the model, so it is left unset
// and the service's default page size applies.
const (
	emrcVirtualClustersOp = "emr-containers:ListVirtualClusters"
	emrcEndpointsOp       = "emr-containers:ListManagedEndpoints"
	emrcSecurityConfigsOp = "emr-containers:ListSecurityConfigurations"
	emrcJobTemplatesOp    = "emr-containers:ListJobTemplates"
)

// scanEMRContainers discovers EMR-on-EKS virtual clusters, their managed
// endpoints, security configurations and job templates. Every phase runs; the
// endpoint phase reads only the virtual clusters the first phase stored, and
// only a store write failure stops the scan.
func scanEMRContainers(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := emrcontainers.NewFromConfig(acct.cfg, func(o *emrcontainers.Options) { o.Region = region })
	var vcs []emrcVirtualCluster
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{emrcVirtualClustersOp, func() (t, i int, err error) {
			t, i, vcs, err = scanEMRCVirtualClusters(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{emrcEndpointsOp, func() (int, int, error) {
			return scanEMRCEndpoints(ctx, client, vcs, acct, region, st, scanID)
		}},
		{emrcSecurityConfigsOp, func() (int, int, error) {
			return scanEMRCSecurityConfigs(ctx, client, acct, region, st, scanID)
		}},
		{emrcJobTemplatesOp, func() (int, int, error) {
			return scanEMRCJobTemplates(ctx, client, acct, region, st, scanID)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// emrcRow builds the row of an EMR-on-EKS resource, or returns why it cannot
// be stored (what says which resources) and its id when the ARN is empty.
func emrcRow(acct *account, region, scanID, rtype, what string, arn, id, name *string, attrs any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty Arn", sv(id)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: name, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

// emrcVirtualCluster is a virtual cluster stored this scan, with the state
// that decides whether the endpoint phase may skip it.
type emrcVirtualCluster struct {
	id    string
	state emrctypes.VirtualClusterState
}

// scanEMRCVirtualClusters stores every virtual cluster the unfiltered listing
// returns, whatever its state, and also returns the stored ones that carry
// an Id for the endpoint phase.
func scanEMRCVirtualClusters(ctx context.Context, client emrContainersAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []emrcVirtualCluster, error) {
	states := map[string]emrctypes.VirtualClusterState{}
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, emrcVirtualClustersOp, "virtual clusters", "Id",
		func(ctx context.Context, token *string) ([]emrctypes.VirtualCluster, *string, error) {
			out, err := client.ListVirtualClusters(ctx, &emrcontainers.ListVirtualClustersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualClusters, out.NextToken, nil
		},
		func(vc emrctypes.VirtualCluster) (*store.Resource, string, string) {
			r, why, key := emrcRow(acct, region, scanID, TypeEMRContainersVirtualCluster, "virtual clusters", vc.Arn, vc.Id, vc.Name, vc, vc.Tags)
			if r != nil {
				r.Status = nonEmptyPtr(string(vc.State))
				states[sv(vc.Id)] = vc.State
			}
			return r, why, key
		},
		func(vc emrctypes.VirtualCluster) string { return sv(vc.Id) })
	vcs := make([]emrcVirtualCluster, len(parents))
	for i, p := range parents {
		vcs[i] = emrcVirtualCluster{id: p.id, state: states[p.id]}
	}
	return total, inserted, vcs, err
}

// scanEMRCEndpoints lists the managed endpoints of each virtual cluster
// concurrently. ListManagedEndpoints models no not-found code. A virtual
// cluster that is not RUNNING (terminating, terminated, arrested) and is
// refused with ValidationException keeps the endpoints read before the error
// and is warned about once with its siblings, after the write succeeded: how
// the service answers for such a cluster has not been observed, and failing
// the phase would fail it on every scan until the cluster ages out. The same
// code for a RUNNING cluster fails the phase.
func scanEMRCEndpoints(ctx context.Context, client emrContainersAPI, vcs []emrcVirtualCluster, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	ids := make([]string, len(vcs))
	running := make(map[string]bool, len(vcs))
	for i, vc := range vcs {
		ids[i] = vc.id
		running[vc.id] = vc.state == emrctypes.VirtualClusterStateRunning
	}
	var notReady notReadyTally
	total, inserted, err := storeTokenEachParentSkip(ctx, st, acct, region, emrcEndpointsOp, nil, ids,
		func(ctx context.Context, vcID string, token *string) ([]emrctypes.Endpoint, *string, error) {
			out, err := client.ListManagedEndpoints(ctx, &emrcontainers.ListManagedEndpointsInput{VirtualClusterId: &vcID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Endpoints, out.NextToken, nil
		},
		func(_ string, e emrctypes.Endpoint) (*store.Resource, string, string) {
			r, why, key := emrcRow(acct, region, scanID, TypeEMRContainersEndpoint, "endpoints", e.Arn, e.Id, e.Name, e, e.Tags)
			if r != nil {
				r.Status = nonEmptyPtr(string(e.State))
			}
			return r, why, key
		},
		func(vcID string, err error) bool { return !running[vcID] && notReady.drop(vcID, err) })
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	notReady.warnSkipped(st, emrcEndpointsOp, acct, region, "virtual cluster(s) not RUNNING",
		"the service may refuse to list the endpoints of a cluster that is terminating, terminated or arrested")
	return total, inserted, err
}

func scanEMRCSecurityConfigs(ctx context.Context, client emrContainersAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, emrcSecurityConfigsOp,
		func(ctx context.Context, token *string) ([]emrctypes.SecurityConfiguration, *string, error) {
			out, err := client.ListSecurityConfigurations(ctx, &emrcontainers.ListSecurityConfigurationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SecurityConfigurations, out.NextToken, nil
		},
		func(c emrctypes.SecurityConfiguration) (*store.Resource, string, string) {
			return emrcRow(acct, region, scanID, TypeEMRContainersSecurityConfig, "security configurations", c.Arn, c.Id, c.Name, c, c.Tags)
		})
}

// scanEMRCJobTemplates discovers reusable EMR-on-EKS job templates (account-wide).
func scanEMRCJobTemplates(ctx context.Context, client emrContainersAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, emrcJobTemplatesOp,
		func(ctx context.Context, token *string) ([]emrctypes.JobTemplate, *string, error) {
			out, err := client.ListJobTemplates(ctx, &emrcontainers.ListJobTemplatesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Templates, out.NextToken, nil
		},
		func(jt emrctypes.JobTemplate) (*store.Resource, string, string) {
			r, why, key := emrcRow(acct, region, scanID, TypeEMRContainersJobTemplate, "job templates", jt.Arn, jt.Id, jt.Name, jt, jt.Tags)
			if r != nil {
				r.CreatedAt = tp(jt.CreatedAt)
			}
			return r, why, key
		})
}
