package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/controltower"
	ctdocument "github.com/aws/aws-sdk-go-v2/service/controltower/document"
	cttypes "github.com/aws/aws-sdk-go-v2/service/controltower/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	ctowerTestPrefix = "arn:aws:controltower:us-east-1:123456789012:"
	ctowerTestLZ     = ctowerTestPrefix + "landingzone/lz1"
	// b1 and b3 are enabled on OU ou-1; b2 is b1's child on an enrolled
	// account.
	ctowerTestB1 = ctowerTestPrefix + "enabledbaseline/b1"
	ctowerTestB2 = ctowerTestPrefix + "enabledbaseline/b2"
	ctowerTestB3 = ctowerTestPrefix + "enabledbaseline/b3"
	ctowerTestC1 = ctowerTestPrefix + "enabledcontrol/c1"
	ctowerTestC2 = ctowerTestPrefix + "enabledcontrol/c2"

	ctowerTestOU      = "arn:aws:organizations::123456789012:ou/o-abc/ou-1"
	ctowerTestAccount = "arn:aws:organizations::123456789012:account/o-abc/111111111111"
	// ctowerTestNoARNControl names the OU's control listed without an ARN.
	ctowerTestNoARNControl = "arn:aws:controlcatalog:::control/no-arn"

	// ctowerTestTotal is the fixture's rows: the landing zone, 3 baselines
	// and 2 controls.
	ctowerTestTotal = 6
)

var (
	ctowerDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: controltower:ListEnabledControls")
	ctowerNotSetUp   = apiErr("AccessDeniedException", "AWS Control Tower is not set up in this account")
	ctowerBadInput   = apiErr("ValidationException", "bad")
	ctowerConflict   = apiErr("ConflictException", "busy")
	ctowerThrottle   = apiErr("ThrottlingException", "slow down")
	ctowerGoneErr    = apiErr("ResourceNotFoundException", "not found")
	ctowerAllTagARNs = []string{ctowerTestB1, ctowerTestB2, ctowerTestB3, ctowerTestC1, ctowerTestC2, ctowerTestLZ}
)

// ctowerCalls records a scan's calls and answers ListTagsForResource with
// tags, or errs, by ARN.
type ctowerCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
	// targets is the TargetIdentifier of each ListEnabledControls call.
	targets []string
}

// newCtowerCalls tags the landing zone, b1 and c1 with their kind; the other
// rows have no tags.
func newCtowerCalls() *ctowerCalls {
	return &ctowerCalls{ops: map[string]int{}, errs: map[string]error{}, tags: map[string]map[string]string{
		ctowerTestLZ: {"kind": "landing-zone"},
		ctowerTestB1: {"kind": "baseline"},
		ctowerTestC1: {"kind": "control"},
	}}
}

func (c *ctowerCalls) ctowerRecord(op string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
}

