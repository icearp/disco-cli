package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lightsailtypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

const lsTestSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

var lsKeyCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func lsBucketARN(name string) string {
	return fmt.Sprintf("arn:aws:lightsail:%s:%s:Bucket/%s-id", testRegion, testAccountID, name)
}

func lsAccessKeyNativeID(bucket, keyID string) string {
	return lsBucketARN(bucket) + "/access-key/" + keyID
}

// lsAccessKey carries a SecretAccessKey although GetBucketAccessKeys documents
// it never returns one, so the stored rows prove the redaction rule holds.
func lsAccessKey(id string) lightsailtypes.AccessKey {
	return lightsailtypes.AccessKey{
		AccessKeyId: sdkaws.String(id), CreatedAt: &lsKeyCreated, Status: lightsailtypes.StatusTypeActive,
		SecretAccessKey: sdkaws.String(lsTestSecret),
		LastUsed:        &lightsailtypes.AccessKeyLastUsed{Region: sdkaws.String("N/A"), ServiceName: sdkaws.String("N/A")},
	}
}

// fakeLightsail serves GetBucketAccessKeys by bucket name; errs fails a bucket.
// childFanOut calls it from concurrent goroutines, so the call log is locked.
type fakeLightsail struct {
	lightsailAPI
	keys map[string][]lightsailtypes.AccessKey
	errs map[string]error

	mu      sync.Mutex
	queried []string
}

func (f *fakeLightsail) GetBucketAccessKeys(_ context.Context, in *lightsail.GetBucketAccessKeysInput, _ ...func(*lightsail.Options)) (*lightsail.GetBucketAccessKeysOutput, error) {
	name := *in.BucketName
	f.mu.Lock()
	f.queried = append(f.queried, name)
	f.mu.Unlock()
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	return &lightsail.GetBucketAccessKeysOutput{AccessKeys: f.keys[name]}, nil
}

func seedLSBuckets(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	parents := make([]childParent, len(names))
	for i, n := range names {
		upsertTestResource(t, st, "aws", testAccountID, TypeLightsailBucket, lsBucketARN(n), testRegion, "{}")
		parents[i] = childParent{id: n, arn: lsBucketARN(n)}
	}
	return parents
}

func runLSBucketAccessKeys(st *store.Store, fake *fakeLightsail, buckets []childParent) error {
	_, _, err := scanLSBucketAccessKeys(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, buckets)
	return err
}

func TestScanLSBucketAccessKeys_StoresEveryBucketsKeysUnderItsBucket(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lsGetBucketAccessKeysOp)
	buckets := seedLSBuckets(t, st, "b1", "b2")
	fake := &fakeLightsail{keys: map[string][]lightsailtypes.AccessKey{
		"b1": {lsAccessKey("AKIAB1ONE"), {Status: lightsailtypes.StatusTypeActive}, lsAccessKey("AKIAB1TWO")},
		"b2": {lsAccessKey("AKIAB2ONE"), lsAccessKey("AKIAB2TWO")},
	}}
	if err := runLSBucketAccessKeys(st, fake, buckets); err != nil {
		t.Fatalf("scanLSBucketAccessKeys: %v", err)
	}
	if len(fake.queried) != 2 {
		t.Errorf("queried buckets %v; want b1 and b2 once each", fake.queried)
	}
	rows := qsRows(t, st, TypeLightsailBucketAccessKey)
	if len(rows) != 4 {
		t.Fatalf("stored %d rows; want 4 (id-less key skipped)", len(rows))
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 for the id-less key", lsGetBucketAccessKeysOp, *warns)
	}
	for _, b := range []string{"b1", "b2"} {
		one, two := lsAccessKeyNativeID(b, "AKIA"+strings.ToUpper(b)+"ONE"), lsAccessKeyNativeID(b, "AKIA"+strings.ToUpper(b)+"TWO")
		r, ok := rows[one]
		if !ok {
			t.Fatalf("no row %s; got %v", one, rows)
		}
		if r.Type != TypeLightsailBucketAccessKey || sv(r.Region) != testRegion || sv(r.Name) != "AKIA"+strings.ToUpper(b)+"ONE" ||
			sv(r.Status) != "Active" || sv(r.CreatedAt) != sv(tp(&lsKeyCreated)) {
			t.Errorf("row %s: type=%q region=%q name=%q status=%q created=%q", one, r.Type, sv(r.Region), sv(r.Name), sv(r.Status), sv(r.CreatedAt))
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs["LastUsed"] == nil {
			t.Errorf("row %s: attributes %s lack LastUsed (err %v)", one, r.AttributesJSON, err)
		}
		if attrs["SecretAccessKey"] != redact.Placeholder {
			t.Errorf("row %s: SecretAccessKey stored as %v; want %q", one, attrs["SecretAccessKey"], redact.Placeholder)
		}
		assertQSContains(t, st, lsBucketARN(b), one, two)
	}
}

