package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/auditmanager"
	amtypes "github.com/aws/aws-sdk-go-v2/service/auditmanager/types"
	"github.com/icearp/disco-cli/store"
)

func audmTestARN(resource string) string {
	return fmt.Sprintf("arn:aws:auditmanager:%s:%s:%s", testRegion, testAccountID, resource)
}

const (
	audmTestA1 = "11111111-1111-1111-1111-111111111111"
	audmTestA2 = "22222222-2222-2222-2222-222222222222"
)

var (
	audmTestAssess1 = audmTestARN("assessment/" + audmTestA1)
	audmTestAssess2 = audmTestARN("assessment/" + audmTestA2)
	audmTestFwC1    = audmTestARN("assessmentFramework/fw-c1")
	audmTestFwC2    = audmTestARN("assessmentFramework/fw-c2")
	audmTestFwS1    = audmTestARN("assessmentFramework/fw-s1")
	audmTestCtlC1   = audmTestARN("control/ctl-c1")
	audmTestCtlC2   = audmTestARN("control/ctl-c2")
	audmTestCtlS1   = audmTestARN("control/ctl-s1")

	audmDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: auditmanager:List")
	audmNotSetUp   = apiErr("AccessDeniedException", "Please complete AWS Audit Manager setup from home page to enable this action in this account.")
	audmThrottle   = apiErr("ThrottlingException", "slow down")
	audmBadInput   = apiErr("ValidationException", "bad input")
	audmGoneErr    = apiErr("ResourceNotFoundException", "gone")
	audmTestFwARN  = audmTestARN("assessmentFramework/fw-used")
	audmTestRoleIn = "arn:aws:iam::" + testAccountID + ":role/audit"

	// audmTestTags are the tags ListTagsForResource returns; every other
	// framework and control has none.
	audmTestTags = map[string]map[string]string{
		audmTestFwC1:  {"team": "grc"},
		audmTestCtlC2: {"owner": "sec", "tier": "1"},
		audmTestFwS1:  {"ComplianceType": "PCI DSS"},
	}
	// audmTestAssessTags are the tags GetAssessment returns for assessment 1.
	audmTestAssessTags = map[string]string{"env": "prod"}
)

// audmTestTotal is every row audmHandlers yields: 2 assessments, 3
// frameworks and 3 controls.
const audmTestTotal = 8

// audmListCall is one listing call: the kind it was sent (FrameworkType or
// ControlType; "" for ListAssessments) and its MaxResults.
type audmListCall struct {
	kind       string
	maxResults *int32
}

// audmCalls records the calls audmHandlers answers, and holds the errors its
// ListTagsForResource returns per ARN and its GetAssessment returns per id.
type audmCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent; getIDs every id
	// GetAssessment was sent; lists every listing op called.
	tagARNs, getIDs, lists parentCalls
	tags                   map[string]map[string]string
	tagErrs, getErrs       map[string]error

	mu        sync.Mutex
	listCalls map[string][]audmListCall
}

// listed records a listing call of op and reports whether it asks for the
// first page.
func (c *audmCalls) listed(op, kind string, in any) bool {
	c.lists.add(op)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listCalls == nil {
		c.listCalls = map[string][]audmListCall{}
	}
	c.listCalls[op] = append(c.listCalls[op], audmListCall{kind, bacField[*int32](in, "MaxResults")})
	return bacField[*string](in, "NextToken") == nil
}

func (c *audmCalls) listTags(in any) (any, error) {
	arn := sv(in.(*auditmanager.ListTagsForResourceInput).ResourceArn)
	c.tagARNs.add(arn)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = audmTestTags[arn]
	}
	return &auditmanager.ListTagsForResourceOutput{Tags: tags}, nil
}

