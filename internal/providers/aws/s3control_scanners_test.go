package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	s3ctypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every regional listing serves its "t" item on the first page
// and its "b" item on the second. Both tag ops answer with tags for every "t"
// item and none for the "b" items.

var (
	s3cDenied    = &smithy.GenericAPIError{Code: "AccessDenied", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: s3:List"}
	s3cBroken    = &smithy.GenericAPIError{Code: "InvalidRequest", Message: "bad"}
	s3cThrottled = &smithy.GenericAPIError{Code: "Throttling", Message: "slow down"}
)

var s3cCreated = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

func s3cARN(res string) string { return arnFor("aws", "s3", testRegion, testAccountID, res) }

// s3cWant is a row the fixture stores: its type, ARN, Name and whether its
// CreatedAt is s3cCreated (else unset).
type s3cWant struct {
	typ, arn, name string
	created        bool
}

// s3cRows is every row the fixture stores.
func s3cRows() []s3cWant {
	var rows []s3cWant
	for _, n := range []string{"t", "b"} {
		rows = append(rows,
			s3cWant{TypeS3AccessGrantsInstance, s3cARN("access-grants/default-" + n), "agi-" + n, true},
			s3cWant{TypeS3AccessGrantsLocation, s3cARN("access-grants/default/location/loc-" + n), "loc-" + n, true},
			s3cWant{TypeS3AccessGrant, s3cARN("access-grants/default/grant/grant-" + n), "grant-" + n, true},
			s3cWant{TypeS3AccessPoint, s3cARN("accesspoint/ap-" + n), "ap-" + n, false},
			s3cWant{TypeS3StorageLens, s3cARN("storage-lens/sl-" + n), "sl-" + n, false},
			s3cWant{TypeS3StorageLensGroup, s3cARN("storage-lens-group/slg-" + n), "slg-" + n, false},
		)
	}
	return rows
}

// s3cARNField is the list element field that holds each listed type's ARN.
var s3cARNField = map[string]string{
	TypeS3AccessGrantsInstance: "AccessGrantsInstanceArn", TypeS3AccessGrantsLocation: "AccessGrantsLocationArn",
	TypeS3AccessGrant: "AccessGrantArn", TypeS3AccessPoint: "AccessPointArn", TypeS3StorageLensGroup: "StorageLensGroupArn",
}

// s3cCalls records the stubbed calls and answers both tag ops from tags and
// errs, keyed by the ARN (ListTagsForResource) or config Id
// (GetStorageLensConfigurationTagging) they are sent. Calls run concurrently.
type s3cCalls struct {
	mu          sync.Mutex
	ops         map[string]int
	maxResults  map[string][]int32
	tagARNs     []string
	tagAccounts []string
	lensTagIDs  []string
	// lensAccounts is the AccountId of every GetStorageLensConfiguration
	// and GetStorageLensConfigurationTagging call.
	lensAccounts []string
	tags         map[string]map[string]string
	errs         map[string]error
}

func newS3CCalls() *s3cCalls {
	c := &s3cCalls{ops: map[string]int{}, maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, r := range s3cRows() {
		if r.name[len(r.name)-1] == 't' {
			key := r.arn
			if r.typ == TypeS3StorageLens {
				key = r.name
			}
			c.tags[key] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *s3cCalls) called(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if v := reflect.ValueOf(in).Elem().FieldByName("MaxResults"); v.IsValid() {
		c.maxResults[op] = append(c.maxResults[op], int32(v.Int()))
	}
}

func (c *s3cCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// tagged answers a tag read for key.
func (c *s3cCalls) tagged(key string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tags[key], c.errs[key]
}

// twoPages answers op with "t" on the first page and "b" on the second.
func (c *s3cCalls) twoPages(op string, out func(n string, next *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		if bacField[*string](in, "NextToken") == nil {
			return out("t", sdkaws.String("p2")), nil
		}
		return out("b", nil), nil
	}
}

// s3cHandlers answers every op scanS3Control calls with the fixture.
func s3cHandlers(c *s3cCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListAccessGrantsInstances": c.twoPages("ListAccessGrantsInstances", func(n string, next *string) any {
			return &s3control.ListAccessGrantsInstancesOutput{NextToken: next, AccessGrantsInstancesList: []s3ctypes.ListAccessGrantsInstanceEntry{{
				AccessGrantsInstanceArn: sdkaws.String(s3cARN("access-grants/default-" + n)), AccessGrantsInstanceId: sdkaws.String("agi-" + n), CreatedAt: &s3cCreated,
			}}}
		}),
		"ListAccessGrantsLocations": c.twoPages("ListAccessGrantsLocations", func(n string, next *string) any {
			return &s3control.ListAccessGrantsLocationsOutput{NextToken: next, AccessGrantsLocationsList: []s3ctypes.ListAccessGrantsLocationsEntry{{
				AccessGrantsLocationArn: sdkaws.String(s3cARN("access-grants/default/location/loc-" + n)), AccessGrantsLocationId: sdkaws.String("loc-" + n),
				CreatedAt: &s3cCreated, LocationScope: sdkaws.String("s3://bucket-" + n),
			}}}
		}),
		"ListAccessGrants": c.twoPages("ListAccessGrants", func(n string, next *string) any {
			return &s3control.ListAccessGrantsOutput{NextToken: next, AccessGrantsList: []s3ctypes.ListAccessGrantEntry{{
				AccessGrantArn: sdkaws.String(s3cARN("access-grants/default/grant/grant-" + n)), AccessGrantId: sdkaws.String("grant-" + n), CreatedAt: &s3cCreated,
			}}}
		}),
		"ListAccessPoints": c.twoPages("ListAccessPoints", func(n string, next *string) any {
			return &s3control.ListAccessPointsOutput{NextToken: next, AccessPointList: []s3ctypes.AccessPoint{{
				AccessPointArn: sdkaws.String(s3cARN("accesspoint/ap-" + n)), Name: sdkaws.String("ap-" + n), Bucket: sdkaws.String("bucket-" + n),
			}}}
		}),
		"ListStorageLensConfigurations": c.twoPages("ListStorageLensConfigurations", func(n string, next *string) any {
			return &s3control.ListStorageLensConfigurationsOutput{NextToken: next, StorageLensConfigurationList: []s3ctypes.ListStorageLensConfigurationEntry{{
				StorageLensArn: sdkaws.String(s3cARN("storage-lens/sl-" + n)), Id: sdkaws.String("sl-" + n), HomeRegion: sdkaws.String(testRegion),
			}}}
		}),
		"GetStorageLensConfiguration": func(in any) (any, error) {
			c.called("GetStorageLensConfiguration", in)
			get := in.(*s3control.GetStorageLensConfigurationInput)
			c.mu.Lock()
			c.lensAccounts = append(c.lensAccounts, sv(get.AccountId))
			c.mu.Unlock()
			id := get.ConfigId
			return &s3control.GetStorageLensConfigurationOutput{StorageLensConfiguration: &s3ctypes.StorageLensConfiguration{
				Id: id, IsEnabled: true, Include: &s3ctypes.Include{Buckets: []string{"arn:aws:s3:::lens-" + sv(id)}},
			}}, nil
		},
		"ListStorageLensGroups": c.twoPages("ListStorageLensGroups", func(n string, next *string) any {
			return &s3control.ListStorageLensGroupsOutput{NextToken: next, StorageLensGroupList: []s3ctypes.ListStorageLensGroupEntry{{
				StorageLensGroupArn: sdkaws.String(s3cARN("storage-lens-group/slg-" + n)), Name: sdkaws.String("slg-" + n), HomeRegion: sdkaws.String(testRegion),
			}}}
		}),
		"ListAccessPointsForObjectLambda": func(in any) (any, error) {
			c.called("ListAccessPointsForObjectLambda", in)
			return &s3control.ListAccessPointsForObjectLambdaOutput{}, nil
		},
		"ListMultiRegionAccessPoints": func(in any) (any, error) {
			c.called("ListMultiRegionAccessPoints", in)
			return &s3control.ListMultiRegionAccessPointsOutput{}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			lt := in.(*s3control.ListTagsForResourceInput)
			c.mu.Lock()
			c.tagARNs = append(c.tagARNs, sv(lt.ResourceArn))
			c.tagAccounts = append(c.tagAccounts, sv(lt.AccountId))
			c.mu.Unlock()
			tags, err := c.tagged(sv(lt.ResourceArn))
			if err != nil {
				return nil, err
			}
			out := &s3control.ListTagsForResourceOutput{}
			for k, v := range tags {
				out.Tags = append(out.Tags, s3ctypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
			}
			return out, nil
		},
		"GetStorageLensConfigurationTagging": func(in any) (any, error) {
			gt := in.(*s3control.GetStorageLensConfigurationTaggingInput)
			id := sv(gt.ConfigId)
			c.mu.Lock()
			c.lensTagIDs = append(c.lensTagIDs, id)
			c.lensAccounts = append(c.lensAccounts, sv(gt.AccountId))
			c.mu.Unlock()
			tags, err := c.tagged(id)
			if err != nil {
				return nil, err
			}
			out := &s3control.GetStorageLensConfigurationTaggingOutput{}
			for k, v := range tags {
				out.Tags = append(out.Tags, s3ctypes.StorageLensTag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
			}
			return out, nil
		},
	}
}

// s3cListOps is every regional listing the scan makes, in call order, with
// the rows each stores from the fixture.
var s3cListOps = []string{
	"ListAccessGrantsInstances", "ListAccessGrantsLocations", "ListAccessGrants", "ListAccessPoints",
	"ListStorageLensConfigurations", "ListStorageLensGroups",
}

// s3cScan runs the scan's real entry point in region against handlers; every
// op they do not answer is denied.
func s3cScan(t *testing.T, st *store.Store, region string, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, s3cDenied)
	return scanS3Control(context.Background(), acct, region, st, testScanID)
}

func s3cRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags its tag op returns, nil for a resource without
// tags; ListTagsForResource is sent every stored Access Grants, access point
// and Storage Lens group ARN with the account, and
// GetStorageLensConfiguration and GetStorageLensConfigurationTagging every
// configuration Id with the account. Both pages of
// every listing are read, the Access Grants and access point listings ask
// for the model's page cap of 1000, and the scan reports no error or
// warning. Each row keeps the base scan's Name, Region, CreatedAt and
// attributes: the list element, or for a Storage Lens configuration its Get
// body.
func TestScanS3Control_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newS3CCalls()

	total, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls))
	if err != nil || total != 12 {
		t.Fatalf("scan = %d, %v; want 12, nil", total, err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs, wantIDs []string
	for _, w := range s3cRows() {
		r, ok := s3cRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s not stored", w.typ, w.arn)
			continue
		}
		if w.typ == TypeS3StorageLens {
			wantIDs = append(wantIDs, w.name)
		} else {
			wantARNs = append(wantARNs, w.arn)
		}
		if sv(r.Name) != w.name || sv(r.Region) != testRegion || (sv(r.CreatedAt) == "2025-01-02T03:04:05Z") != w.created || (!w.created && r.CreatedAt != nil) {
			t.Errorf("%s Name, Region, CreatedAt = %q, %q, %v", w.arn, sv(r.Name), sv(r.Region), r.CreatedAt)
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs %s: %v", w.arn, r.AttributesJSON, err)
		}
		if w.typ == TypeS3StorageLens {
			if attrs["Id"] != w.name || attrs["IsEnabled"] != true || attrs["HomeRegion"] != nil {
				t.Errorf("%s attrs = %s; want the Get body", w.arn, r.AttributesJSON)
			}
		} else if attrs[s3cARNField[w.typ]] != w.arn {
			t.Errorf("%s attrs = %s; want the list element", w.arn, r.AttributesJSON)
		}
		if w.name[len(w.name)-1] == 'b' {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", w.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
	}
	slices.Sort(wantARNs)
	slices.Sort(wantIDs)
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}
	if got := slices.Compact(calls.tagAccounts); !slices.Equal(got, []string{testAccountID}) {
		t.Errorf("ListTagsForResource AccountIds = %v; want only %s", got, testAccountID)
	}
	if got := slices.Compact(slices.Clone(calls.lensAccounts)); len(calls.lensAccounts) != 4 || !slices.Equal(got, []string{testAccountID}) {
		t.Errorf("Storage Lens Get and tag read AccountIds = %v; want 4, each %s", calls.lensAccounts, testAccountID)
	}
	if got := slices.Sorted(slices.Values(calls.lensTagIDs)); !slices.Equal(got, wantIDs) {
		t.Errorf("GetStorageLensConfigurationTagging ConfigIds = %v; want %v", got, wantIDs)
	}
	for _, op := range s3cListOps {
		if n := calls.count(op); n != 2 {
			t.Errorf("%s calls = %d; want 2 (both pages)", op, n)
		}
	}
	if n := calls.count("ListAccessPointsForObjectLambda"); n != 1 {
		t.Errorf("ListAccessPointsForObjectLambda calls = %d; want 1", n)
	}
	for _, op := range []string{"ListAccessGrantsInstances", "ListAccessGrantsLocations", "ListAccessGrants", "ListAccessPoints"} {
		if got := calls.maxResults[op]; !slices.Equal(got, []int32{1000, 1000}) {
			t.Errorf("%s MaxResults = %v; want [1000 1000]", op, got)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs and the
// configuration Id its listings return to the tag ops unchanged and stores
// their tags.
func TestScanS3Control_GovCloudARNsReachTagOps(t *testing.T) {
	const govRegion = "us-gov-west-1"
	ap := arnFor("aws-us-gov", "s3", govRegion, testAccountID, "accesspoint/ap-gov")
	lens := arnFor("aws-us-gov", "s3", govRegion, testAccountID, "storage-lens/sl-gov")
	st := newTestStore(t)
	calls := newS3CCalls()
	calls.tags[ap] = map[string]string{"env": "gov"}
	calls.tags["sl-gov"] = map[string]string{"env": "gov-lens"}
	h := s3cEmptyHandlers(calls)
	h["ListAccessPoints"] = func(any) (any, error) {
		return &s3control.ListAccessPointsOutput{AccessPointList: []s3ctypes.AccessPoint{{AccessPointArn: &ap, Name: sdkaws.String("ap-gov")}}}, nil
	}
	h["ListStorageLensConfigurations"] = func(any) (any, error) {
		return &s3control.ListStorageLensConfigurationsOutput{StorageLensConfigurationList: []s3ctypes.ListStorageLensConfigurationEntry{{StorageLensArn: &lens, Id: sdkaws.String("sl-gov")}}}, nil
	}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, s3cDenied)

	total, _, err := scanS3Control(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	if !slices.Equal(calls.tagARNs, []string{ap}) || !slices.Equal(calls.lensTagIDs, []string{"sl-gov"}) {
		t.Errorf("tag reads = %v, %v; want [%s], [sl-gov]", calls.tagARNs, calls.lensTagIDs, ap)
	}
	for _, c := range []struct{ typ, arn, env string }{{TypeS3AccessPoint, ap, "gov"}, {TypeS3StorageLens, lens, "gov-lens"}} {
		r, ok := s3cRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, sv(r.Region), govRegion)
		}
	}
}

// s3cEmptyHandlers is s3cHandlers with every listing empty.
func s3cEmptyHandlers(c *s3cCalls) map[string]func(any) (any, error) {
	h := s3cHandlers(c)
	empty := map[string]any{
		"ListAccessGrantsInstances":     &s3control.ListAccessGrantsInstancesOutput{},
		"ListAccessGrantsLocations":     &s3control.ListAccessGrantsLocationsOutput{},
		"ListAccessGrants":              &s3control.ListAccessGrantsOutput{},
		"ListAccessPoints":              &s3control.ListAccessPointsOutput{},
		"ListStorageLensConfigurations": &s3control.ListStorageLensConfigurationsOutput{},
		"ListStorageLensGroups":         &s3control.ListStorageLensGroupsOutput{},
	}
	for op, out := range empty {
		h[op] = func(in any) (any, error) {
			c.called(op, in)
			return out, nil
		}
	}
	return h
}

// Every listing returning nothing stores nothing, reads no tags and reports
// nothing.
func TestScanS3Control_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newS3CCalls()

	total, inserted, err := s3cScan(t, st, testRegion, s3cEmptyHandlers(calls))
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs)+len(calls.lensTagIDs) != 0 || calls.count("GetStorageLensConfiguration") != 0 {
		t.Errorf("warnings, tag reads, lens Gets = %d, %d, %d; want 0, 0, 0", *warns, len(calls.tagARNs)+len(calls.lensTagIDs), calls.count("GetStorageLensConfiguration"))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanS3Control_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newS3CCalls()
	if _, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := s3cScan(t, st, testRegion, s3cHandlers(calls))
	if err != nil || total != 12 || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want 12, 0, nil", total, inserted, err)
	}
}