func TestScanLSBucketAccessKeys_BucketErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"bucket deleted since listing is skipped", apiErr("NotFoundException", "gone"), false, 2, 0},
		{"access denied warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: lightsail:GetBucketAccessKeys"), false, 2, 1},
		{"other error keeps siblings and is returned after storing", apiErr("ServiceException", "boom"), true, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w) }
			buckets := seedLSBuckets(t, st, "bad1", "bad2", "ok1", "ok2")
			fake := &fakeLightsail{
				keys: map[string][]lightsailtypes.AccessKey{"ok1": {lsAccessKey("AKIAOK1")}, "ok2": {lsAccessKey("AKIAOK2")}},
				errs: map[string]error{"bad1": tc.err, "bad2": tc.err},
			}
			err := runLSBucketAccessKeys(st, fake, buckets)
			if tc.wantErr {
				if !isAPIErrorCode(err, "ServiceException") {
					t.Fatalf("err = %v; want the ServiceException propagated", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(qsRows(t, st, TypeLightsailBucketAccessKey)); got != tc.wantRows {
				t.Errorf("stored %d rows; want %d", got, tc.wantRows)
			}
			for _, b := range []string{"ok1", "ok2"} {
				assertQSContains(t, st, lsBucketARN(b), lsAccessKeyNativeID(b, "AKIA"+strings.ToUpper(b)))
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, w := range warned {
				if w.Service != lsGetBucketAccessKeysOp || w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning %s scope %q; want %s scope %s/%s", w.Service, w.Scope, lsGetBucketAccessKeysOp, testAccountID, testRegion)
				}
			}
		})
	}
}

func TestScanLSBucketAccessKeys_NoBucketsMakesNoCalls(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeLightsail{}
	if err := runLSBucketAccessKeys(st, fake, nil); err != nil {
		t.Fatalf("scanLSBucketAccessKeys: %v", err)
	}
	if len(fake.queried) != 0 || len(qsRows(t, st, TypeLightsailBucketAccessKey)) != 0 {
		t.Errorf("queried %v and stored rows with no buckets; want neither", fake.queried)
	}
}

func TestScanLSBucketAccessKeys_BucketWithoutKeysStoresNothing(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeLightsail{}
	if err := runLSBucketAccessKeys(st, fake, seedLSBuckets(t, st, "b1")); err != nil {
		t.Fatalf("scanLSBucketAccessKeys: %v", err)
	}
	if len(fake.queried) != 1 || len(qsRows(t, st, TypeLightsailBucketAccessKey)) != 0 {
		t.Errorf("queried %v; want one GetBucketAccessKeys and no rows", fake.queried)
	}
}

func lsBucket(name string) lightsailtypes.Bucket {
	return lightsailtypes.Bucket{Arn: sdkaws.String(lsBucketARN(name)), Name: sdkaws.String(name)}
}