func (c *ctowerCalls) ctowerCount(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// ctowerSentARNs is the sorted ARNs sent to ListTagsForResource.
func (c *ctowerCalls) ctowerSentARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// ctowerPageCap rejects a listing sent any page size but want.
func ctowerPageCap(in any, want int32) error {
	if n := bacField[*int32](in, "MaxResults"); n == nil || *n != want {
		return apiErr("ValidationException", "unexpected maxResults")
	}
	return nil
}

func ctowerControl(arn, target string) cttypes.EnabledControlSummary {
	return cttypes.EnabledControlSummary{
		Arn: nonEmptyPtr(arn), ControlIdentifier: sdkaws.String("arn:aws:controlcatalog:::control/" + arn[strings.LastIndex(arn, "/")+1:]),
		TargetIdentifier: sdkaws.String(target),
		StatusSummary:    &cttypes.EnablementStatusSummary{Status: cttypes.EnablementStatusSucceeded},
	}
}

// ctowerHandlers answers every Control Tower op the scan calls: landing zone
// lz1 (ACTIVE, with a manifest), baselines b1 and b3 on ou-1 and b2 on an
// account, plus one baseline without an ARN; ou-1's controls span two pages,
// c1 and one without an ARN, then c2; the account has none. Every listing
// rejects any page size but its model cap, and the baseline listing one that
// leaves out the child baselines.
func ctowerHandlers(c *ctowerCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*controltower.ListTagsForResourceInput).ResourceArn)
			c.ctowerRecord("ListTagsForResource")
			c.mu.Lock()
			c.tagARNs = append(c.tagARNs, arn)
			err, tags := c.errs[arn], c.tags[arn]
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return &controltower.ListTagsForResourceOutput{Tags: tags}, nil
		},
		"ListLandingZones": func(in any) (any, error) {
			c.ctowerRecord("ListLandingZones")
			if err := ctowerPageCap(in, 1); err != nil {
				return nil, err
			}
			return &controltower.ListLandingZonesOutput{LandingZones: []cttypes.LandingZoneSummary{{Arn: sdkaws.String(ctowerTestLZ)}}}, nil
		},
		"GetLandingZone": func(in any) (any, error) {
			c.ctowerRecord("GetLandingZone")
			if id := sv(in.(*controltower.GetLandingZoneInput).LandingZoneIdentifier); id != ctowerTestLZ {
				return nil, apiErr("ResourceNotFoundException", "unknown landing zone "+id)
			}
			return &controltower.GetLandingZoneOutput{LandingZone: &cttypes.LandingZoneDetail{
				Arn: sdkaws.String(ctowerTestLZ), Status: cttypes.LandingZoneStatusActive, Version: sdkaws.String("3.3"),
				Manifest: ctdocument.NewLazyDocument(map[string]any{"governedRegions": []string{"us-east-1"}}),
			}}, nil
		},
		"ListEnabledBaselines": func(in any) (any, error) {
			c.ctowerRecord("ListEnabledBaselines")
			if err := ctowerPageCap(in, 100); err != nil {
				return nil, err
			}
			if !in.(*controltower.ListEnabledBaselinesInput).IncludeChildren {
				return nil, apiErr("ValidationException", "children left out")
			}
			return &controltower.ListEnabledBaselinesOutput{EnabledBaselines: []cttypes.EnabledBaselineSummary{
				{Arn: sdkaws.String(ctowerTestB1), BaselineIdentifier: sdkaws.String("AWSControlTowerBaseline"), TargetIdentifier: sdkaws.String(ctowerTestOU)},
				{Arn: sdkaws.String(ctowerTestB2), BaselineIdentifier: sdkaws.String("AWSControlTowerBaseline"), TargetIdentifier: sdkaws.String(ctowerTestAccount), ParentIdentifier: sdkaws.String(ctowerTestB1)},
				{Arn: sdkaws.String(ctowerTestB3), BaselineIdentifier: sdkaws.String("BackupBaseline"), TargetIdentifier: sdkaws.String(ctowerTestOU)},
				{BaselineIdentifier: sdkaws.String("no-arn"), TargetIdentifier: sdkaws.String(ctowerTestOU)},
			}}, nil
		},
		"ListEnabledControls": func(in any) (any, error) {
			c.ctowerRecord("ListEnabledControls")
			if err := ctowerPageCap(in, 200); err != nil {
				return nil, err
			}
			ec := in.(*controltower.ListEnabledControlsInput)
			target := sv(ec.TargetIdentifier)
			c.mu.Lock()
			c.targets = append(c.targets, target)
			c.mu.Unlock()
			switch {
			case target == ctowerTestAccount:
				return &controltower.ListEnabledControlsOutput{}, nil
			case target != ctowerTestOU:
				return nil, apiErr("ResourceNotFoundException", "unknown target "+target)
			case ec.NextToken == nil:
				noARN := ctowerControl("", ctowerTestOU)
				noARN.ControlIdentifier = sdkaws.String(ctowerTestNoARNControl)
				return &controltower.ListEnabledControlsOutput{NextToken: sdkaws.String("t2"), EnabledControls: []cttypes.EnabledControlSummary{ctowerControl(ctowerTestC1, ctowerTestOU), noARN}}, nil
			}
			return &controltower.ListEnabledControlsOutput{EnabledControls: []cttypes.EnabledControlSummary{ctowerControl(ctowerTestC2, ctowerTestOU)}}, nil
		},
	}
}

// ctowerScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func ctowerScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ctowerDenied)
	return scanControlTower(ctx, acct, testRegion, st, testScanID)
}

// ctowerEmbedded is the ARN (ControlIdentifier when it has none) of every
// control a stored baseline embeds, in order.
func ctowerEmbedded(t *testing.T, st *store.Store, baseline string) []string {
	t.Helper()
	r, ok := rowsByNativeID(t, st, TypeControlTowerEnabledBaseline)[baseline]
	if !ok {
		t.Fatalf("baseline %s not stored", baseline)
	}
	var attrs ctowerBaselineAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("baseline %s attrs: %v", baseline, err)
	}
	if sv(attrs.Baseline.Arn) != baseline {
		t.Errorf("baseline %s attrs Baseline.Arn = %q", baseline, sv(attrs.Baseline.Arn))
	}
	var ids []string
	for _, c := range attrs.EnabledControls {
		ids = append(ids, cmp.Or(sv(c.Arn), sv(c.ControlIdentifier)))
	}
	return ids
}