// A failed tag read keeps the row without tags: a denial warns once under
// the tag op, a throttle after retries is a transient error, any other code a
// hard one. Neither tag op models an error code, so no code drops the row.
// The same holds for both tag ops.
func TestScanS3Control_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn, key, op string }{
		{TypeS3AccessPoint, s3cARN("accesspoint/ap-t"), s3cARN("accesspoint/ap-t"), s3controlTagsOp},
		{TypeS3StorageLens, s3cARN("storage-lens/sl-t"), "sl-t", s3LensTagsOp},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", s3cDenied, 0, 1},
				{"other error", s3cBroken, outcomeError, 0},
				{"transient", s3cThrottled, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.op)
					calls := newS3CCalls()
					calls.errs[target.key] = c.err

					total, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.err.(smithy.APIError).ErrorCode())
					}
					r, ok := s3cRowOf(t, st, target.typ, target.arn)
					if !ok || r.TagsJSON != nil {
						t.Fatalf("row stored = %v, TagsJSON = %v; want kept without tags", ok, r.TagsJSON)
					}
					if total != 12 {
						t.Errorf("total = %d; want 12", total)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.op, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied ListTagsForResource reads in several listings are one warning for
// the scan.
func TestScanS3Control_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, s3controlTagsOp)
	calls := newS3CCalls()
	calls.errs[s3cARN("access-grants/default/location/loc-t")] = s3cDenied
	calls.errs[s3cARN("storage-lens-group/slg-b")] = s3cDenied

	if _, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", s3controlTagsOp, *warns)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanS3Control_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, s3controlTagsOp)
	calls := newS3CCalls()
	calls.errs[s3cARN("accesspoint/ap-t")] = s3cThrottled
	calls.errs[s3cARN("access-grants/default/grant/grant-b")] = s3cBroken

	_, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequest")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", s3controlTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other listing
