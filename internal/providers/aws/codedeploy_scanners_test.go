package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	cdtypes "github.com/aws/aws-sdk-go-v2/service/codedeploy/types"
	"github.com/icearp/disco-cli/store"
)

var (
	cdTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: codedeploy:List")
	cdTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	cdTestBadName  = apiErr("InvalidApplicationNameException", "bad name")
	cdTestBadARN   = apiErr("InvalidArnException", "bad arn")
	cdTestAppGone  = apiErr("ApplicationDoesNotExistException", "gone")
	cdTestNX       = &net.DNSError{Err: "no such host", Name: "codedeploy.us-east-1.amazonaws.com", IsNotFound: true}
	cdTestRole     = "arn:aws:iam::" + testAccountID + ":role/cd"
)

// cdTestTotal is every row of cdFixture: two applications, two deployment
// groups in each, and two deployment configs.
const cdTestTotal = 2 + 2*2 + 2

// cdFake answers one CodeDeploy region. appPages are the ListApplications
// pages, groupPages each application's ListDeploymentGroups pages, configs
// the deployment configs. tags holds, by ARN, the ListTagsForResource pages;
// an ARN without an entry has no tags. omit names the applications, and the
// "<app>/<group>" deployment groups, a BatchGet call leaves out, and unnamed
// those it describes with a body that has no name. errs fails a
// call by key: "ListApplications <page>", "BatchGetApplications <first name
// sent>", "ListDeploymentGroups <app> <page>", "BatchGetDeploymentGroups
// <app>/<first name sent>", "ListTagsForResource <ARN>" and
// "ListDeploymentConfigs" (pages count from 1).
type cdFake struct {
	partition, region string
	appPages          [][]string
	groupPages        map[string][][]string
	configs           []string
	tags              map[string][]map[string]string
	omit, unnamed     map[string]bool
	errs              map[string]error
	// repeatToken makes ListApplications page 2 answer the token it was
	// sent.
	repeatToken bool

	// calls records each call: "<Op> <key>" as errs keys it, the
	// BatchGet ones with every name sent ("BatchGetApplications a,b").
	calls parentCalls
	// tagARNs is every ARN ListTagsForResource was sent, once per page.
	tagARNs parentCalls
}

// cdFixture is the default region: applications app-tagged (page 1) and
// app-bare (page 2), each with deployment groups dg-tagged (page 1) and
// dg-bare (page 2); deployment configs CodeDeployDefault.OneAtATime (AWS's)
// and custom-half. app-tagged's tags come on two pages; each dg-tagged has
// one tag.
func cdFixture() *cdFake {
	f := &cdFake{
		partition: "aws", region: testRegion,
		appPages: [][]string{{"app-tagged"}, {"app-bare"}},
		groupPages: map[string][][]string{
			"app-tagged": {{"dg-tagged"}, {"dg-bare"}},
			"app-bare":   {{"dg-tagged"}, {"dg-bare"}},
		},
		configs: []string{"CodeDeployDefault.OneAtATime", "custom-half"},
		omit:    map[string]bool{},
		unnamed: map[string]bool{},
		errs:    map[string]error{},
	}
	f.tags = map[string][]map[string]string{
		f.appARN("app-tagged"):                {{"team": "a"}, {"env": "prod"}},
		f.groupARN("app-tagged", "dg-tagged"): {{"tier": "app-tagged"}},
		f.groupARN("app-bare", "dg-tagged"):   {{"tier": "app-bare"}},
	}
	return f
}

func (f *cdFake) arn(resource string) string {
	return partitionARN(f.partition, "codedeploy", f.region, resource)
}

func (f *cdFake) appARN(app string) string { return f.arn("application:" + app) }

func (f *cdFake) groupARN(app, group string) string {
	return f.arn("deploymentgroup:" + app + "/" + group)
}

func (f *cdFake) configARN(name string) string { return f.arn("deploymentconfig:" + name) }

// page is the page a NextToken asks for, counted from 1, and the token of
// the page after it among n pages (nil after the last).
func cdPage(token *string, n int) (int, *string) {
	page := 1
	if token != nil {
		page, _ = strconv.Atoi(strings.TrimPrefix(*token, "p"))
	}
	if page >= n {
		return page, nil
	}
	return page, sdkaws.String("p" + strconv.Itoa(page+1))
}

func (f *cdFake) call(key string) error {
	f.calls.add(key)
	return f.errs[key]
}

