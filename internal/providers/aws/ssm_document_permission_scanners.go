package aws

import (
	"cmp"
	"context"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMDocumentPermission, Service: "ssm"})
}

const (
	ssmDescribeDocumentPermissionOp = "ssm:DescribeDocumentPermission"
	// ssmDocumentPermissionPageLimit is the model's cap on
	// DescribeDocumentPermission MaxResults (DocumentPermissionMaxResults, 1..200).
	ssmDocumentPermissionPageLimit int32 = 200
)

// ssmDocumentPermission is the Share permission of one document, gathered
// over every page. Both lists are sorted because the API documents no order.
// They are nil (stored as null: not read) only when the listing ended
// incomplete, so an unshared document always stores the same body and a
// partial share list is never stored. The keys are
// DescribeDocumentPermissionOutput's.
type ssmDocumentPermission struct {
	AccountIDs             []string `json:"AccountIds"`
	AccountSharingInfoList []types.AccountSharingInfo
}

// ssmDocumentPermissionNativeID is {documentARN}/permission/Share: the
// permission has no ARN of its own, and Share is the only permission type.
func ssmDocumentPermissionNativeID(documentARN string) string {
	return documentARN + "/permission/" + string(types.DocumentPermissionTypeShare)
}

// scanSSMDocumentPermissions stores, under each document in docs, the accounts
// it is shared with ("all" when public). docs are the documents the account
// owns; whether the op accepts every owned document (one still Creating or
// Failed, say) has not been verified.
//
// InvalidDocument is skipped silently as not-found: its model text covers both
// a document that does not exist (deleted since it was listed) and one that
// "isn't available to the user". If an owned Creating or Failed document gets
// it, that document loses its sharing row without a warning (unverified).
// InvalidDocumentOperation is modeled for the op but its effect here is
// unverified, so a document that gets it is skipped with one warning for all of them. A
// document whose call still fails transiently after the SDK's retries is
// skipped with one summary warning. Each skip keeps its siblings' rows.
func scanSSMDocumentPermissions(ctx context.Context, client ssmAPI, acct *account, region string, st *store.Store, scanID string, docs []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "InvalidDocument") }
	codes := []skipCode{{
		code: "InvalidDocumentOperation",
		why:  "modeled for DescribeDocumentPermission; its meaning on this call is unverified",
	}}
	return childFanOutCollectSkipCodes(ctx, st, acct, region, ssmDescribeDocumentPermissionOp, docs, isGone, byParentID, "document(s)", codes,
		func(ctx context.Context, doc childParent) ([]*store.Resource, error) {
			// An incomplete listing keeps the row, its lists null, and returns
			// the error with it.
			perm, err := describeSSMDocumentPermission(ctx, client, doc.id)
			if err != nil && !isIncompleteListing(err) {
				return nil, err
			}
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMDocumentPermission, NativeID: ssmDocumentPermissionNativeID(doc.arn),
				Name: &doc.id, Region: &region,
				AttributesJSON: mustJSON(perm), DiscoveredBy: scanID,
			}}, err
		})
}

// describeSSMDocumentPermission reads every page of the Share permission of
// the document name. The op has no SDK paginator. With an error it returns no
// share lists, never part of them.
func describeSSMDocumentPermission(ctx context.Context, client ssmAPI, name string) (ssmDocumentPermission, error) {
	perm := ssmDocumentPermission{AccountIDs: []string{}, AccountSharingInfoList: []types.AccountSharingInfo{}}
	in := &ssm.DescribeDocumentPermissionInput{
		Name:           &name,
		PermissionType: types.DocumentPermissionTypeShare,
		MaxResults:     sdkaws.Int32(ssmDocumentPermissionPageLimit),
	}
	for {
		out, err := client.DescribeDocumentPermission(ctx, in)
		if err != nil {
			return ssmDocumentPermission{}, err
		}
		perm.AccountIDs = append(perm.AccountIDs, out.AccountIds...)
		perm.AccountSharingInfoList = append(perm.AccountSharingInfoList, out.AccountSharingInfoList...)
		// A repeated token would loop forever.
		done, err := lastPage(in.NextToken, out.NextToken)
		if err != nil {
			return ssmDocumentPermission{}, err
		}
		if done {
			break
		}
		in.NextToken = out.NextToken
	}
	slices.Sort(perm.AccountIDs)
	slices.SortFunc(perm.AccountSharingInfoList, func(a, b types.AccountSharingInfo) int {
		return cmp.Or(cmp.Compare(sv(a.AccountId), sv(b.AccountId)), cmp.Compare(sv(a.SharedDocumentVersion), sv(b.SharedDocumentVersion)))
	})
	return perm, nil
}
