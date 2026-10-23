package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/imagebuilder"
	ibtypes "github.com/aws/aws-sdk-go-v2/service/imagebuilder/types"
	"github.com/icearp/disco-cli/store"
)

var (
	ibTestTags = map[string]string{"team": "build"}
	// ibOtherTags is what ImageTags and ResourceTags carry: tags the resource
	// applies to what it builds, never its own.
	ibOtherTags = map[string]string{"applied": "elsewhere"}
)

func ibTestARN(kind, id string) string {
	return "arn:aws:imagebuilder:us-east-1:123456789012:" + kind + "/" + id
}

type ibElem struct {
	arn, name *string
	tags      map[string]string
}

// ibPage is page p (0 or 1) of a listing of kind: page 0 holds one tagged,
// named element; page 1 one untagged, unnamed element and, when noARN, one
// with no Arn.
func ibPage(kind string, p int, noARN bool) []ibElem {
	if p == 0 {
		return []ibElem{{arn: ptrStr(ibTestARN(kind, "tagged")), name: ptrStr(kind + "-tagged"), tags: ibTestTags}}
	}
	es := []ibElem{{arn: ptrStr(ibTestARN(kind, "untagged"))}}
	if noARN {
		es = append(es, ibElem{name: ptrStr(kind + "-orphan")})
	}
	return es
}

// ibType is one Image Builder type: the ARN kind its test elements use, the
// SDK op and op label that list it, whether its element carries its own Tags,
// whether the op is asked for Owner=Self, the SDK element built from an
// ibElem, and the op's output holding those elements.
type ibType struct {
	typ, kind, sdkOp, op string
	hasTags, ownerSelf   bool
	elem                 func(ibElem) any
	out                  func([]any) any
}

var ibTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

