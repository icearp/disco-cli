package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeVpcLatticeServiceNetworkVpcEndpointAssociation, Service: "vpclattice"})
	registerType(restype.Descriptor{Type: TypeVpcLatticeTarget, Service: "vpclattice"})
}

const (
	vlListSNVpcEndpointAssocsOp = "vpclattice:ListServiceNetworkVpcEndpointAssociations"
	vlListTargetsOp             = "vpclattice:ListTargets"
)

// vlSNVpcEndpointAssocNativeID keys an association under its service network:
// the association has an id but no ARN.
func vlSNVpcEndpointAssocNativeID(netARN, assocID string) string {
	return netARN + "/vpc-endpoint-association/" + assocID
}

// vlTargetNativeID keys a target under its target group: targets have no ARN
// or id of their own. RegisterTargets and DeregisterTargets name a target by
// id and port, so the port is in the key when the target has one. It goes
// before the id because an ALB target's id is an ARN, which contains "/".
func vlTargetNativeID(tgARN, targetID string, port *int32) string {
	if port == nil {
		return tgARN + "/target/" + targetID
	}
	return fmt.Sprintf("%s/target/%d/%s", tgARN, *port, targetID)
}

// scanVLServiceNetworkVpcEndpointAssocs stores the VPC endpoint associations
// of each listed service network, one row per association, each under its
// service network. Networks shared with the account are listed too: an
// association there can name this account's endpoint, which the resolver can
// link. Per network: a denial of another account's network (otherAccountGap)
// keeps the pages read and is not warned about; any other denial warns once
// for the op, a
// transient error after the SDK's retries is skipped with one warning for all
// such networks, and anything else fails the phase, storing none of its rows.
// The op models no not-found error, so none is skipped.
func scanVLServiceNetworkVpcEndpointAssocs(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, networks []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, vlListSNVpcEndpointAssocsOp, networks, func(error) bool { return false }, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := vpclattice.NewListServiceNetworkVpcEndpointAssociationsPaginator(client,
				&vpclattice.ListServiceNetworkVpcEndpointAssociationsInput{ServiceNetworkIdentifier: &p.id},
				func(o *vpclattice.ListServiceNetworkVpcEndpointAssociationsPaginatorOptions) {
					o.Limit = vlPageCap
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if otherAccountGap(p.arn, acct.ID, err, nil) {
					return rows, nil
				}
				if err != nil {
					return nil, err
				}
				for _, a := range out.Items {
					id := sv(a.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeVpcLatticeServiceNetworkVpcEndpointAssociation, NativeID: vlSNVpcEndpointAssocNativeID(p.arn, id),
						Name: &id, Region: &region, Status: nonEmptyPtr(sv(a.State)), CreatedAt: tp(a.CreatedAt),
						AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanVLTargets stores the targets registered with each target group, one row
// per (target id, port), each under its target group. Status is the target's
// health-check status, so a target that turns unhealthy shows as a change.
// Per target group: ResourceNotFoundException (deleted since listing) is
// skipped silently, a denial warns once for the op, a transient error after
// the SDK's retries and ValidationException are each skipped with one warning
// for all such target groups, and anything else fails the phase, storing none
// of its rows. ValidationException is skipped because a target group can be
// listed while its creation is in progress, has failed, or its deletion is in
// progress, and how ListTargets answers for one has not been observed.
func scanVLTargets(ctx context.Context, client vpcLatticeAPI, acct *account, region string, st *store.Store, scanID string, tgs []childParent) (int, int, error) {
	return childFanOutSkipNotReady(ctx, st, acct, region, vlListTargetsOp, tgs,
		func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }, byParentARN,
		"target group(s)", "a target group may still be creating, have failed to create, or be deleting",
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := vpclattice.NewListTargetsPaginator(client, &vpclattice.ListTargetsInput{TargetGroupIdentifier: &p.id},
				func(o *vpclattice.ListTargetsPaginatorOptions) { o.Limit = vlPageCap })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, t := range out.Items {
					id := sv(t.Id)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeVpcLatticeTarget, NativeID: vlTargetNativeID(p.arn, id, t.Port),
						Name: &id, Region: &region, Status: nonEmptyPtr(string(t.Status)),
						AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}