// lsServiceStub serves every op scanLightsail calls in us-east-1: one bucket
// on each of two GetBuckets pages, one contact method (the last pre-existing
// phase), one access key per bucket, and override replacing an op's queue.
func lsServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"GetInstances":                   one(&lightsail.GetInstancesOutput{}),
		"GetRelationalDatabases":         one(&lightsail.GetRelationalDatabasesOutput{}),
		"GetContainerServices":           one(&lightsail.GetContainerServicesOutput{}),
		"GetLoadBalancers":               one(&lightsail.GetLoadBalancersOutput{}),
		"GetAlarms":                      one(&lightsail.GetAlarmsOutput{}),
		"GetCertificates":                one(&lightsail.GetCertificatesOutput{}),
		"GetRelationalDatabaseSnapshots": one(&lightsail.GetRelationalDatabaseSnapshotsOutput{}),
		"GetDisks":                       one(&lightsail.GetDisksOutput{}),
		"GetDiskSnapshots":               one(&lightsail.GetDiskSnapshotsOutput{}),
		"GetDistributions":               one(&lightsail.GetDistributionsOutput{}),
		"GetDomains":                     one(&lightsail.GetDomainsOutput{}),
		"GetInstanceSnapshots":           one(&lightsail.GetInstanceSnapshotsOutput{}),
		"GetStaticIps":                   one(&lightsail.GetStaticIpsOutput{}),
		"GetKeyPairs":                    one(&lightsail.GetKeyPairsOutput{}),
		"GetBuckets": {
			{Output: &lightsail.GetBucketsOutput{Buckets: []lightsailtypes.Bucket{lsBucket("b1"), {Arn: sdkaws.String(lsBucketARN("nameless"))}}, NextPageToken: sdkaws.String("p2")}},
			{Output: &lightsail.GetBucketsOutput{Buckets: []lightsailtypes.Bucket{lsBucket("b2")}}},
		},
		"GetContactMethods": one(&lightsail.GetContactMethodsOutput{ContactMethods: []lightsailtypes.ContactMethod{
			{Arn: sdkaws.String(lsContactMethodARN), Name: sdkaws.String("email")},
		}}),
		"GetBucketAccessKeys": {
			{Output: &lightsail.GetBucketAccessKeysOutput{AccessKeys: []lightsailtypes.AccessKey{lsAccessKey("AKIAKEY1")}}},
			{Output: &lightsail.GetBucketAccessKeysOutput{AccessKeys: []lightsailtypes.AccessKey{lsAccessKey("AKIAKEY2")}}},
		},
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
}

var lsContactMethodARN = fmt.Sprintf("arn:aws:lightsail:%s:%s:ContactMethod/cm-id", testRegion, testAccountID)

// TestScanLightsail_BucketAccessKeysFanOutOverListedBuckets runs the whole
// service against the real SDK client (its input validators included): both
// GetBuckets pages hand their named buckets to the access-key phase, and the
// nameless bucket is stored but never queried.
func TestScanLightsail_BucketAccessKeysFanOutOverListedBuckets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = lsServiceStub(t, nil)
	total, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanLightsail: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (3 buckets, contact method, 2 access keys)", total)
	}
	keys := qsRows(t, st, TypeLightsailBucketAccessKey)
	if len(keys) != 2 {
		t.Fatalf("stored %d access keys; want 2", len(keys))
	}
	// childFanOut runs the buckets concurrently, so which bucket got which
	// queued response is unordered: assert each bucket holds exactly one key.
	for _, b := range []string{"b1", "b2"} {
		rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, lsBucketARN(b)), store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", b, err)
		}
		if len(rels) != 1 {
			t.Errorf("bucket %s contains %d access keys; want 1", b, len(rels))
		}
	}
}

// TestScanLightsail_BucketAccessKeyErrorPropagatesAfterExistingPhases pins that
// the access-key phase's hard error reaches the service result and that the
// last pre-existing phase (contact methods) ran before it.
func TestScanLightsail_BucketAccessKeyErrorPropagatesAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	boom := stubCall{Err: apiErr("ServiceException", "boom")}
	acct.cfg = lsServiceStub(t, map[string][]stubCall{"GetBucketAccessKeys": {boom, boom}})
	_, _, err := scanLightsail(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ServiceException") {
		t.Fatalf("err = %v; want the GetBucketAccessKeys ServiceException", err)
	}
	if _, ok := qsRows(t, st, TypeLightsailContactMethod)[lsContactMethodARN]; !ok {
		t.Error("contact-methods phase did not run before the failing GetBucketAccessKeys")
	}
}

