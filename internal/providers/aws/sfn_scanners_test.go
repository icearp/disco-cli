package aws

import (
	"context"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	sfDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: states:List"}
	sfBadARN   = &smithy.GenericAPIError{Code: "InvalidArn", Message: "bad"}
	sfInvalid  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	sfGone     = &smithy.GenericAPIError{Code: "ResourceNotFound", Message: "not found"}
	sfNoSM     = &smithy.GenericAPIError{Code: "StateMachineDoesNotExist", Message: "not found"}
	sfDeleting = &smithy.GenericAPIError{Code: "StateMachineDeleting", Message: "deleting"}
	sfKMS      = &smithy.GenericAPIError{Code: "KmsAccessDeniedException", Message: "key"}
	sfKMSSlow  = &smithy.GenericAPIError{Code: "KmsThrottlingException", Message: "kms slow down"}
	sfKMSOff   = &smithy.GenericAPIError{Code: "KmsInvalidStateException", Message: "key disabled"}
	sfThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"}
	sfCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

// sfTotal is the rows sfFixture's scan stores: 2 state machines, 2
// activities, 3 aliases and 3 versions.
const sfTotal = 10

// sfFixture answers every op of a Step Functions scan and records its calls.
// ListStateMachines lists sm-1, then sm-2 and an element named "nameless"
// without an ARN; ListActivities act-1, then act-2 and an element with
// neither an ARN nor a name. sm-1's aliases are prod, then beta and one
// without an ARN; sm-2's is live. sm-1's versions are 1, then 2; sm-2's 1
// and one without an ARN. DescribeStateMachine answers sm-1 ACTIVE and sm-2
// DELETING, without the definition when asked for METADATA_ONLY. sm-1 and
// act-1 are tagged {"id": <their name>}; the other rows have no tags. errs
// fails a call: "<Op>/<key>", where key is the ARN sent for
// DescribeStateMachine (then "/METADATA_ONLY" when asked so) and
// ListTagsForResource, the state machine ARN then "/" and the token for a
// later ListStateMachineAliases or ListStateMachineVersions page, and "" or
// "p2" for the two pages of ListStateMachines and ListActivities. Handlers
// run on fan-out goroutines, so mu guards the state they share.
type sfFixture struct {
	partition, region string
	mu                sync.Mutex
	errs              map[string]error
	keys              map[string][]string
	maxResults        map[string][]int32
}

func newSFFixture(partition, region string) *sfFixture {
	return &sfFixture{partition: partition, region: region, errs: map[string]error{}, keys: map[string][]string{}, maxResults: map[string][]int32{}}
}

// arn is the fixture's ARN of resource, built independently of the scanner.
func (f *sfFixture) arn(resource string) string {
	return "arn:" + f.partition + ":states:" + f.region + ":" + testAccountID + ":" + resource
}

func (f *sfFixture) sm(name string) string { return f.arn("stateMachine:" + name) }

// call records op's call under key with in's MaxResults and returns the
// error errs holds for it.
func (f *sfFixture) call(op, key string, in any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[op] = append(f.keys[op], key)
	f.maxResults[op] = append(f.maxResults[op], bacField[int32](in, "MaxResults"))
	return f.errs[op+"/"+key]
}

func (f *sfFixture) called(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.keys[op]))
}

// parents is the state machines op was called for, sorted, each once.
func (f *sfFixture) parents(op string) []string {
	var out []string
	for _, k := range f.called(op) {
		sm, _, _ := strings.Cut(k, "/")
		out = append(out, sm)
	}
	return slices.Compact(out)
}

func sfPageKey(key string, token *string) string {
	if token == nil {
		return key
	}
	return strings.TrimPrefix(key+"/"+*token, "/")
}

