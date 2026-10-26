package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/qapps"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQAppsQApp, Service: "qapps"})
	registerType(restype.Descriptor{Type: TypeQAppsCategory, Service: "qapps"})
	registerType(restype.Descriptor{Type: TypeQAppsLibraryItem, Service: "qapps"})
	registerService(serviceEntry{
		name: "aws:qapps",
		fn:   scanQApps,
	})
}

const (
	qappsListQAppsOp        = "qapps:ListQApps"
	qappsListCategoriesOp   = "qapps:ListCategories"
	qappsListLibraryItemsOp = "qapps:ListLibraryItems"
	// qappsPageLimit is the model's cap on the ListQApps and ListLibraryItems
	// limit (PageLimit, range 1..100).
	qappsPageLimit int32 = 100
)

// qappsInstanceSkipCodes are the codes each per-instance phase skips with one
// warning per code. Failing the phase on either would fail it on every scan
// while the cause lasts, and skipping silently would hide a real input error.
// The inputs disco sets are the listed instance id and, for the paginated
// ops, a page size pinned to the model cap by a test, so neither code is
// expected to name a disco input.
var qappsInstanceSkipCodes = []skipCode{
	// ListApplications also lists applications that are CREATING, UPDATING,
	// DELETING or FAILED, which the instance ops may refuse as invalid input.
	{code: "ValidationException", why: "an application that is not ACTIVE may be refused"},
	// HTTP 401, "not authenticated": the Q Apps APIs read the user identity
	// from the caller's credentials (ListQApps doc), so a caller without a
	// Q Business user identity may be refused.
	{code: "UnauthorizedException", why: "a caller without a Q Business user identity may be refused"},
}

const qappsInstanceSkipWhat = "Q Apps instance(s)"

type qappsAPI interface {
	ListQApps(context.Context, *qapps.ListQAppsInput, ...func(*qapps.Options)) (*qapps.ListQAppsOutput, error)
	ListCategories(context.Context, *qapps.ListCategoriesInput, ...func(*qapps.Options)) (*qapps.ListCategoriesOutput, error)
	ListLibraryItems(context.Context, *qapps.ListLibraryItemsInput, ...func(*qapps.Options)) (*qapps.ListLibraryItemsOutput, error)
}

// scanQApps enumerates Q Business applications (each a Q Apps "instance")
// directly via the qbusiness SDK, then, per instance, the Q Apps ListQApps
// returns, the categories and the library items. Listing parents in-scanner
// avoids depending on the qbusiness scanner's phase-1 ordering — phase-1
// scanners run concurrently, so reading already-stored qbusiness rows would
// race. For the same reason no child is linked under its application: the
// application row may not be stored yet.
//
// What each phase can see depends on the caller. ListQApps returns only the
// Q Apps owned by or associated with the calling identity (SDK doc), not
// every Q App of the instance. Categories and library items are documented
// as the instance's; whether a library item restricted by permissions is
// listed for every caller has not been verified.
//
// The three per-instance phases run whatever the others return, so a phase's
// failure costs the others nothing; the counts are summed and the errors
// joined. Each phase skips a transient instance itself, so no joined error is
// transient.
func scanQApps(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	qbClient := qbusiness.NewFromConfig(acct.cfg, func(o *qbusiness.Options) { o.Region = region })
	pager := qbusiness.NewListApplicationsPaginator(qbClient, &qbusiness.ListApplicationsInput{})
	var instanceIDs []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "qapps:ListApplications", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("qapps:ListApplications: %w", perr)
		}
		for _, a := range out.Applications {
			if id := sv(a.ApplicationId); id != "" {
				instanceIDs = append(instanceIDs, id)
			}
		}
	}

	client := qapps.NewFromConfig(acct.cfg, func(o *qapps.Options) { o.Region = region })
	instances := qappsInstances(acct.partition(), region, acct.ID, instanceIDs)
	qt, qi, qerr := scanQAppsQApps(ctx, client, acct, region, st, scanID, instances)
	ct, ci, cerr := scanQAppsCategories(ctx, client, acct, region, st, scanID, instances)
	lt, li, lerr := scanQAppsLibraryItems(ctx, client, acct, region, st, scanID, instances)
	return qt + ct + lt, qi + ci + li, errors.Join(qerr, cerr, lerr)
}

// scanQAppsInstances runs storeChildren for each instance with the shared
// per-instance error handling: a gone instance is skipped silently, a denial
// warns once, a transient error after the SDK's retries is skipped with one
// warning for all such instances, a qappsInstanceSkipCodes code is skipped
// with one warning per code, and anything else fails the phase. Rows an
// instance stored before its error stay and are counted.
func scanQAppsInstances(ctx context.Context, acct *account, region string, st *store.Store, op string, instances []childParent,
	storeChildren func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	return childFanOutStoredSkipCodes(ctx, st, acct, region, op, instances, isQAppsInstanceGone, byParentID,
		qappsInstanceSkipWhat, qappsInstanceSkipCodes, storeChildren)
}

// scanQAppsQApps stores the Q Apps ListQApps returns for each instance, a
// page at a time.
func scanQAppsQApps(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent) (int, int, error) {
	return scanQAppsInstances(ctx, acct, region, st, qappsListQAppsOp, instances,
		func(ctx context.Context, inst childParent) (int, int, error) {
			return storeQAppsQApps(ctx, client, acct, region, st, scanID, inst)
		})
}

