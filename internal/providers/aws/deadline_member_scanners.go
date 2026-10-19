package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeadlineFarmMember, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineFleetMember, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueMember, Service: "deadline"})
}

const (
	deadlineListFarmMembersOp  = "deadline:ListFarmMembers"
	deadlineListFleetMembersOp = "deadline:ListFleetMembers"
	deadlineListQueueMembersOp = "deadline:ListQueueMembers"

	// deadlineMemberPageLimit is the model's maxResults cap for the three
	// member list ops.
	deadlineMemberPageLimit = 100
)

// deadlineMemberNativeID keys a member under its farm, fleet or queue:
// members have no ARN, and DisassociateMemberFrom* names a membership by
// parent and principal id alone, so a principal is a member of a parent once
// and its membership level is left out of the key. The principal type is kept
// in the key so that the NativeID says which kind of principal it names.
func deadlineMemberNativeID(parentARN string, principalType dltypes.DeadlinePrincipalType, principalID string) string {
	return parentARN + "/member/" + string(principalType) + "/" + principalID
}

// deadlineMemberRow builds the row of one member listed under parentARN, or
// nil when the element lacks the principal type or id that key it.
func deadlineMemberRow(acct *account, region, scanID, typ, parentARN string, principalType dltypes.DeadlinePrincipalType, principalID *string, member any) *store.Resource {
	id := sv(principalID)
	if principalType == "" || id == "" {
		return nil
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: deadlineMemberNativeID(parentARN, principalType, id),
		Name: &id, Region: &region, AttributesJSON: mustJSON(member), DiscoveredBy: scanID,
	}
}

// isDeadlineParentGone reports the farm, fleet or queue deleted between its
// listing and its member listing.
func isDeadlineParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanDeadlineFarmMembers stores the members of each farm, one row per
// principal, each under its farm. Per farm: ResourceNotFoundException is
// skipped silently, a denial warns once for the op, a transient error after
// the SDK's retries is skipped with one warning for all such farms, and
// anything else fails the phase, storing none of its rows.
func scanDeadlineFarmMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	parents := make([]childParent, len(farms))
	for i, f := range farms {
		parents[i] = childParent{id: f.id, arn: f.arn}
	}
	return childFanOutSkipTransient(ctx, st, acct, region, deadlineListFarmMembersOp, parents, isDeadlineParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := deadline.NewListFarmMembersPaginator(client, &deadline.ListFarmMembersInput{FarmId: &p.id},
				func(o *deadline.ListFarmMembersPaginatorOptions) { o.Limit = deadlineMemberPageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, m := range out.Members {
					if r := deadlineMemberRow(acct, region, scanID, TypeDeadlineFarmMember, p.arn, m.PrincipalType, m.PrincipalId, m); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

// deadlineFarmChildParents flattens the fleets or queues (pick) of every farm
// into fan-out parents, with the farm id that each one's member listing also
// needs, keyed by the child's ARN.
func deadlineFarmChildParents(farms []dlFarmRef, pick func(dlFarmRef) []childParent) ([]childParent, map[string]string) {
	var parents []childParent
	farmIDs := map[string]string{}
	for _, f := range farms {
		for _, c := range pick(f) {
			parents = append(parents, c)
			farmIDs[c.arn] = f.id
		}
	}
	return parents, farmIDs
}

// scanDeadlineFleetMembers stores the members of each fleet, one row per
// principal, each under its fleet. Per fleet the errors are handled as in
// scanDeadlineFarmMembers, except that ValidationException is also skipped,
// with one warning for all such fleets: a fleet can be listed while its
// creation is in progress or has failed, and how the member listing answers
// for such a fleet has not been observed.
func scanDeadlineFleetMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	parents, farmIDs := deadlineFarmChildParents(farms, func(f dlFarmRef) []childParent { return f.fleets })
	return childFanOutSkipNotReady(ctx, st, acct, region, deadlineListFleetMembersOp, parents, isDeadlineParentGone, byParentARN,
		"fleet(s)", "a fleet may still be creating or have failed to create",
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			farmID := farmIDs[p.arn]
			pager := deadline.NewListFleetMembersPaginator(client, &deadline.ListFleetMembersInput{FarmId: &farmID, FleetId: &p.id},
				func(o *deadline.ListFleetMembersPaginatorOptions) { o.Limit = deadlineMemberPageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, m := range out.Members {
					if r := deadlineMemberRow(acct, region, scanID, TypeDeadlineFleetMember, p.arn, m.PrincipalType, m.PrincipalId, m); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}

// scanDeadlineQueueMembers stores the members of each queue, one row per
// principal, each under its queue. Per queue the errors are handled as in
// scanDeadlineFarmMembers.
func scanDeadlineQueueMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	parents, farmIDs := deadlineFarmChildParents(farms, func(f dlFarmRef) []childParent { return f.queues })
	return childFanOutSkipTransient(ctx, st, acct, region, deadlineListQueueMembersOp, parents, isDeadlineParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			farmID := farmIDs[p.arn]
			pager := deadline.NewListQueueMembersPaginator(client, &deadline.ListQueueMembersInput{FarmId: &farmID, QueueId: &p.id},
				func(o *deadline.ListQueueMembersPaginatorOptions) { o.Limit = deadlineMemberPageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, m := range out.Members {
					if r := deadlineMemberRow(acct, region, scanID, TypeDeadlineQueueMember, p.arn, m.PrincipalType, m.PrincipalId, m); r != nil {
						rows = append(rows, r)
					}
				}
			}
			return rows, nil
		})
}
