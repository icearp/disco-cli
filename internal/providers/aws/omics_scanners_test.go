package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/omics"
	omicstypes "github.com/aws/aws-sdk-go-v2/service/omics/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubOmics returns annErr from ListAnnotationStores; the other omicsAPI methods
// are unused by the test path and return empty.
type stubOmics struct{ annErr error }

func (s stubOmics) ListAnnotationStores(context.Context, *omics.ListAnnotationStoresInput, ...func(*omics.Options)) (*omics.ListAnnotationStoresOutput, error) {
	return nil, s.annErr
}

func (stubOmics) ListConfigurations(context.Context, *omics.ListConfigurationsInput, ...func(*omics.Options)) (*omics.ListConfigurationsOutput, error) {
	return &omics.ListConfigurationsOutput{}, nil
}

func (stubOmics) ListReferenceStores(context.Context, *omics.ListReferenceStoresInput, ...func(*omics.Options)) (*omics.ListReferenceStoresOutput, error) {
	return &omics.ListReferenceStoresOutput{}, nil
}

func (stubOmics) ListRunGroups(context.Context, *omics.ListRunGroupsInput, ...func(*omics.Options)) (*omics.ListRunGroupsOutput, error) {
	return &omics.ListRunGroupsOutput{}, nil
}

func (stubOmics) ListSequenceStores(context.Context, *omics.ListSequenceStoresInput, ...func(*omics.Options)) (*omics.ListSequenceStoresOutput, error) {
	return &omics.ListSequenceStoresOutput{}, nil
}

func (stubOmics) ListVariantStores(context.Context, *omics.ListVariantStoresInput, ...func(*omics.Options)) (*omics.ListVariantStoresOutput, error) {
	return &omics.ListVariantStoresOutput{}, nil
}

func (stubOmics) ListWorkflows(context.Context, *omics.ListWorkflowsInput, ...func(*omics.Options)) (*omics.ListWorkflowsOutput, error) {
	return &omics.ListWorkflowsOutput{}, nil
}

func (stubOmics) ListWorkflowVersions(context.Context, *omics.ListWorkflowVersionsInput, ...func(*omics.Options)) (*omics.ListWorkflowVersionsOutput, error) {
	return &omics.ListWorkflowVersionsOutput{}, nil
}

func (stubOmics) ListAnnotationStoreVersions(context.Context, *omics.ListAnnotationStoreVersionsInput, ...func(*omics.Options)) (*omics.ListAnnotationStoreVersionsOutput, error) {
	return &omics.ListAnnotationStoreVersionsOutput{}, nil
}

func (stubOmics) ListReferences(context.Context, *omics.ListReferencesInput, ...func(*omics.Options)) (*omics.ListReferencesOutput, error) {
	return &omics.ListReferencesOutput{}, nil
}

func (stubOmics) ListRunCaches(context.Context, *omics.ListRunCachesInput, ...func(*omics.Options)) (*omics.ListRunCachesOutput, error) {
	return &omics.ListRunCachesOutput{}, nil
}

func (stubOmics) ListShares(context.Context, *omics.ListSharesInput, ...func(*omics.Options)) (*omics.ListSharesOutput, error) {
	return &omics.ListSharesOutput{}, nil
}

// Outside HealthOmics regions the endpoint answers "Unable to determine
// service/operation name" (whole service absent), so the phase returns the
// errServiceUnavailable sentinel (dispatcher renders "(region: unavailable)"),
// zero rows, no scan warning.
func TestScanOmicsAnnotationStores_ReturnsUnavailableSentinel(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)
	client := stubOmics{annErr: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")}

	_, total, inserted, err := scanOmicsAnnotationStores(context.Background(), client, acct, "us-east-2", st, testScanID)
	if !errors.Is(err, errServiceUnavailable) {
		t.Fatalf("want errServiceUnavailable, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("want (0,0), got (%d,%d)", total, inserted)
	}
	if warned {
		t.Error("region-gap must not record a scan warning")
	}
}

var (
	omicsTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	omicsTestWfARN   = fmt.Sprintf("arn:aws:omics:%s:%s:workflow/1234", testRegion, testAccountID)
)

// omicsRowsByNativeID returns the stored rows of rtype keyed by NativeID.
func omicsRowsByNativeID(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{rtype}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", rtype, err)
	}
	m := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		m[r.NativeID] = r
	}
	return m
}