// The landing zone, every baseline and every control is stored under its
// ARN with the tags ListTagsForResource returns for it, nil for a resource
// without tags, and exactly those ARNs are sent to the tag op. The landing
// zone is its GetLandingZone body with the manifest rendered and its Status;
// each baseline embeds its target's listed controls, the one without an ARN
// included, and each target is listed once; a control is named by its ARN.
// The elements without an ARN are warned about once each, under their
// listing.
func TestScanControlTower_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCtowerCalls()

	counted, upserts := countUpserts(st)
	total, err := ctowerScan(context.Background(), counted, ctowerHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ctowerTestTotal || inserted != ctowerTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ctowerTestTotal, ctowerTestTotal)
	}
	w := warns()
	slices.SortFunc(w, func(a, b store.ScanWarning) int { return strings.Compare(a.Service, b.Service) })
	if len(w) != 2 || w[0].Service != ctowerListBaselines || !strings.Contains(w[0].Message, "no-arn") ||
		w[1].Service != ctowerListControls || !strings.Contains(w[1].Message, ctowerTestNoARNControl) {
		t.Errorf("warnings = %v; want one under each listing naming its element without an ARN", w)
	}
	for _, c := range []struct {
		typ, arn, name, status, attr string
		tags                         map[string]string
	}{
		{TypeControlTowerLandingZone, ctowerTestLZ, "", "ACTIVE", `"Manifest":{"governedRegions":["us-east-1"]}`, map[string]string{"kind": "landing-zone"}},
		{TypeControlTowerEnabledBaseline, ctowerTestB1, "", "", `"BaselineIdentifier":"AWSControlTowerBaseline"`, map[string]string{"kind": "baseline"}},
		{TypeControlTowerEnabledBaseline, ctowerTestB2, "", "", `"ParentIdentifier":"` + ctowerTestB1 + `"`, nil},
		{TypeControlTowerEnabledBaseline, ctowerTestB3, "", "", `"BaselineIdentifier":"BackupBaseline"`, nil},
		{TypeControlTowerEnabledControl, ctowerTestC1, ctowerTestC1, "", `"TargetIdentifier":"` + ctowerTestOU + `"`, map[string]string{"kind": "control"}},
		{TypeControlTowerEnabledControl, ctowerTestC2, ctowerTestC2, "", `"Status":"SUCCEEDED"`, nil},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s %s missing", c.typ, c.arn)
			continue
		}
		if sv(r.Name) != c.name || sv(r.Status) != c.status || sv(r.Region) != testRegion || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s Name, Status, Region, CreatedAt, Managed = %q, %q, %q, %v, %v; want %q, %q, %q, nil, false",
				c.arn, sv(r.Name), sv(r.Status), sv(r.Region), r.CreatedAt, r.ManagedByProvider, c.name, c.status, testRegion)
		}
		if c.name == "" && r.Name != nil || c.status == "" && r.Status != nil {
			t.Errorf("%s Name, Status = %v, %v; want unset", c.arn, r.Name, r.Status)
		}
		if !strings.Contains(r.AttributesJSON, `"Arn":"`+c.arn+`"`) || !strings.Contains(r.AttributesJSON, c.attr) {
			t.Errorf("%s attrs = %s; want its element with %s", c.arn, r.AttributesJSON, c.attr)
		}
		if c.tags != nil {
			assertTagsMap(t, r.TagsJSON, c.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
		}
	}
	if got := calls.ctowerSentARNs(); !slices.Equal(got, ctowerAllTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, ctowerAllTagARNs)
	}
	ouControls := []string{ctowerTestC1, ctowerTestNoARNControl, ctowerTestC2}
	for b, want := range map[string][]string{ctowerTestB1: ouControls, ctowerTestB2: nil, ctowerTestB3: ouControls} {
		if got := ctowerEmbedded(t, st, b); !slices.Equal(got, want) {
			t.Errorf("%s embeds %v; want %v", b, got, want)
		}
	}
	if !strings.Contains(rowsByNativeID(t, st, TypeControlTowerEnabledBaseline)[ctowerTestB2].AttributesJSON, `"TargetIdentifier":"`+ctowerTestAccount+`"`) {
		t.Errorf("b2 attrs lack its account target")
	}
	// ou-1 is listed for its two pages once, not once per baseline.
	if got := slices.Sorted(slices.Values(calls.targets)); !slices.Equal(got, []string{ctowerTestAccount, ctowerTestOU, ctowerTestOU}) {
		t.Errorf("ListEnabledControls targets = %v; want the account once and ou-1's two pages", got)
	}
}

