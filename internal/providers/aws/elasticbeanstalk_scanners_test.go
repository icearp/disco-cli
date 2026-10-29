package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
	"github.com/icearp/disco-cli/store"
)

// ebFixtureTotal is the rows ebHandlers' scan stores: 3 applications, 1
// environment, 1 version, 1 platform and 3 templates.
const ebFixtureTotal = 9

var (
	ebThrottle    = apiErr("ThrottlingException", "Rate exceeded")
	ebUnsupported = apiErr("ResourceTypeNotSupportedException", "not supported")
)

// ebAllRowARNs is the NativeID of every row ebHandlers' scan stores, sorted.
func ebAllRowARNs() []string {
	arns := []string{ebTestAppWeb, ebTestAppWorker, ebTestAppBare, ebTestEnv, ebTestVersion, ebTestPlatform, ebTestTplBase, ebTestTplProd, ebTestTplWorker1}
	slices.Sort(arns)
	return arns
}

// ebTagged is, per type, the row whose resource ebTagCalls tags with
// {"type": <type>}; every other row's resource has no tags.
var ebTagged = map[string]string{
	TypeBeanstalkApplication:           ebTestAppWeb,
	TypeBeanstalkEnvironment:           ebTestEnv,
	TypeBeanstalkApplicationVersion:    ebTestVersion,
	TypeBeanstalkPlatform:              ebTestPlatform,
	TypeBeanstalkConfigurationTemplate: ebTestTplBase,
}

// ebTagCalls answers ListTagsForResource with tags, or errs, by ARN and
// records every ARN sent, and counts every call per op.
type ebTagCalls struct {
	mu   sync.Mutex
	arns []string
	tags map[string]map[string]string
	errs map[string]error
	ops  map[string]int
	ins  map[string][]any
}

func newEBTagCalls() *ebTagCalls {
	c := &ebTagCalls{tags: map[string]map[string]string{}, errs: map[string]error{}, ops: map[string]int{}, ins: map[string][]any{}}
	for typ, arn := range ebTagged {
		c.tags[arn] = map[string]string{"type": typ}
	}
	return c
}

