package aws

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/icearp/disco-cli/store"
)

func lsARN(kind, id string) string {
	return fmt.Sprintf("arn:aws:lightsail:%s:%s:%s/%s", testRegion, testAccountID, kind, id)
}

func lsTags(v string) []lightsailtypes.Tag {
	return []lightsailtypes.Tag{{Key: sdkaws.String("env"), Value: sdkaws.String(v)}}
}

// lsTaggedKinds maps each tagged Lightsail type to the ARN resource kind its
// fixture uses. Every list op below returns a "tagged" element carrying
// env=<type> and a "plain" element with no tags.
var lsTaggedKinds = map[string]string{
	TypeLightsailInstance:                   "Instance",
	TypeLightsailDatabase:                   "RelationalDatabase",
	TypeLightsailContainerService:           "ContainerService",
	TypeLightsailAlarm:                      "Alarm",
	TypeLightsailBucket:                     "Bucket",
	TypeLightsailCertificate:                "Certificate",
	TypeLightsailDatabaseSnapshot:           "RelationalDatabaseSnapshot",
	TypeLightsailDisk:                       "Disk",
	TypeLightsailDiskSnapshot:               "DiskSnapshot",
	TypeLightsailDistribution:               "Distribution",
	TypeLightsailDomain:                     "Domain",
	TypeLightsailInstanceSnapshot:           "InstanceSnapshot",
	TypeLightsailLoadBalancer:               "LoadBalancer",
	TypeLightsailLoadBalancerTLSCertificate: "LoadBalancerTlsCertificate",
	TypeLightsailKeyPair:                    "KeyPair",
	TypeLightsailContactMethod:              "ContactMethod",
}