// audmHandlers answers a region with assessment 1 on page 1 and 2 on page 2
// (1 tagged, active, using a framework, a role and a report bucket; 2
// inactive and untagged); custom frameworks 1 on page 1 and 2 on page 2 and
// standard framework 1; custom controls 1 on page 1 and 2 on page 2 and
// standard control 1.
func audmHandlers(calls *audmCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListAssessments": func(in any) (any, error) {
			if calls.listed("ListAssessments", "", in) {
				return &auditmanager.ListAssessmentsOutput{
					AssessmentMetadata: []amtypes.AssessmentMetadataItem{{Id: sdkaws.String(audmTestA1), Name: sdkaws.String("SOC 2 audit")}},
					NextToken:          &page2,
				}, nil
			}
			return &auditmanager.ListAssessmentsOutput{AssessmentMetadata: []amtypes.AssessmentMetadataItem{{Id: sdkaws.String(audmTestA2), Name: sdkaws.String("PCI audit")}}}, nil
		},
		"GetAssessment": func(in any) (any, error) {
			id := sv(in.(*auditmanager.GetAssessmentInput).AssessmentId)
			calls.getIDs.add(id)
			if err, ok := calls.getErrs[id]; ok {
				return nil, err
			}
			switch id {
			case audmTestA1:
				return &auditmanager.GetAssessmentOutput{Assessment: &amtypes.Assessment{
					Arn:       &audmTestAssess1,
					Framework: &amtypes.AssessmentFramework{Arn: &audmTestFwARN},
					Metadata: &amtypes.AssessmentMetadata{
						Id: sdkaws.String(audmTestA1), Name: sdkaws.String("SOC 2 audit"), Status: amtypes.AssessmentStatusActive,
						Roles: []amtypes.Role{{RoleArn: &audmTestRoleIn, RoleType: amtypes.RoleTypeProcessOwner}},
						AssessmentReportsDestination: &amtypes.AssessmentReportsDestination{
							Destination: sdkaws.String("s3://audit-reports"), DestinationType: amtypes.AssessmentReportDestinationTypeS3,
						},
					},
					Tags: audmTestAssessTags,
				}}, nil
			case audmTestA2:
				return &auditmanager.GetAssessmentOutput{Assessment: &amtypes.Assessment{
					Arn:      &audmTestAssess2,
					Metadata: &amtypes.AssessmentMetadata{Id: sdkaws.String(audmTestA2), Name: sdkaws.String("PCI audit"), Status: amtypes.AssessmentStatusInactive},
				}}, nil
			}
			return nil, audmGoneErr
		},
		"ListAssessmentFrameworks": func(in any) (any, error) {
			kind := in.(*auditmanager.ListAssessmentFrameworksInput).FrameworkType
			first := calls.listed("ListAssessmentFrameworks", string(kind), in)
			fw := func(arn, name string, ft amtypes.FrameworkType) amtypes.AssessmentFrameworkMetadata {
				return amtypes.AssessmentFrameworkMetadata{
					Arn: sdkaws.String(arn), Name: sdkaws.String(name), Type: ft, ComplianceType: sdkaws.String("SOC2"), ControlsCount: 3,
				}
			}
			out := &auditmanager.ListAssessmentFrameworksOutput{}
			switch {
			case kind == amtypes.FrameworkTypeCustom && first:
				out.FrameworkMetadataList = []amtypes.AssessmentFrameworkMetadata{fw(audmTestFwC1, "Custom one", kind)}
				out.NextToken = &page2
			case kind == amtypes.FrameworkTypeCustom:
				out.FrameworkMetadataList = []amtypes.AssessmentFrameworkMetadata{fw(audmTestFwC2, "Custom two", kind)}
			case kind == amtypes.FrameworkTypeStandard:
				out.FrameworkMetadataList = []amtypes.AssessmentFrameworkMetadata{fw(audmTestFwS1, "PCI DSS v4.0", kind)}
			default:
				return nil, audmBadInput
			}
			return out, nil
		},
		"ListControls": func(in any) (any, error) {
			kind := in.(*auditmanager.ListControlsInput).ControlType
			first := calls.listed("ListControls", string(kind), in)
			ctl := func(arn, name string) amtypes.ControlMetadata {
				return amtypes.ControlMetadata{Arn: sdkaws.String(arn), Name: sdkaws.String(name), ControlSources: sdkaws.String("AWS Config")}
			}
			out := &auditmanager.ListControlsOutput{}
			switch {
			case kind == amtypes.ControlTypeCustom && first:
				out.ControlMetadataList = []amtypes.ControlMetadata{ctl(audmTestCtlC1, "Custom control one")}
				out.NextToken = &page2
			case kind == amtypes.ControlTypeCustom:
				out.ControlMetadataList = []amtypes.ControlMetadata{ctl(audmTestCtlC2, "Custom control two")}
			case kind == amtypes.ControlTypeStandard:
				out.ControlMetadataList = []amtypes.ControlMetadata{ctl(audmTestCtlS1, "1.2.3 MFA enabled")}
			default:
				return nil, audmBadInput
			}
			return out, nil
		},
		"ListTagsForResource": calls.listTags,
	}
}

// audmScan runs the service scan entry point against handlers; an op without
// a handler is denied.
func audmScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, audmDenied)
	return scanAuditManager(ctx, acct, testRegion, st, testScanID)
}

func audmSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

func audmAllIDs() []string {
	return audmSorted(audmTestAssess1, audmTestAssess2, audmTestFwC1, audmTestFwC2, audmTestFwS1, audmTestCtlC1, audmTestCtlC2, audmTestCtlS1)
}