// storeQAppsQApps stores one instance's Q Apps a page at a time.
func storeQAppsQApps(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, inst childParent) (total, inserted int, err error) {
	pager := qapps.NewListQAppsPaginator(client,
		&qapps.ListQAppsInput{InstanceId: &inst.id},
		func(o *qapps.ListQAppsPaginatorOptions) {
			o.Limit = qappsPageLimit
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var batch []*store.Resource
		for _, a := range out.Apps {
			arn := sv(a.AppArn)
			if arn == "" {
				continue
			}
			label := sv(a.Title)
			if label == "" {
				label = sv(a.AppId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQAppsQApp, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		t, i, err := upsertBatch(st, batch, qappsListQAppsOp)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// qappsInstanceARN is the prefix the Service Reference's qapp ARN format
// (arn:${Partition}:qapps:${Region}:${Account}:application/${ApplicationId}/qapp/${AppId})
// puts every Q App of an instance under. Categories and library items have
// no ARN of their own, so their NativeIDs extend it with a kind segment that
// no qapp ARN uses.
func qappsInstanceARN(partition, region, accountID, instanceID string) string {
	return arnFor(partition, "qapps", region, accountID, "application/"+instanceID)
}

// qappsCategoryNativeID and qappsLibraryItemNativeID key a child by its
// model id (a UUID by the model's pattern, so it holds no "/").
func qappsCategoryNativeID(instanceARN, categoryID string) string {
	return instanceARN + "/category/" + categoryID
}

func qappsLibraryItemNativeID(instanceARN, libraryItemID string) string {
	return instanceARN + "/library-item/" + libraryItemID
}

// qappsInstances pairs each listed instance id with its qapps ARN prefix.
func qappsInstances(partition, region, accountID string, instanceIDs []string) []childParent {
	instances := make([]childParent, len(instanceIDs))
	for i, id := range instanceIDs {
		instances[i] = childParent{id: id, arn: qappsInstanceARN(partition, region, accountID, id)}
	}
	return instances
}

// isQAppsInstanceGone reports whether err says the instance no longer exists,
// i.e. its application was deleted after ListApplications listed it.
func isQAppsInstanceGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanQAppsCategories stores each instance's categories (the admin-defined
// labels library items are filed under), with scanQAppsInstances' error
// handling.
//
// AppCount, the number of published apps filed under a category, is kept:
// it changes only when the library does.
func scanQAppsCategories(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent) (int, int, error) {
	return scanQAppsInstances(ctx, acct, region, st, qappsListCategoriesOp, instances,
		func(ctx context.Context, inst childParent) (int, int, error) {
			return storeQAppsCategories(ctx, client, acct, region, st, scanID, inst)
		})
}

// storeQAppsCategories stores one instance's categories; ListCategories is
// not paginated.
func storeQAppsCategories(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, inst childParent) (int, int, error) {
	out, err := client.ListCategories(ctx, &qapps.ListCategoriesInput{InstanceId: &inst.id})
	if err != nil {
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, c := range out.Categories {
		id := sv(c.Id)
		if id == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeQAppsCategory, NativeID: qappsCategoryNativeID(inst.arn, id),
			Name: nonEmptyPtr(sv(c.Title)), Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, qappsListCategoriesOp)
}

// scanQAppsLibraryItems stores each instance's library items (the Q Apps
// published to its library), a page at a time, with scanQAppsInstances'
// error handling. RatingCount and UserCount are kept: they change on real
// activity.
func scanQAppsLibraryItems(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent) (int, int, error) {
	return scanQAppsInstances(ctx, acct, region, st, qappsListLibraryItemsOp, instances,
		func(ctx context.Context, inst childParent) (int, int, error) {
			return storeQAppsLibraryItems(ctx, client, acct, region, st, scanID, inst)
		})
}

// storeQAppsLibraryItems stores one instance's library items a page at a time.
func storeQAppsLibraryItems(ctx context.Context, client qappsAPI, acct *account, region string, st *store.Store, scanID string, inst childParent) (total, inserted int, err error) {
	pager := qapps.NewListLibraryItemsPaginator(client,
		&qapps.ListLibraryItemsInput{InstanceId: &inst.id},
		func(o *qapps.ListLibraryItemsPaginatorOptions) {
			o.Limit = qappsPageLimit
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var batch []*store.Resource
		for _, li := range out.LibraryItems {
			id := sv(li.LibraryItemId)
			if id == "" {
				continue
			}
			// IsRatedByUser is caller-relative, not resource state, so it is
			// cleared: kept, a scan by another principal would version-split
			// an unchanged item.
			li.IsRatedByUser = nil
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQAppsLibraryItem, NativeID: qappsLibraryItemNativeID(inst.arn, id),
				Name: &id, Region: &region, Status: nonEmptyPtr(sv(li.Status)), CreatedAt: tp(li.CreatedAt),
				AttributesJSON: mustJSON(li), DiscoveredBy: scanID,
			})
		}
		t, i, err := upsertBatch(st, batch, qappsListLibraryItemsOp)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