func (f *sfFixture) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListStateMachines": func(in any) (any, error) {
			req := in.(*sfn.ListStateMachinesInput)
			if err := f.call("ListStateMachines", sfPageKey("", req.NextToken), in); err != nil {
				return nil, err
			}
			if req.NextToken == nil {
				return &sfn.ListStateMachinesOutput{StateMachines: []sfntypes.StateMachineListItem{
					{StateMachineArn: ptrStr(f.sm("sm-1")), Name: ptrStr("sm-1"), Type: sfntypes.StateMachineTypeStandard, CreationDate: &sfCreated},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &sfn.ListStateMachinesOutput{StateMachines: []sfntypes.StateMachineListItem{
				{StateMachineArn: ptrStr(f.sm("sm-2")), Name: ptrStr("sm-2"), Type: sfntypes.StateMachineTypeExpress, CreationDate: &sfCreated},
				{Name: ptrStr("nameless"), Type: sfntypes.StateMachineTypeStandard, CreationDate: &sfCreated},
			}}, nil
		},
		"DescribeStateMachine": func(in any) (any, error) {
			req := in.(*sfn.DescribeStateMachineInput)
			arn := sv(req.StateMachineArn)
			if err := f.call("DescribeStateMachine", sfPageKey(arn, nonEmptyPtr(string(req.IncludedData))), in); err != nil {
				return nil, err
			}
			_, name, _ := strings.Cut(arn, ":stateMachine:")
			out := &sfn.DescribeStateMachineOutput{
				StateMachineArn: &arn, Name: ptrStr("described-" + name), Status: sfntypes.StateMachineStatusActive,
				Type: sfntypes.StateMachineTypeStandard, CreationDate: &sfCreated,
				RoleArn: ptrStr("arn:" + f.partition + ":iam::" + testAccountID + ":role/sfn-" + name),
			}
			if name == "sm-2" {
				out.Status = sfntypes.StateMachineStatusDeleting
			}
			if req.IncludedData != sfntypes.IncludedDataMetadataOnly {
				out.Definition = ptrStr(`{"StartAt":"A"}`)
			}
			return out, nil
		},
		"ListActivities": func(in any) (any, error) {
			req := in.(*sfn.ListActivitiesInput)
			if err := f.call("ListActivities", sfPageKey("", req.NextToken), in); err != nil {
				return nil, err
			}
			if req.NextToken == nil {
				return &sfn.ListActivitiesOutput{Activities: []sfntypes.ActivityListItem{
					{ActivityArn: ptrStr(f.arn("activity:act-1")), Name: ptrStr("act-1"), CreationDate: &sfCreated},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &sfn.ListActivitiesOutput{Activities: []sfntypes.ActivityListItem{
				{ActivityArn: ptrStr(f.arn("activity:act-2")), Name: ptrStr("act-2"), CreationDate: &sfCreated},
				{CreationDate: &sfCreated},
			}}, nil
		},
		"ListStateMachineAliases": func(in any) (any, error) {
			req := in.(*sfn.ListStateMachineAliasesInput)
			smARN := sv(req.StateMachineArn)
			if err := f.call("ListStateMachineAliases", sfPageKey(smARN, req.NextToken), in); err != nil {
				return nil, err
			}
			alias := func(name string) sfntypes.StateMachineAliasListItem {
				return sfntypes.StateMachineAliasListItem{StateMachineAliasArn: nonEmptyPtr(smARN + ":" + name), CreationDate: &sfCreated}
			}
			switch {
			case smARN != f.sm("sm-1"):
				return &sfn.ListStateMachineAliasesOutput{StateMachineAliases: []sfntypes.StateMachineAliasListItem{alias("live")}}, nil
			case req.NextToken == nil:
				return &sfn.ListStateMachineAliasesOutput{StateMachineAliases: []sfntypes.StateMachineAliasListItem{alias("prod")}, NextToken: ptrStr("p2")}, nil
			}
			return &sfn.ListStateMachineAliasesOutput{StateMachineAliases: []sfntypes.StateMachineAliasListItem{alias("beta"), {CreationDate: &sfCreated}}}, nil
		},
		"ListStateMachineVersions": func(in any) (any, error) {
			req := in.(*sfn.ListStateMachineVersionsInput)
			smARN := sv(req.StateMachineArn)
			if err := f.call("ListStateMachineVersions", sfPageKey(smARN, req.NextToken), in); err != nil {
				return nil, err
			}
			version := func(n string) sfntypes.StateMachineVersionListItem {
				return sfntypes.StateMachineVersionListItem{StateMachineVersionArn: ptrStr(smARN + ":" + n), CreationDate: &sfCreated}
			}
			switch {
			case smARN != f.sm("sm-1"):
				return &sfn.ListStateMachineVersionsOutput{StateMachineVersions: []sfntypes.StateMachineVersionListItem{version("1"), {CreationDate: &sfCreated}}}, nil
			case req.NextToken == nil:
				return &sfn.ListStateMachineVersionsOutput{StateMachineVersions: []sfntypes.StateMachineVersionListItem{version("1")}, NextToken: ptrStr("p2")}, nil
			}
			return &sfn.ListStateMachineVersionsOutput{StateMachineVersions: []sfntypes.StateMachineVersionListItem{version("2")}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*sfn.ListTagsForResourceInput).ResourceArn)
			if err := f.call("ListTagsForResource", arn, in); err != nil {
				return nil, err
			}
			for _, name := range []string{"stateMachine:sm-1", "activity:act-1"} {
				if arn == f.arn(name) {
					_, id, _ := strings.Cut(name, ":")
					return &sfn.ListTagsForResourceOutput{Tags: []sfntypes.Tag{{Key: ptrStr("id"), Value: &id}}}, nil
				}
			}
			return &sfn.ListTagsForResourceOutput{}, nil
		},
	}
}

// taggedARNs is the ARN of every row whose tags the scan reads, sorted.
func (f *sfFixture) taggedARNs() []string {
	return slices.Sorted(slices.Values([]string{f.sm("sm-1"), f.sm("sm-2"), f.arn("activity:act-1"), f.arn("activity:act-2")}))
}

// scan runs the scan's real entry point against handlers in f's partition
// and region; every op handlers does not answer is denied.
func (f *sfFixture) scanWith(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(handlers, sfDenied)
	return scanSFN(ctx, acct, f.region, st, testScanID)
}

func (f *sfFixture) scan(ctx context.Context, st *store.Store) (int, int, error) {
	return f.scanWith(ctx, st, f.handlers())
}

// Every row is stored under the ARN its listing returned, and the state
// machines and activities with the tags ListTagsForResource returns (nil for
// none): the op is sent exactly their ARNs, never an alias's or a version's.
// Each state machine's attributes are its DescribeStateMachine body, its
// status that body's, its name the list element's. Elements without an ARN
// warn once per listing, named by their name, their state machine, or
// noIdentifier. Every listing sends MaxResults 1000.
func TestScanSFN_StoresTagsOfTaggedTypes(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newSFFixture("aws", testRegion)

	total, inserted, err := f.scan(context.Background(), st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != sfTotal || inserted != sfTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, sfTotal, sfTotal)
	}
	if got, want := f.called("ListTagsForResource"), f.taggedARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	sm1, sm2 := f.sm("sm-1"), f.sm("sm-2")
	wantIDs := map[string][]string{
		TypeSFNStateMachine:        {sm1, sm2},
		TypeSFNActivity:            {f.arn("activity:act-1"), f.arn("activity:act-2")},
		TypeSFNStateMachineAlias:   {sm1 + ":beta", sm1 + ":prod", sm2 + ":live"},
		TypeSFNStateMachineVersion: {sm1 + ":1", sm1 + ":2", sm2 + ":1"},
	}
	rows := map[string]map[string]store.Resource{}
	for typ, want := range wantIDs {
		rows[typ] = rowsByNativeID(t, st, typ)
		if got := slices.Sorted(maps.Keys(rows[typ])); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v; want %v", typ, got, want)
		}
	}
	tagged := map[string]string{sm1: "sm-1", f.arn("activity:act-1"): "act-1"}
	for _, byID := range rows {
		for id, r := range byID {
			if name, ok := tagged[id]; ok {
				assertTagsMap(t, r.TagsJSON, map[string]string{"id": name})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
	}
	for _, c := range []struct {
		typ, id, name, status, attr string
	}{
		{TypeSFNStateMachine, sm1, "sm-1", "ACTIVE", `"RoleArn":"arn:aws:iam::123456789012:role/sfn-sm-1"`},
		{TypeSFNStateMachine, sm2, "sm-2", "DELETING", `"Name":"described-sm-2"`},
		{TypeSFNActivity, f.arn("activity:act-1"), "act-1", "", `"Name":"act-1"`},
		{TypeSFNStateMachineAlias, sm1 + ":prod", "", "", `"StateMachineAliasArn":"` + sm1 + `:prod"`},
		{TypeSFNStateMachineVersion, sm2 + ":1", "", "", `"StateMachineVersionArn":"` + sm2 + `:1"`},
	} {
		r := rows[c.typ][c.id]
		if sv(r.Name) != c.name || sv(r.Status) != c.status || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, c.attr) {
			t.Errorf("%s name/status/region = %q/%q/%q, attrs %s; want %q/%q/%q, %s", c.id, sv(r.Name), sv(r.Status), sv(r.Region), r.AttributesJSON, c.name, c.status, testRegion, c.attr)
		}
		if c.status == "" && r.Status != nil {
			t.Errorf("%s Status = %q; want nil", c.id, *r.Status)
		}
		if c.name == "" && r.Name != nil {
			t.Errorf("%s Name = %q; want nil", c.id, *r.Name)
		}
		if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" {
			t.Errorf("%s provider/account/account name = %q/%q/%q; want aws/%s/Test Account", c.id, r.Provider, r.AccountID, sv(r.AccountName), testAccountID)
		}
		if r.CreatedAt != nil {
			t.Errorf("%s CreatedAt = %q; want nil", c.id, *r.CreatedAt)
		}
	}
	for op, want := range map[string][]int32{
		"ListStateMachines": {1000, 1000}, "ListActivities": {1000, 1000},
		"ListStateMachineAliases": {1000, 1000, 1000}, "ListStateMachineVersions": {1000, 1000, 1000},
	} {
		if got := f.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	wantKeys := map[string]string{
		sfnListStateMachinesOp:  "nameless",
		"states:ListActivities": noIdentifier,
		sfnListAliasesOp:        "an alias of " + sm1,
		sfnListVersionsOp:       "a version of " + sm2,
	}
	if w := warns(); len(w) != len(wantKeys) || !maps.Equal(lxNoIDKeys(w), wantKeys) {
		t.Errorf("warnings = %v; want one per listing with an element without an ARN, naming %v", w, wantKeys)
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanSFN_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSFFixture("aws", testRegion)
	if _, _, err := f.scan(context.Background(), st); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := f.scan(context.Background(), st)
	if err != nil || total != sfTotal {
		t.Errorf("rescan total = %d, err = %v; want %d, nil", total, err, sfTotal)
	}
}

// With no state machines and no activities, no per-state-machine op and no
// tag read is called.
func TestScanSFN_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSFFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListStateMachines"] = func(in any) (any, error) {
		return &sfn.ListStateMachinesOutput{}, f.call("ListStateMachines", "", in)
	}
	handlers["ListActivities"] = func(in any) (any, error) {
		return &sfn.ListActivitiesOutput{}, f.call("ListActivities", "", in)
	}

	total, _, err := f.scanWith(context.Background(), st, handlers)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	for _, op := range []string{"ListStateMachines", "ListActivities"} {
		if got := f.called(op); len(got) != 1 {
			t.Errorf("%s calls = %v; want 1", op, got)
		}
	}
	for _, op := range []string{"DescribeStateMachine", "ListStateMachineAliases", "ListStateMachineVersions", "ListTagsForResource"} {
		if got := f.called(op); len(got) != 0 {
			t.Errorf("%s calls = %v; want none", op, got)
		}
	}
}