// audmLibraryIDs is the NativeID of every framework and control: the ARNs
// sent to ListTagsForResource.
func audmLibraryIDs() []string {
	return audmSorted(audmTestFwC1, audmTestFwC2, audmTestFwS1, audmTestCtlC1, audmTestCtlC2, audmTestCtlS1)
}

// audmStored is the NativeID of every stored row of the service, sorted.
func audmStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeAuditManagerAssessment, TypeAuditManagerFramework, TypeAuditManagerControl} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func audmWithout(drop ...string) []string {
	return slices.DeleteFunc(audmAllIDs(), func(id string) bool { return slices.Contains(drop, id) })
}

// audmPageIs reports whether in, op's input, asks for page (1: the first,
// any other: a later one) of the listing of kind ("" for any kind).
func audmPageIs(in any, op, kind string, page int) bool {
	field := map[string]string{"ListAssessmentFrameworks": "FrameworkType", "ListControls": "ControlType"}[op]
	if kind != "" && fmt.Sprint(bacField[any](in, field)) != kind {
		return false
	}
	return (bacField[*string](in, "NextToken") == nil) == (page == 1)
}

// audmFailPage makes page of op's listing of kind fail with err
// (audmPageIs).
func audmFailPage(h map[string]func(in any) (any, error), op, kind string, page int, err error) {
	list := h[op]
	h[op] = func(in any) (any, error) {
		if audmPageIs(in, op, kind, page) {
			return nil, err
		}
		return list(in)
	}
}

// Every row is stored from every page under its ARN, named and with the
// status its element gives, its attributes the GetAssessment body or the
// listed element. Assessments carry the tags GetAssessment returns;
// frameworks and controls the tags ListTagsForResource returns (nil for
// none), the op sent exactly each one's ARN, once; standard ones are stored
// managed. Every listing is sent MaxResults
// 1000, and frameworks and controls are listed Custom then Standard. No
// error, no warning.
func TestScanAuditManager_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls audmCalls
	counted, upserts := countUpserts(st)
	total, err := audmScan(context.Background(), counted, audmHandlers(&calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != audmTestTotal || inserted != audmTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, audmTestTotal, audmTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, audmLibraryIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, audmLibraryIDs())
	}
	if n := len(calls.tagARNs.ids); n != 6 {
		t.Errorf("ListTagsForResource calls = %d; want one per framework and control, 6", n)
	}
	if got, want := audmSorted(calls.getIDs.ids...), []string{audmTestA1, audmTestA2}; !slices.Equal(got, want) {
		t.Errorf("GetAssessment ids = %v; want %v", got, want)
	}
	for op, want := range map[string][]string{
		"ListAssessments":          {"", ""},
		"ListAssessmentFrameworks": {"Custom", "Custom", "Standard"},
		"ListControls":             {"Custom", "Custom", "Standard"},
	} {
		var kinds []string
		for _, c := range calls.listCalls[op] {
			kinds = append(kinds, c.kind)
			if c.maxResults == nil || *c.maxResults != 1000 {
				t.Errorf("%s MaxResults = %v; want 1000", op, c.maxResults)
			}
		}
		if !slices.Equal(kinds, want) {
			t.Errorf("%s calls sent %q; want %q", op, kinds, want)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	for _, c := range []struct {
		typ, id      string
		name         string
		status       *string
		managed      bool
		tags         map[string]string
		attr         func(string) bool
		wantTagsRead bool
	}{
		{TypeAuditManagerAssessment, audmTestAssess1, "SOC 2 audit", sdkaws.String("ACTIVE"), false, audmTestAssessTags, func(a string) bool {
			var x auditManagerAssessmentAttrs
			return json.Unmarshal([]byte(a), &x) == nil && x.Framework != nil && sv(x.Framework.Arn) == audmTestFwARN &&
				x.Metadata != nil && len(x.Metadata.Roles) == 1 && sv(x.Metadata.Roles[0].RoleArn) == audmTestRoleIn &&
				x.Metadata.AssessmentReportsDestination != nil && sv(x.Metadata.AssessmentReportsDestination.Destination) == "s3://audit-reports"
		}, false},
		{TypeAuditManagerAssessment, audmTestAssess2, "PCI audit", sdkaws.String("INACTIVE"), false, nil, nil, false},
		{TypeAuditManagerFramework, audmTestFwC1, "Custom one", nil, false, audmTestTags[audmTestFwC1], func(a string) bool {
			var f amtypes.AssessmentFrameworkMetadata
			return json.Unmarshal([]byte(a), &f) == nil && sv(f.ComplianceType) == "SOC2" && f.ControlsCount == 3 && f.Type == amtypes.FrameworkTypeCustom
		}, true},
		{TypeAuditManagerFramework, audmTestFwC2, "Custom two", nil, false, nil, nil, true},
		{TypeAuditManagerFramework, audmTestFwS1, "PCI DSS v4.0", nil, true, audmTestTags[audmTestFwS1], func(a string) bool {
			var f amtypes.AssessmentFrameworkMetadata
			return json.Unmarshal([]byte(a), &f) == nil && f.Type == amtypes.FrameworkTypeStandard
		}, true},
		{TypeAuditManagerControl, audmTestCtlC1, "Custom control one", nil, false, nil, func(a string) bool {
			var m amtypes.ControlMetadata
			return json.Unmarshal([]byte(a), &m) == nil && sv(m.ControlSources) == "AWS Config"
		}, true},
		{TypeAuditManagerControl, audmTestCtlC2, "Custom control two", nil, false, audmTestTags[audmTestCtlC2], nil, true},
		{TypeAuditManagerControl, audmTestCtlS1, "1.2.3 MFA enabled", nil, true, nil, nil, true},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			if sv(r.Name) != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			if sv(r.Status) != sv(c.status) || (r.Status == nil) != (c.status == nil) {
				t.Errorf("Status = %v; want %v", r.Status, c.status)
			}
			if r.CreatedAt != nil {
				t.Errorf("CreatedAt = %v; want none", *r.CreatedAt)
			}
			if sv(r.Region) != testRegion || r.AccountID != testAccountID || r.ManagedByProvider != c.managed {
				t.Errorf("Region, AccountID, ManagedByProvider = %q, %q, %v; want %s, %s, %v", sv(r.Region), r.AccountID, r.ManagedByProvider, testRegion, testAccountID, c.managed)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the SDK element", r.AttributesJSON)
			}
			if c.tags != nil {
				assertTagsMap(t, r.TagsJSON, c.tags)
			} else if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if read := slices.Contains(calls.tagARNs.ids, c.id); read != c.wantTagsRead {
				t.Errorf("tags read with ListTagsForResource = %v; want %v", read, c.wantTagsRead)
			}
		})
	}
}

