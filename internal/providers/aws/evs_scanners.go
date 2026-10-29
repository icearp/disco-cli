package aws

import (
	"context"
	"slices"
	"strconv"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/evs"
	evstypes "github.com/aws/aws-sdk-go-v2/service/evs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEVSEnvironment, Service: "evs"})
	registerType(restype.Descriptor{Type: TypeEVSEnvironmentConnector, Service: "evs"})
	registerType(restype.Descriptor{Type: TypeEVSEnvironmentHost, Service: "evs"})
	registerType(restype.Descriptor{Type: TypeEVSEnvironmentVlan, Service: "evs"})
	registerType(restype.Descriptor{Type: TypeEVSVMEntitlement, Service: "evs"})
	registerService(serviceEntry{
		name: "aws:evs",
		fn:   scanEVS,
	})
}

type evsAPI interface {
	ListEnvironments(context.Context, *evs.ListEnvironmentsInput, ...func(*evs.Options)) (*evs.ListEnvironmentsOutput, error)
	ListEnvironmentConnectors(context.Context, *evs.ListEnvironmentConnectorsInput, ...func(*evs.Options)) (*evs.ListEnvironmentConnectorsOutput, error)
	ListEnvironmentHosts(context.Context, *evs.ListEnvironmentHostsInput, ...func(*evs.Options)) (*evs.ListEnvironmentHostsOutput, error)
	ListEnvironmentVlans(context.Context, *evs.ListEnvironmentVlansInput, ...func(*evs.Options)) (*evs.ListEnvironmentVlansOutput, error)
	ListTagsForResource(context.Context, *evs.ListTagsForResourceInput, ...func(*evs.Options)) (*evs.ListTagsForResourceOutput, error)
	ListVmEntitlements(context.Context, *evs.ListVmEntitlementsInput, ...func(*evs.Options)) (*evs.ListVmEntitlementsOutput, error)
}

const (
	evsListEnvironmentsOp          = "evs:ListEnvironments"
	evsListEnvironmentConnectorsOp = "evs:ListEnvironmentConnectors"
	evsListEnvironmentHostsOp      = "evs:ListEnvironmentHosts"
	evsListEnvironmentVlansOp      = "evs:ListEnvironmentVlans"
	evsListVMEntitlementsOp        = "evs:ListVmEntitlements"
	evsTagsOp                      = "evs:ListTagsForResource"
)

// evsPageLimit is the MaxResults cap the EVS model documents for its list ops.
const evsPageLimit = 100

// scanEVS discovers Elastic VMware Service (EVS) environments and their hosts,
// VLANs, connectors and VM entitlements.
func scanEVS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := evs.NewFromConfig(acct.cfg, func(o *evs.Options) { o.Region = region })
	return scanEVSAll(ctx, client, acct, region, st, scanID)
}

// scanEVSAll runs the environment phase, then the per-environment child phases
// over the environments it stored, then the entitlement phase over the
// vCenter connectors the connector phase stored, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases).
func scanEVSAll(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		envs     []childParent
		vcenters []evsConnectorParent
	)
	return runHostPhases(ctx, st, acct, region, "evs", 0, []hostPhase{
		{op: evsListEnvironmentsOp, run: func(p *phaseTotals) (failed bool) {
			envs, failed = scanEVSEnvironments(ctx, client, acct, region, st, scanID, p)
			return failed
		}},
		countedPhase(0, evsListEnvironmentHostsOp, func() (int, int, error) {
			return scanEVSHosts(ctx, client, acct, region, st, scanID, envs)
		}),
		countedPhase(0, evsListEnvironmentVlansOp, func() (int, int, error) {
			return scanEVSVlans(ctx, client, acct, region, st, scanID, envs)
		}),
		countedPhase(0, evsListEnvironmentConnectorsOp, func() (int, int, error) {
			t, i, found, err := scanEVSConnectors(ctx, client, acct, region, st, scanID, envs)
			vcenters = found
			return t, i, err
		}),
		countedPhase(0, evsListVMEntitlementsOp, func() (int, int, error) {
			return scanEVSVMEntitlements(ctx, client, acct, region, st, scanID, vcenters)
		}),
	})
}