// A failed tag read. ResourceNotFound is modeled, so a resource deleted since
// it was listed is dropped silently, and a state machine dropped so is no
// parent of the alias and version phases. A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanSFN_TagReadFailures(t *testing.T) {
	t.Parallel()
	noIDWarns := []string{sfnListStateMachinesOp, "states:ListActivities", sfnListAliasesOp, sfnListVersionsOp}
	for _, c := range []struct {
		name, resource, typ string
		err                 error
		outcome             serviceOutcome // -1: no error
		code                string
		wantTotal           int
		warnOps             []string
	}{
		{"state machine/not found", "stateMachine:sm-1", TypeSFNStateMachine, sfGone, -1, "", sfTotal - 5, []string{sfnListStateMachinesOp, "states:ListActivities", sfnListVersionsOp}},
		{"state machine/denied", "stateMachine:sm-1", TypeSFNStateMachine, sfDenied, -1, "", sfTotal, append([]string{sfnTagsOp}, noIDWarns...)},
		{"activity/not found", "activity:act-1", TypeSFNActivity, sfGone, -1, "", sfTotal - 1, noIDWarns},
		{"activity/transient", "activity:act-1", TypeSFNActivity, sfThrottle, outcomeTransient, "ThrottlingException", sfTotal, noIDWarns},
		{"activity/other error", "activity:act-1", TypeSFNActivity, sfBadARN, outcomeError, "InvalidArn", sfTotal, noIDWarns},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newSFFixture("aws", testRegion)
			arn := f.arn(c.resource)
			f.errs["ListTagsForResource/"+arn] = c.err

			total, _, err := f.scan(context.Background(), st)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, kept := rowsByNativeID(t, st, c.typ)[arn]
			if wantKept := c.err != sfGone; kept != wantKept {
				t.Fatalf("row stored = %v; want %v", kept, wantKept)
			}
			if kept && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if got := lxWarnOps(w); !slices.Equal(got, slices.Sorted(slices.Values(c.warnOps))) {
				t.Fatalf("warnings = %v; want ops %v", w, c.warnOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A state machine whose tag read finds it deleted is no parent: its aliases
// and versions are not read.
func TestScanSFN_StateMachineGoneHasNoChildren(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSFFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.sm("sm-1")] = sfGone

	if _, _, err := f.scan(context.Background(), st); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range []string{"ListStateMachineAliases", "ListStateMachineVersions"} {
		if got := f.parents(op); !slices.Equal(got, []string{f.sm("sm-2")}) {
			t.Errorf("%s keys = %v; want [%s]", op, got, f.sm("sm-2"))
		}
	}
}

// Denied tag reads of state machines and activities send one warning for the
// whole scan; the rows are kept.
func TestScanSFN_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, sfnTagsOp)
	f := newSFFixture("aws", testRegion)
	for _, r := range []string{"stateMachine:sm-2", "activity:act-2"} {
		f.errs["ListTagsForResource/"+f.arn(r)] = sfDenied
	}

	total, _, err := f.scan(context.Background(), st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != sfTotal || *warns != 1 {
		t.Errorf("total = %d, %s warnings = %d; want %d, 1", total, sfnTagsOp, *warns, sfTotal)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSFN_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, sfnTagsOp)
	f := newSFFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.sm("sm-2")] = sfThrottle
	f.errs["ListTagsForResource/"+f.arn("activity:act-1")] = sfBadARN

	_, _, err := f.scan(context.Background(), st)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArn")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", sfnTagsOp, *warns)
	}
}

