package aws

import (
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
	"github.com/aws/aws-sdk-go-v2/service/healthlake"
	healthlaketypes "github.com/aws/aws-sdk-go-v2/service/healthlake/types"
	"github.com/icearp/disco-cli/store"
)

var (
	hlDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: healthlake:ListTagsForResource")
	hlBadInput = apiErr("ValidationException", "bad")
	hlGone     = apiErr("ResourceNotFoundException", "not found")
	hlThrottle = apiErr("ThrottlingException", "slow down")
	hlNXDOMAIN = &net.DNSError{Err: "no such host", Name: "datatransformation.healthlake.us-east-1.amazonaws.com", IsNotFound: true}
	hlUpdated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	hlCreated  = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

const (
	hlPr1ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr1"
	hlPr2ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr2"
	hlPr3ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr3"
	hlDs1ARN = "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/d1"
	hlDs2ARN = "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/d2"
	hlDs3ARN = "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/d3"
	// hlTotal is the default scan's rows: 3 datastores, 3 profiles and 5
	// versions (pr1: 0 1 2; pr2: 0; pr3: 0).
	hlTotal = 11
)

// hlStub answers every health-lake op of the default scan:
//   - datastores d1 (ACTIVE, page one), d2 (DELETED) and d3 (CREATING) on
//     page two;
//   - profiles of format CCDA: pr1 on page one, pr2 and an element with no
//     ProfileId on page two; of format CSV: pr3 on one page;
//   - versions: pr1 has v2 on page one, v1, v0 and an element with no
//     Version on page two; any other profile has v0 on one page;
//   - tags on d1 and pr1.
//
// fail, keyed before the scan, makes a call fail: "datastores/p2" (the
// datastore listing's page two), a format ("CCDA", "CSV"), "CCDA/p2", a
// profile id (its version listing) or an ARN (its tag read). Every input is
// recorded.
type hlStub struct {
	tags map[string]map[string]string
	fail map[string]error

	mu     sync.Mutex
	inputs map[string][]any
}

func newHLStub() *hlStub {
	return &hlStub{
		tags:   map[string]map[string]string{hlDs1ARN: {"env": "prod"}, hlPr1ARN: {"team": "mapping"}},
		fail:   map[string]error{},
		inputs: map[string][]any{},
	}
}

func (s *hlStub) hlRecord(op string, in any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs[op] = append(s.inputs[op], in)
}

func (s *hlStub) hlCalls(op string) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.inputs[op])
}

// hlTagARNs is the sorted ARNs every ListTagsForResource call was sent.
func (s *hlStub) hlTagARNs() []string {
	var arns []string
	for _, in := range s.hlCalls("ListTagsForResource") {
		arns = append(arns, sv(in.(*healthlake.ListTagsForResourceInput).ResourceARN))
	}
	slices.Sort(arns)
	return arns
}

