package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
)

// deadlineMemberNativeID keys a member under its farm, fleet or queue:
// members have no ARN, and DisassociateMemberFrom* names a membership by
// parent and principal id alone, so a principal is a member of a parent once
// and its membership level is left out of the key. The principal type is kept
// in the key so that the NativeID says which kind of principal it names.
func deadlineMemberNativeID(parentARN string, principalType dltypes.DeadlinePrincipalType, principalID string) string {
	return parentARN + "/member/" + string(principalType) + "/" + principalID
}

// deadlineMemberRow builds the row of one member listed under parentARN. An
// element without the principal type or id that key it is not stored: the
// reason and a key naming it go to the not-stored warning.
func deadlineMemberRow(acct *account, region, scanID, typ, parentARN string, principalType dltypes.DeadlinePrincipalType, principalID *string, member any) (*store.Resource, string, string) {
	id := sv(principalID)
	if principalType == "" || id == "" {
		return nil, "members have an empty PrincipalType or PrincipalId", cmp.Or(id, "(a member with no principal id)") + " of " + parentARN
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: deadlineMemberNativeID(parentARN, principalType, id),
		Name: &id, Region: &region, AttributesJSON: mustJSON(member), DiscoveredBy: scanID,
	}, "", ""
}

// deadlineStoreMembers stores the members each parent lists, each under its
// parent, through storeChildrenEachParent: the parents are read concurrently, each
// keeps the pages read before its error (a repeated NextToken included) and
// is classified on its own, so one parent's error costs no sibling its rows.
// Per parent: ResourceNotFoundException (the parent was deleted since it was
// listed) is skipped silently; a denial warns once for the op; an error still
// transient after the SDK's retries, or one skip accepts (nil for none), is
// skipped with one warning for all such parents; anything else, an NXDOMAIN
// included (dropUnlessNXDOMAIN), is returned after the rows are stored. Every
// warning goes out only after the write succeeded.
func deadlineStoreMembers[M any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	skip func(parent string, err error) bool, warnSkipped func(),
	page func(ctx context.Context, p childParent, token *string) ([]M, *string, error),
	row func(p childParent, m M) (*store.Resource, string, string),
) (int, int, error) {
	var transients transientTally
	return storeChildrenEachParent(ctx, st, acct, region, op, deadlineNotFound, parents,
		func(parent string, err error) bool {
			return transients.dropUnlessNXDOMAIN(parent, err) || (skip != nil && skip(parent, err))
		},
		func() {
			transients.warnSkipped(st, op, acct, region)
			if warnSkipped != nil {
				warnSkipped()
			}
		}, page, row)
}

// scanDeadlineFarmMembers stores the members of each farm, one row per
// principal (deadlineStoreMembers).
func scanDeadlineFarmMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	return deadlineStoreMembers(ctx, st, acct, region, deadlineListFarmMembersOp, deadlineFarmParents(farms), nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]dltypes.FarmMember, *string, error) {
			out, err := client.ListFarmMembers(ctx, &deadline.ListFarmMembersInput{FarmId: &p.id, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Members, out.NextToken, nil
		},
		func(p childParent, m dltypes.FarmMember) (*store.Resource, string, string) {
			return deadlineMemberRow(acct, region, scanID, TypeDeadlineFarmMember, p.arn, m.PrincipalType, m.PrincipalId, m)
		})
}

// scanDeadlineFleetMembers stores the members of each fleet, one row per
// principal (deadlineStoreMembers). ValidationException is also skipped,
// with one warning for all such fleets: a fleet can be listed while its
// creation is in progress or has failed, and how the member listing answers
// for such a fleet has not been observed.
func scanDeadlineFleetMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	parents, keys := deadlineFarmChildren(farms, func(f dlFarmRef) []childParent { return f.fleets })
	var notReady notReadyTally
	return deadlineStoreMembers(ctx, st, acct, region, deadlineListFleetMembersOp, parents, notReady.drop,
		func() {
			notReady.warnSkipped(st, deadlineListFleetMembersOp, acct, region, "fleet(s)", "a fleet may still be creating or have failed to create")
		},
		func(ctx context.Context, p childParent, token *string) ([]dltypes.FleetMember, *string, error) {
			k := keys[p.arn]
			out, err := client.ListFleetMembers(ctx, &deadline.ListFleetMembersInput{FarmId: &k.farmID, FleetId: &k.id, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Members, out.NextToken, nil
		},
		func(p childParent, m dltypes.FleetMember) (*store.Resource, string, string) {
			return deadlineMemberRow(acct, region, scanID, TypeDeadlineFleetMember, p.arn, m.PrincipalType, m.PrincipalId, m)
		})
}

// scanDeadlineQueueMembers stores the members of each queue, one row per
// principal (deadlineStoreMembers).
func scanDeadlineQueueMembers(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	parents, keys := deadlineFarmChildren(farms, func(f dlFarmRef) []childParent { return f.queues })
	return deadlineStoreMembers(ctx, st, acct, region, deadlineListQueueMembersOp, parents, nil, nil,
		func(ctx context.Context, p childParent, token *string) ([]dltypes.QueueMember, *string, error) {
			k := keys[p.arn]
			out, err := client.ListQueueMembers(ctx, &deadline.ListQueueMembersInput{FarmId: &k.farmID, QueueId: &k.id, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Members, out.NextToken, nil
		},
		func(p childParent, m dltypes.QueueMember) (*store.Resource, string, string) {
			return deadlineMemberRow(acct, region, scanID, TypeDeadlineQueueMember, p.arn, m.PrincipalType, m.PrincipalId, m)
		})
}