// An account with no landing zone and no baselines stores nothing and reads
// no tags, details or controls.
func TestScanControlTower_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCtowerCalls()
	h := ctowerHandlers(calls)
	h["ListLandingZones"] = func(any) (any, error) { return &controltower.ListLandingZonesOutput{}, nil }
	h["ListEnabledBaselines"] = func(any) (any, error) { return &controltower.ListEnabledBaselinesOutput{}, nil }
	total, err := ctowerScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
	for _, op := range []string{"ListTagsForResource", "GetLandingZone", "ListEnabledControls"} {
		if n := calls.ctowerCount(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
}

// A first page saying Control Tower is not set up, before anything was
// stored, makes the scan disabled with nothing stored; after the landing
// zone's listing says so the baselines are not listed. Once the landing zone
// was stored, or its listing failed, the same answer from the baselines is an
// error, the counts and the earlier failure kept.
func TestScanControlTower_NotSetUp(t *testing.T) {
	t.Parallel()
	notSetUp := apiErr("ValidationException", "AWSControlTowerAdmin role is missing")
	t.Run("landing zones", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCtowerCalls()
		h := ctowerHandlers(calls)
		h["ListLandingZones"] = func(any) (any, error) { return nil, ctowerNotSetUp }
		total, err := ctowerScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeDisabled)
		if total != 0 || calls.ctowerCount("ListEnabledBaselines") != 0 {
			t.Errorf("total, ListEnabledBaselines calls = %d, %d; want 0, 0", total, calls.ctowerCount("ListEnabledBaselines"))
		}
	})
	t.Run("landing zones, then ctx ends", func(t *testing.T) {
		st := newTestStore(t)
		h := ctowerHandlers(newCtowerCalls())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h["ListLandingZones"] = func(any) (any, error) {
			cancel()
			return nil, ctowerNotSetUp
		}
		_, err := ctowerScan(ctx, st, h)
		if !errors.Is(err, context.Canceled) || errors.Is(err, errServiceDisabled) {
			t.Errorf("err = %v; want the stop, not the sentinel", err)
		}
	})
	t.Run("baselines, no landing zone", func(t *testing.T) {
		st := newTestStore(t)
		h := ctowerHandlers(newCtowerCalls())
		h["ListLandingZones"] = func(any) (any, error) { return &controltower.ListLandingZonesOutput{}, nil }
		h["ListEnabledBaselines"] = func(any) (any, error) { return nil, notSetUp }
		total, err := ctowerScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeDisabled)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
	})
	t.Run("baselines after the landing-zone listing failed", func(t *testing.T) {
		st := newTestStore(t)
		h := ctowerHandlers(newCtowerCalls())
		h["ListLandingZones"] = func(any) (any, error) { return nil, ctowerConflict }
		h["ListEnabledBaselines"] = func(any) (any, error) { return nil, notSetUp }
		_, err := ctowerScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if errors.Is(err, errServiceDisabled) {
			t.Errorf("err = %v; want no disabled sentinel", err)
		}
		var codes []string
		for _, m := range bacTopMembers(err) {
			var ae smithy.APIError
			if errors.As(m, &ae) {
				codes = append(codes, ae.ErrorCode())
			}
		}
		if want := []string{"ConflictException", "ValidationException"}; !slices.Equal(codes, want) {
			t.Errorf("error codes = %v; want %v (err %v)", codes, want, err)
		}
	})
	t.Run("baselines after the landing zone was stored", func(t *testing.T) {
		st := newTestStore(t)
		h := ctowerHandlers(newCtowerCalls())
		h["ListEnabledBaselines"] = func(any) (any, error) { return nil, notSetUp }
		total, err := ctowerScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "ValidationException")
		if total != 1 {
			t.Errorf("total = %d; want the landing zone", total)
		}
		if _, ok := rowsByNativeID(t, st, TypeControlTowerLandingZone)[ctowerTestLZ]; !ok {
			t.Error("landing zone not stored")
		}
	})
}