// hlVersionProfiles is the sorted, distinct profile ids versions were listed for.
func (s *hlStub) hlVersionProfiles() []string {
	var ids []string
	for _, in := range s.hlCalls("ListDataTransformationProfileVersions") {
		ids = append(ids, sv(in.(*healthlake.ListDataTransformationProfileVersionsInput).ProfileId))
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func hlDatastore(arn, id string, status healthlaketypes.DatastoreStatus) healthlaketypes.DatastoreProperties {
	return healthlaketypes.DatastoreProperties{
		DatastoreArn: sdkaws.String(arn), DatastoreId: sdkaws.String(id), DatastoreName: sdkaws.String("ds-" + id),
		DatastoreStatus: status, CreatedAt: &hlCreated, DatastoreTypeVersion: healthlaketypes.FHIRVersionR4,
		DatastoreEndpoint: sdkaws.String("https://healthlake.us-east-1.amazonaws.com/datastore/" + id + "/r4/"),
		SseConfiguration: &healthlaketypes.SseConfiguration{KmsEncryptionConfig: &healthlaketypes.KmsEncryptionConfig{
			CmkType: healthlaketypes.CmkTypeCmCmk, KmsKeyId: sdkaws.String("key-" + id),
		}},
	}
}

func hlProfile(id string, format healthlaketypes.SourceFormat) healthlaketypes.DataTransformationProfileSummary {
	return healthlaketypes.DataTransformationProfileSummary{
		ProfileId: sdkaws.String(id), ProfileName: sdkaws.String("profile-" + id), ProfileDescription: sdkaws.String("maps " + id),
		SourceFormat: format, TargetFormat: healthlaketypes.TargetFormatFhirR4, Version: sdkaws.Int32(2), LastUpdatedAt: &hlUpdated,
	}
}

func hlVersion(profile string, n int32) healthlaketypes.DataTransformationProfileVersionSummary {
	return healthlaketypes.DataTransformationProfileVersionSummary{
		ProfileId: sdkaws.String(profile), Version: sdkaws.Int32(n), ProfileName: sdkaws.String("profile-" + profile),
		SourceFormat: healthlaketypes.SourceFormatCcda, TargetFormat: healthlaketypes.TargetFormatFhirR4,
		ChangeDescription: sdkaws.String("change " + profile), LastUpdatedAt: &hlUpdated,
	}
}

func (s *hlStub) hlHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListFHIRDatastores": func(in any) (any, error) {
			s.hlRecord("ListFHIRDatastores", in)
			if in.(*healthlake.ListFHIRDatastoresInput).NextToken == nil {
				return &healthlake.ListFHIRDatastoresOutput{DatastorePropertiesList: []healthlaketypes.DatastoreProperties{
					hlDatastore(hlDs1ARN, "d1", healthlaketypes.DatastoreStatusActive),
				}, NextToken: sdkaws.String("p2")}, nil
			}
			if err := s.fail["datastores/p2"]; err != nil {
				return nil, err
			}
			return &healthlake.ListFHIRDatastoresOutput{DatastorePropertiesList: []healthlaketypes.DatastoreProperties{
				hlDatastore(hlDs2ARN, "d2", healthlaketypes.DatastoreStatusDeleted),
				hlDatastore(hlDs3ARN, "d3", healthlaketypes.DatastoreStatusCreating),
			}}, nil
		},
		"ListDataTransformationProfiles": func(in any) (any, error) {
			s.hlRecord("ListDataTransformationProfiles", in)
			req := in.(*healthlake.ListDataTransformationProfilesInput)
			format := req.SourceFormat
			if err := s.fail[string(format)]; err != nil && req.NextToken == nil {
				return nil, err
			}
			if format == healthlaketypes.SourceFormatCsv {
				return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{hlProfile("pr3", format)}}, nil
			}
			if req.NextToken == nil {
				return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{hlProfile("pr1", format)}, NextToken: sdkaws.String("p2")}, nil
			}
			if err := s.fail["CCDA/p2"]; err != nil {
				return nil, err
			}
			return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{
				hlProfile("pr2", format), {ProfileName: sdkaws.String("no-id"), SourceFormat: format},
			}}, nil
		},
		"ListDataTransformationProfileVersions": func(in any) (any, error) {
			s.hlRecord("ListDataTransformationProfileVersions", in)
			req := in.(*healthlake.ListDataTransformationProfileVersionsInput)
			profile := sv(req.ProfileId)
			if err := s.fail[profile]; err != nil {
				return nil, err
			}
			if profile != "pr1" {
				return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{hlVersion(profile, 0)}}, nil
			}
			if req.NextToken == nil {
				return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{hlVersion("pr1", 2)}, NextToken: sdkaws.String("p2")}, nil
			}
			return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{
				hlVersion("pr1", 1), hlVersion("pr1", 0), {ProfileId: sdkaws.String("pr1")},
			}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			s.hlRecord("ListTagsForResource", in)
			arn := sv(in.(*healthlake.ListTagsForResourceInput).ResourceARN)
			if err := s.fail[arn]; err != nil {
				return nil, err
			}
			var tags []healthlaketypes.Tag
			for k, v := range s.tags[arn] {
				tags = append(tags, healthlaketypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
			}
			return &healthlake.ListTagsForResourceOutput{Tags: tags}, nil
		},
	}
}

func hlScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, hlDenied)
	return scanHealthLake(ctx, acct, testRegion, st, testScanID)
}

// hlRows is every stored health-lake row keyed by NativeID.
func hlRows(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	rows := map[string]store.Resource{}
	for _, typ := range []string{TypeHealthLakeFHIRDatastore, TypeHealthLakeDataTransformationProfile, TypeHealthLakeDataTransformationProfileVersion} {
		for id, r := range rowsByNativeID(t, st, typ) {
			rows[id] = r
		}
	}
	return rows
}

func hlAttr(t *testing.T, r store.Resource, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("%s attributes: %v", r.NativeID, err)
	}
	return string(m[key])
}

