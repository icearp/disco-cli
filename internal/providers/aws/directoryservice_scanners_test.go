package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	dsDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: ds:Describe"}
	dsClientErr = &smithy.GenericAPIError{Code: "ClientException", Message: "bad request"}
	dsThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	dsCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func dsClient(handlers map[string]func(in any) (any, error)) *directoryservice.Client {
	return directoryservice.NewFromConfig(opStubConfig(handlers, dsDenied))
}

func dsDirARN(id string) string {
	return "arn:aws:ds:" + testRegion + ":" + testAccountID + ":directory/" + id
}

func dsDir(id string, typ dstypes.DirectoryType, edition dstypes.DirectoryEdition, stage dstypes.DirectoryStage) dstypes.DirectoryDescription {
	return dstypes.DirectoryDescription{DirectoryId: ptrStr(id), Name: ptrStr(id + ".example.com"), Type: typ, Edition: edition, Stage: stage}
}

// dsTestDirectories is the DescribeDirectories fixture over two pages:
// d-ms (Enterprise, replicated from testRegion), d-std (Standard) and d-simple
// (Simple AD); then d-shared (consumer side of a share), d-conn (AD Connector,
// not stored), d-del (Deleting), d-hyb (Hybrid edition) and an id-less entry.
func dsTestDirectories() (page1, page2 []dstypes.DirectoryDescription) {
	ms := dsDir("d-ms", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionEnterprise, dstypes.DirectoryStageActive)
	ms.RegionsInfo = &dstypes.RegionsInfo{PrimaryRegion: ptrStr(testRegion), AdditionalRegions: []string{"us-west-2", "eu-west-1"}}
	page1 = []dstypes.DirectoryDescription{
		ms,
		dsDir("d-std", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionStandard, dstypes.DirectoryStageActive),
		dsDir("d-simple", dstypes.DirectoryTypeSimpleAd, "", dstypes.DirectoryStageActive),
	}
	page2 = []dstypes.DirectoryDescription{
		dsDir("d-shared", dstypes.DirectoryTypeSharedMicrosoftAd, dstypes.DirectoryEditionStandard, dstypes.DirectoryStageActive),
		dsDir("d-conn", dstypes.DirectoryTypeAdConnector, "", dstypes.DirectoryStageActive),
		dsDir("d-del", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionStandard, dstypes.DirectoryStageDeleting),
		dsDir("d-hyb", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionHybrid, dstypes.DirectoryStageActive),
		{Type: dstypes.DirectoryTypeMicrosoftAd, Edition: dstypes.DirectoryEditionStandard},
	}
	return page1, page2
}