// A failed tag read: ResourceNotFoundException drops the row silently; a
// denial keeps it untagged and warns once under the tag op, naming it; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanControlTower_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, k := range []struct{ typ, arn string }{
		{TypeControlTowerLandingZone, ctowerTestLZ},
		{TypeControlTowerEnabledBaseline, ctowerTestB1},
		{TypeControlTowerEnabledControl, ctowerTestC1},
	} {
		for _, c := range []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			code      string
			wantKept  bool
			wantWarns int
		}{
			{"not found", ctowerGoneErr, -1, "", false, 0},
			{"denied", ctowerDenied, -1, "", true, 1},
			{"transient", ctowerThrottle, outcomeTransient, "ThrottlingException", true, 0},
			{"other error", ctowerBadInput, outcomeError, "ValidationException", true, 0},
		} {
			t.Run(k.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := bacWarnings(st, ctowerTagsOp)
				calls := newCtowerCalls()
				calls.errs[k.arn] = c.err

				total, err := ctowerScan(context.Background(), st, ctowerHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				wantTotal := ctowerTestTotal
				if !c.wantKept {
					wantTotal--
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				r, ok := rowsByNativeID(t, st, k.typ)[k.arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				if len(*warns) != c.wantWarns {
					t.Fatalf("%s warnings = %v; want %d", ctowerTagsOp, *warns, c.wantWarns)
				}
				for _, x := range *warns {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, k.arn) {
						t.Errorf("warning %+v; want one in %s/%s naming %s", x, testAccountID, testRegion, k.arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in both phases send one warning for the whole scan, and
// the rows are kept untagged.
func TestScanControlTower_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := bacWarnings(st, ctowerTagsOp)
	calls := newCtowerCalls()
	denied := map[string]string{ctowerTestLZ: TypeControlTowerLandingZone, ctowerTestB1: TypeControlTowerEnabledBaseline, ctowerTestC1: TypeControlTowerEnabledControl}
	for arn := range denied {
		calls.errs[arn] = ctowerDenied
	}
	total, err := ctowerScan(context.Background(), st, ctowerHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ctowerTestTotal {
		t.Errorf("total = %d; want %d", total, ctowerTestTotal)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, ctowerTestLZ) {
		t.Fatalf("%s warnings = %v; want 1 naming %s", ctowerTagsOp, *warns, ctowerTestLZ)
	}
	for arn, typ := range denied {
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored = %v, TagsJSON = %v; want stored untagged", arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanControlTower_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, ctowerTagsOp)
	calls := newCtowerCalls()
	calls.errs[ctowerTestLZ] = ctowerThrottle
	calls.errs[ctowerTestC2] = ctowerBadInput

	_, err := ctowerScan(context.Background(), st, ctowerHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", ctowerTagsOp, *warns)
	}
}

// A GetLandingZone that finds no such landing zone drops it, with no tag
// read; any other failure stores the listed element, without a Status, and
// still reads its tags: a denial warns once under GetLandingZone, a response
// without its LandingZone and any other error are errors; the baselines are
// stored either way.
func TestScanControlTower_GetLandingZoneFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		wantErr   error // nil: no error
		wantKept  bool
		wantWarns int
	}{
		{"not found", ctowerGoneErr, nil, false, 0},
		{"denied", ctowerDenied, nil, true, 1},
		{"no body", nil, errCtowerNoLandingZoneBody, true, 0},
		{"other error", ctowerConflict, ctowerConflict, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, ctowerGetLandingZone)
			calls := newCtowerCalls()
			h := ctowerHandlers(calls)
			h["GetLandingZone"] = func(any) (any, error) {
				if c.err != nil {
					return nil, c.err
				}
				return &controltower.GetLandingZoneOutput{}, nil
			}
			total, err := ctowerScan(context.Background(), st, h)
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.wantErr != nil {
				assertGlueOutcome(t, err, outcomeError)
				if !errors.Is(err, c.wantErr) {
					t.Errorf("err = %v; want %v", err, c.wantErr)
				}
			}
			r, ok := rowsByNativeID(t, st, TypeControlTowerLandingZone)[ctowerTestLZ]
			if ok != c.wantKept {
				t.Fatalf("landing zone stored = %v; want %v", ok, c.wantKept)
			}
			sentLZ := slices.Contains(calls.ctowerSentARNs(), ctowerTestLZ)
			if sentLZ != c.wantKept {
				t.Errorf("landing zone tag read = %v; want %v", sentLZ, c.wantKept)
			}
			if ok {
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || len(attrs) != 1 || attrs["Arn"] != ctowerTestLZ || r.Status != nil {
					t.Errorf("attrs, Status = %s, %v; want the listed element, {Arn}, and none", r.AttributesJSON, r.Status)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"kind": "landing-zone"})
			}
			if len(*warns) != c.wantWarns {
				t.Errorf("%s warnings = %v; want %d", ctowerGetLandingZone, *warns, c.wantWarns)
			}
			if got := len(rowsByNativeID(t, st, TypeControlTowerEnabledBaseline)); got != 3 {
				t.Errorf("baselines stored = %d; want 3", got)
			}
			wantTotal := ctowerTestTotal
			if !c.wantKept {
				wantTotal--
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
		})
	}
}

// A target's failed control listing keeps the pages read before it, and
// every baseline is still stored, embedding what was read: on a first page, a
// target no longer found is skipped silently, a denial and a
// ValidationException each warn once under ListEnabledControls, and any other
// error is returned; on a later page a ValidationException is returned too,
// with no refused-target warning.
func TestScanControlTower_ControlListingFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		err      error
		outcome  serviceOutcome // -1: no error
		warnSubs []string       // what the one ListEnabledControls warning names; nil: none
	}{
		{"not found", ctowerGoneErr, -1, nil},
		{"denied", ctowerDenied, -1, []string{ctowerTestOU}},
		{"refused", ctowerBadInput, -1, []string{"ValidationException", ctowerTestOU}},
		{"other error", ctowerConflict, outcomeError, nil},
	} {
		t.Run(c.name+"/first page", func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, ctowerListControls)
			h := ctowerHandlers(newCtowerCalls())
			list := h["ListEnabledControls"]
			h["ListEnabledControls"] = func(in any) (any, error) {
				if sv(in.(*controltower.ListEnabledControlsInput).TargetIdentifier) == ctowerTestOU {
					return nil, c.err
				}
				return list(in)
			}
			total, err := ctowerScan(context.Background(), st, h)
			if c.outcome < 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome >= 0 {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "ConflictException")
			}
			if total != ctowerTestTotal-2 {
				t.Errorf("total = %d; want %d", total, ctowerTestTotal-2)
			}
			if got := ctowerEmbedded(t, st, ctowerTestB1); got != nil {
				t.Errorf("b1 embeds %v; want none", got)
			}
			if c.warnSubs == nil && len(*warns) != 0 {
				t.Errorf("warnings = %v; want none", *warns)
			}
			if c.warnSubs != nil {
				if len(*warns) != 1 {
					t.Fatalf("warnings = %v; want 1", *warns)
				}
				for _, s := range c.warnSubs {
					if !strings.Contains((*warns)[0].Message, s) {
						t.Errorf("warning %q; want it to name %s", (*warns)[0].Message, s)
					}
				}
			}
		})
	}
	for _, c := range []struct {
		name string
		err  error
		code string
	}{
		{"other error", ctowerConflict, "ConflictException"},
		{"refused", ctowerBadInput, "ValidationException"},
	} {
		t.Run(c.name+"/later page", func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, ctowerListControls)
			h := ctowerHandlers(newCtowerCalls())
			list := h["ListEnabledControls"]
			h["ListEnabledControls"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, c.err
				}
				return list(in)
			}
			total, err := ctowerScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, c.code)
			if total != ctowerTestTotal-1 {
				t.Errorf("total = %d; want %d", total, ctowerTestTotal-1)
			}
			controls := rowsByNativeID(t, st, TypeControlTowerEnabledControl)
			if _, ok := controls[ctowerTestC1]; !ok {
				t.Error("c1, of the page read before the failure, not stored")
			}
			if _, ok := controls[ctowerTestC2]; ok {
				t.Error("c2, of the failed page, stored")
			}
			if got, want := ctowerEmbedded(t, st, ctowerTestB3), []string{ctowerTestC1, ctowerTestNoARNControl}; !slices.Equal(got, want) {
				t.Errorf("b3 embeds %v; want %v", got, want)
			}
			// The only warning is the element without an ARN's: the target
			// answered, so it is not warned about as refused.
			if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, ctowerTestNoARNControl) {
				t.Errorf("warnings = %v; want 1, naming %s", *warns, ctowerTestNoARNControl)
			}
		})
	}
}