// hlTaggedARNs is the ARNs the default scan reads tags for: every datastore
// but the DELETED d2, and every profile.
func hlTaggedARNs() []string {
	arns := []string{hlDs1ARN, hlDs3ARN, hlPr1ARN, hlPr2ARN, hlPr3ARN}
	slices.Sort(arns)
	return arns
}

func TestScanHealthLake_StoresTagsOfEachType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()

	counted, upserts := countUpserts(st)
	total, err := hlScan(context.Background(), counted, stub.hlHandlers())
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != hlTotal || inserted != hlTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, hlTotal, hlTotal)
	}
	rows := hlRows(t, st)
	created := hlCreated.Format(time.RFC3339)
	for _, w := range []struct {
		arn, typ, name, status, createdAt, attrKey, attrVal string
		tags                                                map[string]string
	}{
		{hlDs1ARN, TypeHealthLakeFHIRDatastore, "ds-d1", "ACTIVE", created, "DatastoreId", `"d1"`, map[string]string{"env": "prod"}},
		{hlDs2ARN, TypeHealthLakeFHIRDatastore, "ds-d2", "DELETED", created, "DatastoreId", `"d2"`, nil},
		{hlDs3ARN, TypeHealthLakeFHIRDatastore, "ds-d3", "CREATING", created, "DatastoreId", `"d3"`, nil},
		{hlPr1ARN, TypeHealthLakeDataTransformationProfile, "profile-pr1", "", "", "ProfileDescription", `"maps pr1"`, map[string]string{"team": "mapping"}},
		{hlPr2ARN, TypeHealthLakeDataTransformationProfile, "profile-pr2", "", "", "ProfileDescription", `"maps pr2"`, nil},
		{hlPr3ARN, TypeHealthLakeDataTransformationProfile, "profile-pr3", "", "", "SourceFormat", `"CSV"`, nil},
		{hlPr1ARN + "/version/1", TypeHealthLakeDataTransformationProfileVersion, "1", "", "", "ChangeDescription", `"change pr1"`, nil},
		{hlPr3ARN + "/version/0", TypeHealthLakeDataTransformationProfileVersion, "0", "", "", "Version", `0`, nil},
	} {
		r, ok := rows[w.arn]
		if !ok {
			t.Errorf("%s not stored", w.arn)
			continue
		}
		if r.Type != w.typ || sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.CreatedAt) != w.createdAt || sv(r.Region) != testRegion {
			t.Errorf("%s: type %q name %q status %q created %q region %q; want %q %q %q %q %q",
				w.arn, r.Type, sv(r.Name), sv(r.Status), sv(r.CreatedAt), sv(r.Region), w.typ, w.name, w.status, w.createdAt, testRegion)
		}
		if w.status == "" && r.Status != nil {
			t.Errorf("%s Status = %q; want nil", w.arn, *r.Status)
		}
		if r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" || r.ManagedByProvider {
			t.Errorf("%s: account %q %q managed %v; want %q %q false", w.arn, r.AccountID, sv(r.AccountName), r.ManagedByProvider, testAccountID, "Test Account")
		}
		if got := hlAttr(t, r, w.attrKey); got != w.attrVal {
			t.Errorf("%s attributes %s = %s; want %s", w.arn, w.attrKey, got, w.attrVal)
		}
		if w.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", w.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, w.tags)
	}
	if got := hlAttr(t, rows[hlDs1ARN], "SseConfiguration"); got != `{"KmsEncryptionConfig":{"CmkType":"CUSTOMER_MANAGED_KMS_KEY","KmsKeyId":"key-d1"}}` {
		t.Errorf("d1 attributes SseConfiguration = %s; want the listed element's", got)
	}
	if len(rows) != hlTotal {
		t.Errorf("stored %d rows; want %d", len(rows), hlTotal)
	}
	if got := stub.hlTagARNs(); !slices.Equal(got, hlTaggedARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, hlTaggedARNs())
	}
	if got := stub.hlVersionProfiles(); !slices.Equal(got, []string{"pr1", "pr2", "pr3"}) {
		t.Errorf("versions listed for %v; want [pr1 pr2 pr3]", got)
	}
	for _, v := range []string{"/version/0", "/version/1", "/version/2"} {
		assertContainsNative(t, st, hlPr1ARN, hlPr1ARN+v)
	}
	assertContainsNative(t, st, hlPr2ARN, hlPr2ARN+"/version/0")
	assertContainsNative(t, st, hlPr3ARN, hlPr3ARN+"/version/0")
	assertContainsEdgesDeclared(t, st)
}