func lsTaggedStub(t *testing.T) sdkaws.Config {
	t.Helper()
	a := func(rtype, id string) *string { return sdkaws.String(lsARN(lsTaggedKinds[rtype], id)) }
	n := sdkaws.String
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	return lsServiceStub(t, map[string][]stubCall{
		"GetInstances": one(&lightsail.GetInstancesOutput{Instances: []lightsailtypes.Instance{
			{Arn: a(TypeLightsailInstance, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailInstance), State: &lightsailtypes.InstanceState{Name: n("running")}},
			{Arn: a(TypeLightsailInstance, "plain"), Name: n("plain"), State: &lightsailtypes.InstanceState{}},
		}}),
		"GetRelationalDatabases": one(&lightsail.GetRelationalDatabasesOutput{RelationalDatabases: []lightsailtypes.RelationalDatabase{
			{Arn: a(TypeLightsailDatabase, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDatabase), State: n("available")},
			{Arn: a(TypeLightsailDatabase, "plain"), Name: n("plain")},
		}}),
		"GetContainerServices": one(&lightsail.GetContainerServicesOutput{ContainerServices: []lightsailtypes.ContainerService{
			{Arn: a(TypeLightsailContainerService, "tagged"), ContainerServiceName: n("tagged"), Tags: lsTags(TypeLightsailContainerService), State: lightsailtypes.ContainerServiceStateRunning},
			{Arn: a(TypeLightsailContainerService, "plain"), ContainerServiceName: n("plain")},
		}}),
		"GetLoadBalancers": one(&lightsail.GetLoadBalancersOutput{LoadBalancers: []lightsailtypes.LoadBalancer{
			{Arn: a(TypeLightsailLoadBalancer, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailLoadBalancer)},
			{Arn: a(TypeLightsailLoadBalancer, "plain"), Name: n("plain")},
		}}),
		// One call per load balancer, in GetLoadBalancers order.
		"GetLoadBalancerTlsCertificates": {
			{Output: &lightsail.GetLoadBalancerTlsCertificatesOutput{TlsCertificates: []lightsailtypes.LoadBalancerTlsCertificate{
				{Arn: a(TypeLightsailLoadBalancerTLSCertificate, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailLoadBalancerTLSCertificate)},
			}}},
			{Output: &lightsail.GetLoadBalancerTlsCertificatesOutput{TlsCertificates: []lightsailtypes.LoadBalancerTlsCertificate{
				{Arn: a(TypeLightsailLoadBalancerTLSCertificate, "plain"), Name: n("plain")},
			}}},
		},
		"GetAlarms": one(&lightsail.GetAlarmsOutput{Alarms: []lightsailtypes.Alarm{
			{Arn: a(TypeLightsailAlarm, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailAlarm)},
			{Arn: a(TypeLightsailAlarm, "plain"), Name: n("plain")},
		}}),
		"GetBuckets": one(&lightsail.GetBucketsOutput{Buckets: []lightsailtypes.Bucket{
			{Arn: a(TypeLightsailBucket, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailBucket)},
			{Arn: a(TypeLightsailBucket, "plain"), Name: n("plain")},
		}}),
		"GetCertificates": one(&lightsail.GetCertificatesOutput{Certificates: []lightsailtypes.CertificateSummary{
			{CertificateArn: a(TypeLightsailCertificate, "tagged"), CertificateName: n("tagged"), Tags: lsTags(TypeLightsailCertificate)},
			{CertificateArn: a(TypeLightsailCertificate, "plain"), CertificateName: n("plain")},
		}}),
		"GetRelationalDatabaseSnapshots": one(&lightsail.GetRelationalDatabaseSnapshotsOutput{RelationalDatabaseSnapshots: []lightsailtypes.RelationalDatabaseSnapshot{
			{Arn: a(TypeLightsailDatabaseSnapshot, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDatabaseSnapshot)},
			{Arn: a(TypeLightsailDatabaseSnapshot, "plain"), Name: n("plain")},
		}}),
		"GetDisks": one(&lightsail.GetDisksOutput{Disks: []lightsailtypes.Disk{
			{Arn: a(TypeLightsailDisk, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDisk)},
			{Arn: a(TypeLightsailDisk, "plain"), Name: n("plain")},
		}}),
		"GetDiskSnapshots": one(&lightsail.GetDiskSnapshotsOutput{DiskSnapshots: []lightsailtypes.DiskSnapshot{
			{Arn: a(TypeLightsailDiskSnapshot, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDiskSnapshot)},
			{Arn: a(TypeLightsailDiskSnapshot, "plain"), Name: n("plain")},
		}}),
		"GetDistributions": one(&lightsail.GetDistributionsOutput{Distributions: []lightsailtypes.LightsailDistribution{
			{Arn: a(TypeLightsailDistribution, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDistribution)},
			{Arn: a(TypeLightsailDistribution, "plain"), Name: n("plain")},
		}}),
		"GetDomains": one(&lightsail.GetDomainsOutput{Domains: []lightsailtypes.Domain{
			{Arn: a(TypeLightsailDomain, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailDomain)},
			{Arn: a(TypeLightsailDomain, "plain"), Name: n("plain")},
		}}),
		"GetInstanceSnapshots": one(&lightsail.GetInstanceSnapshotsOutput{InstanceSnapshots: []lightsailtypes.InstanceSnapshot{
			{Arn: a(TypeLightsailInstanceSnapshot, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailInstanceSnapshot)},
			{Arn: a(TypeLightsailInstanceSnapshot, "plain"), Name: n("plain")},
		}}),
		"GetKeyPairs": one(&lightsail.GetKeyPairsOutput{KeyPairs: []lightsailtypes.KeyPair{
			{Arn: a(TypeLightsailKeyPair, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailKeyPair)},
			{Arn: a(TypeLightsailKeyPair, "plain"), Name: n("plain")},
		}}),
		"GetContactMethods": one(&lightsail.GetContactMethodsOutput{ContactMethods: []lightsailtypes.ContactMethod{
			{Arn: a(TypeLightsailContactMethod, "tagged"), Name: n("tagged"), Tags: lsTags(TypeLightsailContactMethod), Status: lightsailtypes.ContactMethodStatusValid},
			{Arn: a(TypeLightsailContactMethod, "plain"), Name: n("plain")},
		}}),
	})
}

// Every tagged Lightsail type stores the tags of its listed element, and an
// element without tags stores none.
func TestScanLightsail_StoresListedTags(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = lsTaggedStub(t)
	total, inserted, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	if total != 34 || inserted != 34 {
		t.Errorf("total, inserted = %d, %d; want 34, 34 (16 types x 2, 2 access keys)", total, inserted)
	}
	for rtype, kind := range lsTaggedKinds {
		rows := rowsByNativeID(t, st, rtype)
		tagged, ok := rows[lsARN(kind, "tagged")]
		if !ok {
			t.Errorf("%s: tagged row not stored; got %d rows", rtype, len(rows))
			continue
		}
		assertTagsMap(t, tagged.TagsJSON, map[string]string{"env": rtype})
		plain, ok := rows[lsARN(kind, "plain")]
		if !ok {
			t.Errorf("%s: plain row not stored", rtype)
			continue
		}
		if plain.TagsJSON != nil {
			t.Errorf("%s: plain TagsJSON = %q; want nil", rtype, *plain.TagsJSON)
		}
	}
}

