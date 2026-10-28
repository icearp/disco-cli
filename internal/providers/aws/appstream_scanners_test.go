package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appstream"
	astypes "github.com/aws/aws-sdk-go-v2/service/appstream/types"
	"github.com/icearp/disco-cli/store"
)

// stubAppStreamUsers records every AuthenticationType DescribeUsers is asked
// for. The interface is embedded so the 13 methods this test does not exercise
// stay unimplemented — calling one panics, which is the intent.
type stubAppStreamUsers struct {
	appStreamAPI
	asked []astypes.AuthenticationType
	// pages are returned one per call; every page but the last hands back a
	// NextToken, so the scanner's pagination loop is what advances through them.
	pages [][]astypes.User
	calls int
}

func (s *stubAppStreamUsers) DescribeUsers(_ context.Context, in *appstream.DescribeUsersInput, _ ...func(*appstream.Options)) (*appstream.DescribeUsersOutput, error) {
	s.asked = append(s.asked, in.AuthenticationType)
	if s.calls >= len(s.pages) {
		return &appstream.DescribeUsersOutput{}, nil
	}
	out := &appstream.DescribeUsersOutput{Users: s.pages[s.calls]}
	s.calls++
	if s.calls < len(s.pages) {
		tok := "page" + strconv.Itoa(s.calls)
		out.NextToken = &tok
	}
	return out, nil
}

func asUser(accountID, name string) astypes.User {
	arn := "arn:aws:appstream:" + testRegion + ":" + accountID + ":user/USERPOOL/" + name
	return astypes.User{Arn: &arn, UserName: &name}
}

// TestScanASUsersAsksUserpoolOnly pins the one AuthenticationType DescribeUsers
// accepts. The SDK field doc says "You must specify USERPOOL"; the enum's other
// values belong to the shape shared with CreateUser/EnableUser and name nothing
// a user pool holds, so asking for one is a guaranteed 400.
//
// This regressed invisibly once: the scanner iterated USERPOOL + API and
// swallowed the resulting InvalidParameterValueException, so a doomed call per
// region per scan looked like success until an SDK change stopped the swallow
// from matching.
func TestScanASUsersAsksUserpoolOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Two pages, so the assertion below counts pagination calls rather than
	// auth-type calls and cannot pass by conflating the two.
	stub := &stubAppStreamUsers{pages: [][]astypes.User{
		{asUser(testAccountID, "one@example.com")},
		{asUser(testAccountID, "two@example.com")},
	}}

	total, _, err := scanASUsers(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2 (both pages)", total)
	}
	for _, n := range []string{"one@example.com", "two@example.com"} {
		arn := *asUser(testAccountID, n).Arn
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
			t.Errorf("user %s missing: %v", n, err)
		}
	}

	// One call per page, all USERPOOL. An extra auth type would show up here as
	// a third entry.
	want := []astypes.AuthenticationType{astypes.AuthenticationTypeUserpool, astypes.AuthenticationTypeUserpool}
	if !slices.Equal(stub.asked, want) {
		t.Errorf("DescribeUsers asked for %v, want exactly %v", stub.asked, want)
	}
}

// TestScanASUsersEmpty guards the empty-pool path: no rows, no error.
func TestScanASUsersEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanASUsers(context.Background(), &stubAppStreamUsers{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("empty total=%d want 0", total)
	}
}

const (
	asTestStackARN   = "arn:aws:appstream:us-east-1:123456789012:stack/stk"
	asTestImageA     = "arn:aws:appstream:us-east-1:123456789012:image/img-a"
	asTestImageB     = "arn:aws:appstream:us-east-1:123456789012:image/img-b"
	asTestBuilder    = "arn:aws:appstream:us-east-1:123456789012:image-builder/bld-a"
	asTestPermA111   = asTestImageA + "/permission/111111111111"
	asTestPermA222   = asTestImageA + "/permission/222222222222"
	asTestPermB333   = asTestImageB + "/permission/333333333333"
	asTestOfficeA    = asTestImageA + "/software-association/Microsoft_Office_2024_LTSC_Standard_64Bit"
	asTestVisioBld   = asTestBuilder + "/software-association/Microsoft_Visio_2024_LTSC_Standard_64Bit"
	asTestProjectBld = asTestBuilder + "/software-association/Microsoft_Project_2024_Standard_64Bit"
)

var (
	asDenied = apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not "+
		"authorized to perform: appstream:Describe")
	asBadInput  = apiErr("ValidationException", "bad input")
	asThrottled = apiErr("ThrottlingException", "slow down")
	asGone      = apiErr("ResourceNotFoundException", "not found")
)