// evsTags reads an environment's tags with ListTagsForResource, sent the row's
// NativeID, the ARN ListEnvironments returned. The environment is the only
// EVS type that can carry tags: it is the only resource the Service Reference
// defines, the op's model ARN pattern accepts only environment ARNs, only
// CreateEnvironment takes tags, no other response carries them, and
// TagResource's documentation says an environment's tags do not reach its
// hosts or VLANs.
//
// A DELETED environment is not read and stays untagged: TagResource's
// documentation says a resource's tags are deleted with it, and the op could
// answer ResourceNotFoundException, which would drop the listed row.
func evsTags(client evsAPI) rowTagReader {
	return rowTagReader{
		op:       evsTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if sv(r.Status) == string(evstypes.EnvironmentStateDeleted) {
				return nil, nil
			}
			out, err := client.ListTagsForResource(ctx, &evs.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanEVSEnvironments stores the region's environments with their tags, a page
// at a time, and returns those stored and not deleted as the parents of the
// child phases. Counts and errors go to p; it reports whether a write failed,
// after which the scan must stop.
func scanEVSEnvironments(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) ([]childParent, bool) {
	deleted := map[string]bool{}
	parents, failed := storeParentPagesTags(ctx, st, acct, region, evsListEnvironmentsOp, "environments", "EnvironmentId",
		func(ctx context.Context, token *string) ([]evstypes.EnvironmentSummary, *string, error) {
			out, err := client.ListEnvironments(ctx, &evs.ListEnvironmentsInput{MaxResults: sdkaws.Int32(evsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EnvironmentSummaries, out.NextToken, nil
		},
		func(e evstypes.EnvironmentSummary) (*store.Resource, string, string) {
			arn := sv(e.EnvironmentArn)
			if e.EnvironmentState == evstypes.EnvironmentStateDeleted {
				deleted[arn] = true
			}
			return arnRow("environments", e.EnvironmentArn, e.EnvironmentName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEVSEnvironment, NativeID: arn,
				Name: e.EnvironmentName, Region: &region, Status: nonEmptyPtr(string(e.EnvironmentState)),
				CreatedAt: tp(e.CreatedAt), AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		},
		func(e evstypes.EnvironmentSummary) string {
			// A DELETED environment is dropped from the parents below
			// whatever its id, so it must not be warned about as one whose
			// missing id left its children unread.
			if e.EnvironmentState == evstypes.EnvironmentStateDeleted {
				return sv(e.EnvironmentArn)
			}
			return sv(e.EnvironmentId)
		},
		evsTags(client), p)
	return slices.DeleteFunc(parents, func(env childParent) bool { return deleted[env.arn] }), failed
}

// evsChildNativeID keys an environment child under its environment: hosts,
// VLANs and connectors have no ARN (the Service Reference defines only the
// environment ARN). No key contains "/": host names and connector ids have
// model patterns that rule it out, and VLAN ids are integers.
func evsChildNativeID(envARN, kind, key string) string {
	return envARN + "/" + kind + "/" + key
}

// evsVMEntitlementNativeID keys an entitlement under the connector it is
// listed by: the model gives it no id of its own. This assumes a VM (vmId
// ^vm-[0-9]+$) holds at most one entitlement per type through one connector,
// as CreateEntitlement (one type, a list of VM ids) suggests; if not, two
// entitlements would share one row.
func evsVMEntitlementNativeID(connectorNativeID string, typ evstypes.EntitlementType, vmID string) string {
	return connectorNativeID + "/vm-entitlement/" + string(typ) + "/" + vmID
}

// evsChildFanOut runs list for each parent through childFanOut. Per parent:
// ResourceNotFoundException (deleted since listing) is skipped silently, a
// denial warns once for op, a transient error after the SDK's retries is
// skipped with one warning for all such parents, ValidationException is
// skipped with one warning for all such parents, and anything else fails the
// phase, storing none of its rows.
//
// The model lists only ResourceNotFoundException and ValidationException for
// these ops. Parents are fanned out to in every state but DELETED, and how
// EVS answers for one still creating, failed or deleting has not been
// observed; if it is ValidationException, failing the phase would fail it on
// every scan, and skipping silently would hide a real input error, so it
// warns and keeps the siblings.
func evsChildFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipNotReady(ctx, st, acct, region, op, parents, isGone, byParentID,
		"parent(s)", "the parent may not be ready (creating, failed or deleting)", list)
}

// scanEVSHosts stores each environment's hosts that are not deleted.
func scanEVSHosts(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string, envs []childParent) (int, int, error) {
	return evsChildFanOut(ctx, st, acct, region, evsListEnvironmentHostsOp, envs, func(ctx context.Context, env childParent) ([]*store.Resource, error) {
		pager := evs.NewListEnvironmentHostsPaginator(client,
			&evs.ListEnvironmentHostsInput{EnvironmentId: sdkaws.String(env.id)},
			func(o *evs.ListEnvironmentHostsPaginatorOptions) { o.Limit = evsPageLimit })
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, h := range out.EnvironmentHosts {
				name := sv(h.HostName)
				if name == "" || h.HostState == evstypes.HostStateDeleted {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeEVSEnvironmentHost, NativeID: evsChildNativeID(env.arn, "host", name),
					Name: h.HostName, Region: &region, Status: nonEmptyPtr(string(h.HostState)),
					CreatedAt: tp(h.CreatedAt), AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// scanEVSVlans stores each environment's VLANs that are not deleted, keyed by
// VlanId, which the model documents as the VLAN's unique id; the function name
// is free text the model neither requires nor constrains, so it is only the
// row's name.
func scanEVSVlans(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string, envs []childParent) (int, int, error) {
	return evsChildFanOut(ctx, st, acct, region, evsListEnvironmentVlansOp, envs, func(ctx context.Context, env childParent) ([]*store.Resource, error) {
		pager := evs.NewListEnvironmentVlansPaginator(client,
			&evs.ListEnvironmentVlansInput{EnvironmentId: sdkaws.String(env.id)},
			func(o *evs.ListEnvironmentVlansPaginatorOptions) { o.Limit = evsPageLimit })
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, v := range out.EnvironmentVlans {
				if v.VlanId == nil || v.VlanState == evstypes.VlanStateDeleted {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeEVSEnvironmentVlan, NativeID: evsChildNativeID(env.arn, "vlan", strconv.Itoa(int(*v.VlanId))),
					Name: nonEmptyPtr(sv(v.FunctionName)), Region: &region, Status: nonEmptyPtr(string(v.VlanState)),
					CreatedAt: tp(v.CreatedAt), AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// evsConnectorParent is a vCenter connector stored this scan, the parent of
// the entitlement phase.
type evsConnectorParent struct {
	envID, connectorID, nativeID string
}

// scanEVSConnectors stores each environment's connectors that are not deleted
// and returns the vCenter ones, the only type CreateEntitlement accepts. It
// returns none when the phase fails, since their rows were not stored.
func scanEVSConnectors(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string, envs []childParent) (int, int, []evsConnectorParent, error) {
	var (
		mu       sync.Mutex
		vcenters []evsConnectorParent
	)
	t, i, err := evsChildFanOut(ctx, st, acct, region, evsListEnvironmentConnectorsOp, envs, func(ctx context.Context, env childParent) ([]*store.Resource, error) {
		pager := evs.NewListEnvironmentConnectorsPaginator(client,
			&evs.ListEnvironmentConnectorsInput{EnvironmentId: sdkaws.String(env.id)},
			func(o *evs.ListEnvironmentConnectorsPaginatorOptions) { o.Limit = evsPageLimit })
		var (
			rows  []*store.Resource
			found []evsConnectorParent
		)
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, c := range out.Connectors {
				id := sv(c.ConnectorId)
				if id == "" || c.State == evstypes.ConnectorStateDeleted {
					continue
				}
				nativeID := evsChildNativeID(env.arn, "connector", id)
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeEVSEnvironmentConnector, NativeID: nativeID,
					Name: c.ApplianceFqdn, Region: &region, Status: nonEmptyPtr(string(c.State)),
					CreatedAt: tp(c.CreatedAt), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
				if c.Type == evstypes.ConnectorTypeVcenter {
					found = append(found, evsConnectorParent{envID: env.id, connectorID: id, nativeID: nativeID})
				}
			}
		}
		mu.Lock()
		vcenters = append(vcenters, found...)
		mu.Unlock()
		return rows, nil
	})
	if err != nil {
		return t, i, nil, err
	}
	return t, i, vcenters, nil
}

// scanEVSVMEntitlements stores the VM entitlements of each vCenter connector,
// one list per entitlement type the SDK knows, each under its connector. A
// failed connector is handled as evsChildFanOut describes; ResourceNotFound
// there means the connector or its environment was deleted since listing.
// Entitlements whose status is DELETED are not stored.
func scanEVSVMEntitlements(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string, connectors []evsConnectorParent) (int, int, error) {
	parents := make([]childParent, len(connectors))
	byNativeID := make(map[string]evsConnectorParent, len(connectors))
	for i, c := range connectors {
		parents[i] = childParent{id: c.connectorID, arn: c.nativeID}
		byNativeID[c.nativeID] = c
	}
	return evsChildFanOut(ctx, st, acct, region, evsListVMEntitlementsOp, parents, func(ctx context.Context, p childParent) ([]*store.Resource, error) {
		c := byNativeID[p.arn]
		var rows []*store.Resource
		for _, typ := range evstypes.EntitlementType("").Values() {
			pager := evs.NewListVmEntitlementsPaginator(client,
				&evs.ListVmEntitlementsInput{
					EnvironmentId: sdkaws.String(c.envID), ConnectorId: sdkaws.String(c.connectorID), EntitlementType: typ,
				},
				func(o *evs.ListVmEntitlementsPaginatorOptions) { o.Limit = evsPageLimit })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, e := range out.Entitlements {
					vmID := sv(e.VmId)
					if vmID == "" || e.Status == evstypes.EntitlementStatusDeleted {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeEVSVMEntitlement, NativeID: evsVMEntitlementNativeID(c.nativeID, typ, vmID),
						Name: e.VmName, Region: &region, Status: nonEmptyPtr(string(e.Status)),
						AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
					})
				}
			}
		}
		return rows, nil
	})
}