// A region with no assessment, framework or control stores nothing, reads
// no assessment or tags, and warns about nothing.
func TestScanAuditManager_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls audmCalls
	h := audmHandlers(&calls)
	h["ListAssessments"] = func(any) (any, error) { return &auditmanager.ListAssessmentsOutput{}, nil }
	h["ListAssessmentFrameworks"] = func(any) (any, error) { return &auditmanager.ListAssessmentFrameworksOutput{}, nil }
	h["ListControls"] = func(any) (any, error) { return &auditmanager.ListControlsOutput{}, nil }
	counted, upserts := countUpserts(st)
	total, err := audmScan(context.Background(), counted, h)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if len(calls.getIDs.ids) != 0 || len(calls.tagARNs.ids) != 0 {
		t.Errorf("GetAssessment ids, tag ARNs = %v, %v; want none", calls.getIDs.ids, calls.tagARNs.ids)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a framework
// or control deleted since it was listed is dropped silently. A denial keeps
// the row untagged and warns once under the tag op with the scan's scope; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanAuditManager_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"framework/not found", audmTestFwC1, TypeAuditManagerFramework, audmGoneErr, -1, "", false, false},
		{"control/not found", audmTestCtlC2, TypeAuditManagerControl, audmGoneErr, -1, "", false, false},
		{"framework/denied", audmTestFwC1, TypeAuditManagerFramework, audmDenied, -1, "", true, true},
		{"control/transient", audmTestCtlC1, TypeAuditManagerControl, audmThrottle, outcomeTransient, "ThrottlingException", true, false},
		{"control/other error", audmTestCtlC2, TypeAuditManagerControl, audmBadInput, outcomeError, "ValidationException", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := audmCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, err := audmScan(context.Background(), st, audmHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := audmAllIDs()
			if !c.wantKept {
				want = audmWithout(c.arn)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := audmStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if r, ok := rowsByNativeID(t, st, c.typ)[c.arn]; ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var wantOps []string
			if c.warn {
				wantOps = []string{audmListTagsForResOp}
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

// Denied tag reads of frameworks and controls send one warning for the
// whole scan; the rows are kept.
func TestScanAuditManager_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := audmCalls{tagErrs: map[string]error{audmTestFwC2: audmDenied, audmTestCtlC1: audmDenied}}
	total, err := audmScan(context.Background(), st, audmHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != audmTestTotal {
		t.Errorf("total = %d; want %d", total, audmTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{audmListTagsForResOp}) {
		t.Errorf("warnings = %v; want one under %s", got, audmListTagsForResOp)
	}
}

// A throttle beside a hard error is demoted to one warning under its own op,
// so the hard error decides the outcome: a throttled tag read beside a
// failed GetAssessment, a throttled GetAssessment beside a failed tag read,
// and a throttled GetAssessment beside a failed one.
func TestScanAuditManager_ThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		calls *audmCalls
		warn  string
	}{
		{"tag read", &audmCalls{tagErrs: map[string]error{audmTestFwC2: audmThrottle}, getErrs: map[string]error{audmTestA1: audmBadInput}}, audmListTagsForResOp},
		{"GetAssessment", &audmCalls{tagErrs: map[string]error{audmTestCtlC1: audmBadInput}, getErrs: map[string]error{audmTestA2: audmThrottle}}, audmGetAssessmentOp},
		{"GetAssessment beside GetAssessment", &audmCalls{getErrs: map[string]error{audmTestA1: audmBadInput, audmTestA2: audmThrottle}}, audmGetAssessmentOp},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			_, err := audmScan(context.Background(), st, audmHandlers(c.calls))
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if got := amWarnings(warns()); !slices.Equal(got, []string{c.warn}) {
				t.Errorf("warnings = %v; want one under %s", got, c.warn)
			}
		})
	}
}

// A failed GetAssessment: the listed element has no ARN, so the assessment
// is not stored. ResourceNotFoundException (modeled) drops it silently; a
// denial warns once under GetAssessment; a throttle is transient; any other
// error is an error. A read without an ARN, or without a body, is warned
// about under GetAssessment. The other assessment and the later phases are
// stored in every case.
func TestScanAuditManager_GetAssessmentFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		err     error
		out     *auditmanager.GetAssessmentOutput
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}{
		{"not found", audmGoneErr, nil, -1, "", false},
		{"denied", audmDenied, nil, -1, "", true},
		{"transient", audmThrottle, nil, outcomeTransient, "ThrottlingException", false},
		{"other error", audmBadInput, nil, outcomeError, "ValidationException", false},
		{"no ARN", nil, &auditmanager.GetAssessmentOutput{Assessment: &amtypes.Assessment{Metadata: &amtypes.AssessmentMetadata{Name: sdkaws.String("PCI audit")}}}, -1, "", true},
		{"no body", nil, &auditmanager.GetAssessmentOutput{}, -1, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls audmCalls
			h := audmHandlers(&calls)
			get := h["GetAssessment"]
			h["GetAssessment"] = func(in any) (any, error) {
				if sv(in.(*auditmanager.GetAssessmentInput).AssessmentId) != audmTestA2 {
					return get(in)
				}
				if c.out != nil {
					return c.out, nil
				}
				return nil, c.err
			}
			total, err := audmScan(context.Background(), st, h)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := audmWithout(audmTestAssess2)
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := audmStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{audmGetAssessmentOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// Every GetAssessment throttled is one transient error for the scan, not one
// per assessment, and the later phases are stored.
func TestScanAuditManager_GetAssessmentThrottlesAreOneError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := audmCalls{getErrs: map[string]error{audmTestA1: audmThrottle, audmTestA2: audmThrottle}}
	total, err := audmScan(context.Background(), st, audmHandlers(&calls))
	assertGlueOutcome(t, err, outcomeTransient)
	if n := len(bacTopMembers(err)); n != 1 {
		t.Errorf("error members = %d; want 1 (err %v)", n, err)
	}
	if want := audmTestTotal - 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
}

// A listed element without the identifier its row needs is not stored and
// is warned about once under its listing op, and its siblings are stored:
// an assessment without an id is not read, and a framework or control
// without an ARN has no tags read.
func TestScanAuditManager_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, kind string
		wrap     func(out any)
	}{
		{"ListAssessments", "", func(out any) {
			o := out.(*auditmanager.ListAssessmentsOutput)
			o.AssessmentMetadata = append(o.AssessmentMetadata, amtypes.AssessmentMetadataItem{Name: sdkaws.String("no id")})
		}},
		{"ListAssessmentFrameworks", "Custom", func(out any) {
			o := out.(*auditmanager.ListAssessmentFrameworksOutput)
			o.FrameworkMetadataList = append(o.FrameworkMetadataList, amtypes.AssessmentFrameworkMetadata{Name: sdkaws.String("no arn")})
		}},
		{"ListControls", "Standard", func(out any) {
			o := out.(*auditmanager.ListControlsOutput)
			o.ControlMetadataList = append(o.ControlMetadataList, amtypes.ControlMetadata{Name: sdkaws.String("no arn")})
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls audmCalls
			h := audmHandlers(&calls)
			list := h[c.op]
			h[c.op] = func(in any) (any, error) {
				out, err := list(in)
				if err == nil && audmPageIs(in, c.op, c.kind, 1) {
					c.wrap(out)
				}
				return out, err
			}
			total, err := audmScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != audmTestTotal {
				t.Errorf("total = %d; want %d", total, audmTestTotal)
			}
			if got := calls.tagARNs.distinct(); !slices.Equal(got, audmLibraryIDs()) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, audmLibraryIDs())
			}
			if got, want := audmSorted(calls.getIDs.ids...), []string{audmTestA1, audmTestA2}; !slices.Equal(got, want) {
				t.Errorf("GetAssessment ids = %v; want %v", got, want)
			}
			if got, want := amWarnings(warns()), []string{"auditmanager:" + c.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// Audit Manager not set up: refused so on the first page of the first
// listing, the scan is reported disabled, no other listing is called and
// nothing is warned about. Refused so on ListAssessmentFrameworks after a
// denied ListAssessments stored nothing, it is disabled too, with the denial
// warned about. Refused so once rows were stored, it is the denial it reads
// as: warned once, the rows kept, no error. Once ctx ends after the refusal,
// the stop is reported, not the disabled sentinel.
func TestScanAuditManager_NotEnabled(t *testing.T) {
	t.Parallel()
	t.Run("first listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls audmCalls
		h := audmHandlers(&calls)
		audmFailPage(h, "ListAssessments", "", 1, audmNotSetUp)
		counted, upserts := countUpserts(st)
		total, err := audmScan(context.Background(), counted, h)
		inserted := upserts()
		if !errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want errServiceDisabled", err)
		}
		if total != 0 || inserted != 0 {
			t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
		}
		if got := calls.lists.distinct(); len(got) != 0 {
			t.Errorf("listings recorded = %v; want none past the refused one", got)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("after a denied listing stored nothing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls audmCalls
		h := audmHandlers(&calls)
		audmFailPage(h, "ListAssessments", "", 1, audmDenied)
		audmFailPage(h, "ListAssessmentFrameworks", "Custom", 1, audmNotSetUp)
		_, err := audmScan(context.Background(), st, h)
		if !errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want errServiceDisabled", err)
		}
		if got := calls.lists.distinct(); len(got) != 0 {
			t.Errorf("listings recorded = %v; want none past the refused ones", got)
		}
		if got := amWarnings(warns()); !slices.Equal(got, []string{audmListAssessmentsOp}) {
			t.Errorf("warnings = %v; want one under %s", got, audmListAssessmentsOp)
		}
	})
	t.Run("after rows were stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls audmCalls
		h := audmHandlers(&calls)
		audmFailPage(h, "ListControls", "", 1, audmNotSetUp)
		total, err := audmScan(context.Background(), st, h)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		want := audmWithout(audmTestCtlC1, audmTestCtlC2, audmTestCtlS1)
		if total != len(want) {
			t.Errorf("total = %d; want %d", total, len(want))
		}
		if got := audmStored(t, st); !slices.Equal(got, want) {
			t.Errorf("stored = %v; want %v", got, want)
		}
		if got := amWarnings(warns()); !slices.Equal(got, []string{audmListControlsOp}) {
			t.Errorf("warnings = %v; want one under %s", got, audmListControlsOp)
		}
	})
	t.Run("then ctx ends", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls audmCalls
		h := audmHandlers(&calls)
		h["ListAssessments"] = func(any) (any, error) {
			cancel()
			return nil, audmNotSetUp
		}
		_, err := audmScan(ctx, st, h)
		if !errors.Is(err, context.Canceled) || errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want context.Canceled, not errServiceDisabled", err)
		}
	})
}