// A status the element omits is left unset, not stored as "".
func TestScanLightsail_StatusOnlyWhenListed(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = lsTaggedStub(t)
	if _, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	for rtype, want := range map[string]string{
		TypeLightsailInstance:         "running",
		TypeLightsailDatabase:         "available",
		TypeLightsailContainerService: "RUNNING",
		TypeLightsailContactMethod:    "Valid",
	} {
		rows := rowsByNativeID(t, st, rtype)
		kind := lsTaggedKinds[rtype]
		if got := sv(rows[lsARN(kind, "tagged")].Status); got != want {
			t.Errorf("%s tagged Status = %q; want %q", rtype, got, want)
		}
		if s := rows[lsARN(kind, "plain")].Status; s != nil {
			t.Errorf("%s plain Status = %q; want nil", rtype, *s)
		}
	}
}

func lsAlarm(id string) lightsailtypes.Alarm {
	return lightsailtypes.Alarm{Arn: sdkaws.String(lsARN("Alarm", id)), Name: sdkaws.String(id)}
}

// The listing rules every Lightsail list op shares (lsList), driven through
// GetAlarms; the contact-methods phase, which runs after it, pins that a
// failed phase does not stop the later ones.
func TestScanLightsail_ListErrors(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: lightsail:GetAlarms")
	page := func(next *string, alarms ...lightsailtypes.Alarm) stubCall {
		return stubCall{Output: &lightsail.GetAlarmsOutput{Alarms: alarms, NextPageToken: next}}
	}
	cases := []struct {
		name      string
		pages     []stubCall
		wantCode  string
		wantToken bool
		wantRows  []string
		wantWarns int
	}{
		{
			"later page failure keeps the pages read",
			[]stubCall{page(sdkaws.String("p2"), lsAlarm("a1")), {Err: apiErr("ServiceException", "boom")}},
			"ServiceException", false,
			[]string{"a1"},
			0,
		},
		{
			"later page denial keeps the pages read and warns",
			[]stubCall{page(sdkaws.String("p2"), lsAlarm("a1")), {Err: denied}},
			"", false,
			[]string{"a1"},
			1,
		},
		{"first page denial warns", []stubCall{{Err: denied}}, "", false, nil, 1},
		{"first page failure is returned", []stubCall{{Err: apiErr("ServiceException", "boom")}}, "ServiceException", false, nil, 0},
		{
			"repeated token stops incomplete after storing",
			[]stubCall{page(sdkaws.String("t"), lsAlarm("a1")), page(sdkaws.String("t"), lsAlarm("a2"))},
			"", true,
			[]string{"a1", "a2"},
			0,
		},
		{
			"alarm without an ARN warns and is not stored",
			[]stubCall{page(sdkaws.String("p2"), lightsailtypes.Alarm{Name: sdkaws.String("noarn")}), page(nil, lsAlarm("a2"))},
			"", false,
			[]string{"a2"},
			1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, lsGetAlarmsOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = lsServiceStub(t, map[string][]stubCall{"GetAlarms": tc.pages})
			total, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID)
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantToken:
				if !errors.Is(err, errRepeatedNextToken) {
					t.Errorf("err = %v; want errRepeatedNextToken", err)
				}
			case err != nil:
				t.Errorf("err = %v; want nil", err)
			}
			rows := rowsByNativeID(t, st, TypeLightsailAlarm)
			if len(rows) != len(tc.wantRows) {
				t.Errorf("stored %d alarms; want %v", len(rows), tc.wantRows)
			}
			for _, id := range tc.wantRows {
				if _, ok := rows[lsARN("Alarm", id)]; !ok {
					t.Errorf("alarm %s not stored", id)
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", lsGetAlarmsOp, *warns, tc.wantWarns)
			}
			// lsServiceStub's default rows: 3 buckets, a contact method, 2 access keys.
			if want := 6 + len(tc.wantRows); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if _, ok := rowsByNativeID(t, st, TypeLightsailContactMethod)[lsContactMethodARN]; !ok {
				t.Error("contact-methods phase did not run after the GetAlarms phase")
			}
		})
	}
}