func (c *ebTagCalls) listTags(in any) (any, error) {
	arn := sv(in.(*elasticbeanstalk.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.arns = append(c.arns, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []ebtypes.Tag
	for k, v := range tags {
		list = append(list, ebtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &elasticbeanstalk.ListTagsForResourceOutput{ResourceArn: &arn, ResourceTags: list}, nil
}

// sent is the ARNs sent to ListTagsForResource, sorted.
func (c *ebTagCalls) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.arns))
}

func (c *ebTagCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// handlers is ebHandlers with ListTagsForResource answered by c and every
// op's calls counted and their inputs kept.
func (c *ebTagCalls) handlers() map[string]func(in any) (any, error) {
	var templates parentCalls
	h := ebHandlers(&templates)
	h["ListTagsForResource"] = c.listTags
	for op, next := range h {
		h[op] = func(in any) (any, error) {
			c.mu.Lock()
			c.ops[op]++
			c.ins[op] = append(c.ins[op], in)
			c.mu.Unlock()
			return next(in)
		}
	}
	return h
}

func ebScan(st *store.Store, h map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, ebDenied)
	return scanElasticBeanstalk(context.Background(), acct, testRegion, st, testScanID)
}

// Every row of every type has its tags read at its NativeID, and exactly
// those ARNs are sent; a resource without tags stores nil. Each row builder
// keeps its Name, Status, CreatedAt and the listed element. The environment
// and version listings are sent MaxRecords 1000; the platform listing is
// sent none, and filters on PlatformOwner = self.
func TestScanElasticBeanstalk_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	tagWarns := bacWarnings(st, beanstalkTagsOp)
	c := newEBTagCalls()

	total, inserted, err := ebScan(st, c.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ebFixtureTotal || inserted != ebFixtureTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ebFixtureTotal, ebFixtureTotal)
	}
	if len(*tagWarns) != 0 {
		t.Errorf("tag warnings = %v; want none", *tagWarns)
	}
	if got := c.sent(); !slices.Equal(got, ebAllRowARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, ebAllRowARNs())
	}
	for typ, arn := range ebTagged {
		assertTagsMap(t, rowsByNativeID(t, st, typ)[arn].TagsJSON, map[string]string{"type": typ})
	}
	for arn, typ := range map[string]string{
		ebTestAppWorker: TypeBeanstalkApplication, ebTestAppBare: TypeBeanstalkApplication,
		ebTestTplProd: TypeBeanstalkConfigurationTemplate, ebTestTplWorker1: TypeBeanstalkConfigurationTemplate,
	} {
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want stored with nil", arn, ok, r.TagsJSON)
		}
	}
	for _, w := range []struct {
		typ, arn, name, status, created, attr string
	}{
		{TypeBeanstalkApplication, ebTestAppWeb, "web", "", "", `"ApplicationArn":"` + ebTestAppWeb + `"`},
		{TypeBeanstalkEnvironment, ebTestEnv, "web-prod", "Ready", "", `"EnvironmentArn":"` + ebTestEnv + `"`},
		{TypeBeanstalkApplicationVersion, ebTestVersion, "v1", "Processed", "2026-01-02T03:04:05Z", `"ApplicationVersionArn":"` + ebTestVersion + `"`},
		{TypeBeanstalkPlatform, ebTestPlatform, "", "Ready", "", `"PlatformArn":"` + ebTestPlatform + `"`},
	} {
		r := rowsByNativeID(t, st, w.typ)[w.arn]
		if sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.CreatedAt) != w.created || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, w.attr) {
			t.Errorf("%s Name, Status, CreatedAt, Region = %q, %q, %q, %q, attrs %s; want %q, %q, %q, %q, the listed element",
				w.arn, sv(r.Name), sv(r.Status), sv(r.CreatedAt), sv(r.Region), r.AttributesJSON, w.name, w.status, w.created, testRegion)
		}
	}
	if r := rowsByNativeID(t, st, TypeBeanstalkApplication)[ebTestAppWeb]; r.Status != nil || r.CreatedAt != nil {
		t.Errorf("application Status, CreatedAt = %v, %v; want nil, nil", r.Status, r.CreatedAt)
	}
	if r := rowsByNativeID(t, st, TypeBeanstalkPlatform)[ebTestPlatform]; r.Name != nil {
		t.Errorf("platform Name = %q; want nil", *r.Name)
	}
	for _, op := range []string{"DescribeEnvironments", "DescribeApplicationVersions"} {
		if n := bacField[*int32](c.ins[op][0], "MaxRecords"); n == nil || *n != 1000 {
			t.Errorf("%s MaxRecords = %v; want 1000", op, n)
		}
	}
	in := c.ins["ListPlatformVersions"][0].(*elasticbeanstalk.ListPlatformVersionsInput)
	if in.MaxRecords != nil || len(in.Filters) != 1 || sv(in.Filters[0].Type) != "PlatformOwner" || sv(in.Filters[0].Operator) != "=" || !slices.Equal(in.Filters[0].Values, []string{"self"}) {
		t.Errorf("ListPlatformVersions input = %+v; want only the PlatformOwner = self filter", in)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanElasticBeanstalk_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := ebScan(st, newEBTagCalls().handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := ebScan(st, newEBTagCalls().handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != ebFixtureTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, ebFixtureTotal)
	}
}

