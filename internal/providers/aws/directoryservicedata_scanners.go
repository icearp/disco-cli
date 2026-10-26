package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	dsdatatypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// No Redact rule: the ListUsers and ListGroups summaries stored here carry
	// no password or other credential. Their fields AWS models as sensitive,
	// GivenName and Surname, are personal data, not secrets.
	registerType(restype.Descriptor{Type: TypeDSDataUser, Service: "directory-service-data"})
	registerType(restype.Descriptor{Type: TypeDSDataGroup, Service: "directory-service-data"})
	registerService(serviceEntry{
		name: "aws:directory-service-data",
		fn:   scanDirectoryServiceData,
	})
}

type dsDataDirectoriesAPI interface {
	DescribeDirectories(context.Context, *directoryservice.DescribeDirectoriesInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeDirectoriesOutput, error)
}

type directoryServiceDataAPI interface {
	ListUsers(context.Context, *directoryservicedata.ListUsersInput, ...func(*directoryservicedata.Options)) (*directoryservicedata.ListUsersOutput, error)
	ListGroups(context.Context, *directoryservicedata.ListGroupsInput, ...func(*directoryservicedata.Options)) (*directoryservicedata.ListGroupsOutput, error)
}

func scanDirectoryServiceData(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	dirs := directoryservice.NewFromConfig(acct.cfg, func(o *directoryservice.Options) { o.Region = region })
	data := directoryservicedata.NewFromConfig(acct.cfg, func(o *directoryservicedata.Options) { o.Region = region })
	return scanDirectoryServiceDataIn(ctx, dirs, data, acct, region, st, scanID)
}

// scanDirectoryServiceDataIn stores the users and groups of every directory
// Directory Service Data can read. It is a service of its own, with its own
// time budget, so a directory with many users cannot exhaust
// aws:directory-service's. It lists the directories itself and stores none of
// them: aws:directory-service stores them, possibly after this scan reads
// them, so resolveDSDataHierarchy links the children after the scan.
//
// Groups run before users: a directory usually holds far fewer groups, so a
// deadline reached while listing users still leaves the groups stored.
//
// A Directory Service Data host that does not resolve is returned while
// nothing is stored, so the dispatcher reports the region unavailable without
// a warning: the service may be offered in fewer regions than Directory
// Service, and its SDK ships no endpoint table to skip them up front. After
// rows are stored, that unresolvable host is a plain error (lateNXDOMAIN):
// returned as NXDOMAIN it would make the dispatcher report the region
// unavailable and discard the stored rows' counts. The phases' errors are
// joined by phaseErrs, so a lone or all-transient failure marks the scan
// partial.
func scanDirectoryServiceDataIn(ctx context.Context, dirs dsDataDirectoriesAPI, data directoryServiceDataAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	parents, err := dsDataParents(ctx, dirs, acct, region, st)
	if err != nil || len(parents) == 0 {
		return 0, 0, err
	}
	var errs phaseErrs
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{"ds-data:ListGroups", func() (int, int, error) { return scanDSDataGroups(ctx, data, acct, region, st, scanID, parents) }},
		{"ds-data:ListUsers", func() (int, int, error) { return scanDSDataUsers(ctx, data, acct, region, st, scanID, parents) }},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		if isDNSNotFound(perr) && total == 0 {
			return 0, 0, perr
		}
		errs.add(phase.op, lateNXDOMAIN(phase.op, perr))
	}
	return total, inserted, errs.join(st, acct, region)
}