// A listing whose later page fails keeps its first page, and the other kind
// and the later phases still run.
func TestScanAuditManager_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op, kind string
		lost     []string
	}{
		{"ListAssessments", "", []string{audmTestAssess2}},
		{"ListAssessmentFrameworks", "Custom", []string{audmTestFwC2}},
		{"ListControls", "Custom", []string{audmTestCtlC2}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			var calls audmCalls
			h := audmHandlers(&calls)
			audmFailPage(h, tc.op, tc.kind, 2, audmBadInput)
			total, err := audmScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			want := audmWithout(tc.lost...)
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := audmStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; the pages
// read before it are kept, and the other kind and the later phases still
// run. Both kinds of a library denied send one warning.
func TestScanAuditManager_ListDeniedWarnsAndContinues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, op, kind string
		page           int
		lost           []string
	}{
		{"assessments", "ListAssessments", "", 1, []string{audmTestAssess1, audmTestAssess2}},
		{"frameworks, both kinds", "ListAssessmentFrameworks", "", 1, []string{audmTestFwC1, audmTestFwC2, audmTestFwS1}},
		{"custom controls, later page", "ListControls", "Custom", 2, []string{audmTestCtlC2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls audmCalls
			h := audmHandlers(&calls)
			audmFailPage(h, tc.op, tc.kind, tc.page, audmDenied)
			if _, err := audmScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := audmStored(t, st), audmWithout(tc.lost...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if got, want := amWarnings(warns()), []string{"auditmanager:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A library whose kinds are denied at different pages sends one warning
// under its op carrying both denials: the custom kind's later-page denial is
// not overwritten by the standard kind's first-page one. The custom first
// page is kept.
func TestScanAuditManager_ListDenialOfEachKindWarned(t *testing.T) {
	t.Parallel()
	laterPage := apiErr("AccessDeniedException", "custom-page-2-denied")
	firstPage := apiErr("AccessDeniedException", "standard-page-1-denied")
	for _, tc := range []struct {
		op   string
		lost []string
	}{
		{"ListAssessmentFrameworks", []string{audmTestFwC2, audmTestFwS1}},
		{"ListControls", []string{audmTestCtlC2, audmTestCtlS1}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls audmCalls
			h := audmHandlers(&calls)
			audmFailPage(h, tc.op, "Custom", 2, laterPage)
			audmFailPage(h, tc.op, "Standard", 1, firstPage)
			if _, err := audmScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := audmStored(t, st), audmWithout(tc.lost...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			w := warns()
			if got, want := amWarnings(w), []string{"auditmanager:" + tc.op}; !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", got, want)
			}
			for _, key := range []string{"custom-page-2-denied", "standard-page-1-denied"} {
				if !strings.Contains(w[0].Message, key) {
					t.Errorf("warning %q does not carry the denial %s", w[0].Message, key)
				}
			}
		})
	}
}

// Once ctx ends during the GetAssessment fan-out and the write of the
// assessments read then fails, the error carries the stop beside the failed
// write: no later phase runs to report it. The first fanoutMed reads hold
// every slot until ctx has ended, so the read after them is never started.
func TestScanAuditManager_StopDuringReadsBesideFailedWrite(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeAuditManagerAssessment)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listed := make([]amtypes.AssessmentMetadataItem, fanoutMed+1)
	for i := range listed {
		listed[i].Id = sdkaws.String(fmt.Sprintf("assessment-%d", i))
	}
	var calls audmCalls
	h := audmHandlers(&calls)
	h["ListAssessments"] = func(any) (any, error) {
		return &auditmanager.ListAssessmentsOutput{AssessmentMetadata: listed}, nil
	}
	var started atomic.Int32
	release := make(chan struct{})
	h["GetAssessment"] = func(in any) (any, error) {
		if started.Add(1) == fanoutMed {
			cancel()
			close(release)
		}
		<-release
		id := sv(in.(*auditmanager.GetAssessmentInput).AssessmentId)
		return &auditmanager.GetAssessmentOutput{Assessment: &amtypes.Assessment{Arn: sdkaws.String(audmTestARN("assessment/" + id))}}, nil
	}
	_, err := audmScan(ctx, st, h)
	if !errors.Is(err, store.ErrStoreWrite) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want store.ErrStoreWrite and context.Canceled", err)
	}
	if n := started.Load(); n != fanoutMed {
		t.Errorf("GetAssessment calls = %d; want %d", n, fanoutMed)
	}
}

// A failed write stops the scan in each phase: no warning about the rows it
// lost (a denied tag read or GetAssessment among them), no later listing is
// called, and the earlier phases' rows stay counted. A GetAssessment that
// failed, hard or throttled, is returned beside the failed write.
func TestScanAuditManager_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, typ  string
		calls      *audmCalls
		laterLists []string
		wantTotal  int
		code       string // an API error returned beside the write's
	}{
		{"assessment", TypeAuditManagerAssessment, &audmCalls{getErrs: map[string]error{audmTestA2: audmDenied}}, []string{"ListAssessmentFrameworks", "ListControls"}, 0, ""},
		{"assessment beside a failed read", TypeAuditManagerAssessment, &audmCalls{getErrs: map[string]error{audmTestA2: audmBadInput}}, []string{"ListAssessmentFrameworks", "ListControls"}, 0, "ValidationException"},
		{"assessment beside a throttled read", TypeAuditManagerAssessment, &audmCalls{getErrs: map[string]error{audmTestA2: audmThrottle}}, []string{"ListAssessmentFrameworks", "ListControls"}, 0, "ThrottlingException"},
		{"framework", TypeAuditManagerFramework, &audmCalls{tagErrs: map[string]error{audmTestFwC1: audmDenied}}, []string{"ListControls"}, 2, ""},
		{"control", TypeAuditManagerControl, &audmCalls{tagErrs: map[string]error{audmTestCtlC1: audmDenied}}, nil, 2 + 3, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			total, err := audmScan(context.Background(), st, audmHandlers(c.calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(c.calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Frameworks and controls write a page before they read the next: the first
// page's row is stored when the second page is requested.
func TestScanAuditManager_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls audmCalls
	h := audmHandlers(&calls)
	pageOne := map[string]string{"ListAssessmentFrameworks": audmTestFwC1, "ListControls": audmTestCtlC1}
	var mu sync.Mutex
	lookups := map[string]error{}
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
				mu.Lock()
				lookups[op] = err
				mu.Unlock()
			}
			return list(in)
		}
	}
	if _, err := audmScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// other kind and the later phases still run.
func TestScanAuditManager_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListAssessments", "ListAssessmentFrameworks", "ListControls"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			var calls audmCalls
			h := audmHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				same := sdkaws.String("same")
				switch o := out.(type) {
				case *auditmanager.ListAssessmentsOutput:
					o.NextToken = same
				case *auditmanager.ListAssessmentFrameworksOutput:
					if o.NextToken != nil || bacField[*string](in, "NextToken") != nil {
						o.NextToken = same
					}
				case *auditmanager.ListControlsOutput:
					if o.NextToken != nil || bacField[*string](in, "NextToken") != nil {
						o.NextToken = same
					}
				}
				return out, err
			}
			total, err := audmScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != audmTestTotal {
				t.Errorf("total = %d; want %d", total, audmTestTotal)
			}
			if got := audmStored(t, st); !slices.Equal(got, audmAllIDs()) {
				t.Errorf("stored = %v; want %v", got, audmAllIDs())
			}
		})
	}
}