// Each listing pages with NextToken and sends the model's MaxResults cap; the
// profile listing is made once per SourceFormat.
func TestScanHealthLake_ListingInputs(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	if _, err := hlScan(context.Background(), st, stub.hlHandlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	ds := stub.hlCalls("ListFHIRDatastores")
	if len(ds) != 2 {
		t.Fatalf("ListFHIRDatastores called %d times; want 2", len(ds))
	}
	for i, wantToken := range []string{"", "p2"} {
		in := ds[i].(*healthlake.ListFHIRDatastoresInput)
		if sv(in.NextToken) != wantToken || in.MaxResults == nil || *in.MaxResults != 500 || in.Filter != nil {
			t.Errorf("ListFHIRDatastores call %d = token %q max %v filter %v; want %q 500 none", i+1, sv(in.NextToken), in.MaxResults, in.Filter, wantToken)
		}
	}
	var formats []string
	for _, c := range stub.hlCalls("ListDataTransformationProfiles") {
		in := c.(*healthlake.ListDataTransformationProfilesInput)
		formats = append(formats, string(in.SourceFormat)+"/"+sv(in.NextToken))
		if in.MaxResults == nil || *in.MaxResults != 100 {
			t.Errorf("ListDataTransformationProfiles %s MaxResults = %v; want 100", in.SourceFormat, in.MaxResults)
		}
	}
	slices.Sort(formats)
	if want := []string{"CCDA/", "CCDA/p2", "CSV/"}; !slices.Equal(formats, want) {
		t.Errorf("ListDataTransformationProfiles calls = %v; want %v", formats, want)
	}
	for _, c := range stub.hlCalls("ListDataTransformationProfileVersions") {
		in := c.(*healthlake.ListDataTransformationProfileVersionsInput)
		if in.MaxResults == nil || *in.MaxResults != 100 {
			t.Errorf("ListDataTransformationProfileVersions %s MaxResults = %v; want 100", sv(in.ProfileId), in.MaxResults)
		}
	}
	if n := len(stub.hlCalls("ListDataTransformationProfileVersions")); n != 4 {
		t.Errorf("ListDataTransformationProfileVersions called %d times; want 4 (pr1 twice)", n)
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanHealthLake_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := hlScan(context.Background(), st, newHLStub().hlHandlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := hlScan(context.Background(), st, newHLStub().hlHandlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != hlTotal {
		t.Errorf("rescan total = %d; want %d", total, hlTotal)
	}
}

func TestScanHealthLake_NoResources(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	handlers := stub.hlHandlers()
	handlers["ListFHIRDatastores"] = func(any) (any, error) { return &healthlake.ListFHIRDatastoresOutput{}, nil }
	handlers["ListDataTransformationProfiles"] = func(any) (any, error) {
		return &healthlake.ListDataTransformationProfilesOutput{}, nil
	}

	counted, upserts := countUpserts(st)
	total, err := hlScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if rows := hlRows(t, st); len(rows) != 0 {
		t.Errorf("stored %d rows; want none", len(rows))
	}
	for _, op := range []string{"ListTagsForResource", "ListDataTransformationProfileVersions"} {
		if n := len(stub.hlCalls(op)); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// Each tag-read outcome, for a datastore and a profile: a not-found drops the
// row (and a dropped profile has no versions listed); a denial keeps it
// untagged with one warning; a throttle keeps it and leaves the scan
// partial; any other error keeps it and is returned.
func TestScanHealthLake_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, arn := range []string{hlDs1ARN, hlPr1ARN} {
		for _, c := range []struct {
			name    string
			err     error
			kept    bool
			outcome serviceOutcome
			code    string
			warns   int
		}{
			{"not found", hlGone, false, 0, "", 0},
			{"denied", hlDenied, true, 0, "", 1},
			{"throttled", hlThrottle, true, outcomeTransient, "ThrottlingException", 0},
			{"other", hlBadInput, true, outcomeError, "ValidationException", 0},
		} {
			t.Run(arn+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				stub := newHLStub()
				stub.fail[arn] = c.err

				total, err := hlScan(context.Background(), st, stub.hlHandlers())
				if c.code == "" {
					if err != nil {
						t.Fatalf("scan: %v", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				r, stored := hlRows(t, st)[arn]
				if stored != c.kept {
					t.Fatalf("%s stored = %v; want %v", arn, stored, c.kept)
				}
				wantTotal, wantVersionsOf := hlTotal, []string{"pr1", "pr2", "pr3"}
				switch {
				case c.kept:
				case arn == hlPr1ARN:
					// pr1 and its 3 versions.
					wantTotal -= 4
					wantVersionsOf = []string{"pr2", "pr3"}
				default:
					wantTotal--
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				if got := stub.hlVersionProfiles(); !slices.Equal(got, wantVersionsOf) {
					t.Errorf("versions listed for %v; want %v", got, wantVersionsOf)
				}
				if stored && r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s; want nil", arn, *r.TagsJSON)
				}
				var got []store.ScanWarning
				for _, w := range warns() {
					if w.Service == healthLakeTagsOp {
						got = append(got, w)
					}
				}
				if len(got) != c.warns {
					t.Fatalf("%s warnings = %v; want %d", healthLakeTagsOp, got, c.warns)
				}
				if c.warns == 1 && got[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
				}
			})
		}
	}
}

func TestScanHealthLake_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, healthLakeTagsOp)
	stub := newHLStub()
	stub.fail[hlDs3ARN] = hlDenied
	stub.fail[hlPr3ARN] = hlDenied

	if _, err := hlScan(context.Background(), st, stub.hlHandlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 for both phases", healthLakeTagsOp, *warns)
	}
}

// A throttled tag read beside a hard tag-read error is demoted to a warning,
// so the scan reports the hard error rather than reading as transient.
func TestScanHealthLake_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, healthLakeTagsOp)
	stub := newHLStub()
	stub.fail[hlDs1ARN] = hlThrottle
	stub.fail[hlPr2ARN] = hlBadInput

	_, err := hlScan(context.Background(), st, stub.hlHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (the demoted throttle)", healthLakeTagsOp, *warns)
	}
}

// A datastore listing's failed later page keeps its first page, and the
// profile phases still run.
func TestScanHealthLake_DatastorePageTwoFailureKeepsPageOneAndProfiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	stub.fail["datastores/p2"] = hlBadInput

	total, err := hlScan(context.Background(), st, stub.hlHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != hlTotal-2 {
		t.Errorf("total = %d; want %d", total, hlTotal-2)
	}
	rows := rowsByNativeID(t, st, TypeHealthLakeFHIRDatastore)
	if _, ok := rows[hlDs1ARN]; !ok || len(rows) != 1 {
		t.Errorf("datastores = %v; want only d1 (page one)", rows)
	}
	if n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile)); n != 3 {
		t.Errorf("profiles stored = %d; want 3", n)
	}
}

// A repeated NextToken ends the datastore listing with errRepeatedNextToken,
// keeping the pages read, and the profile phases still run.
func TestScanHealthLake_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	handlers := stub.hlHandlers()
	list := handlers["ListFHIRDatastores"]
	handlers["ListFHIRDatastores"] = func(in any) (any, error) {
		out, err := list(in)
		out.(*healthlake.ListFHIRDatastoresOutput).NextToken = sdkaws.String("p2")
		return out, err
	}

	total, err := hlScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != hlTotal {
		t.Errorf("total = %d; want %d", total, hlTotal)
	}
	if n := len(stub.hlCalls("ListFHIRDatastores")); n != 2 {
		t.Errorf("ListFHIRDatastores called %d times; want 2", n)
	}
}