func omicsShare(id, resourceARN string) omicstypes.ShareDetails {
	return omicstypes.ShareDetails{
		ShareId: sdkaws.String(id), ShareName: sdkaws.String("share-" + id), ResourceArn: sdkaws.String(resourceARN),
		Status: omicstypes.ShareStatusActive, CreationTime: &omicsTestCreated, OwnerId: sdkaws.String(testAccountID),
	}
}

type sharesPage struct {
	shares []omicstypes.ShareDetails
	err    error
}

// sharesStub serves pages in order (NextToken is the next page's index) and
// records the ResourceOwner of every call.
type sharesStub struct {
	omicsAPI
	pages  []sharesPage
	owners []omicstypes.ResourceOwner
}

func (s *sharesStub) ListShares(_ context.Context, in *omics.ListSharesInput, _ ...func(*omics.Options)) (*omics.ListSharesOutput, error) {
	s.owners = append(s.owners, in.ResourceOwner)
	page := 0
	if in.NextToken != nil {
		var err error
		if page, err = strconv.Atoi(*in.NextToken); err != nil {
			return nil, err
		}
	}
	p := s.pages[page]
	if p.err != nil {
		return nil, p.err
	}
	out := &omics.ListSharesOutput{Shares: p.shares}
	if page+1 < len(s.pages) {
		out.NextToken = sdkaws.String(strconv.Itoa(page + 1))
	}
	return out, nil
}

func TestScanOmicsShares_PaginatesOwnLiveShares(t *testing.T) {
	st := newTestStore(t)
	noID := omicsShare("", omicsTestWfARN)
	noResource := omicsShare("c", "")
	revoked := omicsShare("d", omicsTestWfARN)
	revoked.Status = omicstypes.ShareStatusDeleted
	stub := &sharesStub{pages: []sharesPage{
		{shares: []omicstypes.ShareDetails{omicsShare("a", omicsTestWfARN), noID, noResource}},
		{shares: []omicstypes.ShareDetails{omicsShare("b", omicsTestWfARN), revoked}},
	}}

	total, _, err := scanOmicsShares(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scanOmicsShares = (%d, %v); want (2, nil)", total, err)
	}
	rows := omicsRowsByNativeID(t, st, TypeOmicsShare)
	for _, id := range []string{"a", "b"} {
		if _, ok := rows[omicsTestWfARN+"/share/"+id]; !ok {
			t.Errorf("share %s not stored under %s/share/%s", id, omicsTestWfARN, id)
		}
	}
	if len(rows) != 2 {
		t.Errorf("stored %d shares; want 2 (ShareId-less, ResourceArn-less and DELETED skipped)", len(rows))
	}
	if _, ok := rows[omicsTestWfARN+"/share/d"]; ok {
		t.Error("DELETED share stored; want skipped")
	}
	r := rows[omicsTestWfARN+"/share/b"]
	if sv(r.Region) != testRegion || sv(r.Name) != "share-b" || sv(r.Status) != "ACTIVE" || r.CreatedAt == nil {
		t.Errorf("row = region %q name %q status %q createdAt %v; want %q share-b ACTIVE non-nil", sv(r.Region), sv(r.Name), sv(r.Status), r.CreatedAt, testRegion)
	}
	var attrs struct{ ResourceArn string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.ResourceArn != omicsTestWfARN {
		t.Errorf("attrs ResourceArn = %q (err %v); want %s", attrs.ResourceArn, err, omicsTestWfARN)
	}
	for _, o := range stub.owners {
		if o != omicstypes.ResourceOwnerSelf {
			t.Errorf("ListShares ResourceOwner = %q; want SELF (inbound shares belong to their owner)", o)
		}
	}
}

func TestScanOmicsShares_Empty(t *testing.T) {
	st := newTestStore(t)
	stub := &sharesStub{pages: []sharesPage{{}}}
	total, _, err := scanOmicsShares(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scanOmicsShares = (%d, %v); want (0, nil)", total, err)
	}
	if rows := omicsRowsByNativeID(t, st, TypeOmicsShare); len(rows) != 0 {
		t.Errorf("stored %d shares; want 0", len(rows))
	}
}

func TestScanOmicsShares_AccessDeniedKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(store.ScanWarning) { warnings++ }
	stub := &sharesStub{pages: []sharesPage{
		{shares: []omicstypes.ShareDetails{omicsShare("a", omicsTestWfARN)}},
		{err: apiErr("AccessDeniedException", "User: x is not authorized to perform: omics:ListShares")},
	}}
	total, _, err := scanOmicsShares(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("scanOmicsShares = (%d, %v); want (1, nil)", total, err)
	}
	if warnings != 1 {
		t.Errorf("warnings = %d; want 1", warnings)
	}
}

