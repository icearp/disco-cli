package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

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

// A transient error in an older phase is demoted to a warning so that a child
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanAppStream_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "appstream")
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

func TestListASSoftwareAssociations_StopsOnRepeatedToken(t *testing.T) {
	var calls atomic.Int32
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"DescribeSoftwareAssociations": func(any) (any, error) {
			if calls.Add(1) > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &appstream.DescribeSoftwareAssociationsOutput{NextToken: ptrStr("same")}, nil
		},
	}, asDenied)
	client := appstream.NewFromConfig(cfg)
	_, err := listASSoftwareAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, testScanID,
		childParent{id: asTestBuilder, arn: asTestBuilder})
	if err != nil {
		t.Fatalf("listASSoftwareAssociations: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}