// A failed DescribeStateMachine. StateMachineDoesNotExist is modeled, so a
// state machine deleted since it was listed is dropped silently, with no tag
// read and no children. A KmsAccessDeniedException or KmsInvalidStateException
// is asked again with METADATA_ONLY: its body is the row, with the RoleArn and
// Status but no definition, warned about once under DescribeStateMachine. Any
// other failure, the second ask's included, keeps the StateMachineListItem as
// the row, without a Status, its tags read and its aliases and versions
// listed: a denial warns once under DescribeStateMachine, a throttle (KMS's
// included) is transient, and any other error, a second
// KmsInvalidStateException included, is an error. Only those two KMS errors
// are asked again.
func TestScanSFN_DescribeFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name          string
		err, retryErr error
		outcome       serviceOutcome // -1: no error
		code          string
		warn          bool
		metadataBody  bool // the row is the METADATA_ONLY body
	}{
		{"not found", sfNoSM, nil, -1, "", false, false},
		{"denied", sfDenied, nil, -1, "", true, false},
		{"kms denied, metadata read", sfKMS, nil, -1, "", true, true},
		{"kms denied twice", sfKMS, sfKMS, -1, "", true, false},
		{"kms denied, metadata throttled", sfKMS, sfThrottle, outcomeTransient, "ThrottlingException", false, false},
		{"kms invalid state, metadata read", sfKMSOff, nil, -1, "", true, true},
		{"kms invalid state twice", sfKMSOff, sfKMSOff, outcomeError, "KmsInvalidStateException", false, false},
		{"transient", sfThrottle, nil, outcomeTransient, "ThrottlingException", false, false},
		{"kms throttle", sfKMSSlow, nil, outcomeTransient, "KmsThrottlingException", false, false},
		{"other error", sfBadARN, nil, outcomeError, "InvalidArn", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, sfnDescribeOp)
			f := newSFFixture("aws", testRegion)
			sm1 := f.sm("sm-1")
			metadataKey := sm1 + "/" + string(sfntypes.IncludedDataMetadataOnly)
			f.errs["DescribeStateMachine/"+sm1] = c.err
			if c.retryErr != nil {
				f.errs["DescribeStateMachine/"+metadataKey] = c.retryErr
			}

			total, _, err := f.scan(context.Background(), st)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if got, want := slices.Contains(f.called("DescribeStateMachine"), metadataKey), c.err == sfKMS || c.err == sfKMSOff; got != want {
				t.Errorf("asked METADATA_ONLY = %v; want %v (calls %v)", got, want, f.called("DescribeStateMachine"))
			}
			r, kept := rowsByNativeID(t, st, TypeSFNStateMachine)[sm1]
			tagRead := slices.Contains(f.called("ListTagsForResource"), sm1)
			childrenRead := slices.Contains(f.parents("ListStateMachineVersions"), sm1)
			if c.err == sfNoSM {
				if kept || tagRead || childrenRead || total != sfTotal-5 {
					t.Errorf("stored = %v, tags read = %v, children read = %v, total = %d; want none, total %d", kept, tagRead, childrenRead, total, sfTotal-5)
				}
				return
			}
			if !kept || !tagRead || !childrenRead || total != sfTotal {
				t.Fatalf("stored = %v, tags read = %v, children read = %v, total = %d; want all, total %d", kept, tagRead, childrenRead, total, sfTotal)
			}
			if c.metadataBody {
				if sv(r.Name) != "sm-1" || sv(r.Status) != "ACTIVE" || !strings.Contains(r.AttributesJSON, `"RoleArn":"arn:aws:iam::123456789012:role/sfn-sm-1"`) || strings.Contains(r.AttributesJSON, "StartAt") {
					t.Errorf("name = %q, status = %v, attrs = %s; want the METADATA_ONLY body", sv(r.Name), r.Status, r.AttributesJSON)
				}
			} else if sv(r.Name) != "sm-1" || r.Status != nil || strings.Contains(r.AttributesJSON, "RoleArn") || !strings.Contains(r.AttributesJSON, `"Type":"STANDARD"`) {
				t.Errorf("name = %q, status = %v, attrs = %s; want the StateMachineListItem", sv(r.Name), r.Status, r.AttributesJSON)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"id": "sm-1"})
			if want := map[bool]int{true: 1, false: 0}[c.warn]; *warns != want {
				t.Errorf("DescribeStateMachine warnings = %d; want %d", *warns, want)
			}
		})
	}
}