// asTopLevelHandlers serves one stack, one image builder and two private
// images (img-a, then img-b on a second page). img-a is shared over two pages
// (111; then 222 plus one grant without an account), img-b with 333. img-a
// holds Office; the builder holds Visio, then Project plus one association
// without a name on a second page; img-b holds none. Every
// DescribeImagePermissions call records its image name in permCalls and every
// DescribeSoftwareAssociations call its owner ARN in saCalls.
func asTopLevelHandlers(permCalls, saCalls *parentCalls) map[string]func(in any) (any, error) {
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	allow := func(fleet, builder bool) *astypes.ImagePermissions {
		return &astypes.ImagePermissions{AllowFleet: &fleet, AllowImageBuilder: &builder}
	}
	return map[string]func(in any) (any, error){
		"DescribeStacks": empty(&appstream.DescribeStacksOutput{Stacks: []astypes.Stack{
			{Arn: ptrStr(asTestStackARN), Name: ptrStr("stk")},
		}}),
		"DescribeEntitlements":                 empty(&appstream.DescribeEntitlementsOutput{}),
		"DescribeAppBlocks":                    empty(&appstream.DescribeAppBlocksOutput{}),
		"DescribeAppBlockBuilders":             empty(&appstream.DescribeAppBlockBuildersOutput{}),
		"DescribeApplications":                 empty(&appstream.DescribeApplicationsOutput{}),
		"DescribeFleets":                       empty(&appstream.DescribeFleetsOutput{}),
		"DescribeDirectoryConfigs":             empty(&appstream.DescribeDirectoryConfigsOutput{}),
		"ListAssociatedFleets":                 empty(&appstream.ListAssociatedFleetsOutput{}),
		"DescribeUserStackAssociations":        empty(&appstream.DescribeUserStackAssociationsOutput{}),
		"DescribeUsers":                        empty(&appstream.DescribeUsersOutput{}),
		"DescribeApplicationFleetAssociations": empty(&appstream.DescribeApplicationFleetAssociationsOutput{}),
		"ListTagsForResource":                  empty(&appstream.ListTagsForResourceOutput{}),
		"DescribeImageBuilders": empty(&appstream.DescribeImageBuildersOutput{ImageBuilders: []astypes.ImageBuilder{
			{Arn: ptrStr(asTestBuilder), Name: ptrStr("bld-a")},
		}}),
		"DescribeImages": func(in any) (any, error) {
			p := in.(*appstream.DescribeImagesInput)
			if p.Type != astypes.VisibilityTypePrivate {
				return nil, fmt.Errorf("DescribeImages Type = %q, want PRIVATE", p.Type)
			}
			if p.NextToken == nil {
				return &appstream.DescribeImagesOutput{NextToken: ptrStr("p2"), Images: []astypes.Image{
					{Arn: ptrStr(asTestImageA), Name: ptrStr("img-a"), Visibility: astypes.VisibilityTypePrivate},
				}}, nil
			}
			return &appstream.DescribeImagesOutput{Images: []astypes.Image{
				{Arn: ptrStr(asTestImageB), Name: ptrStr("img-b"), Visibility: astypes.VisibilityTypePrivate},
			}}, nil
		},
		"DescribeImagePermissions": func(in any) (any, error) {
			p := in.(*appstream.DescribeImagePermissionsInput)
			name := sv(p.Name)
			permCalls.add(name)
			if got := p.MaxResults; got == nil || *got != 500 {
				return nil, fmt.Errorf("MaxResults = %v, want 500", got)
			}
			switch {
			case name == "img-b":
				return &appstream.DescribeImagePermissionsOutput{Name: p.Name, SharedImagePermissionsList: []astypes.SharedImagePermissions{
					{SharedAccountId: ptrStr("333333333333"), ImagePermissions: allow(true, true)},
				}}, nil
			case p.NextToken == nil:
				return &appstream.DescribeImagePermissionsOutput{Name: p.Name, NextToken: ptrStr("p2"), SharedImagePermissionsList: []astypes.SharedImagePermissions{
					{SharedAccountId: ptrStr("111111111111"), ImagePermissions: allow(true, false)},
				}}, nil
			default:
				return &appstream.DescribeImagePermissionsOutput{Name: p.Name, SharedImagePermissionsList: []astypes.SharedImagePermissions{
					{SharedAccountId: ptrStr("222222222222"), ImagePermissions: allow(false, true)},
					{ImagePermissions: allow(true, true)},
				}}, nil
			}
		},
		"DescribeSoftwareAssociations": func(in any) (any, error) {
			p := in.(*appstream.DescribeSoftwareAssociationsInput)
			owner := sv(p.AssociatedResource)
			saCalls.add(owner)
			switch {
			case owner == asTestImageA:
				return &appstream.DescribeSoftwareAssociationsOutput{SoftwareAssociations: []astypes.SoftwareAssociations{
					{SoftwareName: ptrStr("Microsoft_Office_2024_LTSC_Standard_64Bit"), Status: astypes.SoftwareDeploymentStatusInstalled},
				}}, nil
			case owner == asTestBuilder && p.NextToken == nil:
				return &appstream.DescribeSoftwareAssociationsOutput{NextToken: ptrStr("p2"), SoftwareAssociations: []astypes.SoftwareAssociations{
					{SoftwareName: ptrStr("Microsoft_Visio_2024_LTSC_Standard_64Bit"), Status: astypes.SoftwareDeploymentStatusStagedForInstallation},
				}}, nil
			case owner == asTestBuilder:
				return &appstream.DescribeSoftwareAssociationsOutput{SoftwareAssociations: []astypes.SoftwareAssociations{
					{SoftwareName: ptrStr("Microsoft_Project_2024_Standard_64Bit")},
					{Status: astypes.SoftwareDeploymentStatusInstalled},
				}}, nil
			default:
				return &appstream.DescribeSoftwareAssociationsOutput{}, nil
			}
		},
	}
}

func scanAppStreamTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, asDenied)
	total, _, err := scanAppStream(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanAppStream_StoresImagePermissionsAndSoftwareAssociations(t *testing.T) {
	st := newTestStore(t)
	var permCalls, saCalls parentCalls
	total, err := scanAppStreamTopLevel(t, st, asTopLevelHandlers(&permCalls, &saCalls))
	if err != nil {
		t.Fatalf("scanAppStream: %v", err)
	}
	// 1 stack + 1 builder + 2 images + 3 permissions + 3 software associations
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	if got := permCalls.distinct(); !slices.Equal(got, []string{"img-a", "img-b"}) {
		t.Errorf("permissions listed for %v, want [img-a img-b]", got)
	}
	if got := saCalls.distinct(); !slices.Equal(got, []string{asTestBuilder, asTestImageA, asTestImageB}) {
		t.Errorf("software associations listed for %v, want [%s %s %s]", got, asTestBuilder, asTestImageA, asTestImageB)
	}

	perms := rowsByNativeID(t, st, TypeAppStreamImagePermission)
	if got := nativeIDs(perms); !slices.Equal(got, []string{asTestPermA111, asTestPermA222, asTestPermB333}) {
		t.Fatalf("permission rows = %v, want [%s %s %s]", got, asTestPermA111, asTestPermA222, asTestPermB333)
	}
	p111 := perms[asTestPermA111]
	if sv(p111.Name) != "111111111111" || sv(p111.Region) != testRegion || p111.Type != TypeAppStreamImagePermission {
		t.Errorf("perm 111 name/region/type = %q/%q/%q, want 111111111111/%s/%s", sv(p111.Name), sv(p111.Region), p111.Type, testRegion, TypeAppStreamImagePermission)
	}
	var permAttrs astypes.SharedImagePermissions
	if err := json.Unmarshal([]byte(p111.AttributesJSON), &permAttrs); err != nil {
		t.Fatalf("perm attrs %s: %v", p111.AttributesJSON, err)
	}
	if permAttrs.ImagePermissions == nil || !*permAttrs.ImagePermissions.AllowFleet || *permAttrs.ImagePermissions.AllowImageBuilder {
		t.Errorf("perm 111 attrs = %s, want AllowFleet true, AllowImageBuilder false", p111.AttributesJSON)
	}

	sas := rowsByNativeID(t, st, TypeAppStreamSoftwareAssociation)
	if got := nativeIDs(sas); !slices.Equal(got, []string{asTestProjectBld, asTestVisioBld, asTestOfficeA}) {
		t.Fatalf("software association rows = %v, want [%s %s %s]", got, asTestProjectBld, asTestVisioBld, asTestOfficeA)
	}
	visio := sas[asTestVisioBld]
	if sv(visio.Name) != "Microsoft_Visio_2024_LTSC_Standard_64Bit" || sv(visio.Status) != "STAGED_FOR_INSTALLATION" ||
		sv(visio.Region) != testRegion || visio.Type != TypeAppStreamSoftwareAssociation {
		t.Errorf("visio name/status/region/type = %q/%q/%q/%q", sv(visio.Name), sv(visio.Status), sv(visio.Region), visio.Type)
	}
	if sv(sas[asTestOfficeA].Status) != "INSTALLED" {
		t.Errorf("office status = %q, want INSTALLED", sv(sas[asTestOfficeA].Status))
	}
	if s := sas[asTestProjectBld].Status; s != nil {
		t.Errorf("project status = %q, want unset for an absent status", *s)
	}
	var saAttrs map[string]any
	if err := json.Unmarshal([]byte(visio.AttributesJSON), &saAttrs); err != nil {
		t.Fatalf("software association attrs %s: %v", visio.AttributesJSON, err)
	}
	if saAttrs["SoftwareName"] != "Microsoft_Visio_2024_LTSC_Standard_64Bit" {
		t.Errorf("visio attrs SoftwareName = %v", saAttrs["SoftwareName"])
	}

	assertContainsNative(t, st, asTestImageA, asTestPermA111)
	assertContainsNative(t, st, asTestImageA, asTestPermA222)
	assertContainsNative(t, st, asTestImageB, asTestPermB333)
	assertContainsNative(t, st, asTestImageA, asTestOfficeA)
	assertContainsNative(t, st, asTestBuilder, asTestVisioBld)
	assertContainsNative(t, st, asTestBuilder, asTestProjectBld)
	assertContainsEdgesDeclared(t, st)
}

// No images or builders means no child calls and no rows; images and builders
// without grants or associations store none either.
func TestScanAppStream_NoImageChildren(t *testing.T) {
	for _, tc := range []struct {
		name          string
		noParents     bool
		wantPermCalls []string
		wantSACalls   []string
	}{
		{"no images or builders", true, nil, nil},
		{"parents without children", false, []string{"img-a", "img-b"}, []string{asTestBuilder, asTestImageA, asTestImageB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var permCalls, saCalls parentCalls
			h := asTopLevelHandlers(&permCalls, &saCalls)
			if tc.noParents {
				h["DescribeImages"] = func(any) (any, error) { return &appstream.DescribeImagesOutput{}, nil }
				h["DescribeImageBuilders"] = func(any) (any, error) { return &appstream.DescribeImageBuildersOutput{}, nil }
			}
			h["DescribeImagePermissions"] = func(in any) (any, error) {
				permCalls.add(sv(in.(*appstream.DescribeImagePermissionsInput).Name))
				return &appstream.DescribeImagePermissionsOutput{}, nil
			}
			h["DescribeSoftwareAssociations"] = func(in any) (any, error) {
				saCalls.add(sv(in.(*appstream.DescribeSoftwareAssociationsInput).AssociatedResource))
				return &appstream.DescribeSoftwareAssociationsOutput{}, nil
			}
			if _, err := scanAppStreamTopLevel(t, st, h); err != nil {
				t.Fatalf("scanAppStream: %v", err)
			}
			if got := permCalls.distinct(); !slices.Equal(got, tc.wantPermCalls) {
				t.Errorf("permissions listed for %v, want %v", got, tc.wantPermCalls)
			}
			if got := saCalls.distinct(); !slices.Equal(got, tc.wantSACalls) {
				t.Errorf("software associations listed for %v, want %v", got, tc.wantSACalls)
			}
			if n := len(rowsByNativeID(t, st, TypeAppStreamImagePermission)); n != 0 {
				t.Errorf("permission rows = %d, want 0", n)
			}
			if n := len(rowsByNativeID(t, st, TypeAppStreamSoftwareAssociation)); n != 0 {
				t.Errorf("software association rows = %d, want 0", n)
			}
		})
	}
}

// asChildFailure fails one child op for the listed parents and serves the rest.
type asChildFailure struct {
	name      string
	failOn    []string // image name (permissions) or owner ARN (software associations)
	err       error
	wantCode  string // "" means the scan returns nil
	wantRows  []string
	wantWarns int
}

// assertASPhaseRan checks the rows a failing child phase must not cost: the
// stack from the first older phase, and the other child phase's rows.
func assertASPhaseRan(t *testing.T, st *store.Store, otherType string, otherWant []string) {
	t.Helper()
	if _, ok := rowsByNativeID(t, st, TypeAppStreamStack)[asTestStackARN]; !ok {
		t.Errorf("stack %s missing; the child phases must run after every older phase", asTestStackARN)
	}
	if got := nativeIDs(rowsByNativeID(t, st, otherType)); !slices.Equal(got, otherWant) {
		t.Errorf("%s rows = %v, want %v; one child phase's failure must not cost the other its rows", otherType, got, otherWant)
	}
}

// A failed image drops only its own grants: deleted since listing is silent,
// a denial or a transient error after retries warns once, and any other error
// fails the scan after every other phase stored its rows.
func TestScanAppStream_FailedImagePermissions(t *testing.T) {
	for _, tc := range []asChildFailure{
		{"image gone", []string{"img-b"}, asGone, "", []string{asTestPermA111, asTestPermA222}, 0},
		{"denied on every image", []string{"img-a", "img-b"}, asDenied, "", nil, 1},
		{"throttled image", []string{"img-b"}, asThrottled, "", []string{asTestPermA111, asTestPermA222}, 1},
		{"other error", []string{"img-b"}, asBadInput, "ValidationException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "appstream:DescribeImagePermissions")
			var permCalls, saCalls parentCalls
			h := asTopLevelHandlers(&permCalls, &saCalls)
			next := h["DescribeImagePermissions"]
			h["DescribeImagePermissions"] = func(in any) (any, error) {
				if slices.Contains(tc.failOn, sv(in.(*appstream.DescribeImagePermissionsInput).Name)) {
					return nil, tc.err
				}
				return next(in)
			}
			_, err := scanAppStreamTopLevel(t, st, h)
			assertASScanErr(t, err, tc.wantCode)
			if got := nativeIDs(rowsByNativeID(t, st, TypeAppStreamImagePermission)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("permission rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			assertASPhaseRan(t, st, TypeAppStreamSoftwareAssociation, []string{asTestProjectBld, asTestVisioBld, asTestOfficeA})
		})
	}
}

// A failed owner drops only its own associations: deleted since listing is
// silent; OperationNotPermittedException (never observed live), a denial or a
// transient error after retries warns once however many owners hit it; any
// other error fails the scan after every other phase stored its rows.
func TestScanAppStream_FailedSoftwareAssociations(t *testing.T) {
	for _, tc := range []asChildFailure{
		{"builder gone", []string{asTestBuilder}, asGone, "", []string{asTestOfficeA}, 0},
		{
			"not permitted on every owner",
			[]string{asTestImageA, asTestImageB, asTestBuilder},
			apiErr("OperationNotPermittedException", "not permitted"), "", nil, 1,
		},
		{
			"not permitted on one owner",
			[]string{asTestImageA},
			apiErr("OperationNotPermittedException", "not permitted"), "",
			[]string{asTestProjectBld, asTestVisioBld},
			1,
		},
		{"denied on every owner", []string{asTestImageA, asTestImageB, asTestBuilder}, asDenied, "", nil, 1},
		{"throttled owner", []string{asTestImageA}, asThrottled, "", []string{asTestProjectBld, asTestVisioBld}, 1},
		{"other error", []string{asTestBuilder}, asBadInput, "ValidationException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "appstream:DescribeSoftwareAssociations")
			var permCalls, saCalls parentCalls
			h := asTopLevelHandlers(&permCalls, &saCalls)
			next := h["DescribeSoftwareAssociations"]
			h["DescribeSoftwareAssociations"] = func(in any) (any, error) {
				if slices.Contains(tc.failOn, sv(in.(*appstream.DescribeSoftwareAssociationsInput).AssociatedResource)) {
					return nil, tc.err
				}
				return next(in)
			}
			_, err := scanAppStreamTopLevel(t, st, h)
			assertASScanErr(t, err, tc.wantCode)
			if got := nativeIDs(rowsByNativeID(t, st, TypeAppStreamSoftwareAssociation)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("software association rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			assertASPhaseRan(t, st, TypeAppStreamImagePermission, []string{asTestPermA111, asTestPermA222, asTestPermB333})
		})
	}
}

func assertASScanErr(t *testing.T, err error, wantCode string) {
	t.Helper()
	if wantCode == "" {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	assertAPICode(t, err, wantCode)
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient, want a hard error", err)
	}
}

// A hard error in an older phase after the image phase still lets the child
// phases run over the images and builders stored, and is returned.
func TestScanAppStream_OlderPhaseErrorStillRunsChildPhases(t *testing.T) {
	st := newTestStore(t)
	var permCalls, saCalls parentCalls
	h := asTopLevelHandlers(&permCalls, &saCalls)
	h["DescribeUsers"] = func(any) (any, error) { return nil, asBadInput }
	total, err := scanAppStreamTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeAppStreamImagePermission)); !slices.Equal(got, []string{asTestPermA111, asTestPermA222, asTestPermB333}) {
		t.Errorf("permission rows = %v, want all three", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeAppStreamSoftwareAssociation)); !slices.Equal(got, []string{asTestProjectBld, asTestVisioBld, asTestOfficeA}) {
		t.Errorf("software association rows = %v, want all three", got)
	}
	// The older phases' counts up to the failure are kept and summed with the
	// child phases': 1 stack + 1 builder + 2 images + 3 + 3.
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
}

// A transient error in an older phase is demoted to a warning under its own
// op so that a child phase's hard error still reaches the dispatcher as a
// hard error.
func TestScanAppStream_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "appstream:DescribeUsers")
	var permCalls, saCalls parentCalls
	h := asTopLevelHandlers(&permCalls, &saCalls)
	h["DescribeUsers"] = func(any) (any, error) { return nil, asThrottled }
	h["DescribeSoftwareAssociations"] = func(any) (any, error) { return nil, asBadInput }
	_, err := scanAppStreamTopLevel(t, st, h)
	assertASScanErr(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

func TestListASSoftwareAssociations_FailsOnRepeatedToken(t *testing.T) {
	var calls atomic.Int32
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"DescribeSoftwareAssociations": func(any) (any, error) {
			n := calls.Add(1)
			if n > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &appstream.DescribeSoftwareAssociationsOutput{NextToken: ptrStr("same"), SoftwareAssociations: []astypes.SoftwareAssociations{
				{SoftwareName: ptrStr(fmt.Sprintf("sw%d", n))},
			}}, nil
		},
	}, asDenied)
	client := appstream.NewFromConfig(cfg)
	rows, err := listASSoftwareAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, testScanID,
		childParent{id: asTestBuilder, arn: asTestBuilder})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
	want := []string{asTestBuilder + "/software-association/sw1", asTestBuilder + "/software-association/sw2"}
	if got := resourceNativeIDs(rows); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want both pages' rows %v", got, want)
	}
}

