package aws

import (
	"context"
	"errors"
	"fmt"
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
	ListVmEntitlements(context.Context, *evs.ListVmEntitlementsInput, ...func(*evs.Options)) (*evs.ListVmEntitlementsOutput, error)
}

const (
	evsListEnvironmentConnectorsOp = "evs:ListEnvironmentConnectors"
	evsListEnvironmentHostsOp      = "evs:ListEnvironmentHosts"
	evsListEnvironmentVlansOp      = "evs:ListEnvironmentVlans"
	evsListVMEntitlementsOp        = "evs:ListVmEntitlements"
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
// over the environments it listed, each independent of the others, then the
// entitlement phase over the connectors the connector phase stored. A failed
// environment phase stores nothing and lists no parents, so it returns alone.
// Child phases tally their transient errors per parent, so none of their
// errors is transient and the join needs no demotion.
func scanEVSAll(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, envs, err := scanEVSEnvironments(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	th, ih, herr := scanEVSHosts(ctx, client, acct, region, st, scanID, envs)
	tv, iv, verr := scanEVSVlans(ctx, client, acct, region, st, scanID, envs)
	tc, ic, vcenters, cerr := scanEVSConnectors(ctx, client, acct, region, st, scanID, envs)
	te, ie, eerr := scanEVSVMEntitlements(ctx, client, acct, region, st, scanID, vcenters)
	return total + th + tv + tc + te, inserted + ih + iv + ic + ie, errors.Join(herr, verr, cerr, eerr)
}

// scanEVSEnvironments stores the region's environments and returns those not
// deleted as the parents of the child phases.
func scanEVSEnvironments(ctx context.Context, client evsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, envs []childParent, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &evs.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "evs:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("evs:ListEnvironments: %w", err)
		}
		for _, e := range out.EnvironmentSummaries {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			status := string(e.EnvironmentState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEVSEnvironment, NativeID: arn,
				Name: e.EnvironmentName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
			if id := sv(e.EnvironmentId); id != "" && e.EnvironmentState != evstypes.EnvironmentStateDeleted {
				envs = append(envs, childParent{id: id, arn: arn})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err = upsertBatch(st, batch, "evs environments")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, envs, nil
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