// dsHandlers answers every op the scan makes. Each per-directory op returns
// two pages for every directory: the first holds the child <dir>-1 plus an
// id-less and a gone-state element, the second <dir>-2. Each region-wide op
// returns children of d-ms, d-simple, d-conn and d-del (plus an id-less and a
// gone-state element) on one page and of d-std on a second. calls[op] records
// the directory ids a per-directory op was asked for. Ops in override answer
// instead.
func dsHandlers(calls map[string]*parentCalls, override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	for _, op := range []string{"ListCertificates", "ListIpRoutes", "DescribeRegions", "DescribeSharedDirectories"} {
		if calls[op] == nil {
			calls[op] = &parentCalls{}
		}
	}
	page1, page2 := dsTestDirectories()
	handlers := map[string]func(any) (any, error){
		"DescribeDirectories": func(in any) (any, error) {
			if in.(*directoryservice.DescribeDirectoriesInput).NextToken == nil {
				return &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: page1, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: page2}, nil
		},
		"ListCertificates": func(in any) (any, error) {
			req := in.(*directoryservice.ListCertificatesInput)
			dir := *req.DirectoryId
			calls["ListCertificates"].add(dir)
			if req.Limit == nil || *req.Limit != 50 {
				return nil, &smithy.GenericAPIError{Code: "InvalidParameterException", Message: "want Limit 50, the documented page cap"}
			}
			if req.NextToken == nil {
				return &directoryservice.ListCertificatesOutput{CertificatesInfo: []dstypes.CertificateInfo{
					{CertificateId: ptrStr(dir + "-1"), CommonName: ptrStr("ldaps." + dir), State: dstypes.CertificateStateRegistered},
					{CommonName: ptrStr("no-id")},
					{CertificateId: ptrStr(dir + "-gone"), State: dstypes.CertificateStateDeregistered},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.ListCertificatesOutput{CertificatesInfo: []dstypes.CertificateInfo{{CertificateId: ptrStr(dir + "-2")}}}, nil
		},
		"ListIpRoutes": func(in any) (any, error) {
			req := in.(*directoryservice.ListIpRoutesInput)
			calls["ListIpRoutes"].add(*req.DirectoryId)
			if req.NextToken == nil {
				return &directoryservice.ListIpRoutesOutput{IpRoutesInfo: []dstypes.IpRouteInfo{
					{CidrIp: ptrStr("10.1.0.0/16"), IpRouteStatusMsg: dstypes.IpRouteStatusMsgAdded, AddedDateTime: &dsCreated},
					{Description: ptrStr("no cidr")},
					{CidrIp: ptrStr("10.9.0.0/16"), IpRouteStatusMsg: dstypes.IpRouteStatusMsgRemoved},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.ListIpRoutesOutput{IpRoutesInfo: []dstypes.IpRouteInfo{{CidrIpv6: ptrStr("2001:db8::/32")}}}, nil
		},
		"DescribeRegions": func(in any) (any, error) {
			req := in.(*directoryservice.DescribeRegionsInput)
			calls["DescribeRegions"].add(*req.DirectoryId)
			if req.NextToken == nil {
				return &directoryservice.DescribeRegionsOutput{RegionsDescription: []dstypes.RegionDescription{
					{RegionName: ptrStr(testRegion), RegionType: dstypes.RegionTypePrimary, Status: dstypes.DirectoryStageActive},
					{RegionName: ptrStr("us-west-2"), RegionType: dstypes.RegionTypeAdditional, Status: dstypes.DirectoryStageActive, LaunchTime: &dsCreated},
					{RegionType: dstypes.RegionTypeAdditional},
					{RegionName: ptrStr("eu-central-1"), RegionType: dstypes.RegionTypeAdditional, Status: dstypes.DirectoryStageDeleted},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeRegionsOutput{RegionsDescription: []dstypes.RegionDescription{
				{RegionName: ptrStr("eu-west-1"), RegionType: dstypes.RegionTypeAdditional, Status: dstypes.DirectoryStageCreating},
			}}, nil
		},
		"DescribeSharedDirectories": func(in any) (any, error) {
			req := in.(*directoryservice.DescribeSharedDirectoriesInput)
			dir := *req.OwnerDirectoryId
			calls["DescribeSharedDirectories"].add(dir)
			if req.NextToken == nil {
				return &directoryservice.DescribeSharedDirectoriesOutput{SharedDirectories: []dstypes.SharedDirectory{
					{SharedDirectoryId: ptrStr(dir + "-1"), SharedAccountId: ptrStr("210987654321"), ShareStatus: dstypes.ShareStatusShared, CreatedDateTime: &dsCreated},
					{SharedAccountId: ptrStr("no-id")},
					{SharedDirectoryId: ptrStr(dir + "-gone"), ShareStatus: dstypes.ShareStatusDeleted},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeSharedDirectoriesOutput{SharedDirectories: []dstypes.SharedDirectory{{SharedDirectoryId: ptrStr(dir + "-2")}}}, nil
		},
		"DescribeEventTopics": func(any) (any, error) {
			return &directoryservice.DescribeEventTopicsOutput{EventTopics: []dstypes.EventTopic{
				{DirectoryId: ptrStr("d-ms"), TopicName: ptrStr("ms-topic"), TopicArn: ptrStr("arn:aws:sns:us-east-1:123456789012:ms-topic"), Status: dstypes.TopicStatusRegistered, CreatedDateTime: &dsCreated},
				{DirectoryId: ptrStr("d-simple"), TopicName: ptrStr("simple-topic")},
				{DirectoryId: ptrStr("d-conn"), TopicName: ptrStr("conn-topic")},
				{DirectoryId: ptrStr("d-del"), TopicName: ptrStr("del-topic")},
				{DirectoryId: ptrStr("d-ms")},
				{DirectoryId: ptrStr("d-ms"), TopicName: ptrStr("gone-topic"), Status: dstypes.TopicStatusDeleted},
			}}, nil
		},
		"ListLogSubscriptions": func(in any) (any, error) {
			if in.(*directoryservice.ListLogSubscriptionsInput).NextToken == nil {
				return &directoryservice.ListLogSubscriptionsOutput{LogSubscriptions: []dstypes.LogSubscription{
					{DirectoryId: ptrStr("d-ms"), LogGroupName: ptrStr("/aws/directoryservice/d-ms"), SubscriptionCreatedDateTime: &dsCreated},
					{DirectoryId: ptrStr("d-simple"), LogGroupName: ptrStr("/aws/directoryservice/d-simple")},
					{DirectoryId: ptrStr("d-conn"), LogGroupName: ptrStr("/aws/directoryservice/d-conn")},
					{DirectoryId: ptrStr("d-del"), LogGroupName: ptrStr("/aws/directoryservice/d-del")},
					{DirectoryId: ptrStr("d-hyb")},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.ListLogSubscriptionsOutput{LogSubscriptions: []dstypes.LogSubscription{
				{DirectoryId: ptrStr("d-std"), LogGroupName: ptrStr("/aws/directoryservice/d-std")},
			}}, nil
		},
		"DescribeSnapshots": func(in any) (any, error) {
			if in.(*directoryservice.DescribeSnapshotsInput).NextToken == nil {
				return &directoryservice.DescribeSnapshotsOutput{Snapshots: []dstypes.Snapshot{
					{DirectoryId: ptrStr("d-ms"), SnapshotId: ptrStr("s-ms"), Name: ptrStr("before-upgrade"), Type: dstypes.SnapshotTypeManual, Status: dstypes.SnapshotStatusCompleted, StartTime: &dsCreated},
					{DirectoryId: ptrStr("d-simple"), SnapshotId: ptrStr("s-simple"), Type: dstypes.SnapshotTypeAuto},
					{DirectoryId: ptrStr("d-conn"), SnapshotId: ptrStr("s-conn")},
					{DirectoryId: ptrStr("d-del"), SnapshotId: ptrStr("s-del")},
					{DirectoryId: ptrStr("d-ms")},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeSnapshotsOutput{Snapshots: []dstypes.Snapshot{{DirectoryId: ptrStr("d-std"), SnapshotId: ptrStr("s-std")}}}, nil
		},
		"DescribeTrusts": func(in any) (any, error) {
			if in.(*directoryservice.DescribeTrustsInput).NextToken == nil {
				return &directoryservice.DescribeTrustsOutput{Trusts: []dstypes.Trust{
					{DirectoryId: ptrStr("d-ms"), TrustId: ptrStr("t-ms"), RemoteDomainName: ptrStr("corp.example.org"), TrustState: dstypes.TrustStateVerified, TrustDirection: dstypes.TrustDirectionTwoWay, CreatedDateTime: &dsCreated},
					{DirectoryId: ptrStr("d-simple"), TrustId: ptrStr("t-simple")},
					{DirectoryId: ptrStr("d-conn"), TrustId: ptrStr("t-conn")},
					{DirectoryId: ptrStr("d-del"), TrustId: ptrStr("t-del")},
					{DirectoryId: ptrStr("d-ms")},
					{DirectoryId: ptrStr("d-ms"), TrustId: ptrStr("t-gone"), TrustState: dstypes.TrustStateDeleted},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeTrustsOutput{Trusts: []dstypes.Trust{{DirectoryId: ptrStr("d-std"), TrustId: ptrStr("t-std")}}}, nil
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

// dsWantNativeIDs is every row a full scan of dsHandlers stores, by type.
func dsWantNativeIDs() map[string][]string {
	ms, std, simple := dsDirARN("d-ms"), dsDirARN("d-std"), dsDirARN("d-simple")
	return map[string][]string{
		TypeDSMicrosoftAD:     {ms, dsDirARN("d-del"), dsDirARN("d-hyb"), dsDirARN("d-shared"), std},
		TypeDSSimpleAD:        {simple},
		TypeDSCertificate:     {ms + "/certificate/d-ms-1", ms + "/certificate/d-ms-2", std + "/certificate/d-std-1", std + "/certificate/d-std-2"},
		TypeDSIPRoute:         {ms + "/ip-route/10.1.0.0/16", ms + "/ip-route/2001:db8::/32", std + "/ip-route/10.1.0.0/16", std + "/ip-route/2001:db8::/32"},
		TypeDSRegion:          {ms + "/region/eu-west-1", ms + "/region/us-west-2"},
		TypeDSSharedDirectory: {ms + "/shared-directory/d-ms-1", ms + "/shared-directory/d-ms-2", std + "/shared-directory/d-std-1", std + "/shared-directory/d-std-2"},
		TypeDSEventTopic:      {ms + "/event-topic/ms-topic", simple + "/event-topic/simple-topic"},
		TypeDSLogSubscription: {ms + "/log-subscription//aws/directoryservice/d-ms", simple + "/log-subscription//aws/directoryservice/d-simple", std + "/log-subscription//aws/directoryservice/d-std"},
		TypeDSSnapshot:        {ms + "/snapshot/s-ms", simple + "/snapshot/s-simple", std + "/snapshot/s-std"},
		TypeDSTrust:           {ms + "/trust/t-ms", simple + "/trust/t-simple", std + "/trust/t-std"},
	}
}

func dsStoredNativeIDs(t *testing.T, st *store.Store, typ string) []string {
	t.Helper()
	ids := slices.Collect(maps.Keys(rowsByNativeID(t, st, typ)))
	sort.Strings(ids)
	return ids
}

func dsWantTotal(want map[string][]string, except ...string) int {
	n := 0
	for typ, ids := range want {
		if !slices.Contains(except, typ) {
			n += len(ids)
		}
	}
	return n
}

func runDSScan(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanDirectoryServiceIn(context.Background(), dsClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanDirectoryServiceIn_StoresDirectoriesAndChildren(t *testing.T) {
	st := newTestStore(t)
	want := dsWantNativeIDs()
	total, err := runDSScan(t, st, dsHandlers(map[string]*parentCalls{}, nil))
	if err != nil || total != dsWantTotal(want) {
		t.Fatalf("total, err = %d, %v; want %d, nil", total, err, dsWantTotal(want))
	}
	for typ, ids := range want {
		sort.Strings(ids)
		if got := dsStoredNativeIDs(t, st, typ); !slices.Equal(got, ids) {
			t.Errorf("%s rows = %v, want %v", typ, got, ids)
		}
	}
	ms := dsDirARN("d-ms")
	if d := rowsByNativeID(t, st, TypeDSMicrosoftAD)[ms]; sv(d.Name) != "d-ms.example.com" || sv(d.Status) != "Active" || sv(d.Region) != testRegion {
		t.Errorf("directory row = name %q status %q region %q; want d-ms.example.com Active %s", sv(d.Name), sv(d.Status), sv(d.Region), testRegion)
	}
	for _, c := range []struct {
		typ, nativeID, name, status, created string
	}{
		{TypeDSCertificate, ms + "/certificate/d-ms-1", "ldaps.d-ms", "Registered", ""},
		{TypeDSIPRoute, ms + "/ip-route/10.1.0.0/16", "10.1.0.0/16", "Added", "2026-09-01T08:00:00Z"},
		{TypeDSRegion, ms + "/region/us-west-2", "us-west-2", "Active", "2026-09-01T08:00:00Z"},
		{TypeDSSharedDirectory, ms + "/shared-directory/d-ms-1", "210987654321", "Shared", "2026-09-01T08:00:00Z"},
		{TypeDSEventTopic, ms + "/event-topic/ms-topic", "ms-topic", "Registered", "2026-09-01T08:00:00Z"},
		{TypeDSLogSubscription, ms + "/log-subscription//aws/directoryservice/d-ms", "/aws/directoryservice/d-ms", "", "2026-09-01T08:00:00Z"},
		{TypeDSSnapshot, ms + "/snapshot/s-ms", "before-upgrade", "Completed", "2026-09-01T08:00:00Z"},
		{TypeDSTrust, ms + "/trust/t-ms", "corp.example.org", "Verified", "2026-09-01T08:00:00Z"},
	} {
		r := rowsByNativeID(t, st, c.typ)[c.nativeID]
		if r.Type != c.typ || sv(r.Name) != c.name || sv(r.Status) != c.status || sv(r.CreatedAt) != c.created || sv(r.Region) != testRegion {
			t.Errorf("%s row = type %q name %q status %q created %q region %q; want %q %q %q %q",
				c.nativeID, r.Type, sv(r.Name), sv(r.Status), sv(r.CreatedAt), sv(r.Region), c.typ, c.name, c.status, c.created)
		}
		assertContainsNative(t, st, ms, c.nativeID)
	}
	assertContainsNative(t, st, dsDirARN("d-simple"), dsDirARN("d-simple")+"/snapshot/s-simple")
	assertContainsNative(t, st, dsDirARN("d-std"), dsDirARN("d-std")+"/trust/t-std")
	assertContainsEdgesDeclared(t, st)

	var trust dstypes.Trust
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeDSTrust)[ms+"/trust/t-ms"].AttributesJSON), &trust); err != nil || trust.TrustDirection != dstypes.TrustDirectionTwoWay {
		t.Errorf("trust attrs TrustDirection = %q, %v; want Two-Way", trust.TrustDirection, err)
	}
	if sv(rowsByNativeID(t, st, TypeDSSnapshot)[dsDirARN("d-simple")+"/snapshot/s-simple"].Name) != "" {
		t.Errorf("unnamed snapshot got a name")
	}
}

// Certificates, IP routes and shares are asked only of live owner-side
// Standard/Enterprise Managed Microsoft AD directories; replica regions only
// of the Enterprise one replicated from this region.
func TestScanDirectoryServiceIn_FanOutGatedByDirectoryKind(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	if _, err := runDSScan(t, st, dsHandlers(calls, nil)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, want := range map[string][]string{
		"ListCertificates":          {"d-ms", "d-std"},
		"ListIpRoutes":              {"d-ms", "d-std"},
		"DescribeSharedDirectories": {"d-ms", "d-std"},
		"DescribeRegions":           {"d-ms"},
	} {
		if got := calls[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s asked for %v, want %v", op, got, want)
		}
	}
}

func TestScanDSRegions_OnlyFromPrimaryRegion(t *testing.T) {
	ms := dsDir("d-ms", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionEnterprise, dstypes.DirectoryStageActive)
	unreplicated := dsDir("d-one", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionEnterprise, dstypes.DirectoryStageActive)
	unreplicated.RegionsInfo = &dstypes.RegionsInfo{PrimaryRegion: ptrStr(testRegion)}
	consumer := dsDir("d-consumer", dstypes.DirectoryTypeSharedMicrosoftAd, dstypes.DirectoryEditionEnterprise, dstypes.DirectoryStageActive)
	consumer.RegionsInfo = &dstypes.RegionsInfo{PrimaryRegion: ptrStr(testRegion), AdditionalRegions: []string{"eu-west-1"}}
	for name, regions := range map[string]*dstypes.RegionsInfo{
		"replica region":  {PrimaryRegion: ptrStr("eu-west-1"), AdditionalRegions: []string{testRegion}},
		"no regions info": nil,
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			calls := map[string]*parentCalls{}
			d := ms
			d.RegionsInfo = regions
			dirs := []dsDirectory{{arn: dsDirARN("d-ms"), desc: d}, {arn: dsDirARN("d-one"), desc: unreplicated}, {arn: dsDirARN("d-consumer"), desc: consumer}}
			total, _, err := scanDSRegions(context.Background(), dsClient(dsHandlers(calls, nil)), newTestAccount(testAccountID), testRegion, st, testScanID, dirs)
			if err != nil || total != 0 || len(calls["DescribeRegions"].distinct()) != 0 {
				t.Errorf("total, err = %d, %v, asked %v; want 0, nil, none", total, err, calls["DescribeRegions"].distinct())
			}
		})
	}
}

func TestDSIPRouteKey(t *testing.T) {
	for _, c := range []struct {
		route dstypes.IpRouteInfo
		want  string
	}{
		{dstypes.IpRouteInfo{CidrIp: ptrStr("10.0.0.0/24")}, "10.0.0.0/24"},
		{dstypes.IpRouteInfo{CidrIpv6: ptrStr("2001:db8::/32")}, "2001:db8::/32"},
		{dstypes.IpRouteInfo{CidrIp: ptrStr("10.0.0.0/24"), CidrIpv6: ptrStr("2001:db8::/32")}, "10.0.0.0/24,2001:db8::/32"},
		{dstypes.IpRouteInfo{Description: ptrStr("none")}, ""},
	} {
		if got := dsIPRouteKey(c.route); got != c.want {
			t.Errorf("dsIPRouteKey(%s) = %q, want %q", mustJSON(c.route), got, c.want)
		}
	}
}

// A directory deleted between DescribeDirectories and the per-directory call
// drops only its own children, silently.
func TestScanDirectoryServiceIn_GoneDirectorySkipped(t *testing.T) {
	for _, c := range []struct {
		op, code, typ string
		dirID         func(any) string
	}{
		{"ListCertificates", "DirectoryDoesNotExistException", TypeDSCertificate, func(in any) string { return *in.(*directoryservice.ListCertificatesInput).DirectoryId }},
		{"ListIpRoutes", "EntityDoesNotExistException", TypeDSIPRoute, func(in any) string { return *in.(*directoryservice.ListIpRoutesInput).DirectoryId }},
		{"DescribeSharedDirectories", "EntityDoesNotExistException", TypeDSSharedDirectory, func(in any) string {
			return *in.(*directoryservice.DescribeSharedDirectoriesInput).OwnerDirectoryId
		}},
		{"DescribeRegions", "DirectoryDoesNotExistException", TypeDSRegion, func(in any) string { return *in.(*directoryservice.DescribeRegionsInput).DirectoryId }},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds:"+c.op)
			base := dsHandlers(map[string]*parentCalls{}, nil)
			gone := &smithy.GenericAPIError{Code: c.code, Message: "gone"}
			handlers := dsHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.op: func(in any) (any, error) {
					if c.dirID(in) == "d-ms" {
						return nil, gone
					}
					return base[c.op](in)
				},
			})
			if _, err := runDSScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, id := range dsWantNativeIDs()[c.typ] {
				if !strings.HasPrefix(id, dsDirARN("d-ms")+"/") {
					want = append(want, id)
				}
			}
			sort.Strings(want)
			if got := dsStoredNativeIDs(t, st, c.typ); !slices.Equal(got, want) || *warned != 0 {
				t.Errorf("%s rows = %v, warnings %d; want %v, 0", c.typ, got, *warned, want)
			}
		})
	}
}

// A denied op warns once (however many directories it was asked for), stores
// none of its type and keeps every other phase's rows.
func TestScanDirectoryServiceIn_DeniedOpWarnsOnce(t *testing.T) {
	want := dsWantNativeIDs()
	for op, typ := range map[string]string{
		"ListCertificates":          TypeDSCertificate,
		"ListIpRoutes":              TypeDSIPRoute,
		"DescribeRegions":           TypeDSRegion,
		"DescribeSharedDirectories": TypeDSSharedDirectory,
		"DescribeEventTopics":       TypeDSEventTopic,
		"ListLogSubscriptions":      TypeDSLogSubscription,
		"DescribeSnapshots":         TypeDSSnapshot,
		"DescribeTrusts":            TypeDSTrust,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds:"+op)
			handlers := dsHandlers(map[string]*parentCalls{}, nil)
			delete(handlers, op)
			total, err := runDSScan(t, st, handlers)
			if err != nil || total != dsWantTotal(want, typ) || *warned != 1 {
				t.Errorf("total, err, warnings = %d, %v, %d; want %d, nil, 1", total, err, *warned, dsWantTotal(want, typ))
			}
			if n := len(rowsByNativeID(t, st, typ)); n != 0 {
				t.Errorf("%s rows = %d, want 0", typ, n)
			}
		})
	}
	t.Run("DescribeDirectories", func(t *testing.T) {
		st := newTestStore(t)
		warned := countOpWarnings(st, "ds:DescribeDirectories")
		handlers := dsHandlers(map[string]*parentCalls{}, nil)
		delete(handlers, "DescribeDirectories")
		if total, err := runDSScan(t, st, handlers); err != nil || total != 0 || *warned != 1 {
			t.Errorf("total, err, warnings = %d, %v, %d; want 0, nil, 1", total, err, *warned)
		}
	})
}

// A denial on a region-wide list's second page keeps the first page's rows.
func TestScanDirectoryServiceIn_DenialMidListKeepsEarlierPages(t *testing.T) {
	for _, c := range []struct {
		op, typ   string
		firstPage func(any) bool
	}{
		{"ListLogSubscriptions", TypeDSLogSubscription, func(in any) bool { return in.(*directoryservice.ListLogSubscriptionsInput).NextToken == nil }},
		{"DescribeSnapshots", TypeDSSnapshot, func(in any) bool { return in.(*directoryservice.DescribeSnapshotsInput).NextToken == nil }},
		{"DescribeTrusts", TypeDSTrust, func(in any) bool { return in.(*directoryservice.DescribeTrustsInput).NextToken == nil }},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds:"+c.op)
			base := dsHandlers(map[string]*parentCalls{}, nil)
			handlers := dsHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.op: func(in any) (any, error) {
					if c.firstPage(in) {
						return base[c.op](in)
					}
					return nil, dsDenied
				},
			})
			if _, err := runDSScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 || *warned != 1 {
				t.Errorf("%s rows = %d, warnings %d; want the 2 first-page rows and 1 warning", c.typ, n, *warned)
			}
		})
	}
}

// Any other error from a phase is returned, and every other phase still runs.
func TestScanDirectoryServiceIn_PhaseErrorReturnedOthersRun(t *testing.T) {
	want := dsWantNativeIDs()
	for op, typ := range map[string]string{
		"ListCertificates":          TypeDSCertificate,
		"ListIpRoutes":              TypeDSIPRoute,
		"DescribeRegions":           TypeDSRegion,
		"DescribeSharedDirectories": TypeDSSharedDirectory,
		"DescribeEventTopics":       TypeDSEventTopic,
		"ListLogSubscriptions":      TypeDSLogSubscription,
		"DescribeSnapshots":         TypeDSSnapshot,
		"DescribeTrusts":            TypeDSTrust,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := dsHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				op: func(any) (any, error) { return nil, dsClientErr },
			})
			total, err := runDSScan(t, st, handlers)
			if !errors.Is(err, dsClientErr) || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v, want the %s error classified outcomeError", err, op)
			}
			if total != dsWantTotal(want, typ) {
				t.Errorf("total = %d, want %d (every other phase stored)", total, dsWantTotal(want, typ))
			}
		})
	}
	t.Run("DescribeDirectories", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dsHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
			"DescribeDirectories": func(any) (any, error) { return nil, dsClientErr },
		})
		if total, err := runDSScan(t, st, handlers); !errors.Is(err, dsClientErr) || total != 0 {
			t.Errorf("total, err = %d, %v; want 0 and the DescribeDirectories error", total, err)
		}
	})
}

// A later phase's transient error is warned about on its own, so it cannot
// make the dispatcher classify the whole service transient.
func TestScanDirectoryServiceIn_TransientPhaseWarns(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "ds:DescribeTrusts")
	handlers := dsHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
		"DescribeTrusts": func(any) (any, error) { return nil, dsThrottled },
	})
	want := dsWantNativeIDs()
	total, err := runDSScan(t, st, handlers)
	if err != nil || total != dsWantTotal(want, TypeDSTrust) || *warned != 1 {
		t.Errorf("total, err, warnings = %d, %v, %d; want %d, nil, 1", total, err, *warned, dsWantTotal(want, TypeDSTrust))
	}
}

// In a region with no Directory Service endpoint the first phase's NXDOMAIN
// is returned alone, so the dispatcher reports the region unavailable.
func TestScanDirectoryServiceIn_UnreachableEndpointStopsTheScan(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){}
	for op := range dsHandlers(map[string]*parentCalls{}, nil) {
		handlers[op] = func(any) (any, error) {
			calls.add(op)
			return nil, &net.DNSError{Err: "no such host", Name: "ds.us-east-1.amazonaws.com", IsNotFound: true}
		}
	}
	total, err := runDSScan(t, st, handlers)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if got, want := calls.distinct(), []string{"DescribeDirectories"}; !slices.Equal(got, want) || total != 0 {
		t.Errorf("ops called %v total %d, want %v and 0", got, total, want)
	}
}