// Refused targets warn once for the phase, naming the least of them, not
// the first to answer: ou-1 answers before the account, the least.
// Holding the account's answer until ou-1's handler returns orders the two
// handlers but not the tallies after them, so the scan runs ctowerOrderRuns
// times: naming the first to answer instead passes all of them only by
// chance.
func TestScanControlTower_RefusedTargetsWarnOnce(t *testing.T) {
	t.Parallel()
	const ctowerOrderRuns = 10
	for range ctowerOrderRuns {
		st := newTestStore(t)
		warns := bacWarnings(st, ctowerListControls)
		h := ctowerHandlers(newCtowerCalls())
		ouAnswered := make(chan struct{})
		h["ListEnabledControls"] = func(in any) (any, error) {
			if sv(in.(*controltower.ListEnabledControlsInput).TargetIdentifier) == ctowerTestOU {
				defer close(ouAnswered)
				return nil, ctowerBadInput
			}
			select {
			case <-ouAnswered:
			case <-time.After(10 * time.Second):
				t.Error("ou-1 never listed")
			}
			return nil, ctowerBadInput
		}
		total, err := ctowerScan(context.Background(), st, h)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != 4 {
			t.Errorf("total = %d; want the landing zone and 3 baselines", total)
		}
		if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, "first "+ctowerTestAccount+":") {
			t.Fatalf("warnings = %v; want 1 naming %s first", *warns, ctowerTestAccount)
		}
	}
}

// Each control page is written, with its tags, before the next is
// requested.
func TestScanControlTower_WritesEachControlPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := ctowerHandlers(newCtowerCalls())
	list := h["ListEnabledControls"]
	var (
		asked bool
		got   *store.Resource
		gerr  error
	)
	h["ListEnabledControls"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			asked = true
			got, gerr = st.GetResource(store.ResourceID("aws", testAccountID, ctowerTestC1))
		}
		return list(in)
	}
	if _, err := ctowerScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !asked {
		t.Fatal("page 2 never requested")
	}
	if gerr != nil {
		t.Fatalf("c1 not stored before page 2 was read: %v", gerr)
	}
	assertTagsMap(t, got.TagsJSON, map[string]string{"kind": "control"})
}

// A failed later page of the baseline listing keeps the page read before it,
// with its targets' controls, and is returned.
func TestScanControlTower_BaselineListingKeepsEarlierPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := ctowerHandlers(newCtowerCalls())
	h["ListEnabledBaselines"] = failOnCall(2, ctowerConflict, func(any) (any, error) {
		return &controltower.ListEnabledBaselinesOutput{NextToken: sdkaws.String("t2"), EnabledBaselines: []cttypes.EnabledBaselineSummary{
			{Arn: sdkaws.String(ctowerTestB1), TargetIdentifier: sdkaws.String(ctowerTestOU)},
		}}, nil
	})
	total, err := ctowerScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	// The landing zone, b1 and ou-1's two controls.
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
	if got := len(ctowerEmbedded(t, st, ctowerTestB1)); got != 3 {
		t.Errorf("b1 embeds %d controls; want 3", got)
	}
}