// AppStream keeps the commercial hand list for aws accounts, which leaves out
// regions the SDK lists, and reads the SDK table for every other partition.
func TestAppStreamRegionSupported_PerPartition(t *testing.T) {
	cases := []struct {
		partition, region string
		want              bool
	}{
		{"aws", "us-east-1", true},
		{"aws", "eu-west-3", false}, // SDK lists it; the commercial list does not
		{"aws-us-gov", "us-gov-west-1", true},
		{"aws-us-gov", "us-gov-east-1", true},
		{"aws-cn", "cn-north-1", true}, // the SDK's cn entry lists no region: no opinion, fail open
	}
	for _, c := range cases {
		if got := appStreamRegionSupported(testAccountIn(c.partition), c.region); got != c.want {
			t.Errorf("appStreamRegionSupported(%s, %s) = %v; want %v", c.partition, c.region, got, c.want)
		}
	}
}

const (
	// asTestListed is the rows of the two-page tagged listings: two per
	// listing, one per page.
	asTestListed   = 2 * 7
	asTestInvalid  = "InvalidParameterValueException"
	asTestTagsOp   = "appstream:ListTagsForResource"
	asTestImageDay = "2026-01-02T03:04:05Z"
)

var asInvalid = apiErr(asTestInvalid, "bad input")

// asKind is one listing whose rows have their tags read: the Describe op, the
// type it stores, the ARN resource type of its rows, the MaxResults it must
// send (0 for none), elem, which builds one element named name under arn, and
// wrap, which builds the op's output holding that element, with next.
type asKind struct {
	op, typ, arnType string
	pageCap          int32
	elem             func(arn, name *string) any
	wrap             func(elem any, next *string) any
}

func (k asKind) name(suffix string) string { return k.arnType + "-" + suffix }

func (k asKind) arn(suffix string) string {
	return "arn:aws:appstream:" + testRegion + ":" + testAccountID + ":" + k.arnType + "/" + k.name(suffix)
}

func (k asKind) page(arn, name, next *string) any { return k.wrap(k.elem(arn, name), next) }