// lsTLSFake serves GetLoadBalancerTlsCertificates by load balancer name; errs
// fails a load balancer.
type lsTLSFake struct {
	lightsailAPI
	certs map[string][]lightsailtypes.LoadBalancerTlsCertificate
	errs  map[string]error
}

//nolint:revive // method name must match the SDK op (GetLoadBalancerTlsCertificates) to satisfy lightsailAPI.
func (f *lsTLSFake) GetLoadBalancerTlsCertificates(_ context.Context, in *lightsail.GetLoadBalancerTlsCertificatesInput, _ ...func(*lightsail.Options)) (*lightsail.GetLoadBalancerTlsCertificatesOutput, error) {
	if err := f.errs[*in.LoadBalancerName]; err != nil {
		return nil, err
	}
	return &lightsail.GetLoadBalancerTlsCertificatesOutput{TlsCertificates: f.certs[*in.LoadBalancerName]}, nil
}

func lsTLSCert(id string) lightsailtypes.LoadBalancerTlsCertificate {
	return lightsailtypes.LoadBalancerTlsCertificate{Arn: sdkaws.String(lsARN("LoadBalancerTlsCertificate", id)), Name: sdkaws.String(id)}
}

// One load balancer's failure keeps the other load balancers' certificates.
func TestScanLSLBTlsCertificates_LoadBalancerErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{"load balancer deleted since listing is skipped", apiErr("NotFoundException", "gone"), "", 0},
		{"denial warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: lightsail:GetLoadBalancerTlsCertificates"), "", 1},
		{"other error keeps siblings and is returned", apiErr("ServiceException", "boom"), "ServiceException", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) { scopes = append(scopes, w.Scope) }
			warns := countOpWarnings(st, lsGetLoadBalancerTLSCertsOp)
			fake := &lsTLSFake{
				certs: map[string][]lightsailtypes.LoadBalancerTlsCertificate{"ok1": {lsTLSCert("c1")}, "ok2": {lsTLSCert("c2")}},
				errs:  map[string]error{"bad1": tc.err, "bad2": tc.err},
			}
			total, _, err := scanLSLBTlsCertificates(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID,
				[]string{"bad1", "ok1", "bad2", "ok2"})
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Errorf("err = %v; want nil", err)
			}
			rows := rowsByNativeID(t, st, TypeLightsailLoadBalancerTLSCertificate)
			for _, id := range []string{"c1", "c2"} {
				if _, ok := rows[lsARN("LoadBalancerTlsCertificate", id)]; !ok {
					t.Errorf("certificate %s not stored", id)
				}
			}
			if total != 2 || len(rows) != 2 {
				t.Errorf("total = %d, rows = %d; want 2, 2", total, len(rows))
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", lsGetLoadBalancerTLSCertsOp, *warns, tc.wantWarns)
			}
			for _, sc := range scopes {
				if sc != testAccountID+"/"+testRegion {
					t.Errorf("warning scope %q; want %s/%s", sc, testAccountID, testRegion)
				}
			}
		})
	}
}

// A certificate listed without an ARN is warned about and not stored.
func TestScanLSLBTlsCertificates_CertificateWithoutARNWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lsGetLoadBalancerTLSCertsOp)
	fake := &lsTLSFake{certs: map[string][]lightsailtypes.LoadBalancerTlsCertificate{
		"lb": {{Name: sdkaws.String("noarn")}, lsTLSCert("c1")},
	}}
	if _, _, err := scanLSLBTlsCertificates(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"lb"}); err != nil {
		t.Fatalf("scanLSLBTlsCertificates: %v", err)
	}
	if rows := rowsByNativeID(t, st, TypeLightsailLoadBalancerTLSCertificate); len(rows) != 1 {
		t.Errorf("stored %d certificates; want 1", len(rows))
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", lsGetLoadBalancerTLSCertsOp, *warns)
	}
}