// One format's failed listing keeps the other format's profiles and their
// versions; a failed page two keeps page one's.
func TestScanHealthLake_ProfileFormatFailureKeepsOtherFormat(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		fail       string
		kept       []string
		versionsOf []string
		total      int
	}{
		// 3 datastores, pr1 and pr2 with 4 versions.
		{"CSV", []string{hlPr1ARN, hlPr2ARN}, []string{"pr1", "pr2"}, 9},
		// 3 datastores, pr3 with 1 version.
		{"CCDA", []string{hlPr3ARN}, []string{"pr3"}, 5},
		// 3 datastores, pr1 and pr3 with 4 versions.
		{"CCDA/p2", []string{hlPr1ARN, hlPr3ARN}, []string{"pr1", "pr3"}, 9},
	} {
		t.Run(c.fail, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			stub := newHLStub()
			stub.fail[c.fail] = hlBadInput

			total, err := hlScan(context.Background(), st, stub.hlHandlers())
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.total {
				t.Errorf("total = %d; want %d", total, c.total)
			}
			rows := rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile)
			var got []string
			for id := range rows {
				got = append(got, id)
			}
			slices.Sort(got)
			if !slices.Equal(got, c.kept) {
				t.Errorf("profiles stored = %v; want %v", got, c.kept)
			}
			if got := stub.hlVersionProfiles(); !slices.Equal(got, c.versionsOf) {
				t.Errorf("versions listed for %v; want %v", got, c.versionsOf)
			}
		})
	}
}