// dsDataParents lists the region's directories and returns those Directory
// Service Data can read, keyed by the NativeID aws:directory-service gives
// them.
func dsDataParents(ctx context.Context, client dsDataDirectoriesAPI, acct *account, region string, st *store.Store) ([]childParent, error) {
	var parents []childParent
	pager := directoryservice.NewDescribeDirectoriesPaginator(client, &directoryservice.DescribeDirectoriesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied(st, "ds:DescribeDirectories", acct.ID, region, err)
			}
			return nil, fmt.Errorf("ds:DescribeDirectories: %w", err)
		}
		for _, d := range out.DirectoryDescriptions {
			if id := sv(d.DirectoryId); id != "" && dsDataReadable(d, region) {
				parents = append(parents, childParent{id: id, arn: dsDirectoryARN(acct.partition(), region, acct.ID, id)})
			}
		}
	}
	return parents, nil
}

// dsDataReadable reports whether d is a live owner-side Standard/Enterprise
// Managed Microsoft AD whose primary region is this one: the model says
// Directory Service Data supports only Managed Microsoft AD and is available
// only in a directory's primary region. A directory without RegionsInfo or a
// primary region is taken as single-region, so this region is its primary.
func dsDataReadable(d types.DirectoryDescription, region string) bool {
	if !dsLive(d) || !dsManagedAD(d) {
		return false
	}
	if d.RegionsInfo == nil {
		return true
	}
	primary := sv(d.RegionsInfo.PrimaryRegion)
	return primary == "" || primary == region
}

// dsDataPageSize is the documented MaxResults cap of ListUsers and ListGroups.
const dsDataPageSize = 250

// isDSDataDisabled reports whether a directory refused the call because its
// owner has not enabled Directory Service Data on it
// (EnableDirectoryDataAccess). That is the directory's chosen state, which no
// IAM change fixes, so the directory is skipped silently. It shares
// AccessDeniedException with real denials and is told apart by the modeled
// Reason, so it must match before isAccessDenied (fanOutErrs.keyErr checks
// isSkip first).
func isDSDataDisabled(err error) bool {
	var denied *dsdatatypes.AccessDeniedException
	return errors.As(err, &denied) && denied.Reason == dsdatatypes.AccessDeniedReasonDataDisabled
}

// dsDataUnavailable is skipped per directory with one warning per op: the
// model raises it for a directory whose state or configuration cannot serve
// the call (not active, timed out, out of resources or disk, a failed trust),
// a fact about that directory that need not cost its siblings their rows.
var dsDataUnavailable = []skipCode{{
	code: "DirectoryUnavailableException",
	why:  "a directory may be unable to serve the call in its current state",
}}

// dsDataFanOut runs storeDir for every parent, skipping the directories
// isDSDataDisabled or dsDataUnavailable name. An unresolvable host is kept
// out of the fan-out, whose transient skip would warn it away, and returned
// after it as the DNS error it is, so the dispatcher sees it.
func dsDataFanOut(ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	storeDir func(context.Context, childParent) (int, int, error),
) (int, int, error) {
	var (
		mu       sync.Mutex
		hostGone error
	)
	total, inserted, err := childFanOutStoredSkipCodes(ctx, st, acct, region, op, parents, isDSDataDisabled,
		byParentID, "directory(ies)", dsDataUnavailable,
		func(ctx context.Context, dir childParent) (int, int, error) {
			t, i, err := storeDir(ctx, dir)
			if !isDNSNotFound(err) {
				return t, i, err
			}
			mu.Lock()
			defer mu.Unlock()
			if hostGone == nil {
				hostGone = err
			}
			return t, i, nil
		})
	if hostGone != nil {
		return total, inserted, errors.Join(err, hostGone)
	}
	return total, inserted, err
}

// dsDataWellKnown reports whether a SID's relative identifier, its last
// sub-authority, is below 1000. Active Directory reserves those RIDs for the
// accounts and groups it creates with the domain (Administrator 500, krbtgt
// 502, Domain Admins 512, the Builtin S-1-5-32-5xx groups), which cannot be
// deleted, and gives every later object a RID of 1000 or more. Known gap:
// objects AWS or AD create later get RIDs of 1000 or more and stay unmanaged
// (the "AWS Delegated …" groups, the Admin user, DnsAdmins, DnsUpdateProxy),
// and the rule itself is unverified on AWS Managed Microsoft AD.
func dsDataWellKnown(sid string) bool {
	i := strings.LastIndexByte(sid, '-')
	if i < 0 {
		return false
	}
	rid, err := strconv.ParseUint(sid[i+1:], 10, 32)
	return err == nil && rid < 1000
}