// A GovCloud or China ARN of a custom framework and control reaches the tag
// op unchanged, and the tags read are stored.
func TestScanAuditManager_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			t.Parallel()
			fw := partitionARN(pf.partition, "auditmanager", pf.region, "assessmentFramework/fw-gov")
			ctl := partitionARN(pf.partition, "auditmanager", pf.region, "control/ctl-gov")
			calls := audmCalls{tags: map[string]map[string]string{fw: {"env": pf.partition}, ctl: {"env": pf.partition}}}
			h := audmHandlers(&calls)
			h["ListAssessments"] = func(any) (any, error) { return &auditmanager.ListAssessmentsOutput{}, nil }
			h["ListAssessmentFrameworks"] = func(in any) (any, error) {
				out := &auditmanager.ListAssessmentFrameworksOutput{}
				if in.(*auditmanager.ListAssessmentFrameworksInput).FrameworkType == amtypes.FrameworkTypeCustom {
					out.FrameworkMetadataList = []amtypes.AssessmentFrameworkMetadata{{Arn: &fw}}
				}
				return out, nil
			}
			h["ListControls"] = func(in any) (any, error) {
				out := &auditmanager.ListControlsOutput{}
				if in.(*auditmanager.ListControlsInput).ControlType == amtypes.ControlTypeCustom {
					out.ControlMetadataList = []amtypes.ControlMetadata{{Arn: &ctl}}
				}
				return out, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, audmDenied)
			if _, err := scanAuditManager(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := calls.tagARNs.distinct(), audmSorted(fw, ctl); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAuditManagerFramework)[fw].TagsJSON, map[string]string{"env": pf.partition})
			assertTagsMap(t, rowsByNativeID(t, st, TypeAuditManagerControl)[ctl].TagsJSON, map[string]string{"env": pf.partition})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanAuditManager_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls audmCalls
	h := audmHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListAssessmentFrameworks"]
	h["ListAssessmentFrameworks"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, err := audmScan(ctx, st, h)
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
	if slices.Contains(calls.lists.distinct(), "ListControls") {
		t.Error("ListControls called after ctx ended")
	}
}

// An NXDOMAIN after the endpoint answered is a failure of this scan, not a
// region gap: the counts are kept and the later phases run. With nothing
// answered it stays region-unavailable, and nothing else is called.
func TestScanAuditManager_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "auditmanager.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("every GetAssessment after the listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		calls := audmCalls{getErrs: map[string]error{audmTestA1: nx, audmTestA2: nx}}
		total, err := audmScan(context.Background(), st, audmHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
		if want := audmTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("one listing after others stored rows", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		var calls audmCalls
		h := audmHandlers(&calls)
		h["ListControls"] = func(any) (any, error) { return nil, nx }
		total, err := audmScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := audmTestTotal - 3; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		calls := audmCalls{tagErrs: map[string]error{audmTestCtlC2: nx}}
		_, err := audmScan(context.Background(), st, audmHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		var calls audmCalls
		h := audmHandlers(&calls)
		h["ListAssessments"] = func(any) (any, error) { return nil, nx }
		_, err := audmScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if got := calls.lists.distinct(); len(got) != 0 {
			t.Errorf("listings = %v; want none after the NXDOMAIN", got)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