func TestScanOmicsShares_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	stub := &sharesStub{pages: []sharesPage{{err: apiErr("ValidationException", "bad")}}}
	_, _, err := scanOmicsShares(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want ValidationException", err)
	}
}

// omicsScanResponses queues one page per op for a full scanOmics run with one
// workflow and the given ListShares response.
func omicsScanResponses(shares stubCall) map[string][]stubCall {
	return map[string][]stubCall{
		"ListConfigurations":   {{Output: &omics.ListConfigurationsOutput{}}},
		"ListRunGroups":        {{Output: &omics.ListRunGroupsOutput{}}},
		"ListSequenceStores":   {{Output: &omics.ListSequenceStoresOutput{}}},
		"ListVariantStores":    {{Output: &omics.ListVariantStoresOutput{}}},
		"ListRunCaches":        {{Output: &omics.ListRunCachesOutput{}}},
		"ListAnnotationStores": {{Output: &omics.ListAnnotationStoresOutput{}}},
		"ListReferenceStores":  {{Output: &omics.ListReferenceStoresOutput{}}},
		"ListWorkflows": {{Output: &omics.ListWorkflowsOutput{Items: []omicstypes.WorkflowListItem{{
			Id: sdkaws.String("1234"), Arn: sdkaws.String(omicsTestWfARN),
		}}}}},
		"ListWorkflowVersions": {{Output: &omics.ListWorkflowVersionsOutput{}}},
		"ListShares":           {shares},
	}
}

func TestScanOmics_StoresShares(t *testing.T) {
	st := newTestStore(t)
	stub := stubResponses(t, omicsScanResponses(
		stubCall{Output: &omics.ListSharesOutput{Shares: []omicstypes.ShareDetails{omicsShare("a", omicsTestWfARN)}}},
	))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	if _, _, err := scanOmics(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanOmics: %v", err)
	}
	if _, ok := omicsRowsByNativeID(t, st, TypeOmicsShare)[omicsTestWfARN+"/share/a"]; !ok {
		t.Error("share not stored by scanOmics")
	}
}

// A shares failure must not cost the rows of the phases that existed before
// it, so shares run after every pre-existing phase.
func TestScanOmics_SharesErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	stub := stubResponses(t, omicsScanResponses(stubCall{Err: apiErr("ValidationException", "bad")}))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	_, _, err := scanOmics(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("scanOmics err = %v; want ValidationException", err)
	}
	if _, ok := omicsRowsByNativeID(t, st, TypeOmicsWorkflow)[omicsTestWfARN]; !ok {
		t.Error("workflow (pre-existing phase) not stored before the failing shares phase")
	}
}