// A denied profile listing warns once for both formats; the datastores are
// kept and no versions are listed.
func TestScanHealthLake_ProfilesDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, healthLakeListProfilesOp)
	stub := newHLStub()
	stub.fail["CCDA"] = hlDenied
	stub.fail["CSV"] = hlDenied

	total, err := hlScan(context.Background(), st, stub.hlHandlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 3 datastores, 1", total, *warns)
	}
	if n := len(stub.hlCalls("ListDataTransformationProfileVersions")); n != 0 {
		t.Errorf("versions listed %d times; want 0", n)
	}
}

// A denied datastore listing warns and the profile phases still run.
func TestScanHealthLake_DatastoresDeniedKeepProfiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, healthLakeListDatastoresOp)
	handlers := newHLStub().hlHandlers()
	delete(handlers, "ListFHIRDatastores")

	total, err := hlScan(context.Background(), st, handlers)
	if err != nil || total != hlTotal-3 || *warns != 1 {
		t.Errorf("total = %d, err = %v, warnings = %d; want %d (3 profiles + 5 versions), nil, 1", total, err, *warns, hlTotal-3)
	}
}

// An element without its identifier is not stored, has nothing read, and is
// warned about once under its listing, naming the first such element: a
// datastore by name, a profile by name (noIdentifier when it has none) and a
// version by its profile's ARN. The other elements are stored.
func TestScanHealthLake_ElementsWithoutIdentifier(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name        string
		profileName *string
		profileKey  string
	}{
		{"named profile", sdkaws.String("no-id"), "no-id"},
		{"nameless profile", nil, noIdentifier},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			stub := newHLStub()
			handlers := stub.hlHandlers()
			listDatastores := handlers["ListFHIRDatastores"]
			handlers["ListFHIRDatastores"] = func(in any) (any, error) {
				out, err := listDatastores(in)
				o := out.(*healthlake.ListFHIRDatastoresOutput)
				o.DatastorePropertiesList = append(o.DatastorePropertiesList, healthlaketypes.DatastoreProperties{DatastoreName: sdkaws.String("orphan")})
				return o, err
			}
			listProfiles := handlers["ListDataTransformationProfiles"]
			handlers["ListDataTransformationProfiles"] = func(in any) (any, error) {
				out, err := listProfiles(in)
				if o, ok := out.(*healthlake.ListDataTransformationProfilesOutput); ok {
					for i := range o.Items {
						if o.Items[i].ProfileId == nil {
							o.Items[i].ProfileName = c.profileName
						}
					}
				}
				return out, err
			}

			total, err := hlScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != hlTotal {
				t.Errorf("total = %d; want %d", total, hlTotal)
			}
			// The datastore warning covers both pages' orphans; the default
			// fixture carries one profile without a ProfileId and one version
			// without a Version.
			want := map[string]string{
				healthLakeListDatastoresOp: "orphan",
				healthLakeListProfilesOp:   c.profileKey,
				healthLakeListVersionsOp:   hlPr1ARN,
			}
			w := warns()
			if len(w) != len(want) {
				t.Fatalf("warnings = %v; want one each under %v", w, want)
			}
			for _, got := range w {
				key, ok := want[got.Service]
				if !ok || got.Scope != testAccountID+"/"+testRegion || !strings.Contains(got.Message, key) {
					t.Errorf("warning %+v; want one under %s naming %q in %s/%s", got, got.Service, key, testAccountID, testRegion)
				}
				delete(want, got.Service)
			}
			if got := stub.hlTagARNs(); !slices.Equal(got, hlTaggedARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, hlTaggedARNs())
			}
		})
	}
}