func (f *cdFake) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			page, next := cdPage(in.(*codedeploy.ListApplicationsInput).NextToken, len(f.appPages))
			if err := f.call("ListApplications " + strconv.Itoa(page)); err != nil {
				return nil, err
			}
			if f.repeatToken && page == 2 {
				next = sdkaws.String("p2")
			}
			if len(f.appPages) == 0 {
				return &codedeploy.ListApplicationsOutput{}, nil
			}
			return &codedeploy.ListApplicationsOutput{Applications: f.appPages[page-1], NextToken: next}, nil
		},
		"BatchGetApplications": func(in any) (any, error) {
			names := in.(*codedeploy.BatchGetApplicationsInput).ApplicationNames
			f.calls.add("BatchGetApplications " + strings.Join(names, ","))
			if err := f.errs["BatchGetApplications "+names[0]]; err != nil {
				return nil, err
			}
			out := &codedeploy.BatchGetApplicationsOutput{}
			for _, n := range names {
				if f.omit[n] {
					continue
				}
				a := cdtypes.ApplicationInfo{
					ApplicationName: sdkaws.String(n), ApplicationId: sdkaws.String("id-" + n),
					ComputePlatform: cdtypes.ComputePlatformServer,
				}
				if f.unnamed[n] {
					a.ApplicationName = nil
				}
				out.ApplicationsInfo = append(out.ApplicationsInfo, a)
			}
			return out, nil
		},
		"ListDeploymentGroups": func(in any) (any, error) {
			l := in.(*codedeploy.ListDeploymentGroupsInput)
			app := sv(l.ApplicationName)
			page, next := cdPage(l.NextToken, len(f.groupPages[app]))
			if err := f.call("ListDeploymentGroups " + app + " " + strconv.Itoa(page)); err != nil {
				return nil, err
			}
			if len(f.groupPages[app]) == 0 {
				return &codedeploy.ListDeploymentGroupsOutput{ApplicationName: &app}, nil
			}
			return &codedeploy.ListDeploymentGroupsOutput{ApplicationName: &app, DeploymentGroups: f.groupPages[app][page-1], NextToken: next}, nil
		},
		"BatchGetDeploymentGroups": func(in any) (any, error) {
			b := in.(*codedeploy.BatchGetDeploymentGroupsInput)
			app := sv(b.ApplicationName)
			f.calls.add("BatchGetDeploymentGroups " + app + "/" + strings.Join(b.DeploymentGroupNames, ","))
			if err := f.errs["BatchGetDeploymentGroups "+app+"/"+b.DeploymentGroupNames[0]]; err != nil {
				return nil, err
			}
			out := &codedeploy.BatchGetDeploymentGroupsOutput{}
			for _, g := range b.DeploymentGroupNames {
				if f.omit[app+"/"+g] {
					out.ErrorMessage = sdkaws.String("DEPLOYMENT_GROUP_MISSING: " + g)
					continue
				}
				info := cdtypes.DeploymentGroupInfo{
					ApplicationName: &app, DeploymentGroupName: sdkaws.String(g), DeploymentGroupId: sdkaws.String("id-" + app + "-" + g),
					DeploymentConfigName: sdkaws.String("CodeDeployDefault.OneAtATime"), ServiceRoleArn: &cdTestRole,
				}
				if f.unnamed[app+"/"+g] {
					info.DeploymentGroupName = nil
				}
				out.DeploymentGroupsInfo = append(out.DeploymentGroupsInfo, info)
			}
			return out, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			l := in.(*codedeploy.ListTagsForResourceInput)
			arn := sv(l.ResourceArn)
			f.tagARNs.add(arn)
			if err := f.call("ListTagsForResource " + arn); err != nil {
				return nil, err
			}
			pages := f.tags[arn]
			if len(pages) == 0 {
				return &codedeploy.ListTagsForResourceOutput{}, nil
			}
			page, next := cdPage(l.NextToken, len(pages))
			out := &codedeploy.ListTagsForResourceOutput{NextToken: next}
			for k, v := range pages[page-1] {
				out.Tags = append(out.Tags, cdtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
			}
			return out, nil
		},
		"ListDeploymentConfigs": func(any) (any, error) {
			if err := f.call("ListDeploymentConfigs"); err != nil {
				return nil, err
			}
			return &codedeploy.ListDeploymentConfigsOutput{DeploymentConfigsList: f.configs}, nil
		},
		"GetDeploymentConfig": func(in any) (any, error) {
			name := in.(*codedeploy.GetDeploymentConfigInput).DeploymentConfigName
			return &codedeploy.GetDeploymentConfigOutput{DeploymentConfigInfo: &cdtypes.DeploymentConfigInfo{
				DeploymentConfigName: name, DeploymentConfigId: sdkaws.String("id-" + sv(name)),
			}}, nil
		},
	}
}

// cdScan runs the service scan entry point against f; an op without a
// handler is denied.
func cdScan(ctx context.Context, st *store.Store, f *cdFake) (int, error) {
	acct := partitionAccount(f.partition)
	if f.partition == "aws" {
		acct = newTestAccount(testAccountID)
	}
	acct.cfg = opStubConfig(f.handlers(), cdTestDenied)
	return scanCodeDeploy(ctx, acct, f.region, st, testScanID)
}

// cdStored is the NativeID of every stored row of the service, sorted.
func cdStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeCodeDeployApplication, TypeCodeDeployDeploymentGroup, TypeCodeDeployDeploymentConfig} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// all is the NativeID of every row f answers with nothing failing, sorted.
func (f *cdFake) all() []string {
	var ids []string
	for _, page := range f.appPages {
		for _, app := range page {
			ids = append(ids, f.appARN(app))
			for _, gp := range f.groupPages[app] {
				for _, g := range gp {
					ids = append(ids, f.groupARN(app, g))
				}
			}
		}
	}
	for _, c := range f.configs {
		ids = append(ids, f.configARN(c))
	}
	sort.Strings(ids)
	return ids
}

func (f *cdFake) without(drop ...string) []string {
	return slices.DeleteFunc(f.all(), func(id string) bool { return slices.Contains(drop, id) })
}