var ibTypes = []ibType{
	{
		TypeImageBuilderComponent, "component", "ListComponents", ibListComponentsOp, false, true,
		func(e ibElem) any {
			return ibtypes.ComponentVersion{Arn: e.arn, Name: e.name, Version: ptrStr("1.0.0"), Platform: ibtypes.PlatformLinux}
		},
		func(es []any) any {
			out := &imagebuilder.ListComponentsOutput{}
			for _, e := range es {
				out.ComponentVersionList = append(out.ComponentVersionList, e.(ibtypes.ComponentVersion))
			}
			return out
		},
	},
	{
		TypeImageBuilderContainerRecipe, "container-recipe", "ListContainerRecipes", ibListContainerRecipesOp, true, true,
		func(e ibElem) any {
			return ibtypes.ContainerRecipeSummary{Arn: e.arn, Name: e.name, Tags: e.tags, ContainerType: ibtypes.ContainerTypeDocker}
		},
		func(es []any) any {
			out := &imagebuilder.ListContainerRecipesOutput{}
			for _, e := range es {
				out.ContainerRecipeSummaryList = append(out.ContainerRecipeSummaryList, e.(ibtypes.ContainerRecipeSummary))
			}
			return out
		},
	},
	{
		TypeImageBuilderDistributionConfiguration, "distribution-configuration", "ListDistributionConfigurations", ibListDistributionConfigurationsOp, true, false,
		func(e ibElem) any {
			return ibtypes.DistributionConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags, Regions: []string{"us-east-1"}}
		},
		func(es []any) any {
			out := &imagebuilder.ListDistributionConfigurationsOutput{}
			for _, e := range es {
				out.DistributionConfigurationSummaryList = append(out.DistributionConfigurationSummaryList, e.(ibtypes.DistributionConfigurationSummary))
			}
			return out
		},
	},
	{
		TypeImageBuilderImage, "image", "ListImages", ibListImagesOp, false, true,
		func(e ibElem) any {
			return ibtypes.ImageVersion{Arn: e.arn, Name: e.name, Version: ptrStr("1.0.0"), Type: ibtypes.ImageTypeAmi}
		},
		func(es []any) any {
			out := &imagebuilder.ListImagesOutput{}
			for _, e := range es {
				out.ImageVersionList = append(out.ImageVersionList, e.(ibtypes.ImageVersion))
			}
			return out
		},
	},
	{
		TypeImageBuilderImagePipeline, "image-pipeline", "ListImagePipelines", ibListImagePipelinesOp, true, false,
		func(e ibElem) any {
			return ibtypes.ImagePipeline{Arn: e.arn, Name: e.name, Tags: e.tags, ImageTags: ibOtherTags, Status: ibtypes.PipelineStatusEnabled}
		},
		func(es []any) any {
			out := &imagebuilder.ListImagePipelinesOutput{}
			for _, e := range es {
				out.ImagePipelineList = append(out.ImagePipelineList, e.(ibtypes.ImagePipeline))
			}
			return out
		},
	},
	{
		TypeImageBuilderImageRecipe, "image-recipe", "ListImageRecipes", ibListImageRecipesOp, true, true,
		func(e ibElem) any {
			return ibtypes.ImageRecipeSummary{Arn: e.arn, Name: e.name, Tags: e.tags, ParentImage: ptrStr("ami-123")}
		},
		func(es []any) any {
			out := &imagebuilder.ListImageRecipesOutput{}
			for _, e := range es {
				out.ImageRecipeSummaryList = append(out.ImageRecipeSummaryList, e.(ibtypes.ImageRecipeSummary))
			}
			return out
		},
	},
	{
		TypeImageBuilderInfrastructureConfig, "infrastructure-configuration", "ListInfrastructureConfigurations", ibListInfrastructureConfigurationsOp, true, false,
		func(e ibElem) any {
			return ibtypes.InfrastructureConfigurationSummary{Arn: e.arn, Name: e.name, Tags: e.tags, ResourceTags: ibOtherTags, InstanceProfileName: ptrStr("Prof")}
		},
		func(es []any) any {
			out := &imagebuilder.ListInfrastructureConfigurationsOutput{}
			for _, e := range es {
				out.InfrastructureConfigurationSummaryList = append(out.InfrastructureConfigurationSummaryList, e.(ibtypes.InfrastructureConfigurationSummary))
			}
			return out
		},
	},
	{
		TypeImageBuilderLifecyclePolicy, "lifecycle-policy", "ListLifecyclePolicies", ibListLifecyclePoliciesOp, true, false,
		func(e ibElem) any {
			return ibtypes.LifecyclePolicySummary{Arn: e.arn, Name: e.name, Tags: e.tags, DateCreated: &ibTestCreated, Status: ibtypes.LifecyclePolicyStatusEnabled}
		},
		func(es []any) any {
			out := &imagebuilder.ListLifecyclePoliciesOutput{}
			for _, e := range es {
				out.LifecyclePolicySummaryList = append(out.LifecyclePolicySummaryList, e.(ibtypes.LifecyclePolicySummary))
			}
			return out
		},
	},
	{
		TypeImageBuilderWorkflow, "workflow", "ListWorkflows", ibListWorkflowsOp, false, true,
		func(e ibElem) any {
			return ibtypes.WorkflowVersion{Arn: e.arn, Name: e.name, Version: ptrStr("1.0.0"), Type: ibtypes.WorkflowTypeBuild}
		},
		func(es []any) any {
			out := &imagebuilder.ListWorkflowsOutput{}
			for _, e := range es {
				out.WorkflowVersionList = append(out.WorkflowVersionList, e.(ibtypes.WorkflowVersion))
			}
			return out
		},
	},
}