// A datastore listed without a status is stored with no Status, and its tags
// are read.
func TestScanHealthLake_DatastoreWithoutStatus(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	handlers := stub.hlHandlers()
	handlers["ListFHIRDatastores"] = func(any) (any, error) {
		return &healthlake.ListFHIRDatastoresOutput{DatastorePropertiesList: []healthlaketypes.DatastoreProperties{
			hlDatastore(hlDs1ARN, "d1", ""),
		}}, nil
	}

	if _, err := hlScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeHealthLakeFHIRDatastore)[hlDs1ARN]
	if !ok {
		t.Fatalf("%s not stored", hlDs1ARN)
	}
	if r.Status != nil {
		t.Errorf("Status = %q; want nil", *r.Status)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"env": "prod"})
}

// A failed write stops the scan without a warning about the rows it lost (a
// denied tag read among them), and the profiles are not listed.
func TestScanHealthLake_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeHealthLakeFHIRDatastore)
	stub := newHLStub()
	stub.fail[hlDs1ARN] = hlDenied

	_, err := hlScan(context.Background(), st, stub.hlHandlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := len(stub.hlCalls("ListDataTransformationProfiles")); n != 0 {
		t.Errorf("ListDataTransformationProfiles called %d times after the failed write", n)
	}
	if n := len(stub.hlCalls("ListFHIRDatastores")); n != 1 {
		t.Errorf("ListFHIRDatastores called %d times; want 1 (no page after the failed write)", n)
	}
}

// A failed profile write stops the scan before the versions are listed.
func TestScanHealthLake_FailedProfileWriteStopsVersions(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeHealthLakeDataTransformationProfile)
	stub := newHLStub()

	_, err := hlScan(context.Background(), st, stub.hlHandlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := len(stub.hlCalls("ListDataTransformationProfileVersions")); n != 0 {
		t.Errorf("versions listed %d times after the failed write", n)
	}
	if n := len(rowsByNativeID(t, st, TypeHealthLakeFHIRDatastore)); n != 3 {
		t.Errorf("datastores stored = %d; want 3", n)
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it, and the profiles are not listed.
func TestScanHealthLake_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newHLStub()
	handlers := stub.hlHandlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListFHIRDatastores"]
	handlers["ListFHIRDatastores"] = func(in any) (any, error) {
		if in.(*healthlake.ListFHIRDatastoresInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := hlScan(ctx, st, handlers)
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
	if n := len(stub.hlCalls("ListDataTransformationProfiles")); n != 0 {
		t.Errorf("ListDataTransformationProfiles called %d times after ctx ended", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeHealthLakeFHIRDatastore)[hlDs1ARN]; !ok {
		t.Errorf("%s (page one) not stored", hlDs1ARN)
	}
}

// A region whose data transformation host does not resolve keeps its
// datastores and reports no error, but one format's NXDOMAIN beside the other
// format's hard error is that error; a datastore host that does not resolve
// still has the profiles and their versions listed, which proves it was
// reached, so its NXDOMAIN is a failure of this scan; so is a tag read's
// NXDOMAIN after the listing; with nothing stored the region is unavailable.
func TestScanHealthLake_NXDOMAIN(t *testing.T) {
	t.Parallel()
	t.Run("data transformation host", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		stub := newHLStub()
		stub.fail["CCDA"] = hlNXDOMAIN
		stub.fail["CSV"] = hlNXDOMAIN
		total, err := hlScan(context.Background(), st, stub.hlHandlers())
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d; want the 3 datastores", total)
		}
	})
	t.Run("one format NXDOMAIN beside a hard error", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		stub := newHLStub()
		stub.fail["CCDA"] = hlNXDOMAIN
		stub.fail["CSV"] = hlBadInput
		total, err := hlScan(context.Background(), st, stub.hlHandlers())
		assertGlueOutcome(t, err, outcomeError)
		if total != 3 {
			t.Errorf("total = %d; want the 3 datastores", total)
		}
	})
	t.Run("datastore host", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		stub := newHLStub()
		handlers := stub.hlHandlers()
		handlers["ListFHIRDatastores"] = func(any) (any, error) { return nil, hlNXDOMAIN }
		total, err := hlScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := hlTotal - 3; total != want {
			t.Errorf("total = %d; want %d (3 profiles + 5 versions)", total, want)
		}
		if got, want := stub.hlVersionProfiles(), []string{"pr1", "pr2", "pr3"}; !slices.Equal(got, want) {
			t.Errorf("versions listed for %v; want %v", got, want)
		}
		if n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)); n != 5 {
			t.Errorf("versions stored = %d; want 5", n)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		stub := newHLStub()
		stub.fail[hlPr3ARN] = hlNXDOMAIN
		_, err := hlScan(context.Background(), st, stub.hlHandlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, hlNXDOMAIN)
		_, err := scanHealthLake(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A cn or gov datastore's listed ARN, and a profile ARN built in the
// account's partition, reach ListTagsForResource unchanged.
func TestScanHealthLake_PartitionARNsReachTagOp(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			acct := partitionAccount(p)
			region := acct.Regions[0]
			dsARN := "arn:" + p + ":healthlake:" + region + ":" + testAccountID + ":datastore/fhir/dg"
			profileARN := "arn:" + p + ":healthlake:" + region + ":" + testAccountID + ":dataTransformationProfile/prg"
			stub := newHLStub()
			stub.tags[dsARN] = map[string]string{"env": "prod"}
			stub.tags[profileARN] = map[string]string{"team": "mapping"}
			handlers := stub.hlHandlers()
			handlers["ListFHIRDatastores"] = func(any) (any, error) {
				return &healthlake.ListFHIRDatastoresOutput{DatastorePropertiesList: []healthlaketypes.DatastoreProperties{
					hlDatastore(dsARN, "dg", healthlaketypes.DatastoreStatusActive),
				}}, nil
			}
			handlers["ListDataTransformationProfiles"] = func(in any) (any, error) {
				if in.(*healthlake.ListDataTransformationProfilesInput).SourceFormat != healthlaketypes.SourceFormatCsv {
					return &healthlake.ListDataTransformationProfilesOutput{}, nil
				}
				return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{
					hlProfile("prg", healthlaketypes.SourceFormatCsv),
				}}, nil
			}
			acct.cfg = opStubConfig(handlers, hlDenied)

			if _, err := scanHealthLake(context.Background(), acct, region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := []string{dsARN, profileARN}
			slices.Sort(want)
			if got := stub.hlTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			rows := hlRows(t, st)
			for id, wantTags := range map[string]map[string]string{dsARN: {"env": "prod"}, profileARN: {"team": "mapping"}} {
				r, ok := rows[id]
				if !ok {
					t.Errorf("%s not stored", id)
					continue
				}
				if sv(r.Region) != region {
					t.Errorf("%s Region = %q; want %q", id, sv(r.Region), region)
				}
				assertTagsMap(t, r.TagsJSON, wantTags)
			}
			assertContainsNative(t, st, profileARN, profileARN+"/version/0")
			if len(rows) != 3 {
				t.Errorf("stored %d rows; want 3", len(rows))
			}
		})
	}
}