// A repeated token ends a listing as an error after its second call,
// keeping both pages.
func TestScanControlTower_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	same := sdkaws.String("same")
	t.Run("baselines", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCtowerCalls()
		h := ctowerHandlers(calls)
		h["ListEnabledBaselines"] = func(in any) (any, error) {
			calls.ctowerRecord("repeating")
			arn := ctowerTestB1
			if bacField[*string](in, "NextToken") != nil {
				arn = ctowerTestB3
			}
			return &controltower.ListEnabledBaselinesOutput{NextToken: same, EnabledBaselines: []cttypes.EnabledBaselineSummary{{Arn: sdkaws.String(arn), TargetIdentifier: sdkaws.String(ctowerTestOU)}}}, nil
		}
		total, err := ctowerScan(context.Background(), st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if n := calls.ctowerCount("repeating"); n != 2 {
			t.Errorf("ListEnabledBaselines calls = %d; want 2", n)
		}
		if total != 5 {
			t.Errorf("total = %d; want the landing zone, b1, b3 and 2 controls", total)
		}
	})
	t.Run("controls", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCtowerCalls()
		h := ctowerHandlers(calls)
		list := h["ListEnabledControls"]
		h["ListEnabledControls"] = func(in any) (any, error) {
			if sv(in.(*controltower.ListEnabledControlsInput).TargetIdentifier) != ctowerTestOU {
				return list(in)
			}
			calls.ctowerRecord("repeating")
			arn := ctowerTestC1
			if bacField[*string](in, "NextToken") != nil {
				arn = ctowerTestC2
			}
			return &controltower.ListEnabledControlsOutput{NextToken: same, EnabledControls: []cttypes.EnabledControlSummary{ctowerControl(arn, ctowerTestOU)}}, nil
		}
		total, err := ctowerScan(context.Background(), st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if n := calls.ctowerCount("repeating"); n != 2 {
			t.Errorf("ou-1 ListEnabledControls calls = %d; want 2", n)
		}
		if total != ctowerTestTotal {
			t.Errorf("total = %d; want %d", total, ctowerTestTotal)
		}
	})
}

// A failed write stops the scan and sends no warning about rows it lost:
// a failed landing-zone write lists no baselines, and a failed control write
// writes no baseline.
func TestScanControlTower_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ       string
		wantTotal int
		notCalled string
	}{
		{TypeControlTowerLandingZone, 0, "ListEnabledBaselines"},
		{TypeControlTowerEnabledControl, 1, ""},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newCtowerCalls()
			calls.errs[ctowerTestB1] = ctowerDenied
			calls.errs[ctowerTestC1] = ctowerDenied

			total, err := ctowerScan(context.Background(), st, ctowerHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if got := len(rowsByNativeID(t, st, TypeControlTowerEnabledBaseline)); got != 0 {
				t.Errorf("baselines stored = %d; want 0", got)
			}
			if c.notCalled != "" && calls.ctowerCount(c.notCalled) != 0 {
				t.Errorf("%s called after the failed write", c.notCalled)
			}
		})
	}
}