// ibCheckInput fails the test unless the request asks for the whole
// self-owned listing: no Filters, ByName unset, deprecated images not asked
// for, and Owner=Self exactly where the scanner is meant to send it.
func ibCheckInput(t *testing.T, c ibType, in any) {
	t.Helper()
	req := reflect.ValueOf(in).Elem()
	if f := req.FieldByName("Filters"); !f.IsNil() {
		t.Errorf("%s Filters = %v, want none", c.sdkOp, f.Interface())
	}
	if f := req.FieldByName("ByName"); f.IsValid() && f.Bool() {
		t.Errorf("%s ByName = true, want false", c.sdkOp)
	}
	if f := req.FieldByName("IncludeDeprecated"); f.IsValid() && !f.IsNil() {
		t.Errorf("%s IncludeDeprecated = %v, want unset", c.sdkOp, *f.Interface().(*bool))
	}
	want := ibtypes.Ownership("")
	if c.ownerSelf {
		want = ibtypes.OwnershipSelf
	}
	if f := req.FieldByName("Owner"); f.IsValid() && f.Interface().(ibtypes.Ownership) != want {
		t.Errorf("%s Owner = %q, want %q", c.sdkOp, f.Interface(), want)
	} else if !f.IsValid() && c.ownerSelf {
		t.Errorf("%s has no Owner input", c.sdkOp)
	}
}

func ibElems(c ibType, es []ibElem) []any {
	out := make([]any, len(es))
	for i, e := range es {
		out[i] = c.elem(e)
	}
	return out
}

// ibHandlers serves every Image Builder listing as two pages (ibPage),
// through ivsTwoPages, which pins NextToken forwarding and MaxResults 25 (the
// cap every Image Builder list op's model gives).
func ibHandlers(t *testing.T, noARN bool) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){}
	for _, c := range ibTypes {
		h[c.sdkOp] = ivsTwoPages(t, c.sdkOp, 25, func(in any, p int) any {
			ibCheckInput(t, c, in)
			return c.out(ibElems(c, ibPage(c.kind, p, noARN)))
		})
	}
	return h
}

func ibScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	cfg := opStubConfig(h, apiErr("AccessDeniedException", "not authorized to perform this action"))
	total, _, err := scanImageBuilderAll(context.Background(), imagebuilder.NewFromConfig(cfg), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// assertIBRow checks the row stored under e's Arn: its Name, its attributes
// (the listed element verbatim) and its tags (nil for none).
func assertIBRow(t *testing.T, rows map[string]store.Resource, c ibType, e ibElem, wantName string, wantTags map[string]string) {
	t.Helper()
	arn := sv(e.arn)
	r, ok := rows[arn]
	if !ok {
		t.Errorf("%s %s not stored", c.typ, arn)
		return
	}
	if got := sv(r.Name); got != wantName {
		t.Errorf("%s %s Name = %q, want %q", c.typ, arn, got, wantName)
	}
	var got, want any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
		t.Fatalf("%s %s attributes %q: %v", c.typ, arn, r.AttributesJSON, err)
	}
	if err := json.Unmarshal([]byte(mustJSON(c.elem(e))), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s %s attributes = %s, want the listed element %s", c.typ, arn, r.AttributesJSON, mustJSON(c.elem(e)))
	}
	if wantTags == nil {
		if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q, want nil", c.typ, arn, *r.TagsJSON)
		}
		return
	}
	assertTagsMap(t, r.TagsJSON, wantTags)
}

// TestScanImageBuilderAll_StoresRowsWithTags pins, per type, the NativeID (the
// listed Arn), the Name (the listed Name, else the Arn), the attributes (the
// listed element) and the tags: the element's own Tags, never ImageTags or
// ResourceTags, and none for the types whose element carries no Tags.
func TestScanImageBuilderAll_StoresRowsWithTags(t *testing.T) {
	st := newTestStore(t)
	total, err := ibScanAll(t, st, ibHandlers(t, false))
	if err != nil {
		t.Fatalf("scanImageBuilderAll: %v", err)
	}
	if want := 2 * len(ibTypes); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	for _, c := range ibTypes {
		rows := rowsByNativeID(t, st, c.typ)
		if len(rows) != 2 {
			t.Errorf("%s: stored %d rows, want 2", c.typ, len(rows))
		}
		tagged, untagged := ibPage(c.kind, 0, false)[0], ibPage(c.kind, 1, false)[0]
		wantTags := ibTestTags
		if !c.hasTags {
			wantTags = nil
		}
		assertIBRow(t, rows, c, tagged, c.kind+"-tagged", wantTags)
		assertIBRow(t, rows, c, untagged, sv(untagged.arn), nil)
	}
}