// GetCertificates paginates; every page is stored.
func TestScanLightsail_CertificatesReadEveryPage(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cert := func(id string) lightsailtypes.CertificateSummary {
		return lightsailtypes.CertificateSummary{CertificateArn: sdkaws.String(lsARN("Certificate", id)), CertificateName: sdkaws.String(id)}
	}
	acct.cfg = lsServiceStub(t, map[string][]stubCall{"GetCertificates": {
		{Output: &lightsail.GetCertificatesOutput{Certificates: []lightsailtypes.CertificateSummary{cert("c1")}, NextPageToken: sdkaws.String("p2")}},
		{Output: &lightsail.GetCertificatesOutput{Certificates: []lightsailtypes.CertificateSummary{cert("c2")}}},
	}})
	if _, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeLightsailCertificate)
	for _, id := range []string{"c1", "c2"} {
		if _, ok := rows[lsARN("Certificate", id)]; !ok {
			t.Errorf("certificate %s not stored; got %d rows", id, len(rows))
		}
	}
}

// lsStoreFailFake lists one instance after closing the store, so the instance
// phase's write fails; it records whether a later phase ran.
type lsStoreFailFake struct {
	lightsailAPI
	closeStore func()
	laterRan   bool
}

func (f *lsStoreFailFake) GetInstances(context.Context, *lightsail.GetInstancesInput, ...func(*lightsail.Options)) (*lightsail.GetInstancesOutput, error) {
	f.closeStore()
	return &lightsail.GetInstancesOutput{Instances: []lightsailtypes.Instance{{Arn: sdkaws.String(lsARN("Instance", "i1"))}}}, nil
}

func (f *lsStoreFailFake) GetRelationalDatabases(context.Context, *lightsail.GetRelationalDatabasesInput, ...func(*lightsail.Options)) (*lightsail.GetRelationalDatabasesOutput, error) {
	f.laterRan = true
	return &lightsail.GetRelationalDatabasesOutput{}, nil
}

// A failed store write stops the later phases: every write would fail too.
func TestScanLightsail_StoreWriteStopsLaterPhases(t *testing.T) {
	st, closeStore := openClosingStore(t)
	fake := &lsStoreFailFake{closeStore: closeStore}
	_, _, err := scanLightsailWith(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want ErrStoreWrite", err)
	}
	if fake.laterRan {
		t.Error("GetRelationalDatabases ran after the instance write failed")
	}
}

// lsPageToken reads the PageToken every paginated Lightsail Get* input carries.
func lsPageToken(in any) *string {
	return reflect.ValueOf(in).Elem().FieldByName("PageToken").Interface().(*string)
}