// its rows.
func TestScanS3Control_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range s3cListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "s3control:"+op)
			calls := newS3CCalls()
			h := s3cHandlers(calls)
			h[op] = func(any) (any, error) { return nil, s3cDenied }

			total, _, err := s3cScan(t, st, testRegion, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total != 10 || stored != 10 {
				t.Errorf("total = %d, stored rows = %d; want 10", total, stored)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanS3Control_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeS3AccessGrantsLocation)
	calls := newS3CCalls()
	calls.errs[s3cARN("access-grants/default/location/loc-t")] = s3cDenied

	_, _, err := s3cScan(t, st, testRegion, s3cHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListAccessGrants", "ListAccessPoints", "ListStorageLensGroups"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, a repeated token
// ends a listing with errRepeatedNextToken keeping its pages, and no
// listing's failure stops the listings after it.
func TestScanS3Control_ListFailureKeepsPagesAndRunsEveryListing(t *testing.T) {
	st := newTestStore(t)
	calls := newS3CCalls()
	h := s3cHandlers(calls)
	h["ListAccessGrantsInstances"] = func(any) (any, error) { return nil, s3cBroken }
	h["ListAccessPoints"] = failOnCall(2, s3cBroken, h["ListAccessPoints"])
	groups := h["ListStorageLensGroups"]
	h["ListStorageLensGroups"] = func(in any) (any, error) {
		out, err := groups(in)
		out.(*s3control.ListStorageLensGroupsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := s3cScan(t, st, testRegion, h)
	assertGlueOutcome(t, err, outcomeError)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken among its errors", err)
	}
	// 12 rows, less both instances and the second access point.
	if stored := countStoredRows(t, st); total != 9 || stored != 9 {
		t.Errorf("total = %d, stored rows = %d; want 9", total, stored)
	}
	if _, ok := s3cRowOf(t, st, TypeS3AccessPoint, s3cARN("accesspoint/ap-t")); !ok {
		t.Error("the first access point page was not kept")
	}
	if n := calls.count("ListStorageLensGroups"); n != 2 {
		t.Errorf("ListStorageLensGroups calls = %d; want 2 (stop when the token repeats)", n)
	}
}

// A region without an Access Grants instance or outside Storage Lens's home
// Regions has none of their resources: no row, no warning, no error. A
// Storage Lens listing answered with a bare 403 is warned about as a denial.
func TestScanS3Control_RegionGaps(t *testing.T) {
	homeGap := &smithy.GenericAPIError{Code: "AccessDenied", Message: "Region is not supported as home region for S3 Storage Lens"}
	noInstance := &smithy.GenericAPIError{Code: "AccessGrantsInstanceNotExistsError", Message: "no instance"}
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newS3CCalls()
	h := s3cHandlers(calls)
	h["ListAccessGrantsLocations"] = func(any) (any, error) { return nil, noInstance }
	h["ListAccessGrants"] = func(any) (any, error) { return nil, noInstance }
	h["ListStorageLensConfigurations"] = func(any) (any, error) { return nil, homeGap }
	h["ListStorageLensGroups"] = func(any) (any, error) { return nil, homeGap }

	total, _, err := s3cScan(t, st, testRegion, h)
	if err != nil || total != 4 || *warns != 0 {
		t.Fatalf("scan = %d, %v, %d warnings; want 4 (instances, access points), nil, 0", total, err, *warns)
	}
	if n := calls.count("GetStorageLensConfiguration") + len(calls.lensTagIDs); n != 0 {
		t.Errorf("Storage Lens configuration Gets and tag reads = %d; want 0", n)
	}

	st = newTestStore(t)
	lensWarns := countOpWarnings(st, "s3control:ListStorageLensConfigurations")
	h["ListStorageLensConfigurations"] = func(any) (any, error) { return nil, s3Answer(http.StatusForbidden, http.Header{}, nil) }
	total, _, err = s3cScan(t, st, testRegion, h)
	if err != nil || total != 4 || *lensWarns != 1 {
		t.Errorf("bare 403: scan = %d, %v, %d lens warnings; want 4, nil, 1", total, err, *lensWarns)
	}
}

// A Storage Lens configuration listing whose page after the first answers
// the home-Region gap keeps the first page's configuration and warns once
// under the listing that it stopped: the op served this Region a moment ago.
func TestScanS3Control_StorageLensHomeRegionGapAfterFirstPage(t *testing.T) {
	homeGap := &smithy.GenericAPIError{Code: "AccessDenied", Message: "Region is not supported as home region for S3 Storage Lens"}
	st := newTestStore(t)
	warns := countWarnings(st)
	lensWarns := countOpWarnings(st, "s3control:ListStorageLensConfigurations")
	calls := newS3CCalls()
	h := s3cHandlers(calls)
	h["ListStorageLensConfigurations"] = failOnCall(2, homeGap, h["ListStorageLensConfigurations"])

	total, _, err := s3cScan(t, st, testRegion, h)
	if err != nil || total != 11 {
		t.Fatalf("scan = %d, %v; want 11 (sl-b on the second page unread), nil", total, err)
	}
	if *warns != 1 || *lensWarns != 1 {
		t.Errorf("warnings = %d (s3control:ListStorageLensConfigurations %d); want 1 under the listing", *warns, *lensWarns)
	}
	if _, ok := s3cRowOf(t, st, TypeS3StorageLens, s3cARN("storage-lens/sl-t")); !ok {
		t.Error("sl-t from the first page was not kept")
	}
}

// A Storage Lens configuration whose Get fails keeps its list entry as the
// row, with its tags: a denial warns once under the Get, any other failure
// is returned.
func TestScanS3Control_StorageLensGetFailureKeepsListEntry(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{"denied", s3cDenied, 1},
		{"other error", s3cBroken, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			getWarns := countOpWarnings(st, "s3control:GetStorageLensConfiguration")
			calls := newS3CCalls()
			h := s3cHandlers(calls)
			get := h["GetStorageLensConfiguration"]
			h["GetStorageLensConfiguration"] = func(in any) (any, error) {
				if sv(in.(*s3control.GetStorageLensConfigurationInput).ConfigId) == "sl-t" {
					return nil, c.err
				}
				return get(in)
			}

			total, _, err := s3cScan(t, st, testRegion, h)
			if c.wantWarns == 0 {
				assertAPICode(t, err, "InvalidRequest")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			r, ok := s3cRowOf(t, st, TypeS3StorageLens, s3cARN("storage-lens/sl-t"))
			if !ok || total != 12 {
				t.Fatalf("sl-t stored = %v, total = %d; want kept, 12", ok, total)
			}
			var entry s3ctypes.ListStorageLensConfigurationEntry
			if err := json.Unmarshal([]byte(r.AttributesJSON), &entry); err != nil || sv(entry.HomeRegion) != testRegion {
				t.Errorf("sl-t attrs = %s (%v); want its list entry", r.AttributesJSON, err)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeS3StorageLens})
			if *getWarns != c.wantWarns {
				t.Errorf("GetStorageLensConfiguration warnings = %d; want %d", *getWarns, c.wantWarns)
			}
		})
	}
}

// A Get that answers without a configuration body keeps the list entry as
// the row rather than storing null attributes.
func TestScanS3Control_StorageLensGetWithoutBodyKeepsListEntry(t *testing.T) {
	st := newTestStore(t)
	calls := newS3CCalls()
	h := s3cHandlers(calls)
	h["GetStorageLensConfiguration"] = func(any) (any, error) { return &s3control.GetStorageLensConfigurationOutput{}, nil }

	if _, _, err := s3cScan(t, st, testRegion, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	r, ok := s3cRowOf(t, st, TypeS3StorageLens, s3cARN("storage-lens/sl-b"))
	var entry s3ctypes.ListStorageLensConfigurationEntry
	if !ok || json.Unmarshal([]byte(r.AttributesJSON), &entry) != nil || sv(entry.StorageLensArn) != r.NativeID {
		t.Errorf("sl-b stored = %v, attrs = %s; want its list entry", ok, r.AttributesJSON)
	}
}

// A listed item without an ARN is warned about once under its listing rather
// than stored under an empty NativeID, and has no tags read.
func TestScanS3Control_ItemWithoutARNWarnedNotStored(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	apWarns := countOpWarnings(st, "s3control:ListAccessPoints")
	calls := newS3CCalls()
	h := s3cHandlers(calls)
	h["ListAccessPoints"] = func(any) (any, error) {
		return &s3control.ListAccessPointsOutput{AccessPointList: []s3ctypes.AccessPoint{{Name: sdkaws.String("ap-noarn")}}}, nil
	}

	total, _, err := s3cScan(t, st, testRegion, h)
	if err != nil || total != 10 {
		t.Fatalf("scan = %d, %v; want 10, nil", total, err)
	}
	if *warns != 1 || *apWarns != 1 {
		t.Errorf("warnings = %d (s3control:ListAccessPoints %d); want 1 under the listing", *warns, *apWarns)
	}
	if slices.Contains(calls.tagARNs, "") {
		t.Errorf("ListTagsForResource ARNs = %v; want no empty ARN", calls.tagARNs)
	}
}

// A policy read that stays throttled beside a hard error elsewhere is
// demoted to a warning under its own op, not its listing's, and the access
// point listed before it is stored.
func TestScanS3Control_PolicyThrottleWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct{ op, listType string }{
		{"GetMultiRegionAccessPointPolicy", TypeS3MultiRegionAccessPoint},
		{"GetAccessPointPolicyForObjectLambda", TypeS3ObjectLambdaAccessPoint},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var (
				mu       sync.Mutex
				warnings []store.ScanWarning
			)
			st.OnWarn = func(w store.ScanWarning) { mu.Lock(); warnings = append(warnings, w); mu.Unlock() }
			h := s3cEmptyHandlers(newS3CCalls())
			h["ListAccessPoints"] = func(any) (any, error) { return nil, s3cBroken }
			h["ListMultiRegionAccessPoints"] = func(any) (any, error) {
				return &s3control.ListMultiRegionAccessPointsOutput{AccessPoints: []s3ctypes.MultiRegionAccessPointReport{{Name: sdkaws.String("mrap")}}}, nil
			}
			h["ListAccessPointsForObjectLambda"] = func(any) (any, error) {
				return &s3control.ListAccessPointsForObjectLambdaOutput{ObjectLambdaAccessPointList: []s3ctypes.ObjectLambdaAccessPoint{{
					Name: sdkaws.String("olap"), ObjectLambdaAccessPointArn: sdkaws.String(arnFor("aws", "s3-object-lambda", "us-west-2", testAccountID, "accesspoint/olap")),
				}}}, nil
			}
			h["GetMultiRegionAccessPointPolicy"] = func(any) (any, error) { return &s3control.GetMultiRegionAccessPointPolicyOutput{}, nil }
			h["GetAccessPointPolicyForObjectLambda"] = func(any) (any, error) { return &s3control.GetAccessPointPolicyForObjectLambdaOutput{}, nil }
			h[c.op] = func(any) (any, error) { return nil, s3cThrottled }

			_, _, err := s3cScan(t, st, "us-west-2", h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequest")
			if len(warnings) != 1 || warnings[0].Service != "s3control:"+c.op {
				t.Errorf("warnings = %v; want one under s3control:%s", warnings, c.op)
			}
			if n := len(rowsByNativeID(t, st, c.listType)); n != 1 {
				t.Errorf("%s rows = %d; want 1", c.listType, n)
			}
		})
	}
}