// Empty listings store nothing, read no tags and describe no template.
func TestScanElasticBeanstalk_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	c := newEBTagCalls()
	h := c.handlers()
	h["DescribeApplications"] = func(any) (any, error) { return &elasticbeanstalk.DescribeApplicationsOutput{}, nil }
	h["DescribeEnvironments"] = func(any) (any, error) { return &elasticbeanstalk.DescribeEnvironmentsOutput{}, nil }
	h["DescribeApplicationVersions"] = func(any) (any, error) { return &elasticbeanstalk.DescribeApplicationVersionsOutput{}, nil }
	h["ListPlatformVersions"] = func(any) (any, error) { return &elasticbeanstalk.ListPlatformVersionsOutput{}, nil }

	total, _, err := ebScan(st, h)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := c.count("ListTagsForResource") + c.count("DescribeConfigurationSettings"); n != 0 {
		t.Errorf("tag reads + template describes = %d; want 0", n)
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently (a
// dropped application's templates are not described); a denial, the
// modeled InsufficientPrivilegesException included, keeps the row untagged
// and warns once under the tag op naming it; a throttle keeps it and is
// transient; any other error keeps it and is an error.
func TestScanElasticBeanstalk_TagReadFailures(t *testing.T) {
	const noErr serviceOutcome = -1
	type tagCase struct {
		name      string
		typ, arn  string
		err       error
		outcome   serviceOutcome
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for typ, arn := range ebTagged {
		gone := ebFixtureTotal - 1
		if typ == TypeBeanstalkApplication {
			gone -= 2 // web's templates base and prod/v2
		}
		cases = append(cases,
			tagCase{typ + "/not found", typ, arn, apiErr("ResourceNotFoundException", "gone"), noErr, "", gone, false, 0},
			tagCase{typ + "/denied", typ, arn, ebDenied, noErr, "", ebFixtureTotal, true, 1},
			tagCase{typ + "/insufficient privileges", typ, arn, apiErr("InsufficientPrivilegesException", "no"), noErr, "", ebFixtureTotal, true, 1},
			tagCase{typ + "/transient", typ, arn, ebThrottle, outcomeTransient, "ThrottlingException", ebFixtureTotal, true, 0},
			tagCase{typ + "/other error", typ, arn, ebUnsupported, outcomeError, "ResourceTypeNotSupportedException", ebFixtureTotal, true, 0},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, beanstalkTagsOp)
			c := newEBTagCalls()
			c.errs[tc.arn] = tc.err

			total, _, err := ebScan(st, c.handlers())
			if tc.outcome == noErr {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, tc.outcome)
				assertAPICode(t, err, tc.code)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d", total, tc.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, tc.typ)[tc.arn]
			if ok != tc.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, tc.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if len(*warns) != tc.wantWarns {
				t.Fatalf("tag warnings = %v; want %d", *warns, tc.wantWarns)
			}
			for _, w := range *warns {
				if w.Scope != testAccountID+"/"+testRegion || !strings.Contains(w.Message, tc.arn) {
					t.Errorf("warning %+v; want one in %s/%s naming %s", w, testAccountID, testRegion, tc.arn)
				}
			}
		})
	}
}

// Denied tag reads in three phases send one warning for the whole scan,
// naming the first denied row; the rows are kept untagged.
func TestScanElasticBeanstalk_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, beanstalkTagsOp)
	c := newEBTagCalls()
	denied := map[string]string{ebTestEnv: TypeBeanstalkEnvironment, ebTestVersion: TypeBeanstalkApplicationVersion, ebTestTplBase: TypeBeanstalkConfigurationTemplate}
	for arn := range denied {
		c.errs[arn] = ebDenied
	}

	total, _, err := ebScan(st, c.handlers())
	if err != nil || total != ebFixtureTotal {
		t.Fatalf("total, err = %d, %v; want %d, nil", total, err, ebFixtureTotal)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, ebTestEnv) {
		t.Fatalf("tag warnings = %v; want 1 naming %s", *warns, ebTestEnv)
	}
	for arn, typ := range denied {
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", arn, ok)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanElasticBeanstalk_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, beanstalkTagsOp)
	c := newEBTagCalls()
	c.errs[ebTestEnv] = ebThrottle
	c.errs[ebTestVersion] = ebUnsupported

	_, _, err := ebScan(st, c.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ResourceTypeNotSupportedException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", beanstalkTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the applications stored before it stay.
func TestScanElasticBeanstalk_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBeanstalkEnvironment)
	c := newEBTagCalls()
	c.errs[ebTestEnv] = ebDenied

	total, _, err := ebScan(st, c.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"DescribeApplicationVersions", "ListPlatformVersions", "DescribeConfigurationSettings"} {
		if n := c.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	if total != 3 {
		t.Errorf("total = %d; want 3, the applications", total)
	}
}

// ebPages builds each paged listing's output holding its fixture element
// under next.
var ebPages = map[string]func(next *string) any{
	"DescribeEnvironments": func(next *string) any {
		return &elasticbeanstalk.DescribeEnvironmentsOutput{NextToken: next, Environments: []ebtypes.EnvironmentDescription{
			{EnvironmentArn: sdkaws.String(ebTestEnv), EnvironmentName: sdkaws.String("web-prod")},
		}}
	},
	"DescribeApplicationVersions": func(next *string) any {
		return &elasticbeanstalk.DescribeApplicationVersionsOutput{NextToken: next, ApplicationVersions: []ebtypes.ApplicationVersionDescription{
			{ApplicationVersionArn: sdkaws.String(ebTestVersion), VersionLabel: sdkaws.String("v1")},
		}}
	},
	"ListPlatformVersions": func(next *string) any {
		return &elasticbeanstalk.ListPlatformVersionsOutput{NextToken: next, PlatformSummaryList: []ebtypes.PlatformSummary{
			{PlatformArn: sdkaws.String(ebTestPlatform)},
		}}
	},
}

// ebPagedTypes is the type each paged listing stores.
var ebPagedTypes = map[string]string{
	"DescribeEnvironments":        TypeBeanstalkEnvironment,
	"DescribeApplicationVersions": TypeBeanstalkApplicationVersion,
	"ListPlatformVersions":        TypeBeanstalkPlatform,
}

// A listing's failed second page keeps the first page, tagged, and its
// error does not stop the later phases.
func TestScanElasticBeanstalk_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for op, page := range ebPages {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			c := newEBTagCalls()
			h := c.handlers()
			h[op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, ebBadInput
				}
				return page(sdkaws.String("t2")), nil
			}
			total, _, err := ebScan(st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "TooManyBucketsException")
			if total != ebFixtureTotal {
				t.Errorf("total = %d; want %d", total, ebFixtureTotal)
			}
			typ := ebPagedTypes[op]
			assertTagsMap(t, rowsByNativeID(t, st, typ)[ebTagged[typ]].TagsJSON, map[string]string{"type": typ})
			if got := nativeIDs(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); !slices.Equal(got, ebAllTemplates) {
				t.Errorf("templates (the last phase) = %v; want %v", got, ebAllTemplates)
			}
		})
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanElasticBeanstalk_RepeatedTokenKeepsPages(t *testing.T) {
	for op, page := range ebPages {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			h := newEBTagCalls().handlers()
			h[op] = func(any) (any, error) { return page(sdkaws.String("same")), nil }
			total, _, err := ebScan(st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != ebFixtureTotal+1 {
				t.Errorf("total = %d; want %d (the element counted once per page)", total, ebFixtureTotal+1)
			}
			if _, ok := rowsByNativeID(t, st, ebPagedTypes[op])[ebTagged[ebPagedTypes[op]]]; !ok {
				t.Error("the paged element is missing")
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none; its siblings are stored.
func TestScanElasticBeanstalk_ElementWithoutARNWarns(t *testing.T) {
	for _, tc := range []struct {
		op, typ, key string
		answer       any
		// wantTotal and wantRows: the scan's total and the rows of typ.
		wantTotal, wantRows int
	}{
		// Only bare is stored: web has no ARN, worker is not listed, and
		// with them go their three templates.
		{"DescribeApplications", TypeBeanstalkApplication, "web", &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
			{ApplicationName: sdkaws.String("web"), ConfigurationTemplates: []string{"base"}},
			{ApplicationArn: sdkaws.String(ebTestAppBare), ApplicationName: sdkaws.String("bare")},
		}}, ebFixtureTotal - 5, 1},
		{"DescribeEnvironments", TypeBeanstalkEnvironment, "web-prod", &elasticbeanstalk.DescribeEnvironmentsOutput{Environments: []ebtypes.EnvironmentDescription{
			{EnvironmentName: sdkaws.String("web-prod")},
		}}, ebFixtureTotal - 1, 0},
		{"DescribeApplicationVersions", TypeBeanstalkApplicationVersion, "v1", &elasticbeanstalk.DescribeApplicationVersionsOutput{ApplicationVersions: []ebtypes.ApplicationVersionDescription{
			{VersionLabel: sdkaws.String("v1")},
		}}, ebFixtureTotal - 1, 0},
		{"ListPlatformVersions", TypeBeanstalkPlatform, noIdentifier, &elasticbeanstalk.ListPlatformVersionsOutput{PlatformSummaryList: []ebtypes.PlatformSummary{
			{PlatformStatus: ebtypes.PlatformStatusReady},
		}}, ebFixtureTotal - 1, 0},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, "elasticbeanstalk:"+tc.op)
			c := newEBTagCalls()
			h := c.handlers()
			h[tc.op] = func(any) (any, error) { return tc.answer, nil }

			total, _, err := ebScan(st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d", total, tc.wantTotal)
			}
			if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, tc.key) {
				t.Fatalf("warnings = %v; want 1 naming %q", *warns, tc.key)
			}
			if slices.Contains(c.sent(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if n := len(rowsByNativeID(t, st, tc.typ)); n != tc.wantRows {
				t.Errorf("%s rows = %d; want %d", tc.typ, n, tc.wantRows)
			}
		})
	}
}