// called reports whether f was sent a call recorded as key.
func (f *cdFake) called(key string) bool { return slices.Contains(f.calls.distinct(), key) }

// calledOp reports whether f was sent any call of op.
func (f *cdFake) calledOp(op string) bool {
	return slices.ContainsFunc(f.calls.distinct(), func(k string) bool { return strings.HasPrefix(k, op+" ") || k == op })
}

func cdSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// Every row is stored under the ARN built from its names, named by its
// name, its attributes the BatchGet or GetDeploymentConfig body, with no
// Status or CreatedAt. Applications and deployment groups carry the tags
// ListTagsForResource returns, every page of them (nil for none), the op
// sent exactly each one's ARN; deployment configs are never tag-read, and
// AWS's is stored managed. Each listing's names are described in one
// BatchGet call. No error, no warning.
func TestScanCodeDeploy_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := cdFixture()
	counted, upserts := countUpserts(st)
	total, err := cdScan(context.Background(), counted, f)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cdTestTotal || inserted != cdTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, cdTestTotal, cdTestTotal)
	}
	if got := cdStored(t, st); !slices.Equal(got, f.all()) {
		t.Errorf("stored = %v; want %v", got, f.all())
	}
	wantTagARNs := cdSorted(f.appARN("app-tagged"), f.appARN("app-bare"),
		f.groupARN("app-tagged", "dg-tagged"), f.groupARN("app-tagged", "dg-bare"),
		f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare"))
	if got := f.tagARNs.distinct(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	if n := len(f.tagARNs.ids); n != 7 {
		t.Errorf("ListTagsForResource calls = %d; want 7 (app-tagged's tags on two pages)", n)
	}
	for _, key := range []string{
		"BatchGetApplications app-tagged,app-bare",
		"BatchGetDeploymentGroups app-tagged/dg-tagged,dg-bare",
		"BatchGetDeploymentGroups app-bare/dg-tagged,dg-bare",
	} {
		if !f.called(key) {
			t.Errorf("no call %q; calls %v", key, f.calls.distinct())
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	appAttr := func(name string) func(string) bool {
		return func(a string) bool {
			var x cdtypes.ApplicationInfo
			return json.Unmarshal([]byte(a), &x) == nil && sv(x.ApplicationId) == "id-"+name && x.ComputePlatform == cdtypes.ComputePlatformServer
		}
	}
	groupAttr := func(app, group string) func(string) bool {
		return func(a string) bool {
			var x cdtypes.DeploymentGroupInfo
			return json.Unmarshal([]byte(a), &x) == nil && sv(x.DeploymentGroupId) == "id-"+app+"-"+group &&
				sv(x.ApplicationName) == app && sv(x.ServiceRoleArn) == cdTestRole
		}
	}
	configAttr := func(name string) func(string) bool {
		return func(a string) bool {
			var x cdtypes.DeploymentConfigInfo
			return json.Unmarshal([]byte(a), &x) == nil && sv(x.DeploymentConfigId) == "id-"+name
		}
	}
	for _, c := range []struct {
		typ, id, name string
		managed       bool
		tags          map[string]string
		attr          func(string) bool
	}{
		{TypeCodeDeployApplication, f.appARN("app-tagged"), "app-tagged", false, map[string]string{"team": "a", "env": "prod"}, appAttr("app-tagged")},
		{TypeCodeDeployApplication, f.appARN("app-bare"), "app-bare", false, nil, appAttr("app-bare")},
		{TypeCodeDeployDeploymentGroup, f.groupARN("app-tagged", "dg-tagged"), "dg-tagged", false, map[string]string{"tier": "app-tagged"}, groupAttr("app-tagged", "dg-tagged")},
		{TypeCodeDeployDeploymentGroup, f.groupARN("app-tagged", "dg-bare"), "dg-bare", false, nil, groupAttr("app-tagged", "dg-bare")},
		{TypeCodeDeployDeploymentGroup, f.groupARN("app-bare", "dg-tagged"), "dg-tagged", false, map[string]string{"tier": "app-bare"}, groupAttr("app-bare", "dg-tagged")},
		{TypeCodeDeployDeploymentGroup, f.groupARN("app-bare", "dg-bare"), "dg-bare", false, nil, groupAttr("app-bare", "dg-bare")},
		{TypeCodeDeployDeploymentConfig, f.configARN("CodeDeployDefault.OneAtATime"), "CodeDeployDefault.OneAtATime", true, nil, configAttr("CodeDeployDefault.OneAtATime")},
		{TypeCodeDeployDeploymentConfig, f.configARN("custom-half"), "custom-half", false, nil, configAttr("custom-half")},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			if sv(r.Name) != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			if r.Status != nil || r.CreatedAt != nil {
				t.Errorf("Status, CreatedAt = %v, %v; want none", r.Status, r.CreatedAt)
			}
			if sv(r.Region) != testRegion || r.AccountID != testAccountID || r.ManagedByProvider != c.managed {
				t.Errorf("Region, AccountID, ManagedByProvider = %q, %q, %v; want %s, %s, %v", sv(r.Region), r.AccountID, r.ManagedByProvider, testRegion, testAccountID, c.managed)
			}
			if !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the SDK body", r.AttributesJSON)
			}
			if c.tags != nil {
				assertTagsMap(t, r.TagsJSON, c.tags)
			} else if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
		})
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanCodeDeploy_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := cdScan(context.Background(), st, cdFixture()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := cdScan(context.Background(), st, cdFixture())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != cdTestTotal {
		t.Errorf("rescan total = %d; want %d", total, cdTestTotal)
	}
}

// A region without applications or configs stores nothing, describes
// nothing, reads no tags and warns about nothing.
func TestScanCodeDeploy_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := cdFixture()
	f.appPages, f.configs = nil, nil
	counted, upserts := countUpserts(st)
	total, err := cdScan(context.Background(), counted, f)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	for _, op := range []string{"BatchGetApplications", "ListDeploymentGroups", "ListTagsForResource"} {
		if f.calledOp(op) {
			t.Errorf("%s called; calls %v", op, f.calls.distinct())
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A GovCloud or China account's ARNs are built in its partition and reach
// the tag op unchanged.
func TestScanCodeDeploy_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			t.Parallel()
			f := cdFixture()
			f.partition, f.region = pf.partition, pf.region
			f.tags = map[string][]map[string]string{f.appARN("app-tagged"): {{"env": pf.partition}}}
			st := newTestStore(t)
			if _, err := cdScan(context.Background(), st, f); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := cdSorted(f.appARN("app-tagged"), f.appARN("app-bare"),
				f.groupARN("app-tagged", "dg-tagged"), f.groupARN("app-tagged", "dg-bare"),
				f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare"))
			if !strings.HasPrefix(want[0], "arn:"+pf.partition+":codedeploy:"+pf.region+":") {
				t.Fatalf("fixture ARN %s not in %s", want[0], pf.partition)
			}
			if got := f.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			if got := cdStored(t, st); !slices.Equal(got, f.all()) {
				t.Errorf("stored = %v; want %v", got, f.all())
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeCodeDeployApplication)[f.appARN("app-tagged")].TagsJSON, map[string]string{"env": pf.partition})
		})
	}
}