// A KMS denial of more than one state machine's definition is one warning for
// the whole scan, and none once a write failed. A state machine the tag read
// then finds deleted is not stored, so it is not warned about as stored
// without its definition.
func TestScanSFN_DefinitionsNotReadWarnOnce(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		kms       []string // state machines whose DescribeStateMachine fails with KMS
		gone      string   // a state machine whose tag read finds it deleted; "" for none
		refuse    string   // a type whose writes fail; "" for none
		wantErr   error
		wantWarns int
	}{
		{"stored", []string{"sm-1", "sm-2"}, "", "", nil, 1},
		{"write failed", []string{"sm-1", "sm-2"}, "", TypeSFNStateMachine, store.ErrStoreWrite, 0},
		{"dropped by the tag read", []string{"sm-1"}, "sm-1", "", nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			if c.refuse != "" {
				refuseStoreWrites(t, st, c.refuse)
			}
			f := newSFFixture("aws", testRegion)
			for _, sm := range c.kms {
				f.errs["DescribeStateMachine/"+f.sm(sm)] = sfKMS
			}
			if c.gone != "" {
				f.errs["ListTagsForResource/"+f.sm(c.gone)] = sfGone
			}

			_, _, err := f.scan(context.Background(), st)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v; want %v", err, c.wantErr)
			}
			if _, kept := rowsByNativeID(t, st, TypeSFNStateMachine)[f.sm(c.gone)]; c.gone != "" && kept {
				t.Fatalf("%s stored; want it dropped by the tag read", c.gone)
			}
			var got []store.ScanWarning
			for _, w := range warns() {
				if w.Service == sfnDescribeOp {
					got = append(got, w)
				}
			}
			if len(got) != c.wantWarns {
				t.Fatalf("DescribeStateMachine warnings = %+v; want %d", got, c.wantWarns)
			}
			if c.wantWarns == 1 && got[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// The client scanSFN builds retries KmsThrottlingException, which the SDK's
// standard retryer does not, and keeps a non-throttle client error
// unretried.
func TestSFNClientOptions_RetriesKmsThrottling(t *testing.T) {
	t.Parallel()
	kmsThrottle := &sfntypes.KmsThrottlingException{Message: ptrStr("slow down")}
	if retry.NewStandard().IsErrorRetryable(kmsThrottle) {
		t.Fatal("the SDK's standard retryer retries KmsThrottlingException; sfnClientOptions' retryer wrap is redundant")
	}
	cfg := sdkaws.Config{Retryer: func() sdkaws.Retryer { return retry.NewStandard() }}
	o := sfn.NewFromConfig(cfg, sfnClientOptions(testRegion)).Options()
	if !o.Retryer.IsErrorRetryable(kmsThrottle) {
		t.Error("sfn client does not retry KmsThrottlingException")
	}
	if o.Retryer.IsErrorRetryable(&sfntypes.InvalidArn{Message: ptrStr("bad")}) {
		t.Error("sfn client retries InvalidArn")
	}
	if o.Region != testRegion {
		t.Errorf("region = %q, want %q", o.Region, testRegion)
	}
}

// One state machine's failed alias or version listing keeps the other's rows
// and the pages read before it, and stops no later phase: a denial warns once
// under its op; a code the op models for a deleted state machine skips it
// silently; any other error is returned, among them ValidationException from
// ListStateMachineAliases and ResourceNotFound from ListStateMachineVersions,
// which neither op models. warns counts the op's warnings: a denial's, plus
// one for the element without an ARN when its page is read.
func TestScanSFN_PerStateMachineFailureKeepsSiblingsAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, key  string // key: the state machine, then "/p2" for its later page
		err      error
		returned bool
		typ      string
		wantRows []string // stored of typ, as state machine and qualifier
		warns    int
	}{
		{"ListStateMachineAliases", "sm-1/p2", sfBadARN, true, TypeSFNStateMachineAlias, []string{"sm-1:prod", "sm-2:live"}, 0},
		{"ListStateMachineAliases", "sm-1", sfInvalid, true, TypeSFNStateMachineAlias, []string{"sm-2:live"}, 0},
		{"ListStateMachineAliases", "sm-2", sfDenied, false, TypeSFNStateMachineAlias, []string{"sm-1:beta", "sm-1:prod"}, 2},
		{"ListStateMachineAliases", "sm-2", sfGone, false, TypeSFNStateMachineAlias, []string{"sm-1:beta", "sm-1:prod"}, 1},
		{"ListStateMachineAliases", "sm-2", sfNoSM, false, TypeSFNStateMachineAlias, []string{"sm-1:beta", "sm-1:prod"}, 1},
		{"ListStateMachineAliases", "sm-2", sfDeleting, false, TypeSFNStateMachineAlias, []string{"sm-1:beta", "sm-1:prod"}, 1},
		{"ListStateMachineVersions", "sm-1/p2", sfBadARN, true, TypeSFNStateMachineVersion, []string{"sm-1:1", "sm-2:1"}, 1},
		{"ListStateMachineVersions", "sm-1", sfInvalid, true, TypeSFNStateMachineVersion, []string{"sm-2:1"}, 1},
		{"ListStateMachineVersions", "sm-1", sfGone, true, TypeSFNStateMachineVersion, []string{"sm-2:1"}, 1},
		{"ListStateMachineVersions", "sm-2", sfDenied, false, TypeSFNStateMachineVersion, []string{"sm-1:1", "sm-1:2"}, 1},
	} {
		code := c.err.(*smithy.GenericAPIError).Code
		t.Run(c.op+"/"+c.key+"/"+code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "states:"+c.op)
			f := newSFFixture("aws", testRegion)
			sm, page, _ := strings.Cut(c.key, "/")
			f.errs[c.op+"/"+sfPageKey(f.sm(sm), nonEmptyPtr(page))] = c.err

			_, _, err := f.scan(context.Background(), st)
			if c.returned {
				assertAPICode(t, err, code)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.warns {
				t.Errorf("%s warnings = %d; want %d", c.op, *warns, c.warns)
			}
			want := make([]string, len(c.wantRows))
			for i, r := range c.wantRows {
				want[i] = f.arn("stateMachine:" + r)
			}
			if got := storedNativeIDs(t, st, c.typ); !slices.Equal(got, want) {
				t.Errorf("%s rows = %v; want %v", c.typ, got, want)
			}
			if c.op == "ListStateMachineAliases" {
				if n := len(rowsByNativeID(t, st, TypeSFNStateMachineVersion)); n != 3 {
					t.Errorf("version rows = %d; want 3, the phase after the failure", n)
				}
			}
		})
	}
}