// An application stored without a name is warned about once, as stored
// without its templates, and its templates are not described.
func TestScanElasticBeanstalk_ApplicationWithoutNameWarns(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, "elasticbeanstalk:DescribeApplications")
	c := newEBTagCalls()
	h := c.handlers()
	h["DescribeApplications"] = func(any) (any, error) {
		return &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
			{ApplicationArn: sdkaws.String(ebTestAppWeb), ConfigurationTemplates: []string{"base"}},
		}}, nil
	}
	if _, _, err := ebScan(st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeBeanstalkApplication)[ebTestAppWeb]; !ok {
		t.Error("the nameless application was not stored")
	}
	if n := c.count("DescribeConfigurationSettings"); n != 0 {
		t.Errorf("DescribeConfigurationSettings calls = %d; want 0", n)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, ebTestAppWeb) {
		t.Errorf("warnings = %v; want 1 naming %s", *warns, ebTestAppWeb)
	}
}

// A terminated environment and a deleted platform version are stored
// untagged and never sent to the tag op; their live siblings are read.
func TestScanElasticBeanstalk_DeletedRowsNotRead(t *testing.T) {
	st := newTestStore(t)
	c := newEBTagCalls()
	h := c.handlers()
	oldEnv := "arn:aws:elasticbeanstalk:us-east-1:123456789012:environment/web/web-old"
	oldPlatform := "arn:aws:elasticbeanstalk:us-east-1:123456789012:platform/custom/0.9.0"
	h["DescribeEnvironments"] = func(any) (any, error) {
		return &elasticbeanstalk.DescribeEnvironmentsOutput{Environments: []ebtypes.EnvironmentDescription{
			{EnvironmentArn: sdkaws.String(ebTestEnv), EnvironmentName: sdkaws.String("web-prod"), Status: ebtypes.EnvironmentStatusTerminating},
			{EnvironmentArn: sdkaws.String(oldEnv), EnvironmentName: sdkaws.String("web-old"), Status: ebtypes.EnvironmentStatusTerminated},
		}}, nil
	}
	h["ListPlatformVersions"] = func(any) (any, error) {
		return &elasticbeanstalk.ListPlatformVersionsOutput{PlatformSummaryList: []ebtypes.PlatformSummary{
			{PlatformArn: sdkaws.String(ebTestPlatform), PlatformStatus: ebtypes.PlatformStatusDeleting},
			{PlatformArn: sdkaws.String(oldPlatform), PlatformStatus: ebtypes.PlatformStatusDeleted},
		}}, nil
	}
	if _, _, err := ebScan(st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	sent := c.sent()
	for typ, arn := range map[string]string{TypeBeanstalkEnvironment: oldEnv, TypeBeanstalkPlatform: oldPlatform} {
		if slices.Contains(sent, arn) {
			t.Errorf("%s sent to ListTagsForResource", arn)
		}
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", arn, ok)
		}
		assertTagsMap(t, rowsByNativeID(t, st, typ)[ebTagged[typ]].TagsJSON, map[string]string{"type": typ})
	}
}

