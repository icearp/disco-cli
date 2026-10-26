package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// AWS models a directory's ShareNotes and RADIUS shared secret as sensitive.
	registerType(restype.Descriptor{Type: TypeDSMicrosoftAD, Service: "directory-service", Redact: dsDirectoryRedact})
	registerType(restype.Descriptor{Type: TypeDSSimpleAD, Service: "directory-service", Redact: dsDirectoryRedact})
	registerType(restype.Descriptor{Type: TypeDSCertificate, Service: "directory-service"})
	registerType(restype.Descriptor{Type: TypeDSEventTopic, Service: "directory-service"})
	registerType(restype.Descriptor{Type: TypeDSIPRoute, Service: "directory-service"})
	registerType(restype.Descriptor{Type: TypeDSLogSubscription, Service: "directory-service"})
	registerType(restype.Descriptor{Type: TypeDSRegion, Service: "directory-service"})
	// AWS models ShareNotes, the free-text invitation the owner sends the
	// consumer account, as sensitive.
	registerType(restype.Descriptor{
		Type: TypeDSSharedDirectory, Service: "directory-service",
		Redact: []redact.Rule{{Path: "ShareNotes", Mode: redact.RedactScalar}},
	})
	registerType(restype.Descriptor{Type: TypeDSSnapshot, Service: "directory-service"})
	registerType(restype.Descriptor{Type: TypeDSTrust, Service: "directory-service"})
	registerService(serviceEntry{
		name: "aws:directory-service",
		fn:   scanDirectoryService,
	})
}

var dsDirectoryRedact = []redact.Rule{
	{Path: "ShareNotes", Mode: redact.RedactScalar},
	{Path: "RadiusSettings.SharedSecret", Mode: redact.RedactScalar},
}

type directoryServiceAPI interface {
	DescribeDirectories(context.Context, *directoryservice.DescribeDirectoriesInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeDirectoriesOutput, error)
	ListCertificates(context.Context, *directoryservice.ListCertificatesInput, ...func(*directoryservice.Options)) (*directoryservice.ListCertificatesOutput, error)
	DescribeEventTopics(context.Context, *directoryservice.DescribeEventTopicsInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeEventTopicsOutput, error)
	ListIpRoutes(context.Context, *directoryservice.ListIpRoutesInput, ...func(*directoryservice.Options)) (*directoryservice.ListIpRoutesOutput, error)
	ListLogSubscriptions(context.Context, *directoryservice.ListLogSubscriptionsInput, ...func(*directoryservice.Options)) (*directoryservice.ListLogSubscriptionsOutput, error)
	DescribeRegions(context.Context, *directoryservice.DescribeRegionsInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeRegionsOutput, error)
	DescribeSharedDirectories(context.Context, *directoryservice.DescribeSharedDirectoriesInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeSharedDirectoriesOutput, error)
	DescribeSnapshots(context.Context, *directoryservice.DescribeSnapshotsInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeSnapshotsOutput, error)
	DescribeTrusts(context.Context, *directoryservice.DescribeTrustsInput, ...func(*directoryservice.Options)) (*directoryservice.DescribeTrustsOutput, error)
}

func scanDirectoryService(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := directoryservice.NewFromConfig(acct.cfg, func(o *directoryservice.Options) { o.Region = region })
	return scanDirectoryServiceIn(ctx, client, acct, region, st, scanID)
}

// scanDirectoryServiceIn runs every phase even when one fails, so one phase's
// error costs no other phase its rows. The child phases run after the
// directory phase, over the directories it stored (none when it failed).
//
// Every phase calls the same endpoint, so a first phase that cannot reach it
// (isEndpointUnreachable) is returned alone. The later phases' errors are
// joined by phaseErrs, so a lone or all-transient failure still marks the
// scan partial; a later NXDOMAIN is a plain error (lateNXDOMAIN).
func scanDirectoryServiceIn(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		dirs []dsDirectory
		errs phaseErrs
	)
	for n, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"ds:DescribeDirectories", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			dirs, t, i, err = scanDSDirectories(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"ds:ListCertificates", func() (int, int, error) { return scanDSCertificates(ctx, client, acct, region, st, scanID, dirs) }},
		{"ds:DescribeEventTopics", func() (int, int, error) { return scanDSEventTopics(ctx, client, acct, region, st, scanID, dirs) }},
		{"ds:ListIpRoutes", func() (int, int, error) { return scanDSIPRoutes(ctx, client, acct, region, st, scanID, dirs) }},
		{"ds:ListLogSubscriptions", func() (int, int, error) {
			return scanDSLogSubscriptions(ctx, client, acct, region, st, scanID, dirs)
		}},
		{"ds:DescribeRegions", func() (int, int, error) { return scanDSRegions(ctx, client, acct, region, st, scanID, dirs) }},
		{"ds:DescribeSharedDirectories", func() (int, int, error) {
			return scanDSSharedDirectories(ctx, client, acct, region, st, scanID, dirs)
		}},
		{"ds:DescribeSnapshots", func() (int, int, error) { return scanDSSnapshots(ctx, client, acct, region, st, scanID, dirs) }},
		{"ds:DescribeTrusts", func() (int, int, error) { return scanDSTrusts(ctx, client, acct, region, st, scanID, dirs) }},
	} {
		t, i, perr := phase.run()
		if n == 0 && isEndpointUnreachable(perr) {
			return t, i, perr
		}
		total += t
		inserted += i
		errs.add(phase.name, lateNXDOMAIN(phase.name, perr))
	}
	return total, inserted, errs.join(st, acct, region)
}