// The calls go to the scanned region, not the config's.
func TestScanHealthLake_CallsGoToScannedRegion(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(newHLStub().hlHandlers(), hlDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, err := scanHealthLake(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanHealthLake: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
}

// A profile deleted after it was listed has its versions skipped silently; a
// denial warns once for the op; any other error is returned. Each keeps the
// other profiles' versions.
func TestScanHealthLake_VersionListingFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		fail  map[string]error
		kept  []string
		code  string
		warns int
	}{
		{"gone", map[string]error{"pr1": hlGone}, []string{hlPr2ARN + "/version/0", hlPr3ARN + "/version/0"}, "", 0},
		{"denied", map[string]error{"pr2": hlDenied, "pr3": hlDenied}, []string{hlPr1ARN + "/version/0", hlPr1ARN + "/version/1", hlPr1ARN + "/version/2"}, "", 1},
		{"other", map[string]error{"pr2": hlBadInput}, []string{hlPr1ARN + "/version/0", hlPr1ARN + "/version/1", hlPr1ARN + "/version/2", hlPr3ARN + "/version/0"}, "ValidationException", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := countOpWarnings(st, healthLakeListVersionsOp)
			stub := newHLStub()
			for k, v := range c.fail {
				stub.fail[k] = v
			}

			_, err := hlScan(context.Background(), st, stub.hlHandlers())
			if c.code == "" {
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			}
			var got []string
			for id := range rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion) {
				got = append(got, id)
			}
			slices.Sort(got)
			if !slices.Equal(got, c.kept) {
				t.Errorf("versions stored = %v; want %v", got, c.kept)
			}
			// The fixture's version without a Version warns too, whenever
			// pr1's second page is read.
			want := c.warns
			if slices.Contains(c.kept, hlPr1ARN+"/version/0") {
				want++
			}
			if *warns != want {
				t.Errorf("%s warnings = %d; want %d", healthLakeListVersionsOp, *warns, want)
			}
		})
	}
}