// TestScanImageBuilderAll_EmptyARNWarnsOncePerOp pins that every listing
// warns once, under its own op and scoped to the account and region, for its
// elements with no Arn, and stores the others.
func TestScanImageBuilderAll_EmptyARNWarnsOncePerOp(t *testing.T) {
	st := newTestStore(t)
	var warns []string
	st.OnWarn = func(w store.ScanWarning) { warns = append(warns, w.Service+" "+w.Scope) }
	total, err := ibScanAll(t, st, ibHandlers(t, true))
	if err != nil {
		t.Fatalf("scanImageBuilderAll: %v", err)
	}
	if want := 2 * len(ibTypes); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	var want []string
	for _, c := range ibTypes {
		want = append(want, c.op+" "+testAccountID+"/"+testRegion)
		if _, ok := rowsByNativeID(t, st, c.typ)[""]; ok {
			t.Errorf("%s stored a row with an empty NativeID", c.typ)
		}
	}
	if !reflect.DeepEqual(warns, want) {
		t.Errorf("warnings = %v, want %v", warns, want)
	}
}

// TestScanImageBuilderAll_ListOutcomes pins, for every phase, that rows read
// before a failed page are kept: a hard error is returned, a denial is warned
// about once under the phase's op, scoped to the account and region, and not
// returned.
func TestScanImageBuilderAll_ListOutcomes(t *testing.T) {
	bad := apiErr("InvalidRequestException", "bad")
	denied := apiErr("AccessDeniedException", "not authorized to perform this action")
	for _, c := range ibTypes {
		for _, o := range []struct {
			name    string
			failErr error
			wantErr error
			warns   int
		}{
			{"later page fails keeps rows read", bad, bad, 0},
			{"denied after a page keeps rows and warns", denied, nil, 1},
		} {
			t.Run(c.sdkOp+" "+o.name, func(t *testing.T) {
				h := ibHandlers(t, false)
				h[c.sdkOp] = func(in any) (any, error) {
					if sv(reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string)) != "" {
						return nil, o.failErr
					}
					out := c.out(ibElems(c, ibPage(c.kind, 0, false)))
					reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(ptrStr("t1")))
					return out, nil
				}
				st := newTestStore(t)
				var scopes []string
				st.OnWarn = func(w store.ScanWarning) { scopes = append(scopes, w.Service+" "+w.Scope) }
				total, err := ibScanAll(t, st, h)
				if o.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if o.wantErr != nil && !errors.Is(err, o.wantErr) {
					t.Fatalf("err = %v, want %v", err, o.wantErr)
				}
				if want := 2*len(ibTypes) - 1; total != want {
					t.Errorf("total = %d, want %d", total, want)
				}
				if _, ok := rowsByNativeID(t, st, c.typ)[ibTestARN(c.kind, "tagged")]; !ok {
					t.Errorf("%s read before the failed page was not stored", c.typ)
				}
				var want []string
				if o.warns == 1 {
					want = []string{c.op + " " + testAccountID + "/" + testRegion}
				}
				if !reflect.DeepEqual(scopes, want) {
					t.Errorf("warnings = %v, want %v", scopes, want)
				}
			})
		}
	}
}