// lsPagedOps is every paginated Lightsail list op: its type, the fixture's
// ARN kind, and its output holding one element and a next token.
var lsPagedOps = []struct {
	op, rtype, kind string
	out             func(arn, name, next *string) any
}{
	{"GetInstances", TypeLightsailInstance, "Instance", func(a, n, next *string) any {
		return &lightsail.GetInstancesOutput{Instances: []lightsailtypes.Instance{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetRelationalDatabases", TypeLightsailDatabase, "RelationalDatabase", func(a, n, next *string) any {
		return &lightsail.GetRelationalDatabasesOutput{RelationalDatabases: []lightsailtypes.RelationalDatabase{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetLoadBalancers", TypeLightsailLoadBalancer, "LoadBalancer", func(a, n, next *string) any {
		return &lightsail.GetLoadBalancersOutput{LoadBalancers: []lightsailtypes.LoadBalancer{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetAlarms", TypeLightsailAlarm, "Alarm", func(a, n, next *string) any {
		return &lightsail.GetAlarmsOutput{Alarms: []lightsailtypes.Alarm{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetBuckets", TypeLightsailBucket, "Bucket", func(a, n, next *string) any {
		return &lightsail.GetBucketsOutput{Buckets: []lightsailtypes.Bucket{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetCertificates", TypeLightsailCertificate, "Certificate", func(a, n, next *string) any {
		return &lightsail.GetCertificatesOutput{Certificates: []lightsailtypes.CertificateSummary{{CertificateArn: a, CertificateName: n}}, NextPageToken: next}
	}},
	{"GetRelationalDatabaseSnapshots", TypeLightsailDatabaseSnapshot, "RelationalDatabaseSnapshot", func(a, n, next *string) any {
		return &lightsail.GetRelationalDatabaseSnapshotsOutput{RelationalDatabaseSnapshots: []lightsailtypes.RelationalDatabaseSnapshot{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetDisks", TypeLightsailDisk, "Disk", func(a, n, next *string) any {
		return &lightsail.GetDisksOutput{Disks: []lightsailtypes.Disk{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetDiskSnapshots", TypeLightsailDiskSnapshot, "DiskSnapshot", func(a, n, next *string) any {
		return &lightsail.GetDiskSnapshotsOutput{DiskSnapshots: []lightsailtypes.DiskSnapshot{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetDistributions", TypeLightsailDistribution, "Distribution", func(a, n, next *string) any {
		return &lightsail.GetDistributionsOutput{Distributions: []lightsailtypes.LightsailDistribution{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetDomains", TypeLightsailDomain, "Domain", func(a, n, next *string) any {
		return &lightsail.GetDomainsOutput{Domains: []lightsailtypes.Domain{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetInstanceSnapshots", TypeLightsailInstanceSnapshot, "InstanceSnapshot", func(a, n, next *string) any {
		return &lightsail.GetInstanceSnapshotsOutput{InstanceSnapshots: []lightsailtypes.InstanceSnapshot{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetStaticIps", TypeLightsailStaticIP, "StaticIp", func(a, n, next *string) any {
		return &lightsail.GetStaticIpsOutput{StaticIps: []lightsailtypes.StaticIp{{Arn: a, Name: n}}, NextPageToken: next}
	}},
	{"GetKeyPairs", TypeLightsailKeyPair, "KeyPair", func(a, n, next *string) any {
		return &lightsail.GetKeyPairsOutput{KeyPairs: []lightsailtypes.KeyPair{{Arn: a, Name: n}}, NextPageToken: next}
	}},
}

// Every paginated op sends the token its previous page returned: page 2 is
// served only for that token, and any other token fails the call.
func TestScanLightsail_ForwardsPageToken(t *testing.T) {
	handlers := map[string]func(in any) (any, error){}
	for _, p := range lsPagedOps {
		handlers[p.op] = func(in any) (any, error) {
			want := "tok-" + p.op
			switch tok := sv(lsPageToken(in)); tok {
			case "":
				return p.out(sdkaws.String(lsARN(p.kind, "page1")), sdkaws.String("page1"), &want), nil
			case want:
				return p.out(sdkaws.String(lsARN(p.kind, "page2")), sdkaws.String("page2"), nil), nil
			default:
				return nil, apiErr("ServiceException", "unexpected PageToken "+tok)
			}
		}
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// Ops without a handler (the non-paginated ones and the fan-outs) are denied
	// and only warn.
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: lightsail:Get"))
	if _, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	for _, p := range lsPagedOps {
		rows := rowsByNativeID(t, st, p.rtype)
		for _, id := range []string{"page1", "page2"} {
			if _, ok := rows[lsARN(p.kind, id)]; !ok {
				t.Errorf("%s: %s row not stored; got %d rows", p.op, id, len(rows))
			}
		}
	}
}

// Outside us-east-1 the distribution and domain ops are never called, while
// the regional ops still run.
func TestScanLightsail_DistributionsAndDomainsOnlyInUSEast1(t *testing.T) {
	const region = "us-west-2"
	gated := func(in any) (any, error) {
		t.Errorf("%T called in %s; want it only in us-east-1", in, region)
		return nil, apiErr("InvalidInputException", "only available in the us-east-1 Region")
	}
	diskARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Disk/d1", region, testAccountID)
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"GetDistributions": gated,
		"GetDomains":       gated,
		"GetDisks": func(any) (any, error) {
			return &lightsail.GetDisksOutput{Disks: []lightsailtypes.Disk{{Arn: sdkaws.String(diskARN), Name: sdkaws.String("d1")}}}, nil
		},
	}, apiErr("AccessDeniedException", "User: x is not authorized to perform: lightsail:Get"))
	if _, _, err := scanLightsail(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	for _, rtype := range []string{TypeLightsailDistribution, TypeLightsailDomain} {
		if rows := rowsByNativeID(t, st, rtype); len(rows) != 0 {
			t.Errorf("%s: stored %d rows in %s; want 0", rtype, len(rows), region)
		}
	}
	disk, ok := rowsByNativeID(t, st, TypeLightsailDisk)[diskARN]
	if !ok || sv(disk.Region) != region {
		t.Errorf("disk stored = %v, region %q; want stored in %s", ok, sv(disk.Region), region)
	}
}