// dsDataNativeID keys a user or group (kind) by its SID under its directory:
// the SID is the model's required unique identifier, which, unlike
// SAMAccountName, a rename does not change.
func dsDataNativeID(dirARN, kind, sid string) string {
	return dirARN + "/" + kind + "/" + sid
}

// dsDataStorePages stores one directory's listing a page at a time, so a
// directory with tens of thousands of users is never held in memory. The
// pages stored before an error stay and are counted.
func dsDataStorePages(st *store.Store, op string, hasMore func() bool, next func() ([]*store.Resource, error)) (int, int, error) {
	var total, inserted int
	for hasMore() {
		rows, err := next()
		if err != nil {
			return total, inserted, err
		}
		t, n, err := upsertBatch(st, rows, op)
		total += t
		inserted += n
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// dsDataRow is the row of one user or group summary, or nil for a summary
// without a SID.
func dsDataRow(acct *account, region, scanID string, dir childParent, typ, kind string, sid, name *string, summary any) *store.Resource {
	id := sv(sid)
	if id == "" {
		return nil
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: dsDataNativeID(dir.arn, kind, id),
		Name: name, Region: &region, ManagedByProvider: dsDataWellKnown(id),
		AttributesJSON: mustJSON(summary), DiscoveredBy: scanID,
	}
}

// scanDSDataUsers stores each directory's users. Without a Realm ListUsers
// returns only the directory's own domain; a trusted realm's users belong to
// that realm, not to this directory.
func scanDSDataUsers(ctx context.Context, client directoryServiceDataAPI, acct *account, region string, st *store.Store, scanID string, parents []childParent) (int, int, error) {
	const op = "ds-data:ListUsers"
	return dsDataFanOut(ctx, st, acct, region, op, parents, func(ctx context.Context, dir childParent) (int, int, error) {
		pager := directoryservicedata.NewListUsersPaginator(client, &directoryservicedata.ListUsersInput{DirectoryId: &dir.id},
			func(o *directoryservicedata.ListUsersPaginatorOptions) { o.Limit = dsDataPageSize })
		return dsDataStorePages(st, op, pager.HasMorePages, func() ([]*store.Resource, error) {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, u := range out.Users {
				if r := dsDataRow(acct, region, scanID, dir, TypeDSDataUser, "user", u.SID, u.SAMAccountName, u); r != nil {
					rows = append(rows, r)
				}
			}
			return rows, nil
		})
	})
}

// scanDSDataGroups stores each directory's groups, of its own domain only as
// scanDSDataUsers describes.
func scanDSDataGroups(ctx context.Context, client directoryServiceDataAPI, acct *account, region string, st *store.Store, scanID string, parents []childParent) (int, int, error) {
	const op = "ds-data:ListGroups"
	return dsDataFanOut(ctx, st, acct, region, op, parents, func(ctx context.Context, dir childParent) (int, int, error) {
		pager := directoryservicedata.NewListGroupsPaginator(client, &directoryservicedata.ListGroupsInput{DirectoryId: &dir.id},
			func(o *directoryservicedata.ListGroupsPaginatorOptions) { o.Limit = dsDataPageSize })
		return dsDataStorePages(st, op, pager.HasMorePages, func() ([]*store.Resource, error) {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, g := range out.Groups {
				if r := dsDataRow(acct, region, scanID, dir, TypeDSDataGroup, "group", g.SID, g.SAMAccountName, g); r != nil {
					rows = append(rows, r)
				}
			}
			return rows, nil
		})
	})
}