// A failed later page of ListStateMachines or ListActivities keeps the page
// read before it, is returned, and stops no later phase; only the state
// machines stored are listed for aliases. A denied listing is warned about
// once under its op, with the scan's scope, instead, and the later phases
// run.
func TestScanSFN_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, key         string
		err               error
		deniedOp          string // the op a denial warns under; "" when err is returned
		wantSMs, wantActs []string
		wantParents       []string
	}{
		{"state machines later page", "ListStateMachines/p2", sfInvalid, "", []string{"sm-1"}, []string{"act-1", "act-2"}, []string{"sm-1"}},
		{"state machines denied", "ListStateMachines/", sfDenied, sfnListStateMachinesOp, nil, []string{"act-1", "act-2"}, nil},
		{"activities later page", "ListActivities/p2", sfInvalid, "", []string{"sm-1", "sm-2"}, []string{"act-1"}, []string{"sm-1", "sm-2"}},
		{"activities denied", "ListActivities/", sfDenied, "states:ListActivities", []string{"sm-1", "sm-2"}, nil, []string{"sm-1", "sm-2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newSFFixture("aws", testRegion)
			f.errs[c.key] = c.err

			_, _, err := f.scan(context.Background(), st)
			if c.deniedOp != "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				var denials []store.ScanWarning
				for _, w := range warns() {
					if w.Service == c.deniedOp {
						denials = append(denials, w)
					}
				}
				if len(denials) != 1 || denials[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("%s warnings = %v; want one with scope %s/%s", c.deniedOp, denials, testAccountID, testRegion)
				}
			} else {
				assertAPICode(t, err, "ValidationException")
			}
			ids := func(resource string, names []string) []string {
				out := make([]string, len(names))
				for i, n := range names {
					out[i] = f.arn(resource + n)
				}
				return out
			}
			if got, want := storedNativeIDs(t, st, TypeSFNStateMachine), ids("stateMachine:", c.wantSMs); !slices.Equal(got, want) {
				t.Errorf("state machine rows = %v; want %v", got, want)
			}
			if got, want := storedNativeIDs(t, st, TypeSFNActivity), ids("activity:", c.wantActs); !slices.Equal(got, want) {
				t.Errorf("activity rows = %v; want %v", got, want)
			}
			if got, want := f.parents("ListStateMachineVersions"), ids("stateMachine:", c.wantParents); !slices.Equal(got, want) {
				t.Errorf("ListStateMachineVersions state machines = %v; want %v, the state machines stored", got, want)
			}
		})
	}
}