// With no directory stored no child op is called: an unanswered op would
// warn as denied.
func TestScanDirectoryServiceIn_NoDirectoriesCallsNoChildOp(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(store.ScanWarning) { warnings++ }
	handlers := map[string]func(any) (any, error){
		"DescribeDirectories": func(any) (any, error) {
			return &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: []dstypes.DirectoryDescription{
				dsDir("d-conn", dstypes.DirectoryTypeAdConnector, "", dstypes.DirectoryStageActive),
			}}, nil
		},
	}
	if total, err := runDSScan(t, st, handlers); err != nil || total != 0 || warnings != 0 {
		t.Errorf("total, err, warnings = %d, %v, %d; want 0, nil, 0", total, err, warnings)
	}
}

// Directories with no children store only themselves.
func TestScanDirectoryServiceIn_EmptyChildren(t *testing.T) {
	st := newTestStore(t)
	empty := map[string]func(any) (any, error){
		"ListCertificates":          func(any) (any, error) { return &directoryservice.ListCertificatesOutput{}, nil },
		"ListIpRoutes":              func(any) (any, error) { return &directoryservice.ListIpRoutesOutput{}, nil },
		"DescribeRegions":           func(any) (any, error) { return &directoryservice.DescribeRegionsOutput{}, nil },
		"DescribeSharedDirectories": func(any) (any, error) { return &directoryservice.DescribeSharedDirectoriesOutput{}, nil },
		"DescribeEventTopics":       func(any) (any, error) { return &directoryservice.DescribeEventTopicsOutput{}, nil },
		"ListLogSubscriptions":      func(any) (any, error) { return &directoryservice.ListLogSubscriptionsOutput{}, nil },
		"DescribeSnapshots":         func(any) (any, error) { return &directoryservice.DescribeSnapshotsOutput{}, nil },
		"DescribeTrusts":            func(any) (any, error) { return &directoryservice.DescribeTrustsOutput{}, nil },
	}
	want := dsWantNativeIDs()
	total, err := runDSScan(t, st, dsHandlers(map[string]*parentCalls{}, empty))
	if wantTotal := len(want[TypeDSMicrosoftAD]) + len(want[TypeDSSimpleAD]); err != nil || total != wantTotal {
		t.Errorf("total, err = %d, %v; want %d, nil", total, err, wantTotal)
	}
}
