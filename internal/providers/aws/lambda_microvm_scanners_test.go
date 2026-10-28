package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	lambdacoretypes "github.com/aws/aws-sdk-go-v2/service/lambdacore/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdamicrovmstypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

var lmvCreated = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// lambdaStubFail makes a stubbed list call serve its first page pages and
// then fail with err.
type lambdaStubFail struct {
	page int
	err  error
}

// stubLambdaNew serves the Lambda Core and Lambda MicroVMs list ops from
// fixed page sets. fail is keyed by op name, or "op/<image>" for the
// per-image version list.
type stubLambdaNew struct {
	mu         sync.Mutex
	calls      []string
	fail       map[string]lambdaStubFail
	connectors [][]lambdacoretypes.NetworkConnectorSummary
	images     [][]lambdamicrovmstypes.MicrovmImageSummary
	versions   map[string][][]lambdamicrovmstypes.MicrovmImageVersionSummary
	microvms   [][]lambdamicrovmstypes.MicrovmItem
	managed    [][]lambdamicrovmstypes.ManagedMicrovmImageSummary
	// tagErrs fails ListTags for an ARN; any other ARN has no tags.
	tagErrs map[string]error
}

func (s *stubLambdaNew) ListTags(_ context.Context, in *lambdamicrovms.ListTagsInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListTagsOutput, error) {
	if err := s.tagErrs[sv(in.Resource)]; err != nil {
		return nil, err
	}
	return &lambdamicrovms.ListTagsOutput{}, nil
}

// lambdaServe records the call to key and pages through pages via stubPage,
// cut short by the failure queued for key.
func lambdaServe[T any](s *stubLambdaNew, key string, pages [][]T, token *string) ([]T, *string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, key)
	f, failing := s.fail[key]
	s.mu.Unlock()
	if failing {
		return stubPage(pages[:min(f.page, len(pages))], token, f.err)
	}
	return stubPage(pages, token, nil)
}

func (s *stubLambdaNew) ListNetworkConnectors(_ context.Context, in *lambdacore.ListNetworkConnectorsInput, _ ...func(*lambdacore.Options)) (*lambdacore.ListNetworkConnectorsOutput, error) {
	items, next, err := lambdaServe(s, "ListNetworkConnectors", s.connectors, in.Marker)
	if err != nil {
		return nil, err
	}
	return &lambdacore.ListNetworkConnectorsOutput{NetworkConnectors: items, NextMarker: next}, nil
}