// An NXDOMAIN from the first ListStateMachines page, with nothing stored, is
// a region that does not serve Step Functions: the scan is unavailable there
// and runs no later phase. Once state machines are stored, a later phase's
// NXDOMAIN is a failure of this scan instead, and the stored rows keep their
// counts.
func TestScanSFN_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "states.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newSFFixture("aws", testRegion)
		f.errs["ListStateMachines/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		if got := f.called("ListActivities"); len(got) != 0 {
			t.Errorf("ListActivities calls = %v; want none once the region proved unavailable", got)
		}
	})
	t.Run("after state machines were stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newSFFixture("aws", testRegion)
		f.errs["ListActivities/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeError)
		if want := sfTotal - 2; total != want {
			t.Errorf("total = %d; want the %d rows stored but the activities", total, want)
		}
	})
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phases are not run, and the rows
// stored before it are kept.
func TestScanSFN_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSFFixture("aws", testRegion)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := f.handlers()
	handlers["ListActivities"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	total, _, err := f.scanWith(ctx, st, handlers)
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
	if total != 2 {
		t.Errorf("total = %d; want the 2 state machines stored before ctx ended", total)
	}
	if got := f.called("ListStateMachineAliases"); len(got) != 0 {
		t.Errorf("ListStateMachineAliases calls = %v; want none after ctx ended", got)
	}
}