func asKinds() []asKind {
	created, _ := time.Parse(time.RFC3339, asTestImageDay)
	return []asKind{
		{"DescribeStacks", TypeAppStreamStack, "stack", 0, func(arn, name *string) any {
			return astypes.Stack{Arn: arn, Name: name, Description: sdkaws.String("d")}
		}, func(e any, next *string) any {
			return &appstream.DescribeStacksOutput{NextToken: next, Stacks: []astypes.Stack{e.(astypes.Stack)}}
		}},
		{"DescribeAppBlocks", TypeAppStreamAppBlock, "app-block", 0, func(arn, name *string) any {
			return astypes.AppBlock{Arn: arn, Name: name, State: astypes.AppBlockStateActive}
		}, func(e any, next *string) any {
			return &appstream.DescribeAppBlocksOutput{NextToken: next, AppBlocks: []astypes.AppBlock{e.(astypes.AppBlock)}}
		}},
		{"DescribeAppBlockBuilders", TypeAppStreamAppBlockBuilder, "app-block-builder", 25, func(arn, name *string) any {
			return astypes.AppBlockBuilder{Arn: arn, Name: name, State: astypes.AppBlockBuilderStateRunning}
		}, func(e any, next *string) any {
			return &appstream.DescribeAppBlockBuildersOutput{NextToken: next, AppBlockBuilders: []astypes.AppBlockBuilder{e.(astypes.AppBlockBuilder)}}
		}},
		{"DescribeApplications", TypeAppStreamApplication, "application", 0, func(arn, name *string) any {
			return astypes.Application{Arn: arn, Name: name, Enabled: sdkaws.Bool(true)}
		}, func(e any, next *string) any {
			return &appstream.DescribeApplicationsOutput{NextToken: next, Applications: []astypes.Application{e.(astypes.Application)}}
		}},
		{"DescribeFleets", TypeAppStreamFleet, "fleet", 0, func(arn, name *string) any {
			return astypes.Fleet{Arn: arn, Name: name, State: astypes.FleetStateRunning}
		}, func(e any, next *string) any {
			return &appstream.DescribeFleetsOutput{NextToken: next, Fleets: []astypes.Fleet{e.(astypes.Fleet)}}
		}},
		{"DescribeImageBuilders", TypeAppStreamImageBuilder, "image-builder", 0, func(arn, name *string) any {
			return astypes.ImageBuilder{Arn: arn, Name: name, State: astypes.ImageBuilderStateRunning}
		}, func(e any, next *string) any {
			return &appstream.DescribeImageBuildersOutput{NextToken: next, ImageBuilders: []astypes.ImageBuilder{e.(astypes.ImageBuilder)}}
		}},
		{"DescribeImages", TypeAppStreamImage, "image", 25, func(arn, name *string) any {
			return astypes.Image{Arn: arn, Name: name, CreatedTime: &created, Visibility: astypes.VisibilityTypePrivate}
		}, func(e any, next *string) any {
			return &appstream.DescribeImagesOutput{NextToken: next, Images: []astypes.Image{e.(astypes.Image)}}
		}},
	}
}

func asKindOf(t *testing.T, op string) asKind {
	t.Helper()
	for _, k := range asKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged AppStream listing", op)
	return asKind{}
}

// asCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type asCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newASCalls tags the "tagged" row of every kind with its type.
func newASCalls() *asCalls {
	c := &asCalls{ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range asKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *asCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *asCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// sent is the string field of every input op was called with, sorted.
func (c *asCalls) sent(op, field string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var got []string
	for _, in := range c.inputs[op] {
		got = append(got, sv(bacField[*string](in, field)))
	}
	slices.Sort(got)
	return got
}

func (c *asCalls) listTags(in any) (any, error) {
	arn := sv(in.(*appstream.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &appstream.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *asCalls) twoPages(k asKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// asTagHandlers answers every AppStream op the scan calls: each tagged kind
// in two pages, every other listing empty.
func asTagHandlers(c *asCalls) map[string]func(in any) (any, error) {
	empty := func(op string, out any) func(any) (any, error) {
		return func(in any) (any, error) {
			c.record(op, in)
			return out, nil
		}
	}
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":                  c.listTags,
		"DescribeEntitlements":                 empty("DescribeEntitlements", &appstream.DescribeEntitlementsOutput{}),
		"DescribeApplicationFleetAssociations": empty("DescribeApplicationFleetAssociations", &appstream.DescribeApplicationFleetAssociationsOutput{}),
		"DescribeDirectoryConfigs":             empty("DescribeDirectoryConfigs", &appstream.DescribeDirectoryConfigsOutput{}),
		"ListAssociatedFleets":                 empty("ListAssociatedFleets", &appstream.ListAssociatedFleetsOutput{}),
		"DescribeUserStackAssociations":        empty("DescribeUserStackAssociations", &appstream.DescribeUserStackAssociationsOutput{}),
		"DescribeUsers":                        empty("DescribeUsers", &appstream.DescribeUsersOutput{}),
		"DescribeImagePermissions":             empty("DescribeImagePermissions", &appstream.DescribeImagePermissionsOutput{}),
		"DescribeSoftwareAssociations":         empty("DescribeSoftwareAssociations", &appstream.DescribeSoftwareAssociationsOutput{}),
	}
	for _, k := range asKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// asTagScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func asTagScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, asDenied)
	return scanAppStream(ctx, acct, testRegion, st, testScanID)
}

func asTagReadARNs() []string {
	var arns []string
	for _, k := range asKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// Every tagged listing stores both its pages, each row under its ARN, named
// by its name, with the listed element as attributes and the tags
// ListTagsForResource returns for that ARN, nil for a resource without tags.
// Exactly those ARNs are sent to the tag op. Images and app block builders
// are sent the page cap 25, the other listings none; images are asked for
// PRIVATE only. Each listing is called once per page: the fleets are not
// listed again for their application associations, which are read for the
// fleets stored. No error, no warning.
func TestScanAppStream_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newASCalls()

	total, inserted, err := asTagScan(context.Background(), st, asTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != asTestListed || inserted != asTestListed {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, asTestListed, asTestListed)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range asKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || r.Status != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, Managed = %q, %q, %v, %v; want %q, %q, nil, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.ManagedByProvider, k.name(suffix), testRegion)
			}
			if k.typ == TypeAppStreamImage {
				if sv(r.CreatedAt) != asTestImageDay {
					t.Errorf("%s CreatedAt = %q; want %s", arn, sv(r.CreatedAt), asTestImageDay)
				}
			} else if r.CreatedAt != nil {
				t.Errorf("%s CreatedAt = %q; want nil", arn, *r.CreatedAt)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != newTestAccount(testAccountID).Name || r.DiscoveredBy != testScanID {
				t.Errorf("%s Provider, AccountID, AccountName, DiscoveredBy = %q, %q, %q, %q; want aws, %s, %s, %s",
					arn, r.Provider, r.AccountID, sv(r.AccountName), r.DiscoveredBy, testAccountID, newTestAccount(testAccountID).Name, testScanID)
			}
			name := k.name(suffix)
			if want := mustJSON(k.elem(&arn, &name)); r.AttributesJSON != want {
				t.Errorf("%s attrs = %s; want the listed element %s", arn, r.AttributesJSON, want)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, in := range calls.inputs[k.op] {
			n := bacField[*int32](in, "MaxResults")
			switch {
			case k.pageCap == 0 && n != nil:
				t.Errorf("%s MaxResults = %d; want none sent", k.op, *n)
			case k.pageCap != 0 && (n == nil || *n != k.pageCap):
				t.Errorf("%s MaxResults = %v; want %d", k.op, n, k.pageCap)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
	}
	for _, in := range calls.inputs["DescribeImages"] {
		if v := in.(*appstream.DescribeImagesInput).Type; v != astypes.VisibilityTypePrivate {
			t.Errorf("DescribeImages Type = %q; want PRIVATE", v)
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, asTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, asTagReadARNs())
	}
	fleets := asKindOf(t, "DescribeFleets")
	if got, want := calls.sent("DescribeApplicationFleetAssociations", "FleetName"), []string{fleets.name("bare"), fleets.name("tagged")}; !slices.Equal(got, want) {
		t.Errorf("application-fleet associations read for %v; want %v", got, want)
	}
	stacks := asKindOf(t, "DescribeStacks")
	if got, want := calls.sent("DescribeEntitlements", "StackName"), []string{stacks.name("bare"), stacks.name("tagged")}; !slices.Equal(got, want) {
		t.Errorf("entitlements read for %v; want %v", got, want)
	}
}

// Empty listings store nothing, read no tags and warn nothing.
func TestScanAppStream_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newASCalls()
	h := asTagHandlers(calls)
	for op, out := range map[string]any{
		"DescribeStacks":           &appstream.DescribeStacksOutput{},
		"DescribeAppBlocks":        &appstream.DescribeAppBlocksOutput{},
		"DescribeAppBlockBuilders": &appstream.DescribeAppBlockBuildersOutput{},
		"DescribeApplications":     &appstream.DescribeApplicationsOutput{},
		"DescribeFleets":           &appstream.DescribeFleetsOutput{},
		"DescribeImageBuilders":    &appstream.DescribeImageBuildersOutput{},
		"DescribeImages":           &appstream.DescribeImagesOutput{},
	} {
		h[op] = func(any) (any, error) { return out, nil }
	}
	total, inserted, err := asTagScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	for _, op := range []string{"ListTagsForResource", "DescribeEntitlements", "DescribeApplicationFleetAssociations", "DescribeImagePermissions", "DescribeSoftwareAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppStream_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := asTagScan(context.Background(), st, asTagHandlers(newASCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := asTagScan(context.Background(), st, asTagHandlers(newASCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != asTestListed || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, asTestListed)
	}
}

// A failed tag read: ResourceNotFoundException (the one code the op models)
// drops the row silently; a denial, its code bare or namespaced as an
// rpc-v2-cbor service may send it, keeps the row untagged and warns once
// under the tag op; a throttle keeps it and is transient; any other error
// keeps it and is an error.
func TestScanAppStream_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range asKinds() {
		arn := k.arn("tagged")
		cases = append(cases,
			tagCase{k.typ + "/not found", arn, k.typ, asGone, -1, "", asTestListed - 1, false, 0},
			tagCase{k.typ + "/denied", arn, k.typ, asDenied, -1, "", asTestListed, true, 1},
			tagCase{k.typ + "/transient", arn, k.typ, asThrottled, outcomeTransient, "ThrottlingException", asTestListed, true, 0},
			tagCase{k.typ + "/other error", arn, k.typ, asInvalid, outcomeError, asTestInvalid, asTestListed, true, 0},
		)
	}
	stack := asKindOf(t, "DescribeStacks").arn("tagged")
	cases = append(cases, tagCase{
		"namespaced denial", stack, TypeAppStreamStack,
		apiErr("com.amazon.coral.service#AccessDeniedException", "not authorized"), -1, "", asTestListed, true, 1,
	})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newASCalls()
			calls.errs[c.arn] = c.err

			total, _, err := asTagScan(context.Background(), st, asTagHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != asTestTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, asTestTagsOp, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// A parent whose tag read says it was deleted is neither stored nor read for
// children: a stack gets no entitlement listing, a fleet no application
// association listing, an image no permission or software listing, an image
// builder no software listing.
func TestScanAppStream_ParentGoneHasNoChildReads(t *testing.T) {
	for _, c := range []struct {
		op         string
		childOps   []string
		childField string
		key        func(k asKind) string
	}{
		{"DescribeStacks", []string{"DescribeEntitlements", "ListAssociatedFleets", "DescribeUserStackAssociations"}, "StackName", func(k asKind) string { return k.name("tagged") }},
		{"DescribeFleets", []string{"DescribeApplicationFleetAssociations"}, "FleetName", func(k asKind) string { return k.name("tagged") }},
		{"DescribeImages", []string{"DescribeImagePermissions"}, "Name", func(k asKind) string { return k.name("tagged") }},
		{"DescribeImages", []string{"DescribeSoftwareAssociations"}, "AssociatedResource", func(k asKind) string { return k.arn("tagged") }},
		{"DescribeImageBuilders", []string{"DescribeSoftwareAssociations"}, "AssociatedResource", func(k asKind) string { return k.arn("tagged") }},
	} {
		t.Run(c.op+"/"+c.childOps[0], func(t *testing.T) {
			st := newTestStore(t)
			calls := newASCalls()
			k := asKindOf(t, c.op)
			calls.errs[k.arn("tagged")] = asGone
			if _, _, err := asTagScan(context.Background(), st, asTagHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, op := range c.childOps {
				got := calls.sent(op, c.childField)
				if slices.Contains(got, c.key(k)) {
					t.Errorf("%s read for the deleted %s: %v", op, c.key(k), got)
				}
				if !slices.Contains(got, strings.Replace(c.key(k), "tagged", "bare", 1)) {
					t.Errorf("%s not read for the stored sibling: %v", op, got)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanAppStream_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, asTestTagsOp)
	calls := newASCalls()
	for _, op := range []string{"DescribeStacks", "DescribeImages"} {
		k := asKindOf(t, op)
		calls.errs[k.arn("tagged")] = asDenied
		calls.errs[k.arn("bare")] = asDenied
	}

	total, _, err := asTagScan(context.Background(), st, asTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != asTestListed {
		t.Errorf("total = %d; want %d", total, asTestListed)
	}
	first := asKindOf(t, "DescribeStacks").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", asTestTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range []string{"DescribeStacks", "DescribeImages"} {
		k := asKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanAppStream_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, asTestTagsOp)
	calls := newASCalls()
	calls.errs[asKindOf(t, "DescribeFleets").arn("tagged")] = asThrottled
	calls.errs[asKindOf(t, "DescribeImages").arn("bare")] = asInvalid

	_, _, err := asTagScan(context.Background(), st, asTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, asTestInvalid)
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", asTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows.
func TestScanAppStream_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeAppStreamApplication)
	calls := newASCalls()
	k := asKindOf(t, "DescribeApplications")
	calls.errs[k.arn("tagged")] = asDenied

	total, _, err := asTagScan(context.Background(), st, asTagHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count(k.op); n != 1 {
		t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
	}
	for _, op := range []string{"DescribeFleets", "DescribeImageBuilders", "DescribeImages", "DescribeUsers", "DescribeImagePermissions"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// Stacks, app blocks and app block builders (2 each) ran before it.
	if total != 3*2 {
		t.Errorf("total = %d; want %d", total, 3*2)
	}
}

// A listing's failed page keeps the page read before it, and its error stops
// no later listing. The stacks listing, which once ended the whole scan on
// its first error, now keeps its first page and its children.
func TestScanAppStream_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"DescribeStacks", "DescribeAppBlocks", "DescribeImages"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newASCalls()
			handlers := asTagHandlers(calls)
			k := asKindOf(t, op)
			handlers[k.op] = failOnCall(2, asInvalid, handlers[k.op])

			total, _, err := asTagScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, asTestInvalid)
			if total != asTestListed-1 {
				t.Errorf("total = %d; want %d", total, asTestListed-1)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeAppStreamImage)); op != "DescribeImages" && got != 2 {
				t.Errorf("images (a later phase) = %d; want 2", got)
			}
			if n := calls.count("DescribeSoftwareAssociations"); n == 0 {
				t.Error("software associations (the last phase) not read")
			}
		})
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanAppStream_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := asTagHandlers(newASCalls())
	k := asKindOf(t, "DescribeImages")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}
	total, _, err := asTagScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != asTestListed {
		t.Errorf("total = %d; want %d", total, asTestListed)
	}
	if got := nativeIDs(rowsByNativeID(t, st, k.typ)); !slices.Equal(got, []string{k.arn("bare"), k.arn("tagged")}) {
		t.Errorf("images = %v; want both pages'", got)
	}
}

// An element without an ARN is not stored, is not sent to the tag op, is not
// read for children, and is warned about once under its listing, named by
// its name or by the fallback text when it has none.
func TestScanAppStream_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, error)
	}{
		{"fleet", "DescribeFleets", "fleet-x", func(any) (any, error) {
			return &appstream.DescribeFleetsOutput{Fleets: []astypes.Fleet{{Name: sdkaws.String("fleet-x")}}}, nil
		}},
		{"app block without a name", "DescribeAppBlocks", noIdentifier, func(any) (any, error) {
			return &appstream.DescribeAppBlocksOutput{AppBlocks: []astypes.AppBlock{{}}}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newASCalls()
			handlers := asTagHandlers(calls)
			handlers[c.op] = c.answer

			total, _, err := asTagScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := asTestListed - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "appstream:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Fatalf("warnings = %v; want one under appstream:%s naming %q", w, c.op, c.key)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if n := calls.count("DescribeApplicationFleetAssociations"); c.op == "DescribeFleets" && n != 0 {
				t.Errorf("application-fleet associations read %d times for a fleet not stored; want 0", n)
			}
		})
	}
}

// An application listed with an ARN but no name (the model requires
// neither) is stored named by its ARN, with its tags read.
func TestScanAppStream_NamelessRowNamedByARN(t *testing.T) {
	st := newTestStore(t)
	calls := newASCalls()
	handlers := asTagHandlers(calls)
	arn := asKindOf(t, "DescribeApplications").arn("nameless")
	calls.tags[arn] = map[string]string{"k": "v"}
	handlers["DescribeApplications"] = func(any) (any, error) {
		return &appstream.DescribeApplicationsOutput{Applications: []astypes.Application{{Arn: &arn}}}, nil
	}
	if _, _, err := asTagScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeAppStreamApplication)[arn]
	if !ok || sv(r.Name) != arn {
		t.Fatalf("%s stored %v named %q; want stored named by its ARN", arn, ok, sv(r.Name))
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanAppStream_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newASCalls()
			handlers := asTagHandlers(calls)
			stackARN := "arn:" + c.partition + ":appstream:" + c.region + ":" + testAccountID + ":stack/s-p"
			calls.tags[stackARN] = map[string]string{"k": "v"}
			handlers["DescribeStacks"] = func(any) (any, error) {
				return &appstream.DescribeStacksOutput{Stacks: []astypes.Stack{{Arn: &stackARN, Name: sdkaws.String("s-p")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, asDenied)
			if _, _, err := scanAppStream(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.tagARNs, stackARN) {
				t.Errorf("ListTagsForResource ARNs %v lack %s", calls.tagARNs, stackARN)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAppStreamStack)[stackARN].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every later listing still runs.
func TestScanAppStream_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, call := range []int{1, 2} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := asTagHandlers(newASCalls())
			k := asKindOf(t, "DescribeFleets")
			handlers[k.op] = failOnCall(call, asDenied, handlers[k.op])

			total, _, err := asTagScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			kept := call - 1
			if want := asTestListed - 2 + kept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 1 || w[0].Service != "appstream:"+k.op {
				t.Errorf("warnings = %v; want one under appstream:%s", w, k.op)
			}
			if got := len(rowsByNativeID(t, st, TypeAppStreamImage)); got != 2 {
				t.Errorf("images (a later phase) = %d; want 2", got)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanAppStream_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newASCalls()
	handlers := asTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := asKindOf(t, "DescribeFleets")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := asTagScan(ctx, st, handlers)
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
	if n := calls.count("DescribeImages"); n != 0 {
		t.Errorf("DescribeImages called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanAppStream_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "appstream2.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := asTagHandlers(newASCalls())
		handlers["DescribeImageBuilders"] = func(any) (any, error) { return nil, nx }
		total, _, err := asTagScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != asTestListed-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, asTestListed-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newASCalls()
		calls.errs[asKindOf(t, "DescribeAppBlocks").arn("bare")] = nx
		_, _, err := asTagScan(context.Background(), st, asTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanAppStream(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// asAppFleetAssoc is the association of application app with fleet, as
// DescribeApplicationFleetAssociations returns it.
func asAppFleetAssoc(fleet, app string) astypes.ApplicationFleetAssociation {
	return astypes.ApplicationFleetAssociation{
		ApplicationArn: sdkaws.String("arn:aws:appstream:" + testRegion + ":" + testAccountID + ":application/" + app),
		FleetName:      sdkaws.String(fleet),
	}
}

// asAppFleetAssocNativeID is the NativeID of the association of app with
// fleet.
func asAppFleetAssocNativeID(fleet, app string) string {
	return "arn:aws:appstream:" + testRegion + ":" + testAccountID + ":application-fleet-association/" + fleet + "/" + sv(asAppFleetAssoc(fleet, app).ApplicationArn)
}

// The application-fleet associations are read per stored fleet. The first
// fleet's two pages are stored under {fleet}/{applicationArn}, with the
// listed element as attributes. A failure of the second fleet keeps them: a
// hard error is returned under the op, a denial warns once under it, and a
// repeated token is an error. A throttle on it beside another phase's hard
// error is demoted to one warning under the op, which pins that the phase's
// errors are filed under that op. An association without an application ARN
// is warned about, naming its fleet, and not stored.
func TestScanAppStream_AppFleetAssociations(t *testing.T) {
	fleets := asKindOf(t, "DescribeFleets")
	first, second := fleets.name("tagged"), fleets.name("bare")
	const op = "appstream:DescribeApplicationFleetAssociations"
	wantFirst := []string{asAppFleetAssocNativeID(first, "app-1"), asAppFleetAssocNativeID(first, "app-2")}
	for _, c := range []struct {
		name      string
		second    func(token *string) (any, error)
		repeat    bool
		wantErr   func(t *testing.T, err error)
		wantRows  []string
		wantWarns int
		usersErr  error
		wantNamed string
	}{
		{"second fleet's associations stored", func(*string) (any, error) {
			return &appstream.DescribeApplicationFleetAssociationsOutput{ApplicationFleetAssociations: []astypes.ApplicationFleetAssociation{asAppFleetAssoc(second, "app-3")}}, nil
		}, false, nil, append([]string{asAppFleetAssocNativeID(second, "app-3")}, wantFirst...), 0, nil, ""},
		{"hard error on the second fleet", func(*string) (any, error) { return nil, asInvalid }, false, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, asTestInvalid)
		}, wantFirst, 0, nil, ""},
		{"denied on the second fleet", func(*string) (any, error) { return nil, asDenied }, false, nil, wantFirst, 1, nil, ""},
		{"association without an application ARN", func(*string) (any, error) {
			return &appstream.DescribeApplicationFleetAssociationsOutput{ApplicationFleetAssociations: []astypes.ApplicationFleetAssociation{{FleetName: sdkaws.String(second)}}}, nil
		}, false, nil, wantFirst, 1, nil, second},
		{"throttle demoted under the op beside a hard error", func(*string) (any, error) { return nil, asThrottled }, false, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, asTestInvalid)
		}, wantFirst, 1, asInvalid, ""},
		{"repeated token", func(*string) (any, error) {
			return &appstream.DescribeApplicationFleetAssociationsOutput{}, nil
		}, true, func(t *testing.T, err error) {
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
		}, wantFirst, 0, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, op)
			handlers := asTagHandlers(newASCalls())
			if c.usersErr != nil {
				handlers["DescribeUsers"] = func(any) (any, error) { return nil, c.usersErr }
			}
			handlers["DescribeApplicationFleetAssociations"] = func(in any) (any, error) {
				p := in.(*appstream.DescribeApplicationFleetAssociationsInput)
				if sv(p.FleetName) == second {
					return c.second(p.NextToken)
				}
				if p.MaxResults != nil {
					return nil, fmt.Errorf("MaxResults = %d; want none sent", *p.MaxResults)
				}
				next := sdkaws.String("p2")
				if c.repeat {
					next = sdkaws.String("same")
				}
				if p.NextToken == nil {
					return &appstream.DescribeApplicationFleetAssociationsOutput{NextToken: next, ApplicationFleetAssociations: []astypes.ApplicationFleetAssociation{asAppFleetAssoc(first, "app-1")}}, nil
				}
				out := &appstream.DescribeApplicationFleetAssociationsOutput{ApplicationFleetAssociations: []astypes.ApplicationFleetAssociation{asAppFleetAssoc(first, "app-2")}}
				if c.repeat {
					out.NextToken = next
				}
				return out, nil
			}
			_, _, err := asTagScan(context.Background(), st, handlers)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				c.wantErr(t, err)
			}
			rows := rowsByNativeID(t, st, TypeAppStreamApplicationFleetAssociation)
			if got := nativeIDs(rows); !slices.Equal(got, c.wantRows) {
				t.Errorf("association rows = %v; want %v", got, c.wantRows)
			}
			r := rows[wantFirst[0]]
			wantName := first + "/" + sv(asAppFleetAssoc(first, "app-1").ApplicationArn)
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(asAppFleetAssoc(first, "app-1")) || r.TagsJSON != nil {
				t.Errorf("association Name, Region, attrs, tags = %q, %q, %s, %v; want %q, %s, the listed element, nil",
					sv(r.Name), sv(r.Region), r.AttributesJSON, r.TagsJSON, wantName, testRegion)
			}
			if len(*warns) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", op, *warns, c.wantWarns)
			}
			if c.wantNamed != "" && !strings.Contains((*warns)[0].Message, c.wantNamed) {
				t.Errorf("warning %q; want it to name %s", (*warns)[0].Message, c.wantNamed)
			}
		})
	}
}

// A parent stored without the name its children key on is stored with its
// tags, is not read for children, and is warned about once under its
// listing, naming its ARN.
func TestScanAppStream_NamelessParentWarns(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newASCalls()
	handlers := asTagHandlers(calls)
	arn := asKindOf(t, "DescribeStacks").arn("nameless")
	calls.tags[arn] = map[string]string{"k": "v"}
	handlers["DescribeStacks"] = func(in any) (any, error) {
		calls.record("DescribeStacks", in)
		return &appstream.DescribeStacksOutput{Stacks: []astypes.Stack{{Arn: &arn}}}, nil
	}
	if _, _, err := asTagScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeAppStreamStack)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	for _, op := range []string{"DescribeEntitlements", "ListAssociatedFleets", "DescribeUserStackAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times for a stack without a name; want 0", op, n)
		}
	}
	w := warns()
	if len(w) != 1 || w[0].Service != "appstream:DescribeStacks" || w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, arn) {
		t.Fatalf("warnings = %v; want one under appstream:DescribeStacks naming %s", w, arn)
	}
}

// A nameless stack stored on the first page sends no warning when the second
// page's write fails: no warning goes out after a failed write.
func TestScanAppStream_NamelessParentFailedWriteWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	k := asKindOf(t, "DescribeStacks")
	refused := k.arn("refused")
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_stack BEFORE INSERT ON resources
		WHEN NEW.native_id = '` + refused + `'
		BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	handlers := asTagHandlers(newASCalls())
	arn := k.arn("nameless")
	handlers["DescribeStacks"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") == nil {
			return &appstream.DescribeStacksOutput{NextToken: sdkaws.String("p2"), Stacks: []astypes.Stack{{Arn: &arn}}}, nil
		}
		name := k.name("refused")
		return &appstream.DescribeStacksOutput{Stacks: []astypes.Stack{{Arn: &refused, Name: &name}}}, nil
	}
	if _, _, err := asTagScan(context.Background(), st, handlers); !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}