// Rows of every tagged type in the aws-us-gov and aws-cn partitions reach the
// tag op under their listed ARNs unchanged, and a configuration template under the Service
// Reference format configurationtemplate/${ApplicationName}/${TemplateName}
// in the account's partition and region.
func TestScanElasticBeanstalk_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			c := newEBTagCalls()
			h := c.handlers()
			app := partitionARN(pf.partition, "elasticbeanstalk", pf.region, "application/web")
			env := partitionARN(pf.partition, "elasticbeanstalk", pf.region, "environment/web/web-prod")
			version := partitionARN(pf.partition, "elasticbeanstalk", pf.region, "applicationversion/web/v1")
			platform := partitionARN(pf.partition, "elasticbeanstalk", pf.region, "platform/custom/1.0.0")
			tpl := "arn:" + pf.partition + ":elasticbeanstalk:" + pf.region + ":" + testAccountID + ":configurationtemplate/web/base"
			c.tags[tpl] = map[string]string{"k": "v"}
			h["DescribeApplications"] = func(any) (any, error) {
				return &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
					{ApplicationArn: &app, ApplicationName: sdkaws.String("web"), ConfigurationTemplates: []string{"base"}},
				}}, nil
			}
			h["DescribeEnvironments"] = func(any) (any, error) {
				return &elasticbeanstalk.DescribeEnvironmentsOutput{Environments: []ebtypes.EnvironmentDescription{{EnvironmentArn: &env}}}, nil
			}
			h["DescribeApplicationVersions"] = func(any) (any, error) {
				return &elasticbeanstalk.DescribeApplicationVersionsOutput{ApplicationVersions: []ebtypes.ApplicationVersionDescription{{ApplicationVersionArn: &version}}}, nil
			}
			h["ListPlatformVersions"] = func(any) (any, error) {
				return &elasticbeanstalk.ListPlatformVersionsOutput{PlatformSummaryList: []ebtypes.PlatformSummary{{PlatformArn: &platform, PlatformStatus: ebtypes.PlatformStatusReady}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, ebDenied)
			if _, _, err := scanElasticBeanstalk(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := []string{app, tpl, env, version, platform}
			slices.Sort(want)
			if got := c.sent(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)[tpl].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// ListPlatformVersions answering its modeled InsufficientPrivilegesException
// is a denial: no error, one warning under the op, no platform row, and the
// phase after it still runs.
func TestScanElasticBeanstalk_PlatformListingInsufficientPrivileges(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, "elasticbeanstalk:ListPlatformVersions")
	h := newEBTagCalls().handlers()
	h["ListPlatformVersions"] = func(any) (any, error) {
		return nil, apiErr("InsufficientPrivilegesException", "no")
	}

	total, _, err := ebScan(st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != ebFixtureTotal-1 {
		t.Errorf("total = %d; want %d, every row but the platform", total, ebFixtureTotal-1)
	}
	if len(*warns) != 1 || (*warns)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("warnings = %v; want 1 in %s/%s", *warns, testAccountID, testRegion)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); !slices.Equal(got, ebAllTemplates) {
		t.Errorf("templates (the next phase) = %v; want %v", got, ebAllTemplates)
	}
}

// A failed template write sends no warning: not the empty template name's,
// not the response without the requested template's, not the denied tag
// read's of a template row. The throttled template's error joins the
// write's instead of being demoted to a warning beside it.
func TestScanElasticBeanstalk_FailedTemplateWriteSendsNoWarning(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBeanstalkConfigurationTemplate)
	c := newEBTagCalls()
	c.errs[ebTestTplProd] = ebDenied
	h := c.handlers()
	next := h["DescribeConfigurationSettings"]
	h["DescribeConfigurationSettings"] = func(in any) (any, error) {
		switch sv(in.(*elasticbeanstalk.DescribeConfigurationSettingsInput).TemplateName) {
		case "base":
			return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{
				ebTemplate("web", "other"),
			}}, nil
		case "w1":
			return nil, ebThrottle
		}
		return next(in)
	}

	_, _, err := ebScan(st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ThrottlingException")
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// An NXDOMAIN on the first listing with nothing stored is the region
// without the service: unavailable, no warning, no later call. Once the
// applications are stored, an NXDOMAIN from a later listing or a tag read
// fails the scan and keeps the stored rows' counts.
func TestScanElasticBeanstalk_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "elasticbeanstalk.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		c := newEBTagCalls()
		h := c.handlers()
		h["DescribeApplications"] = func(any) (any, error) { return nil, nx }
		_, _, err := ebScan(st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, op := range []string{"DescribeEnvironments", "DescribeApplicationVersions", "ListPlatformVersions", "DescribeConfigurationSettings", "ListTagsForResource"} {
			if n := c.count(op); n != 0 {
				t.Errorf("%s called %d times; want 0", op, n)
			}
		}
	})
	t.Run("listing after the applications were stored", func(t *testing.T) {
		st := newTestStore(t)
		h := newEBTagCalls().handlers()
		h["DescribeEnvironments"] = func(any) (any, error) { return nil, nx }
		total, _, err := ebScan(st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != ebFixtureTotal-1 {
			t.Errorf("total = %d; want %d, every row but the environment", total, ebFixtureTotal-1)
		}
	})
	t.Run("tag read after the applications were stored", func(t *testing.T) {
		st := newTestStore(t)
		c := newEBTagCalls()
		c.errs[ebTestEnv] = nx
		total, _, err := ebScan(st, c.handlers())
		assertGlueOutcome(t, err, outcomeError)
		if total != ebFixtureTotal {
			t.Errorf("total = %d; want %d, the environment kept untagged", total, ebFixtureTotal)
		}
	})
}

// The template warning and the transient template error name the smallest
// template ARN, not the first tallied. At most fanoutMed describes run at
// once, so the template listed last, whose ARN is the smallest, is described
// only after an earlier describe returned and was tallied.
func TestScanElasticBeanstalk_TemplateTalliesNameSmallestARN(t *testing.T) {
	const smallest = "arn:aws:elasticbeanstalk:us-east-1:123456789012:configurationtemplate/app/base"
	web := make([]string, fanoutMed)
	for i := range web {
		web[i] = fmt.Sprintf("t%02d", i)
	}
	for _, tc := range []struct {
		name string
		// transient: the describes fail transiently, so the scan's error
		// names the template rather than a warning.
		transient bool
		answer    func(app string) (any, error)
	}{
		{"response without the template", false, func(app string) (any, error) {
			return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{
				ebTemplate(app, "other"),
			}}, nil
		}},
		{"throttled", true, func(string) (any, error) { return nil, ebThrottle }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, beanstalkDescribeConfigurationSettingsOp)
			h := newEBTagCalls().handlers()
			h["DescribeApplications"] = func(any) (any, error) {
				return &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
					{ApplicationArn: sdkaws.String(ebTestAppWeb), ApplicationName: sdkaws.String("web"), ConfigurationTemplates: web},
					{ApplicationArn: sdkaws.String("arn:aws:elasticbeanstalk:us-east-1:123456789012:application/app"), ApplicationName: sdkaws.String("app"), ConfigurationTemplates: []string{"base"}},
				}}, nil
			}
			h["DescribeConfigurationSettings"] = func(in any) (any, error) {
				return tc.answer(sv(in.(*elasticbeanstalk.DescribeConfigurationSettingsInput).ApplicationName))
			}

			_, _, err := ebScan(st, h)
			if tc.transient {
				assertGlueOutcome(t, err, outcomeTransient)
				if !strings.Contains(err.Error(), smallest) {
					t.Errorf("err = %v; want it naming %s", err, smallest)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, smallest) {
				t.Errorf("warnings = %v; want 1 naming %s", *warns, smallest)
			}
		})
	}
}