// A failed tag read keeps its row untagged: a denial warns once under the
// tag op with the scan's scope; a throttle is transient; any other error is
// an error. ListTagsForResource models no not-found, so even
// ApplicationDoesNotExistException keeps the row and is returned.
func TestScanCodeDeploy_TagReadFailures(t *testing.T) {
	t.Parallel()
	app := cdFixture().appARN("app-tagged")
	group := cdFixture().groupARN("app-bare", "dg-tagged")
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		warn     bool
	}{
		{"application/denied", app, TypeCodeDeployApplication, cdTestDenied, -1, "", true},
		{"deployment group/denied", group, TypeCodeDeployDeploymentGroup, cdTestDenied, -1, "", true},
		{"application/transient", app, TypeCodeDeployApplication, cdTestThrottle, outcomeTransient, "ThrottlingException", false},
		{"deployment group/other error", group, TypeCodeDeployDeploymentGroup, cdTestBadARN, outcomeError, "InvalidArnException", false},
		{"application/unmodeled not-found", app, TypeCodeDeployApplication, cdTestAppGone, outcomeError, "ApplicationDoesNotExistException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs["ListTagsForResource "+c.arn] = c.err
			total, err := cdScan(context.Background(), st, f)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != cdTestTotal {
				t.Errorf("total = %d; want %d", total, cdTestTotal)
			}
			if got := cdStored(t, st); !slices.Equal(got, f.all()) {
				t.Errorf("stored = %v; want %v", got, f.all())
			}
			if r := rowsByNativeID(t, st, c.typ)[c.arn]; r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var wantOps []string
			if c.warn {
				wantOps = []string{cdTagsOp}
			}
			if got := amWarnings(w); !slices.Equal(got, wantOps) {
				t.Fatalf("warnings = %v; want %v", w, wantOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads of an application and a deployment group, in different
// phases, send one warning for the whole scan.
func TestScanCodeDeploy_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := cdFixture()
	f.errs["ListTagsForResource "+f.appARN("app-bare")] = cdTestDenied
	f.errs["ListTagsForResource "+f.groupARN("app-tagged", "dg-bare")] = cdTestDenied
	if _, err := cdScan(context.Background(), st, f); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{cdTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, cdTagsOp)
	}
}

// A throttle beside a hard error is demoted to one warning under its own
// op, so the hard error decides the outcome.
func TestScanCodeDeploy_ThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	f := cdFixture()
	for _, c := range []struct {
		name string
		errs map[string]error
		code string
		warn string
	}{
		{"tag read beside a failed group batch", map[string]error{
			"ListTagsForResource " + f.appARN("app-bare"): cdTestThrottle,
			"BatchGetDeploymentGroups app-bare/dg-tagged": cdTestBadName,
		}, "InvalidApplicationNameException", cdTagsOp},
		{"application batch beside a failed tag read", map[string]error{
			"BatchGetApplications app-tagged":                          cdTestThrottle,
			"ListTagsForResource " + f.groupARN("app-bare", "dg-bare"): cdTestBadARN,
		}, "InvalidArnException", cdGetAppsOp},
		{"group batch beside a failed group batch", map[string]error{
			"BatchGetDeploymentGroups app-tagged/dg-tagged": cdTestThrottle,
			"BatchGetDeploymentGroups app-bare/dg-tagged":   cdTestBadName,
		}, "InvalidApplicationNameException", cdGetGroupsOp},
		{"group batch beside a failed group listing", map[string]error{
			"BatchGetDeploymentGroups app-tagged/dg-tagged": cdTestThrottle,
			"ListDeploymentGroups app-bare 1":               cdTestBadName,
		}, "InvalidApplicationNameException", cdGetGroupsOp},
		{"group listing beside a failed group listing", map[string]error{
			"ListDeploymentGroups app-tagged 2": cdTestThrottle,
			"ListDeploymentGroups app-bare 2":   cdTestBadName,
		}, "InvalidApplicationNameException", cdListGroupsOp},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs = c.errs
			_, err := cdScan(context.Background(), st, f)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, c.code)
			if got := amWarnings(warns()); !slices.Equal(got, []string{c.warn}) {
				t.Errorf("warnings = %v; want one under %s", got, c.warn)
			}
		})
	}
}