// TestScanImageBuilderAll_RepeatedTokenEndsListing pins that a NextToken
// served twice ends the listing with errRepeatedNextToken, keeping both
// pages' rows.
func TestScanImageBuilderAll_RepeatedTokenEndsListing(t *testing.T) {
	c := ibTypes[0]
	h := ibHandlers(t, false)
	var call int
	h[c.sdkOp] = func(any) (any, error) {
		call++
		out := &imagebuilder.ListComponentsOutput{NextToken: ptrStr("t1")}
		out.ComponentVersionList = []ibtypes.ComponentVersion{{Arn: ptrStr(ibTestARN(c.kind, fmt.Sprint(call)))}}
		return out, nil
	}
	st := newTestStore(t)
	if _, err := ibScanAll(t, st, h); !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, c.typ)); n != 2 || call != 2 {
		t.Errorf("stored %d components after %d calls, want 2 and 2", n, call)
	}
}

// TestScanImageBuilderAll_PhaseErrorRunsOtherPhases pins that a failed phase
// stops no other phase: its error is returned and every other type stored.
func TestScanImageBuilderAll_PhaseErrorRunsOtherPhases(t *testing.T) {
	bad := apiErr("InvalidRequestException", "bad")
	for _, failed := range ibTypes {
		t.Run(failed.sdkOp, func(t *testing.T) {
			h := ibHandlers(t, false)
			h[failed.sdkOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			if _, err := ibScanAll(t, st, h); !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			for _, c := range ibTypes {
				if c.typ != failed.typ && len(rowsByNativeID(t, st, c.typ)) != 2 {
					t.Errorf("%s not stored after %s failed", c.typ, failed.sdkOp)
				}
			}
		})
	}
}

// TestScanImageBuilderAll_StoreWriteStops pins that a failed write stops the
// scan: no phase after it calls AWS.
func TestScanImageBuilderAll_StoreWriteStops(t *testing.T) {
	calls := map[string]int{}
	h := ibHandlers(t, false)
	for op, f := range h {
		h[op] = func(in any) (any, error) {
			calls[op]++
			return f(in)
		}
	}
	st, closeStore := openClosingStore(t)
	serve := h["ListComponents"]
	h["ListComponents"] = func(in any) (any, error) {
		closeStore()
		return serve(in)
	}
	if _, err := ibScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	for _, c := range ibTypes[1:] {
		if calls[c.sdkOp] != 0 {
			t.Errorf("%s called %d times after the failed write, want 0", c.sdkOp, calls[c.sdkOp])
		}
	}
}

// TestScanImageBuilderAll_TransientBesideHardError pins that a throttled
// phase beside a hard-failing one is demoted to one warning under the
// throttled phase's own op, and the scan classified as an error.
func TestScanImageBuilderAll_TransientBesideHardError(t *testing.T) {
	for _, c := range ibTypes {
		t.Run(c.sdkOp, func(t *testing.T) {
			hardOp := "ListWorkflows"
			if c.sdkOp == hardOp {
				hardOp = "ListComponents"
			}
			h := ibHandlers(t, false)
			h[c.sdkOp] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hardOp] = func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") }
			st := newTestStore(t)
			var labels []string
			st.OnWarn = func(w store.ScanWarning) { labels = append(labels, w.Service) }
			_, err := ibScanAll(t, st, h)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if len(labels) != 1 || labels[0] != c.op {
				t.Errorf("warnings under %v, want exactly one under %s", labels, c.op)
			}
		})
	}
}

// TestScanImageBuilderAll_DeniedEveryOpWarnsWithRegion pins that a role
// denied every listing gets one warning per op, scoped to the account and
// region, and no error.
func TestScanImageBuilderAll_DeniedEveryOpWarnsWithRegion(t *testing.T) {
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) { scopes = append(scopes, w.Service+" "+w.Scope) }
	if _, err := ibScanAll(t, st, nil); err != nil {
		t.Fatalf("scanImageBuilderAll: %v", err)
	}
	var want []string
	for _, c := range ibTypes {
		if !strings.HasPrefix(c.op, "imagebuilder:") {
			t.Errorf("op label %q is not in the imagebuilder IAM namespace", c.op)
		}
		want = append(want, c.op+" "+testAccountID+"/"+testRegion)
	}
	if !reflect.DeepEqual(scopes, want) {
		t.Errorf("warnings = %v, want %v", scopes, want)
	}
}