// dsDirectory is a directory stored this scan; arn is its row's NativeID.
type dsDirectory struct {
	arn  string
	desc types.DirectoryDescription
}

// scanDSDirectories stores the Microsoft AD and Simple AD directories and
// returns them as child-phase parents. DescribeDirectories returns all types;
// AD Connector is skipped (not a CFN-creatable directory), and so are its
// children.
func scanDSDirectories(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string) ([]dsDirectory, int, int, error) {
	var (
		batch []*store.Resource
		dirs  []dsDirectory
	)
	pager := directoryservice.NewDescribeDirectoriesPaginator(client, &directoryservice.DescribeDirectoriesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "ds:DescribeDirectories", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("ds:DescribeDirectories: %w", err)
		}
		for _, d := range out.DirectoryDescriptions {
			id := sv(d.DirectoryId)
			if id == "" {
				continue
			}
			var discoType string
			switch d.Type {
			case types.DirectoryTypeMicrosoftAd, types.DirectoryTypeSharedMicrosoftAd:
				discoType = TypeDSMicrosoftAD
			case types.DirectoryTypeSimpleAd:
				discoType = TypeDSSimpleAD
			default:
				continue
			}
			arn := dsDirectoryARN(acct.partition(), region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: discoType, NativeID: arn,
				Name: d.Name, Region: &region, Status: nonEmptyPtr(string(d.Stage)),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
			dirs = append(dirs, dsDirectory{arn: arn, desc: d})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "directory-service directories")
	if err != nil {
		return nil, 0, 0, err
	}
	return dirs, total, inserted, nil
}

// dsDirectoryARN is a directory's NativeID, shared with the Directory Service
// Data scanner, whose users and groups are keyed under it.
func dsDirectoryARN(partition, region, accountID, id string) string {
	return arnFor(partition, "ds", region, accountID, "directory/"+id)
}

// dsLive reports whether a directory's documented Stage still admits child
// calls; a directory being torn down has none worth listing.
func dsLive(d types.DirectoryDescription) bool {
	switch d.Stage {
	case types.DirectoryStageDeleting, types.DirectoryStageDeleted, types.DirectoryStageFailed:
		return false
	}
	return true
}

// dsManagedAD reports whether a directory is an owner-side Managed Microsoft
// AD (Standard or Enterprise), the kind the certificate, IP-route and
// directory-sharing features are documented for. Simple AD and the consumer
// side of a shared directory (SharedMicrosoftAD) are not queried, nor is the
// Hybrid edition: its support for these APIs is unverified.
func dsManagedAD(d types.DirectoryDescription) bool {
	return d.Type == types.DirectoryTypeMicrosoftAd &&
		(d.Edition == types.DirectoryEditionStandard || d.Edition == types.DirectoryEditionEnterprise)
}

// dsFanOutParents returns the live directories keep accepts, keyed by the
// directory id the per-directory list ops take.
func dsFanOutParents(dirs []dsDirectory, keep func(types.DirectoryDescription) bool) []childParent {
	var parents []childParent
	for _, d := range dirs {
		if dsLive(d.desc) && keep(d.desc) {
			parents = append(parents, childParent{id: sv(d.desc.DirectoryId), arn: d.arn})
		}
	}
	return parents
}

// dsParentARNs maps each live directory's id to its NativeID, for the ops
// that list the whole region's children at once and name each child's
// directory.
func dsParentARNs(dirs []dsDirectory) map[string]string {
	arns := make(map[string]string, len(dirs))
	for _, d := range dirs {
		if dsLive(d.desc) {
			arns[sv(d.desc.DirectoryId)] = d.arn
		}
	}
	return arns
}

// dsChild is one child row under the directory with NativeID dirARN.
func dsChild(acct *account, dirARN string, r *store.Resource) containedChild {
	return containedChild{row: r, parentID: store.ResourceID("aws", acct.ID, dirARN)}
}

func scanDSCertificates(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ds:ListCertificates", dsFanOutParents(dirs, dsManagedAD),
		func(err error) bool { return isAPIErrorCode(err, "DirectoryDoesNotExistException") },
		func(ctx context.Context, dir childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := directoryservice.NewListCertificatesPaginator(client, &directoryservice.ListCertificatesInput{DirectoryId: &dir.id},
				func(o *directoryservice.ListCertificatesPaginatorOptions) { o.Limit = 50 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.CertificatesInfo {
					id := sv(c.CertificateId)
					if id == "" || c.State == types.CertificateStateDeregistered {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDSCertificate, NativeID: dir.arn + "/certificate/" + id,
						Name: c.CommonName, Region: &region, Status: nonEmptyPtr(string(c.State)),
						AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanDSEventTopics lists every topic registration in the region in one
// unpaginated call (no DirectoryId) and keeps those of stored directories.
func scanDSEventTopics(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	parents := dsParentARNs(dirs)
	if len(parents) == 0 {
		return 0, 0, nil
	}
	out, err := client.DescribeEventTopics(ctx, &directoryservice.DescribeEventTopicsInput{})
	if err != nil {
		return upsertRegionChildren(st, acct, region, "ds:DescribeEventTopics", nil, err)
	}
	var children []containedChild
	for _, e := range out.EventTopics {
		dirARN, name := parents[sv(e.DirectoryId)], sv(e.TopicName)
		if dirARN == "" || name == "" || e.Status == types.TopicStatusDeleted {
			continue
		}
		children = append(children, dsChild(acct, dirARN, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDSEventTopic, NativeID: dirARN + "/event-topic/" + name,
			Name: &name, Region: &region, Status: nonEmptyPtr(string(e.Status)),
			CreatedAt: tp(e.CreatedDateTime), AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
		}))
	}
	return upsertContained(st, "ds:DescribeEventTopics", children)
}

// dsIPRouteKey is an IP route's identity: its CIDR blocks. RemoveIpRoutes
// addresses a route by CIDR alone, and an entry carries an IPv4 block, an
// IPv6 block or (if added as one IpRoute) both, so the key always joins every
// block present.
func dsIPRouteKey(r types.IpRouteInfo) string {
	var cidrs []string
	for _, c := range []string{sv(r.CidrIp), sv(r.CidrIpv6)} {
		if c != "" {
			cidrs = append(cidrs, c)
		}
	}
	return strings.Join(cidrs, ",")
}

func scanDSIPRoutes(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ds:ListIpRoutes", dsFanOutParents(dirs, dsManagedAD),
		func(err error) bool { return isAPIErrorCode(err, "EntityDoesNotExistException") },
		func(ctx context.Context, dir childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := directoryservice.NewListIpRoutesPaginator(client, &directoryservice.ListIpRoutesInput{DirectoryId: &dir.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, r := range out.IpRoutesInfo {
					key := dsIPRouteKey(r)
					if key == "" || r.IpRouteStatusMsg == types.IpRouteStatusMsgRemoved {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDSIPRoute, NativeID: dir.arn + "/ip-route/" + key,
						Name: &key, Region: &region, Status: nonEmptyPtr(string(r.IpRouteStatusMsg)),
						CreatedAt: tp(r.AddedDateTime), AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanDSLogSubscriptions lists every log subscription in the region (no
// DirectoryId) and keeps those of stored directories.
func scanDSLogSubscriptions(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	parents := dsParentARNs(dirs)
	if len(parents) == 0 {
		return 0, 0, nil
	}
	var (
		children []containedChild
		err      error
	)
	pager := directoryservice.NewListLogSubscriptionsPaginator(client, &directoryservice.ListLogSubscriptionsInput{})
	for pager.HasMorePages() {
		var out *directoryservice.ListLogSubscriptionsOutput
		if out, err = pager.NextPage(ctx); err != nil {
			break
		}
		for _, s := range out.LogSubscriptions {
			dirARN, group := parents[sv(s.DirectoryId)], sv(s.LogGroupName)
			if dirARN == "" || group == "" {
				continue
			}
			children = append(children, dsChild(acct, dirARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSLogSubscription, NativeID: dirARN + "/log-subscription/" + group,
				Name: &group, Region: &region,
				CreatedAt: tp(s.SubscriptionCreatedDateTime), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}))
		}
	}
	return upsertRegionChildren(st, acct, region, "ds:ListLogSubscriptions", children, err)
}

// dsReplicatedFrom reports whether a directory is an Enterprise Managed
// Microsoft AD (the only edition with multi-Region replication) replicated
// to another region and whose primary region is this one. Asking only the
// primary region stores each replica once, not again from every replica
// region.
func dsReplicatedFrom(region string) func(types.DirectoryDescription) bool {
	return func(d types.DirectoryDescription) bool {
		return d.Type == types.DirectoryTypeMicrosoftAd && d.Edition == types.DirectoryEditionEnterprise && d.RegionsInfo != nil &&
			sv(d.RegionsInfo.PrimaryRegion) == region && len(d.RegionsInfo.AdditionalRegions) > 0
	}
}

// scanDSRegions stores each additional region a directory is replicated to.
// The primary region's entry describes the directory itself, already stored.
func scanDSRegions(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ds:DescribeRegions", dsFanOutParents(dirs, dsReplicatedFrom(region)),
		func(err error) bool { return isAPIErrorCode(err, "DirectoryDoesNotExistException") },
		func(ctx context.Context, dir childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := directoryservice.NewDescribeRegionsPaginator(client, &directoryservice.DescribeRegionsInput{DirectoryId: &dir.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, r := range out.RegionsDescription {
					name := sv(r.RegionName)
					if name == "" || r.RegionType != types.RegionTypeAdditional || r.Status == types.DirectoryStageDeleted {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDSRegion, NativeID: dir.arn + "/region/" + name,
						Name: &name, Region: &region, Status: nonEmptyPtr(string(r.Status)),
						CreatedAt: tp(r.LaunchTime), AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanDSSharedDirectories stores the owner side of each share. The consumer
// side is a SharedMicrosoftAD directory in the consumer account, stored there
// when that account is scanned.
func scanDSSharedDirectories(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "ds:DescribeSharedDirectories", dsFanOutParents(dirs, dsManagedAD),
		func(err error) bool { return isAPIErrorCode(err, "EntityDoesNotExistException") },
		func(ctx context.Context, dir childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := directoryservice.NewDescribeSharedDirectoriesPaginator(client, &directoryservice.DescribeSharedDirectoriesInput{OwnerDirectoryId: &dir.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, s := range out.SharedDirectories {
					id := sv(s.SharedDirectoryId)
					if id == "" || s.ShareStatus == types.ShareStatusDeleted {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDSSharedDirectory, NativeID: dir.arn + "/shared-directory/" + id,
						Name: s.SharedAccountId, Region: &region, Status: nonEmptyPtr(string(s.ShareStatus)),
						CreatedAt: tp(s.CreatedDateTime), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanDSSnapshots lists every snapshot in the region (no DirectoryId) and
// keeps those of stored directories. Automatic snapshots are kept with the
// manual ones: both are restorable, and Type tells them apart.
func scanDSSnapshots(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	parents := dsParentARNs(dirs)
	if len(parents) == 0 {
		return 0, 0, nil
	}
	var (
		children []containedChild
		err      error
	)
	pager := directoryservice.NewDescribeSnapshotsPaginator(client, &directoryservice.DescribeSnapshotsInput{})
	for pager.HasMorePages() {
		var out *directoryservice.DescribeSnapshotsOutput
		if out, err = pager.NextPage(ctx); err != nil {
			break
		}
		for _, s := range out.Snapshots {
			dirARN, id := parents[sv(s.DirectoryId)], sv(s.SnapshotId)
			if dirARN == "" || id == "" {
				continue
			}
			children = append(children, dsChild(acct, dirARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSSnapshot, NativeID: dirARN + "/snapshot/" + id,
				Name: nonEmptyPtr(sv(s.Name)), Region: &region, Status: nonEmptyPtr(string(s.Status)),
				CreatedAt: tp(s.StartTime), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}))
		}
	}
	return upsertRegionChildren(st, acct, region, "ds:DescribeSnapshots", children, err)
}

// scanDSTrusts lists every trust in the region (no DirectoryId) and keeps
// those of stored directories. The trust password is write-only: no
// response carries it.
func scanDSTrusts(ctx context.Context, client directoryServiceAPI, acct *account, region string, st *store.Store, scanID string, dirs []dsDirectory) (int, int, error) {
	parents := dsParentARNs(dirs)
	if len(parents) == 0 {
		return 0, 0, nil
	}
	var (
		children []containedChild
		err      error
	)
	pager := directoryservice.NewDescribeTrustsPaginator(client, &directoryservice.DescribeTrustsInput{})
	for pager.HasMorePages() {
		var out *directoryservice.DescribeTrustsOutput
		if out, err = pager.NextPage(ctx); err != nil {
			break
		}
		for _, tr := range out.Trusts {
			dirARN, id := parents[sv(tr.DirectoryId)], sv(tr.TrustId)
			if dirARN == "" || id == "" || tr.TrustState == types.TrustStateDeleted {
				continue
			}
			children = append(children, dsChild(acct, dirARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSTrust, NativeID: dirARN + "/trust/" + id,
				Name: tr.RemoteDomainName, Region: &region, Status: nonEmptyPtr(string(tr.TrustState)),
				CreatedAt: tp(tr.CreatedDateTime), AttributesJSON: mustJSON(tr), DiscoveredBy: scanID,
			}))
		}
	}
	return upsertRegionChildren(st, acct, region, "ds:DescribeTrusts", children, err)
}