// A denied baseline listing warns under its op; the landing zone is kept.
func TestScanControlTower_BaselinesDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, ctowerListBaselines)
	h := ctowerHandlers(newCtowerCalls())
	delete(h, "ListEnabledBaselines")
	total, err := ctowerScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 || total != 1 {
		t.Errorf("warnings, total = %d, %d; want 1, 1", *warns, total)
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged; a landing zone whose body has no Status is stored
// without one.
func TestScanControlTower_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCtowerCalls()
			h := ctowerHandlers(calls)
			prefix := "arn:" + c.partition + ":controltower:" + c.region + ":" + testAccountID + ":"
			lz, control := prefix+"landingzone/lzp", prefix+"enabledcontrol/cp"
			calls.tags[lz] = map[string]string{"k": "lz"}
			calls.tags[control] = map[string]string{"k": "control"}
			h["ListLandingZones"] = func(any) (any, error) {
				return &controltower.ListLandingZonesOutput{LandingZones: []cttypes.LandingZoneSummary{{Arn: &lz}}}, nil
			}
			h["GetLandingZone"] = func(any) (any, error) {
				return &controltower.GetLandingZoneOutput{LandingZone: &cttypes.LandingZoneDetail{Arn: &lz}}, nil
			}
			h["ListEnabledControls"] = func(in any) (any, error) {
				if sv(in.(*controltower.ListEnabledControlsInput).TargetIdentifier) != ctowerTestOU {
					return &controltower.ListEnabledControlsOutput{}, nil
				}
				return &controltower.ListEnabledControlsOutput{EnabledControls: []cttypes.EnabledControlSummary{ctowerControl(control, ctowerTestOU)}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(h, ctowerDenied)
			if _, err := scanControlTower(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for arn, typ := range map[string]string{lz: TypeControlTowerLandingZone, control: TypeControlTowerEnabledControl} {
				if !slices.Contains(calls.ctowerSentARNs(), arn) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.ctowerSentARNs(), arn)
				}
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if !ok {
					t.Fatalf("%s not stored", arn)
				}
				assertTagsMap(t, r.TagsJSON, calls.tags[arn])
			}
			// The GetLandingZone body has no Status, so none is stored.
			if r := rowsByNativeID(t, st, TypeControlTowerLandingZone)[lz]; r.Status != nil {
				t.Errorf("landing zone Status = %q; want unset", *r.Status)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanControlTower_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCtowerCalls()
	h := ctowerHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["GetLandingZone"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}
	_, err := ctowerScan(ctx, st, h)
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
	if n := calls.ctowerCount("ListEnabledBaselines"); n != 0 {
		t.Errorf("ListEnabledBaselines called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after the landing zone was stored is a failure of this scan,
// with the counts kept; with nothing stored the region is unavailable.
func TestScanControlTower_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "controltower.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("after the landing zone", func(t *testing.T) {
		st := newTestStore(t)
		h := ctowerHandlers(newCtowerCalls())
		h["ListEnabledBaselines"] = func(any) (any, error) { return nil, nx }
		total, err := ctowerScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != 1 {
			t.Errorf("total = %d; want 1", total)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanControlTower(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A landing zone listed without an ARN is not stored, read or tag-read, and
// is warned about once under its listing; the baselines and controls are
// stored as ever.
func TestScanControlTower_LandingZoneWithoutARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCtowerCalls()
	h := ctowerHandlers(calls)
	h["ListLandingZones"] = func(any) (any, error) {
		return &controltower.ListLandingZonesOutput{LandingZones: []cttypes.LandingZoneSummary{{}}}, nil
	}
	total, err := ctowerScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ctowerTestTotal-1 {
		t.Errorf("total = %d; want %d", total, ctowerTestTotal-1)
	}
	if got := len(rowsByNativeID(t, st, TypeControlTowerLandingZone)); got != 0 {
		t.Errorf("landing zones stored = %d; want 0", got)
	}
	if n := calls.ctowerCount("GetLandingZone"); n != 0 {
		t.Errorf("GetLandingZone calls = %d; want 0", n)
	}
	if got, want := calls.ctowerSentARNs(), slices.DeleteFunc(slices.Clone(ctowerAllTagARNs), func(a string) bool { return a == ctowerTestLZ }); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	lzWarns := 0
	for _, w := range warns() {
		if w.Service == ctowerListLandingZones {
			lzWarns++
		}
	}
	if lzWarns != 1 {
		t.Errorf("%s warnings = %d; want 1", ctowerListLandingZones, lzWarns)
	}
}

// The controls of a target named only by a baseline without an ARN are
// listed and stored, though that baseline is not; a baseline without a
// target is stored with no controls, and no listing is sent an empty target.
func TestScanControlTower_BaselineTargets(t *testing.T) {
	t.Parallel()
	const (
		ou2 = "arn:aws:organizations::123456789012:ou/o-abc/ou-2"
		b4  = ctowerTestPrefix + "enabledbaseline/b4"
		c3  = ctowerTestPrefix + "enabledcontrol/c3"
	)
	st := newTestStore(t)
	calls := newCtowerCalls()
	h := ctowerHandlers(calls)
	h["ListEnabledBaselines"] = func(any) (any, error) {
		return &controltower.ListEnabledBaselinesOutput{EnabledBaselines: []cttypes.EnabledBaselineSummary{
			{BaselineIdentifier: sdkaws.String("no-arn"), TargetIdentifier: sdkaws.String(ou2)},
			{Arn: sdkaws.String(b4), BaselineIdentifier: sdkaws.String("no-target")},
		}}, nil
	}
	list := h["ListEnabledControls"]
	h["ListEnabledControls"] = func(in any) (any, error) {
		if sv(in.(*controltower.ListEnabledControlsInput).TargetIdentifier) == ou2 {
			calls.mu.Lock()
			calls.targets = append(calls.targets, ou2)
			calls.mu.Unlock()
			return &controltower.ListEnabledControlsOutput{EnabledControls: []cttypes.EnabledControlSummary{ctowerControl(c3, ou2)}}, nil
		}
		return list(in)
	}
	total, err := ctowerScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The landing zone, b4 and c3.
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := slices.Sorted(slices.Values(calls.targets)); !slices.Equal(got, []string{ou2}) {
		t.Errorf("ListEnabledControls targets = %q; want only ou-2", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeControlTowerEnabledControl)[c3]; !ok {
		t.Errorf("c3, of the ARN-less baseline's target, not stored")
	}
	if got := ctowerEmbedded(t, st, b4); got != nil {
		t.Errorf("b4 embeds %v; want none", got)
	}
}