// A failed Object Lambda policy read returns its error without dropping the
// policy read for the other access point, stored as "<access point ARN>/policy"
// with the access point's name, the scan region and the Get body.
func TestScanS3Control_ObjectLambdaPolicyFailureKeepsOtherPolicies(t *testing.T) {
	st := newTestStore(t)
	olapARN := func(name string) string {
		return arnFor("aws", "s3-object-lambda", testRegion, testAccountID, "accesspoint/"+name)
	}
	h := s3cEmptyHandlers(newS3CCalls())
	h["ListAccessPointsForObjectLambda"] = func(any) (any, error) {
		return &s3control.ListAccessPointsForObjectLambdaOutput{ObjectLambdaAccessPointList: []s3ctypes.ObjectLambdaAccessPoint{
			{Name: sdkaws.String("olap-a"), ObjectLambdaAccessPointArn: sdkaws.String(olapARN("olap-a"))},
			{Name: sdkaws.String("olap-b"), ObjectLambdaAccessPointArn: sdkaws.String(olapARN("olap-b"))},
		}}, nil
	}
	h["GetAccessPointPolicyForObjectLambda"] = func(in any) (any, error) {
		if sv(in.(*s3control.GetAccessPointPolicyForObjectLambdaInput).Name) == "olap-a" {
			return nil, s3cBroken
		}
		return &s3control.GetAccessPointPolicyForObjectLambdaOutput{Policy: sdkaws.String("{}")}, nil
	}

	_, _, err := s3cScan(t, st, testRegion, h)
	assertAPICode(t, err, "InvalidRequest")
	aps := rowsByNativeID(t, st, TypeS3ObjectLambdaAccessPoint)
	_, okA := aps[olapARN("olap-a")]
	_, okB := aps[olapARN("olap-b")]
	if !okA || !okB || len(aps) != 2 {
		t.Errorf("access point rows = %v; want olap-a and olap-b", slices.Sorted(maps.Keys(aps)))
	}
	policies := rowsByNativeID(t, st, TypeS3ObjectLambdaAccessPointPolicy)
	want := olapARN("olap-b") + "/policy"
	r, ok := policies[want]
	if !ok || len(policies) != 1 {
		t.Fatalf("policy rows = %v; want only %s", slices.Sorted(maps.Keys(policies)), want)
	}
	if sv(r.Name) != "olap-b" || sv(r.Region) != testRegion {
		t.Errorf("policy Name, Region = %q, %q; want olap-b, %s", sv(r.Name), sv(r.Region), testRegion)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("policy attrs %s: %v", r.AttributesJSON, err)
	}
	if !reflect.DeepEqual(attrs, map[string]any{"Policy": "{}"}) {
		t.Errorf("policy attrs = %s; want the Get body", r.AttributesJSON)
	}
}

// Multi-Region Access Points are listed in us-west-2 only.
func TestScanS3Control_MultiRegionAccessPointsOnlyInUSWest2(t *testing.T) {
	for region, want := range map[string]int{"us-west-2": 1, testRegion: 0} {
		calls := newS3CCalls()
		if _, _, err := s3cScan(t, newTestStore(t), region, s3cEmptyHandlers(calls)); err != nil {
			t.Fatalf("%s: %v", region, err)
		}
		if n := calls.count("ListMultiRegionAccessPoints"); n != want {
			t.Errorf("%s: ListMultiRegionAccessPoints calls = %d; want %d", region, n, want)
		}
	}
}