func (s *stubLambdaNew) ListMicrovmImages(_ context.Context, in *lambdamicrovms.ListMicrovmImagesInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImagesOutput, error) {
	items, next, err := lambdaServe(s, "ListMicrovmImages", s.images, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &lambdamicrovms.ListMicrovmImagesOutput{Items: items, NextToken: next}, nil
}

func (s *stubLambdaNew) ListMicrovmImageVersions(_ context.Context, in *lambdamicrovms.ListMicrovmImageVersionsInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImageVersionsOutput, error) {
	image := sv(in.ImageIdentifier)
	items, next, err := lambdaServe(s, "ListMicrovmImageVersions/"+image, s.versions[image], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &lambdamicrovms.ListMicrovmImageVersionsOutput{Items: items, NextToken: next}, nil
}

func (s *stubLambdaNew) ListMicrovms(_ context.Context, in *lambdamicrovms.ListMicrovmsInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmsOutput, error) {
	items, next, err := lambdaServe(s, "ListMicrovms", s.microvms, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &lambdamicrovms.ListMicrovmsOutput{Items: items, NextToken: next}, nil
}

func (s *stubLambdaNew) ListManagedMicrovmImages(_ context.Context, in *lambdamicrovms.ListManagedMicrovmImagesInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListManagedMicrovmImagesOutput, error) {
	items, next, err := lambdaServe(s, "ListManagedMicrovmImages", s.managed, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &lambdamicrovms.ListManagedMicrovmImagesOutput{Items: items, NextToken: next}, nil
}

func lmvARN(kind, id string) string {
	return "arn:aws:lambda:" + testRegion + ":" + testAccountID + ":" + kind + ":" + id
}

func lmvRows(t *testing.T, st *store.Store, typ string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: testAccountID, Types: []string{typ}, Limit: util.AllResources, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources %s: %v", typ, err)
	}
	out := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		out[r.NativeID] = r
	}
	return out
}

func lmvImage(name string) lambdamicrovmstypes.MicrovmImageSummary {
	return lambdamicrovmstypes.MicrovmImageSummary{
		ImageArn: sdkaws.String(lmvARN("microvm-image", name)), Name: sdkaws.String(name),
		State: lambdamicrovmstypes.MicrovmImageStateCreated, CreatedAt: &lmvCreated,
	}
}

func lmvMicrovm(id string) lambdamicrovmstypes.MicrovmItem {
	return lambdamicrovmstypes.MicrovmItem{
		ImageArn: sdkaws.String(lmvARN("microvm-image", "img")), ImageVersion: sdkaws.String("1"),
		MicrovmId: sdkaws.String(id), State: lambdamicrovmstypes.MicrovmStateRunning, StartedAt: &lmvCreated,
	}
}

func lmvManaged(name string) lambdamicrovmstypes.ManagedMicrovmImageSummary {
	return lambdamicrovmstypes.ManagedMicrovmImageSummary{
		ImageArn: sdkaws.String("arn:aws:lambda:" + testRegion + ":aws:microvm-image:" + name), CreatedAt: &lmvCreated,
	}
}

// lmvListCase is one account-wide list scanner: its fill seeds two pages
// holding items a and b (plus one id-less item) then c.
type lmvListCase struct {
	name, op, typ string
	fill          func(*stubLambdaNew)
	run           func(context.Context, *stubLambdaNew, *store.Store) (int, int, error)
	wantIDs       []string // NativeIDs of a, b, c
	wantName      string   // Name of a
	wantStatus    string   // Status of a; "" = unset
	wantCreated   bool
	wantManaged   bool
	attrKey       string // an attribute the fixture sets on a
	attrWant      string
	// idLessWarns is the warnings the id-less item on the first page sends:
	// 1 for the listings stored with their tags, which warn about it.
	idLessWarns int
}

// lmvTaggedPhase runs a tagged phase and returns its counts and joined
// errors.
func lmvTaggedPhase(st *store.Store, run func(p *phaseTotals) bool) (int, int, error) {
	var p phaseTotals
	run(&p)
	return p.result(st, newTestAccount(testAccountID), testRegion)
}

func lmvListCases() []lmvListCase {
	vmNative := func(id string) string { return lmvARN("microvm-image", "img") + "/microvm/" + id }
	managedARN := func(name string) string { return sv(lmvManaged(name).ImageArn) }
	return []lmvListCase{
		{
			name: "network connectors", op: "ListNetworkConnectors", typ: TypeLambdaNetworkConnector,
			fill: func(s *stubLambdaNew) {
				nc := func(id string) lambdacoretypes.NetworkConnectorSummary {
					return lambdacoretypes.NetworkConnectorSummary{
						Arn: sdkaws.String(lmvARN("network-connector", id)), Id: sdkaws.String(id),
						Name: sdkaws.String("n-" + id), State: lambdacoretypes.NetworkConnectorStateActive,
					}
				}
				s.connectors = [][]lambdacoretypes.NetworkConnectorSummary{{nc("a"), nc("b"), {Id: sdkaws.String("x")}}, {nc("c")}}
			},
			run: func(ctx context.Context, s *stubLambdaNew, st *store.Store) (int, int, error) {
				return lmvTaggedPhase(st, func(p *phaseTotals) bool {
					return scanLambdaNetworkConnectors(ctx, s, newTestAccount(testAccountID), testRegion, st, testScanID, lambdaTags(&stubLambdaPerFn{}), p)
				})
			},
			idLessWarns: 1,
			wantIDs:     []string{lmvARN("network-connector", "a"), lmvARN("network-connector", "b"), lmvARN("network-connector", "c")},
			wantName:    "n-a", wantStatus: "ACTIVE", attrKey: "Id", attrWant: "a",
		},
		{
			name: "microvm images", op: "ListMicrovmImages", typ: TypeLambdaMicrovmImage,
			fill: func(s *stubLambdaNew) {
				s.images = [][]lambdamicrovmstypes.MicrovmImageSummary{{lmvImage("a"), lmvImage("b"), {Name: sdkaws.String("x")}}, {lmvImage("c")}}
			},
			run: func(ctx context.Context, s *stubLambdaNew, st *store.Store) (int, int, error) {
				return lmvTaggedPhase(st, func(p *phaseTotals) bool {
					_, failed := scanLambdaMicrovmImages(ctx, s, newTestAccount(testAccountID), testRegion, st, testScanID, lambdaMicrovmTags(s), p)
					return failed
				})
			},
			idLessWarns: 1,
			wantIDs:     []string{lmvARN("microvm-image", "a"), lmvARN("microvm-image", "b"), lmvARN("microvm-image", "c")},
			wantName:    "a", wantStatus: "CREATED", wantCreated: true, attrKey: "ImageArn", attrWant: lmvARN("microvm-image", "a"),
		},
		{
			name: "microvms", op: "ListMicrovms", typ: TypeLambdaMicrovm,
			fill: func(s *stubLambdaNew) {
				s.microvms = [][]lambdamicrovmstypes.MicrovmItem{{lmvMicrovm("a"), lmvMicrovm("b"), {ImageArn: sdkaws.String(lmvARN("microvm-image", "img"))}}, {lmvMicrovm("c")}}
			},
			run: func(ctx context.Context, s *stubLambdaNew, st *store.Store) (int, int, error) {
				return scanLambdaMicrovms(ctx, s, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			wantIDs:  []string{vmNative("a"), vmNative("b"), vmNative("c")},
			wantName: "a", wantStatus: "RUNNING", attrKey: "MicrovmId", attrWant: "a",
		},
		{
			name: "managed microvm images", op: "ListManagedMicrovmImages", typ: TypeLambdaManagedMicrovmImage,
			fill: func(s *stubLambdaNew) {
				s.managed = [][]lambdamicrovmstypes.ManagedMicrovmImageSummary{{lmvManaged("a"), lmvManaged("b"), {CreatedAt: &lmvCreated}}, {lmvManaged("c")}}
			},
			run: func(ctx context.Context, s *stubLambdaNew, st *store.Store) (int, int, error) {
				return scanLambdaManagedMicrovmImages(ctx, s, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			wantIDs:  []string{managedARN("a"), managedARN("b"), managedARN("c")},
			wantName: managedARN("a"), wantCreated: true, wantManaged: true, attrKey: "CreatedAt", attrWant: lmvCreated.Format(time.RFC3339),
		},
	}
}

func TestLambdaNewListScanners_PagesAndFields(t *testing.T) {
	for _, tc := range lmvListCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countWarnings(st)
			stub := &stubLambdaNew{}
			tc.fill(stub)

			total, _, err := tc.run(context.Background(), stub, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 {
				t.Errorf("total=%d, want 3 (two pages, id-less item skipped)", total)
			}
			if *warnings != tc.idLessWarns {
				t.Errorf("warnings=%d, want %d (the id-less item)", *warnings, tc.idLessWarns)
			}
			rows := lmvRows(t, st, tc.typ)
			if len(rows) != 3 {
				t.Fatalf("stored %d rows, want 3: %v", len(rows), rows)
			}
			for _, id := range tc.wantIDs {
				r, ok := rows[id]
				if !ok {
					t.Errorf("missing row %s", id)
					continue
				}
				if r.Type != tc.typ || sv(r.Region) != testRegion || r.ManagedByProvider != tc.wantManaged {
					t.Errorf("%s: type=%s region=%s managed=%t, want %s %s %t", id, r.Type, sv(r.Region), r.ManagedByProvider, tc.typ, testRegion, tc.wantManaged)
				}
			}
			a := rows[tc.wantIDs[0]]
			if sv(a.Name) != tc.wantName {
				t.Errorf("name=%q, want %q", sv(a.Name), tc.wantName)
			}
			if sv(a.Status) != tc.wantStatus {
				t.Errorf("status=%q, want %q", sv(a.Status), tc.wantStatus)
			}
			if tc.wantCreated && sv(a.CreatedAt) != lmvCreated.Format(time.RFC3339) {
				t.Errorf("createdAt=%q, want %q", sv(a.CreatedAt), lmvCreated.Format(time.RFC3339))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			if got := attrs[tc.attrKey]; got != tc.attrWant {
				t.Errorf("attrs[%s]=%v, want %q", tc.attrKey, got, tc.attrWant)
			}
		})
	}
}

func TestLambdaNewListScanners_Empty(t *testing.T) {
	for _, tc := range lmvListCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := tc.run(context.Background(), &stubLambdaNew{}, st)
			if err != nil || total != 0 {
				t.Fatalf("total=%d err=%v, want 0/nil", total, err)
			}
			if rows := lmvRows(t, st, tc.typ); len(rows) != 0 {
				t.Errorf("stored %d rows, want 0", len(rows))
			}
		})
	}
}

// An IAM denial warns; a region that has not launched the API answers with
// the gateway's AccessDenied and is skipped without a warning, keeping the
// rows of the pages read before it.
func TestLambdaNewListScanners_ErrorHandling(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: lambda:List")
	gap := apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")
	for _, tc := range lmvListCases() {
		for _, ec := range []struct {
			name         string
			fail         lambdaStubFail
			wantRows     int
			wantWarnings int
			wantErr      string // API error code expected back; "" = nil
		}{
			{name: "denied", fail: lambdaStubFail{page: 0, err: deny}, wantWarnings: 1},
			{name: "region gap", fail: lambdaStubFail{page: 0, err: gap}},
			{name: "region gap on second page", fail: lambdaStubFail{page: 1, err: gap}, wantRows: 2},
			{name: "other error", fail: lambdaStubFail{page: 0, err: apiErr("ValidationException", "bad")}, wantErr: "ValidationException"},
		} {
			t.Run(tc.name+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := 0
				st.OnWarn = func(store.ScanWarning) { warnings++ }
				stub := &stubLambdaNew{fail: map[string]lambdaStubFail{tc.op: ec.fail}}
				tc.fill(stub)

				_, _, err := tc.run(context.Background(), stub, st)
				if ec.wantErr != "" {
					if !isAPIErrorCode(err, ec.wantErr) {
						t.Fatalf("err=%v, want %s propagated", err, ec.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				want := ec.wantWarnings
				if ec.fail.page > 0 {
					want += tc.idLessWarns
				}
				if warnings != want {
					t.Errorf("warnings=%d, want %d", warnings, want)
				}
				if rows := lmvRows(t, st, tc.typ); len(rows) != ec.wantRows {
					t.Errorf("stored %d rows, want %d", len(rows), ec.wantRows)
				}
			})
		}
	}
}

// The parents are the images stored: not the id-less one, nor one the tag
// read no longer finds.
func TestScanLambdaMicrovmImages_ReturnsStoredImagesAsParents(t *testing.T) {
	stub := &stubLambdaNew{
		images:  [][]lambdamicrovmstypes.MicrovmImageSummary{{lmvImage("a"), {Name: sdkaws.String("x")}, lmvImage("gone")}, {lmvImage("c")}},
		tagErrs: map[string]error{lmvARN("microvm-image", "gone"): apiErr("ResourceNotFoundException", "gone")},
	}
	st := newTestStore(t)
	var p phaseTotals
	parents, failed := scanLambdaMicrovmImages(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, lambdaMicrovmTags(stub), &p)
	if _, _, err := p.result(st, newTestAccount(testAccountID), testRegion); failed || err != nil {
		t.Fatalf("scan: failed %v, err %v", failed, err)
	}
	want := []childParent{{id: lmvARN("microvm-image", "a"), arn: lmvARN("microvm-image", "a")}, {id: lmvARN("microvm-image", "c"), arn: lmvARN("microvm-image", "c")}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents=%v, want %v", parents, want)
	}
}

func lmvVersion(image, version string) lambdamicrovmstypes.MicrovmImageVersionSummary {
	return lambdamicrovmstypes.MicrovmImageVersionSummary{
		ImageArn: sdkaws.String(lmvARN("microvm-image", image)), ImageVersion: sdkaws.String(version),
		State: lambdamicrovmstypes.MicrovmImageVersionStateSuccessful, CreatedAt: &lmvCreated,
		Tags:                 map[string]string{"team": "core"},
		EnvironmentVariables: map[string]string{"DB_PASSWORD": "hunter2"},
	}
}

// lmvVersionsFixture seeds images a and b (rows plus stub pages): a has
// versions 1 and 2 on two pages plus a version-less item, b has version 1.
func lmvVersionsFixture(t *testing.T, st *store.Store) (*stubLambdaNew, []childParent) {
	t.Helper()
	var parents []childParent
	for _, name := range []string{"a", "b"} {
		arn := lmvARN("microvm-image", name)
		upsertTestResource(t, st, "aws", testAccountID, TypeLambdaMicrovmImage, arn, testRegion, "{}")
		parents = append(parents, childParent{id: arn, arn: arn})
	}
	stub := &stubLambdaNew{versions: map[string][][]lambdamicrovmstypes.MicrovmImageVersionSummary{
		parents[0].id: {{lmvVersion("a", "1"), {ImageArn: sdkaws.String(parents[0].arn)}}, {lmvVersion("a", "2")}},
		parents[1].id: {{lmvVersion("b", "1")}},
	}}
	return stub, parents
}

func TestScanLambdaMicrovmImageVersions_PagesEveryImage(t *testing.T) {
	st := newTestStore(t)
	stub, parents := lmvVersionsFixture(t, st)

	total, _, err := scanLambdaMicrovmImageVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d, want 3", total)
	}
	rows := lmvRows(t, st, TypeLambdaMicrovmImageVersion)
	for _, w := range []struct {
		parent  childParent
		version string
	}{{parents[0], "1"}, {parents[0], "2"}, {parents[1], "1"}} {
		native := w.parent.arn + "/version/" + w.version
		r, ok := rows[native]
		if !ok {
			t.Errorf("missing row %s (have %v)", native, rows)
			continue
		}
		if r.Type != TypeLambdaMicrovmImageVersion || sv(r.Region) != testRegion || sv(r.Name) != w.version {
			t.Errorf("%s: type=%s region=%s name=%s", native, r.Type, sv(r.Region), sv(r.Name))
		}
	}
	a1 := rows[parents[0].arn+"/version/1"]
	if sv(a1.Status) != "SUCCESSFUL" || sv(a1.CreatedAt) != lmvCreated.Format(time.RFC3339) {
		t.Errorf("status=%q createdAt=%q", sv(a1.Status), sv(a1.CreatedAt))
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(sv(a1.TagsJSON)), &tags); err != nil || tags["team"] != "core" {
		t.Errorf("tags=%q err=%v, want team=core", sv(a1.TagsJSON), err)
	}
	var attrs struct{ EnvironmentVariables map[string]string }
	if err := json.Unmarshal([]byte(a1.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if got := attrs.EnvironmentVariables["DB_PASSWORD"]; got != "[REDACTED]" {
		t.Errorf("EnvironmentVariables.DB_PASSWORD=%q, want [REDACTED]", got)
	}

	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, parents[0].arn), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, store.ResourceID("aws", testAccountID, parents[0].arn), store.ResourceID("aws", testAccountID, parents[0].arn+"/version/2"), store.RelContains)
	for _, r := range rels {
		if r.ToID == store.ResourceID("aws", testAccountID, parents[1].arn+"/version/1") {
			t.Errorf("image a contains image b's version")
		}
	}
}

func TestScanLambdaMicrovmImageVersions_PerImageErrors(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: lambda:ListMicrovmImageVersions")
	for _, tc := range []struct {
		name         string
		failA, failB error
		wantRows     int
		wantWarnings int
		wantErr      string
	}{
		{name: "image gone between list and call", failA: apiErr("ResourceNotFoundException", "gone"), wantRows: 1},
		{name: "denied on every image warns once", failA: deny, failB: deny, wantWarnings: 1},
		{name: "other error", failA: apiErr("ValidationException", "bad"), wantErr: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub, parents := lmvVersionsFixture(t, st)
			stub.fail = map[string]lambdaStubFail{}
			if tc.failA != nil {
				stub.fail["ListMicrovmImageVersions/"+parents[0].id] = lambdaStubFail{err: tc.failA}
			}
			if tc.failB != nil {
				stub.fail["ListMicrovmImageVersions/"+parents[1].id] = lambdaStubFail{err: tc.failB}
			}

			_, _, err := scanLambdaMicrovmImageVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) {
					t.Fatalf("err=%v, want %s propagated", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v, want nil", err)
			}
			if warnings != tc.wantWarnings {
				t.Errorf("warnings=%d, want %d", warnings, tc.wantWarnings)
			}
			if rows := lmvRows(t, st, TypeLambdaMicrovmImageVersion); len(rows) != tc.wantRows {
				t.Errorf("stored %d rows, want %d", len(rows), tc.wantRows)
			}
			if len(stub.calls) < len(parents) {
				t.Errorf("calls=%v, want every image tried", stub.calls)
			}
		})
	}
}