// A failed BatchGetApplications call: its applications are not stored (the
// listing has nothing else to store), but their deployment groups are still
// listed by name and stored, and the configs are stored. A denial warns
// once under BatchGetApplications; a throttle is transient; any other error,
// ApplicationDoesNotExistException included (whether one deleted name fails
// the whole call is not modeled), is an error.
func TestScanCodeDeploy_BatchGetApplicationsFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}{
		{"denied", cdTestDenied, -1, "", true},
		{"transient", cdTestThrottle, outcomeTransient, "ThrottlingException", false},
		{"other error", cdTestBadName, outcomeError, "InvalidApplicationNameException", false},
		{"application gone", cdTestAppGone, outcomeError, "ApplicationDoesNotExistException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs["BatchGetApplications app-tagged"] = c.err
			total, err := cdScan(context.Background(), st, f)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := f.without(f.appARN("app-tagged"), f.appARN("app-bare"))
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{cdGetAppsOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// A failed BatchGetDeploymentGroups call costs only its application's
// groups. ApplicationDoesNotExistException (modeled: the application was
// deleted after it was listed) drops them silently; a denial warns once
// under BatchGetDeploymentGroups; a throttle is transient; any other error
// is an error.
func TestScanCodeDeploy_BatchGetDeploymentGroupsFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}{
		{"application gone", cdTestAppGone, -1, "", false},
		{"denied", cdTestDenied, -1, "", true},
		{"transient", cdTestThrottle, outcomeTransient, "ThrottlingException", false},
		{"other error", cdTestBadName, outcomeError, "InvalidApplicationNameException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs["BatchGetDeploymentGroups app-bare/dg-tagged"] = c.err
			total, err := cdScan(context.Background(), st, f)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := f.without(f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare"))
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{cdGetGroupsOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// A name a BatchGet call was sent but did not describe, by leaving it out
// or by a body without a name, is not stored and is warned about once under
// that op; nothing fails. An application left out still has its groups
// listed.
func TestScanCodeDeploy_BatchLeavesNamesOut(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		set  func(f *cdFake) map[string]bool
	}{
		{"left out", func(f *cdFake) map[string]bool { return f.omit }},
		{"no name", func(f *cdFake) map[string]bool { return f.unnamed }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			for _, k := range []string{"app-bare", "app-tagged/dg-bare", "app-bare/dg-bare"} {
				c.set(f)[k] = true
			}
			total, err := cdScan(context.Background(), st, f)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := f.without(f.appARN("app-bare"), f.groupARN("app-tagged", "dg-bare"), f.groupARN("app-bare", "dg-bare"))
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := amWarnings(warns()); !slices.Equal(got, []string{cdGetAppsOp, cdGetGroupsOp}) {
				t.Errorf("warnings = %v; want one under each BatchGet op", got)
			}
		})
	}
}

// Each BatchGet call is sent at most 100 names, the model's limit, and a
// failed call costs only its own names.
func TestScanCodeDeploy_BatchCap(t *testing.T) {
	t.Parallel()
	names := make([]string, 101)
	for i := range names {
		names[i] = fmt.Sprintf("n%03d", i)
	}
	t.Run("applications", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		f := cdFixture()
		f.appPages = [][]string{names}
		f.groupPages, f.configs = nil, nil
		f.errs["BatchGetApplications n100"] = cdTestBadName
		total, err := cdScan(context.Background(), st, f)
		assertAPICode(t, err, "InvalidApplicationNameException")
		if want := []string{"BatchGetApplications " + strings.Join(names[:100], ","), "BatchGetApplications n100"}; !slices.Equal(cdBatchCalls(f, "BatchGetApplications"), want) {
			t.Errorf("BatchGetApplications calls = %v; want %v", cdBatchCalls(f, "BatchGetApplications"), want)
		}
		if total != 100 {
			t.Errorf("total = %d; want the 100 of the call that succeeded", total)
		}
	})
	t.Run("deployment groups", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		f := cdFixture()
		f.appPages = [][]string{{"app"}}
		f.groupPages = map[string][][]string{"app": {names}}
		f.configs = nil
		f.errs["BatchGetDeploymentGroups app/n000"] = cdTestBadName
		total, err := cdScan(context.Background(), st, f)
		assertAPICode(t, err, "InvalidApplicationNameException")
		if want := []string{"BatchGetDeploymentGroups app/" + strings.Join(names[:100], ","), "BatchGetDeploymentGroups app/n100"}; !slices.Equal(cdBatchCalls(f, "BatchGetDeploymentGroups"), want) {
			t.Errorf("BatchGetDeploymentGroups calls = %v; want %v", cdBatchCalls(f, "BatchGetDeploymentGroups"), want)
		}
		if total != 1+1 {
			t.Errorf("total = %d; want the application and the group of the call that succeeded", total)
		}
	})
}

// cdBatchCalls is every call of op f recorded, in call order.
func cdBatchCalls(f *cdFake, op string) []string {
	f.calls.mu.Lock()
	defer f.calls.mu.Unlock()
	var got []string
	for _, k := range f.calls.ids {
		if strings.HasPrefix(k, op+" ") {
			got = append(got, k)
		}
	}
	return got
}

// A failed ListDeploymentGroups page costs only its application, and the
// groups listed before it are still described and stored.
// ApplicationDoesNotExistException (modeled) on the first page is silent; on
// a later page it warns that the listing was cut short, unless the
// BatchGetDeploymentGroups call for the names listed answers it too, or
// describes them only by bodies without a name, so that none of the
// application was stored. A denial warns once under the op; a
// throttle is transient; any other error is an error.
func TestScanCodeDeploy_ListDeploymentGroupsFailures(t *testing.T) {
	t.Parallel()
	page1, page2 := cdFixture().groupARN("app-bare", "dg-tagged"), cdFixture().groupARN("app-bare", "dg-bare")
	for _, c := range []struct {
		name     string
		page     string
		err      error
		batchErr error // BatchGetDeploymentGroups' answer for page 1's names; nil: described
		unnamed  bool  // page 1's name is described by a body without a name
		lost     []string
		outcome  serviceOutcome // -1: no error
		code     string
		warns    []string
	}{
		{"gone on page 1", "1", cdTestAppGone, nil, false, []string{page1, page2}, -1, "", nil},
		{"gone on page 2", "2", cdTestAppGone, nil, false, []string{page2}, -1, "", []string{cdListGroupsOp}},
		// Nothing of the application was stored, so its partial listing is
		// not warned about; only the name left undescribed is.
		{"gone on page 2, page 1 returned without a name", "2", cdTestAppGone, nil, true, []string{page1, page2}, -1, "", []string{cdGetGroupsOp}},
		{"gone on page 2 and to its batch", "2", cdTestAppGone, cdTestAppGone, false, []string{page1, page2}, -1, "", nil},
		{"throttled on page 2, gone to its batch", "2", cdTestThrottle, cdTestAppGone, false, []string{page1, page2}, -1, "", nil},
		{"denied", "1", cdTestDenied, nil, false, []string{page1, page2}, -1, "", []string{cdListGroupsOp}},
		{"transient", "2", cdTestThrottle, nil, false, []string{page2}, outcomeTransient, "ThrottlingException", nil},
		{"other error", "2", cdTestBadName, nil, false, []string{page2}, outcomeError, "InvalidApplicationNameException", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs["ListDeploymentGroups app-bare "+c.page] = c.err
			if c.batchErr != nil {
				f.errs["BatchGetDeploymentGroups app-bare/dg-tagged"] = c.batchErr
			}
			f.unnamed["app-bare/dg-tagged"] = c.unnamed
			total, err := cdScan(context.Background(), st, f)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := f.without(c.lost...)
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := amWarnings(warns()); !slices.Equal(got, c.warns) {
				t.Errorf("warnings = %v; want %v", got, c.warns)
			}
		})
	}
}

// A failed ListApplications page keeps the applications of the pages before
// it, their groups included, and the configs phase still runs; a denial
// warns once instead of failing; a repeated NextToken ends the listing as an
// error, keeping the pages read.
func TestScanCodeDeploy_ListApplicationsFailures(t *testing.T) {
	t.Parallel()
	f := cdFixture()
	appBare := []string{f.appARN("app-bare"), f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare")}
	everyApp := append([]string{f.appARN("app-tagged"), f.groupARN("app-tagged", "dg-tagged"), f.groupARN("app-tagged", "dg-bare")}, appBare...)
	for _, c := range []struct {
		name   string
		errs   map[string]error
		repeat bool
		lost   []string
		is     error  // nil: no error unless code is set
		code   string // the API error returned
		warn   bool
	}{
		{"page 2 fails", map[string]error{"ListApplications 2": cdTestBadName}, false, appBare, nil, "InvalidApplicationNameException", false},
		{"page 1 fails", map[string]error{"ListApplications 1": cdTestBadName}, false, everyApp, nil, "InvalidApplicationNameException", false},
		{"page 1 denied", map[string]error{"ListApplications 1": cdTestDenied}, false, everyApp, nil, "", true},
		{"repeated token", map[string]error{}, true, nil, errRepeatedNextToken, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.errs, f.repeatToken = c.errs, c.repeat
			total, err := cdScan(context.Background(), st, f)
			switch {
			case c.is != nil:
				if !errors.Is(err, c.is) {
					t.Fatalf("err = %v; want %v", err, c.is)
				}
			case c.code != "":
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			case err != nil:
				t.Fatalf("err = %v; want nil", err)
			}
			want := cdFixture().without(c.lost...)
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{cdListAppsOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// A failed write stops the scan: no warning about the rows it lost (a
// denied tag read or batch among them), no later phase is called, and the
// earlier phases' rows stay counted. A batch call that failed, hard or
// throttled, is returned beside the failed write rather than warned about.
func TestScanCodeDeploy_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	f := cdFixture()
	for _, c := range []struct {
		name, typ string
		errs      map[string]error
		later     []string
		wantTotal int
		code      string // an API error returned beside the write's
	}{
		{"applications", TypeCodeDeployApplication, map[string]error{"ListTagsForResource " + f.appARN("app-bare"): cdTestDenied}, []string{"ListDeploymentGroups", "ListDeploymentConfigs"}, 0, ""},
		{"deployment groups", TypeCodeDeployDeploymentGroup, map[string]error{
			"ListTagsForResource " + f.groupARN("app-bare", "dg-bare"): cdTestDenied,
			"ListDeploymentGroups app-tagged 2":                        cdTestDenied,
			"BatchGetDeploymentGroups app-bare/dg-tagged":              cdTestDenied,
		}, []string{"ListDeploymentConfigs"}, 2, ""},
		{"deployment groups beside a throttled listing", TypeCodeDeployDeploymentGroup, map[string]error{"ListDeploymentGroups app-bare 2": cdTestThrottle}, []string{"ListDeploymentConfigs"}, 2, "ThrottlingException"},
		{"deployment groups beside a throttled batch", TypeCodeDeployDeploymentGroup, map[string]error{"BatchGetDeploymentGroups app-bare/dg-tagged": cdTestThrottle}, []string{"ListDeploymentConfigs"}, 2, "ThrottlingException"},
		{"deployment groups beside a failed batch", TypeCodeDeployDeploymentGroup, map[string]error{"BatchGetDeploymentGroups app-bare/dg-tagged": cdTestBadName}, []string{"ListDeploymentConfigs"}, 2, "InvalidApplicationNameException"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			f := cdFixture()
			f.errs = c.errs
			total, err := cdScan(context.Background(), st, f)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.later {
				if f.calledOp(op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// An NXDOMAIN after the endpoint answered is a failure of this scan, not a
// region gap: the counts are kept and the later phases run. With nothing
// stored, the first listing's NXDOMAIN reports the region unavailable, and
// no later phase runs.
func TestScanCodeDeploy_NXDOMAIN(t *testing.T) {
	t.Parallel()
	f := cdFixture()
	for _, c := range []struct {
		name, key string
		lost      []string
	}{
		{"application batch", "BatchGetApplications app-tagged", []string{f.appARN("app-tagged"), f.appARN("app-bare")}},
		{"group listing", "ListDeploymentGroups app-bare 1", []string{f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare")}},
		{"group batch", "BatchGetDeploymentGroups app-bare/dg-tagged", []string{f.groupARN("app-bare", "dg-tagged"), f.groupARN("app-bare", "dg-bare")}},
		{"tag read", "ListTagsForResource " + f.appARN("app-bare"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			f := cdFixture()
			f.errs[c.key] = cdTestNX
			total, err := cdScan(context.Background(), st, f)
			assertGlueOutcome(t, err, outcomeError)
			want := f.without(c.lost...)
			if got := cdStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
		})
	}
	// ListApplications answered, so a later NXDOMAIN fails the scan even when
	// no row was stored: the later phases run.
	for _, c := range []struct {
		name string
		errs map[string]error
	}{
		{"application batch, nothing stored", map[string]error{"BatchGetApplications app-tagged": cdTestNX}},
		{"group listings, nothing stored", map[string]error{
			"BatchGetApplications app-tagged":   cdTestDenied,
			"ListDeploymentGroups app-tagged 1": cdTestNX,
			"ListDeploymentGroups app-bare 1":   cdTestNX,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			f := cdFixture()
			f.configs = nil
			if _, ok := c.errs["ListDeploymentGroups app-bare 1"]; !ok {
				f.groupPages = nil
			}
			f.errs = c.errs
			total, err := cdScan(context.Background(), st, f)
			assertGlueOutcome(t, err, outcomeError)
			if total != 0 {
				t.Errorf("total = %d; want 0", total)
			}
			if !f.calledOp("ListDeploymentConfigs") {
				t.Error("ListDeploymentConfigs not called after the NXDOMAIN")
			}
		})
	}
	// ListApplications answered page 1, so its page 2 NXDOMAIN fails the scan
	// even when no application was stored: page 1's application still has
	// its groups listed and stored, and the configs phase runs.
	t.Run("application listing page 2, nothing stored by its phase", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		f := cdFixture()
		f.configs = nil
		f.errs = map[string]error{"ListApplications 2": cdTestNX, "BatchGetApplications app-tagged": cdTestDenied}
		total, err := cdScan(context.Background(), st, f)
		assertGlueOutcome(t, err, outcomeError)
		want := cdSorted(f.groupARN("app-tagged", "dg-tagged"), f.groupARN("app-tagged", "dg-bare"))
		if got := cdStored(t, st); !slices.Equal(got, want) {
			t.Errorf("stored = %v; want %v", got, want)
		}
		if total != len(want) {
			t.Errorf("total = %d; want %d", total, len(want))
		}
		if !f.calledOp("ListDeploymentConfigs") {
			t.Error("ListDeploymentConfigs not called after the NXDOMAIN")
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := cdFixture()
		f.errs["ListApplications 1"] = cdTestNX
		_, err := cdScan(context.Background(), st, f)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if got := f.calls.distinct(); !slices.Equal(got, []string{"ListApplications 1"}) {
			t.Errorf("calls = %v; want only the first listing", got)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// Once ctx ends, the stop is one member of the error, although the
// deployment group phase saw it in its listings, and the configs phase is
// not run.
func TestScanCodeDeploy_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := cdFixture()
	h := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListDeploymentGroups"]
	h["ListDeploymentGroups"] = func(in any) (any, error) {
		if in.(*codedeploy.ListDeploymentGroupsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, cdTestDenied)
	_, err := scanCodeDeploy(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if f.calledOp("ListDeploymentConfigs") {
		t.Error("ListDeploymentConfigs called after ctx ended")
	}
}

// An application deleted while its groups were described: once a
// BatchGetDeploymentGroups call answers ApplicationDoesNotExistException,
// the groups of its other calls already stored are kept and one warning
// under ListDeploymentGroups says only part of the application was stored;
// nothing fails. With none of its groups stored the skip is silent
// (TestScanCodeDeploy_BatchGetDeploymentGroupsFailures).
func TestScanCodeDeploy_GroupBatchAppGoneAfterPartStored(t *testing.T) {
	t.Parallel()
	names := make([]string, 101)
	for i := range names {
		names[i] = fmt.Sprintf("n%03d", i)
	}
	for _, c := range []struct {
		name, failed string
		stored       int
	}{
		{"second call gone", "n100", 100},
		{"first call gone", "n000", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := cdFixture()
			f.appPages = [][]string{{"app"}}
			f.groupPages = map[string][][]string{"app": {names}}
			f.configs = nil
			f.errs["BatchGetDeploymentGroups app/"+c.failed] = cdTestAppGone
			total, err := cdScan(context.Background(), st, f)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(rowsByNativeID(t, st, TypeCodeDeployDeploymentGroup)); got != c.stored {
				t.Errorf("deployment groups stored = %d; want %d", got, c.stored)
			}
			if total != 1+c.stored {
				t.Errorf("total = %d; want %d", total, 1+c.stored)
			}
			if got := amWarnings(warns()); !slices.Equal(got, []string{cdListGroupsOp}) {
				t.Errorf("warnings = %v; want one under %s", got, cdListGroupsOp)
			}
		})
	}
}

// With ctx already ended, the deployment group phase reads no application
// and stores nothing, and says so: one error under ListDeploymentGroups
// carrying the stop, not a success.
func TestScanCDDeploymentGroups_ContextEndedIsAnError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := cdFixture()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(f.handlers(), cdTestDenied)
	client := codedeploy.NewFromConfig(acct.cfg, func(o *codedeploy.Options) { o.Region = testRegion })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var p phaseTotals
	if failed := scanCDDeploymentGroups(ctx, client, []string{"app-tagged", "app-bare"}, acct, testRegion, st, testScanID, cdTags(client), &p); failed {
		t.Fatal("write failed; want no write failure")
	}
	if len(p.errs) != 1 || p.errs[0].op != cdListGroupsOp || !errors.Is(p.errs[0].err, context.Canceled) {
		t.Errorf("errs = %v; want one under %s carrying context.Canceled", p.errs, cdListGroupsOp)
	}
	if p.total != 0 {
		t.Errorf("total = %d; want 0", p.total)
	}
	if got := cdStored(t, st); len(got) != 0 {
		t.Errorf("stored = %v; want none", got)
	}
}

// A failed BatchGet call is tallied once per name it was sent, by kind; a
// call answering the parent's deletion (notFound) is tallied nowhere, and
// a hard error is one error for the call.
func TestCDBatchOutcomes_TallyPerName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                                   string
		err                                    error
		denied, transients, hard, missingCount int
	}{
		{"denied", cdTestDenied, 2, 0, 0, 0},
		{"transient", cdTestThrottle, 0, 2, 0, 0},
		{"not found", cdTestAppGone, 0, 0, 0, 0},
		{"hard", cdTestBadName, 0, 0, 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			o := cdBatchOutcomes{op: cdGetGroupsOp, notFound: cdAppGone}
			o.tallyCDBatchMisses("app/", cdBatchMisses{failed: []cdBatchFailure{{names: []string{"a", "b"}, err: c.err}}})
			if o.denied.count != c.denied || o.transients.count != c.transients || len(o.hard) != c.hard || o.missing.count != c.missingCount {
				t.Errorf("denied, transients, hard, missing = %d, %d, %d, %d; want %d, %d, %d, %d",
					o.denied.count, o.transients.count, len(o.hard), o.missing.count, c.denied, c.transients, c.hard, c.missingCount)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		var o cdBatchOutcomes
		o.tallyCDBatchMisses("app/", cdBatchMisses{missing: []string{"a", "b"}})
		if o.missing.count != 2 || o.missing.first != "app/a" {
			t.Errorf("missing count, first = %d, %q; want 2, app/a", o.missing.count, o.missing.first)
		}
	})
}
