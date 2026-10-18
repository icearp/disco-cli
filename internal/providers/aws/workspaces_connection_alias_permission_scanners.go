package aws

import (
	"cmp"
	"context"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkSpacesConnectionAliasPermission, Service: "work-spaces"})
}

const wsDescribeAliasPermissionsOp = "workspaces:DescribeConnectionAliasPermissions"

// wsConnectionAliasPermission is the sharing permission set of one connection
// alias, gathered over every page. The list is sorted because the API
// documents no order, and is never nil, so an unshared alias always stores
// the same body. The keys are DescribeConnectionAliasPermissionsOutput's.
type wsConnectionAliasPermission struct {
	AliasID                    string `json:"AliasId"`
	ConnectionAliasPermissions []wstypes.ConnectionAliasPermission
}

// wsConnectionAliasPermissionNativeID is {aliasARN}/permission: the
// permission set has no ARN of its own and there is one per alias.
func wsConnectionAliasPermissionNativeID(aliasARN string) string {
	return aliasARN + "/permission"
}

// isWSAliasGone matches a connection alias deleted between its list call and
// the call that reads its permissions.
func isWSAliasGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanWSConnectionAliasPermissions stores, under each alias in aliases, the
// accounts it is shared with and whether each may associate it with a
// directory. One row per alias, stored even when the alias is shared with no
// one, so that sharing and unsharing show as a change on that row. aliases
// are the aliases the account owns.
//
// OperationNotSupportedException is modeled for the op but its meaning here
// is unverified, so an alias that gets it is skipped with one warning for all
// of them. InvalidParameterValuesException is skipped the same way: disco's
// inputs are a listed alias id and the model's page cap, so the likely cause
// is an alias not yet CREATED. The model documents the CREATED precondition
// only for the alias write ops, not for this call, so the aliases are not
// filtered on State. An alias whose call still fails transiently after the
// SDK's retries is skipped with one summary warning. Each skip keeps its
// siblings' rows.
func scanWSConnectionAliasPermissions(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, aliases []childParent) (int, int, error) {
	codes := []skipCode{{
		code: "OperationNotSupportedException",
		why:  "modeled for DescribeConnectionAliasPermissions; its meaning on this call is unverified",
	}, {
		code: "InvalidParameterValuesException",
		why:  "an alias still CREATING or DELETING may be refused (unverified)",
	}}
	return childFanOutCollectSkipCodes(ctx, st, acct, region, wsDescribeAliasPermissionsOp, aliases, isWSAliasGone, byParentID, "alias(es)", codes,
		func(ctx context.Context, alias childParent) ([]*store.Resource, error) {
			perm, err := describeWSConnectionAliasPermission(ctx, client, alias.id)
			if err != nil {
				return nil, err
			}
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesConnectionAliasPermission, NativeID: wsConnectionAliasPermissionNativeID(alias.arn),
				Name: &alias.id, Region: &region,
				AttributesJSON: mustJSON(perm), DiscoveredBy: scanID,
			}}, nil
		})
}

// describeWSConnectionAliasPermission reads every page of the permissions of
// the alias aliasID. The op has no SDK paginator.
func describeWSConnectionAliasPermission(ctx context.Context, client workSpacesAPI, aliasID string) (wsConnectionAliasPermission, error) {
	perm := wsConnectionAliasPermission{AliasID: aliasID, ConnectionAliasPermissions: []wstypes.ConnectionAliasPermission{}}
	in := &workspaces.DescribeConnectionAliasPermissionsInput{AliasId: &aliasID, MaxResults: sdkaws.Int32(wsPageLimit)}
	for {
		out, err := client.DescribeConnectionAliasPermissions(ctx, in)
		if err != nil {
			return wsConnectionAliasPermission{}, err
		}
		perm.ConnectionAliasPermissions = append(perm.ConnectionAliasPermissions, out.ConnectionAliasPermissions...)
		// A repeated token would loop forever.
		if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
			break
		}
		in.NextToken = out.NextToken
	}
	slices.SortFunc(perm.ConnectionAliasPermissions, func(a, b wstypes.ConnectionAliasPermission) int {
		return cmp.Or(
			cmp.Compare(sv(a.SharedAccountId), sv(b.SharedAccountId)),
			cmp.Compare(boolPtrRank(a.AllowAssociation), boolPtrRank(b.AllowAssociation)),
		)
	})
	return perm, nil
}

// boolPtrRank orders nil before false before true. AllowAssociation is
// model-required, but nil and false marshal differently, so the sort must
// still tell them apart to be total.
func boolPtrRank(b *bool) int {
	switch {
	case b == nil:
		return 0
	case !*b:
		return 1
	default:
		return 2
	}
}