// A repeated NextToken ends the state machine listing with
// errRepeatedNextToken after its first page is stored.
func TestScanSFN_RepeatedStateMachineToken(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newSFFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListStateMachines"] = func(any) (any, error) {
		return &sfn.ListStateMachinesOutput{StateMachines: []sfntypes.StateMachineListItem{{StateMachineArn: ptrStr(f.sm("sm-1")), Name: ptrStr("sm-1")}}, NextToken: ptrStr("same")}, nil
	}

	_, _, err := f.scanWith(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if got := storedNativeIDs(t, st, TypeSFNStateMachine); !slices.Equal(got, []string{f.sm("sm-1")}) {
		t.Errorf("state machine rows = %v; want [%s]", got, f.sm("sm-1"))
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the rows of the phases
// before it stay stored. act-1's tag read is denied, so its warning is sent
// only when the activities were stored.
func TestScanSFN_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		refuse     string
		wantStored map[string]int
		wantWarns  []string
		notCalled  []string
	}{
		{
			TypeSFNStateMachine,
			map[string]int{TypeSFNStateMachine: 0},
			nil,
			[]string{"ListActivities", "ListStateMachineAliases", "ListStateMachineVersions"},
		},
		{
			TypeSFNActivity,
			map[string]int{TypeSFNStateMachine: 2, TypeSFNActivity: 0},
			[]string{sfnListStateMachinesOp},
			[]string{"ListStateMachineAliases", "ListStateMachineVersions"},
		},
		{
			TypeSFNStateMachineAlias,
			map[string]int{TypeSFNStateMachine: 2, TypeSFNActivity: 2, TypeSFNStateMachineAlias: 0},
			[]string{sfnListStateMachinesOp, "states:ListActivities", sfnTagsOp},
			[]string{"ListStateMachineVersions"},
		},
	} {
		t.Run(c.refuse, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.refuse)
			f := newSFFixture("aws", testRegion)
			f.errs["ListTagsForResource/"+f.arn("activity:act-1")] = sfDenied

			total, _, err := f.scan(context.Background(), st)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if got, want := lxWarnOps(warns()), slices.Sorted(slices.Values(c.wantWarns)); !slices.Equal(got, want) {
				t.Errorf("warning ops = %v; want %v, those sent before the failed write", got, want)
			}
			for _, op := range c.notCalled {
				if got := f.called(op); len(got) != 0 {
					t.Errorf("%s calls = %v; want none after the failed write", op, got)
				}
			}
			wantTotal := 0
			for typ, want := range c.wantStored {
				wantTotal += want
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d; want %d", typ, n, want)
				}
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
		})
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID.
func TestScanSFN_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range partitionFixtures {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newSFFixture(c.partition, c.region)
			if _, _, err := f.scan(context.Background(), st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := f.called("ListTagsForResource"), f.taggedARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for typ, resource := range map[string]string{TypeSFNStateMachine: "stateMachine:sm-1", TypeSFNActivity: "activity:act-1"} {
				_, name, _ := strings.Cut(resource, ":")
				assertTagsMap(t, rowsByNativeID(t, st, typ)[f.arn(resource)].TagsJSON, map[string]string{"id": name})
			}
		})
	}
}
