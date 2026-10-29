package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rekognitiontypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListCollections, DescribeProjects and ListStreamProcessors
// each serve a "t" element on their first page and a "b" element on their
// second. Project A (alpha) embeds a train and a test dataset and has two
// versions over two pages; project B (beta) has neither. Project A's policies
// span two pages (plus a nameless element), B has one. Every "t" resource,
// the train dataset and version v-t have tags. Collection faces-t lists a
// face model version; faces-b's page lists none.

const (
	rekTestProjA      = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/1700000000"
	rekTestProjB      = "arn:aws:rekognition:us-east-1:123456789012:project/beta/1700000001"
	rekTestTrainA     = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/dataset/train/1700000010"
	rekTestTestA      = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/dataset/test/1700000011"
	rekTestVersionT   = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/version/v-t/1700000100"
	rekTestVersionB   = "arn:aws:rekognition:us-east-1:123456789012:project/alpha/version/v-b/1700000101"
	rekTestCollection = "arn:aws:rekognition:us-east-1:123456789012:collection/faces-t"
	rekTestCollB      = "arn:aws:rekognition:us-east-1:123456789012:collection/faces-b"
	rekTestSPT        = "arn:aws:rekognition:us-east-1:123456789012:streamprocessor/sp-t"
	rekTestSPB        = "arn:aws:rekognition:us-east-1:123456789012:streamprocessor/sp-b"
	rekTestPolA1      = rekTestProjA + "/policy/share-a1"
	rekTestPolA2      = rekTestProjA + "/policy/share-a2"
	rekTestPolB1      = rekTestProjB + "/policy/share-b1"
	rekCreatedText    = "2026-09-03T08:00:00Z"
	// rekFixtureRows is every row the fixture stores: 2 collections, 2
	// projects, 2 datasets, 2 versions, 2 stream processors, 3 policies.
	rekFixtureRows = 13
)

var (
	rekCreated     = time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	rekDenied      = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: rekognition:ListProjectPolicies")
	rekBadInput    = apiErr("InvalidParameterException", "bad")
	rekNotFound    = apiErr("ResourceNotFoundException", "gone")
	rekThrottled   = apiErr("ThrottlingException", "slow down")
	rekClosed      = apiErr("AccessDeniedException", "")
	rekTaggedNames = []string{rekTestCollection, rekTestProjA, rekTestTrainA, rekTestVersionT, rekTestSPT}
)

func rekPolicy(projectARN, name string) rekognitiontypes.ProjectPolicy {
	return rekognitiontypes.ProjectPolicy{
		ProjectArn:        sdkaws.String(projectARN),
		PolicyName:        sdkaws.String(name),
		PolicyRevisionId:  sdkaws.String("rev-" + name),
		PolicyDocument:    sdkaws.String(`{"Version":"2012-10-17","Statement":[]}`),
		CreationTimestamp: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
}

func rekDataset(arn string, typ rekognitiontypes.DatasetType) rekognitiontypes.DatasetMetadata {
	return rekognitiontypes.DatasetMetadata{DatasetArn: sdkaws.String(arn), DatasetType: typ, Status: rekognitiontypes.DatasetStatusCreateComplete, CreationTimestamp: &rekCreated}
}

// rekProject is the fixture project of page n ("t" is A, "b" is B).
func rekProject(n string) rekognitiontypes.ProjectDescription {
	if n == "t" {
		return rekognitiontypes.ProjectDescription{
			ProjectArn: sdkaws.String(rekTestProjA), Status: rekognitiontypes.ProjectStatusCreated, CreationTimestamp: &rekCreated,
			Datasets: []rekognitiontypes.DatasetMetadata{rekDataset(rekTestTrainA, rekognitiontypes.DatasetTypeTrain), rekDataset(rekTestTestA, rekognitiontypes.DatasetTypeTest)},
		}
	}
	return rekognitiontypes.ProjectDescription{ProjectArn: sdkaws.String(rekTestProjB), Status: rekognitiontypes.ProjectStatusCreated, CreationTimestamp: &rekCreated}
}

func rekVersion(arn string) rekognitiontypes.ProjectVersionDescription {
	return rekognitiontypes.ProjectVersionDescription{ProjectVersionArn: sdkaws.String(arn), Status: rekognitiontypes.ProjectVersionStatusTrainingCompleted, CreationTimestamp: &rekCreated}
}

func rekStreamProcessor(n string) rekognitiontypes.StreamProcessor {
	return rekognitiontypes.StreamProcessor{Name: sdkaws.String("sp-" + n), Status: rekognitiontypes.StreamProcessorStatusRunning}
}

// rekWant is a row of a tagged type the fixture stores: its type, ARN, Name,
// Status, CreatedAt and attributes.
type rekWant struct {
	typ, arn, name  string
	status, created *string
	attrs           string
}

func rekWants() []rekWant {
	created := sdkaws.String(rekCreatedText)
	dataset := func(arn string, typ rekognitiontypes.DatasetType) rekWant {
		return rekWant{TypeRekognitionDataset, arn, arn, sdkaws.String("CREATE_COMPLETE"), created, mustJSON(rekDatasetAttrs{DatasetMetadata: rekDataset(arn, typ), ProjectArn: rekTestProjA})}
	}
	version := func(arn string) rekWant {
		return rekWant{TypeRekognitionProjectVersion, arn, arn, sdkaws.String("TRAINING_COMPLETED"), created, mustJSON(rekProjectVersionAttrs{ProjectVersionDescription: rekVersion(arn), ProjectArn: rekTestProjA})}
	}
	return []rekWant{
		{TypeRekognitionCollection, rekTestCollection, "faces-t", nil, nil, `{"CollectionId":"faces-t","FaceModelVersion":"7.0"}`},
		{TypeRekognitionCollection, rekTestCollB, "faces-b", nil, nil, `{"CollectionId":"faces-b"}`},
		{TypeRekognitionProject, rekTestProjA, rekTestProjA, sdkaws.String("CREATED"), created, mustJSON(rekProject("t"))},
		{TypeRekognitionProject, rekTestProjB, rekTestProjB, sdkaws.String("CREATED"), created, mustJSON(rekProject("b"))},
		dataset(rekTestTrainA, rekognitiontypes.DatasetTypeTrain),
		dataset(rekTestTestA, rekognitiontypes.DatasetTypeTest),
		version(rekTestVersionT),
		version(rekTestVersionB),
		{TypeRekognitionStreamProcessor, rekTestSPT, "sp-t", sdkaws.String("RUNNING"), nil, mustJSON(rekStreamProcessor("t"))},
		{TypeRekognitionStreamProcessor, rekTestSPB, "sp-b", sdkaws.String("RUNNING"), nil, mustJSON(rekStreamProcessor("b"))},
	}
}

// rekCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type rekCalls struct {
	*crtCalls
	policies parentCalls
}

func newRekCalls() *rekCalls {
	c := &rekCalls{crtCalls: &crtCalls{newBACCalls()}}
	for _, w := range rekWants() {
		if slices.Contains(rekTaggedNames, w.arn) {
			c.tags[w.arn] = map[string]string{"type": w.typ}
		}
	}
	return c
}

func (c *rekCalls) listTags(in any) (any, error) {
	c.called("ListTagsForResource", in)
	arn := sv(in.(*rekognition.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &rekognition.ListTagsForResourceOutput{Tags: tags}, nil
}

// rekProjectOf is the project a per-project call was sent.
func rekProjectOf(in any) string { return sv(bacField[*string](in, "ProjectArn")) }

// rekVersions answers DescribeProjectVersions: project A's v-t, then v-b on
// a second page; any other project has none.
func (c *rekCalls) rekVersions(in any) (any, error) {
	c.called("DescribeProjectVersions", in)
	if rekProjectOf(in) != rekTestProjA {
		return &rekognition.DescribeProjectVersionsOutput{}, nil
	}
	if bacField[*string](in, "NextToken") == nil {
		return &rekognition.DescribeProjectVersionsOutput{ProjectVersionDescriptions: []rekognitiontypes.ProjectVersionDescription{rekVersion(rekTestVersionT)}, NextToken: sdkaws.String("p2")}, nil
	}
	return &rekognition.DescribeProjectVersionsOutput{ProjectVersionDescriptions: []rekognitiontypes.ProjectVersionDescription{rekVersion(rekTestVersionB)}}, nil
}

func (c *rekCalls) rekPolicies(in any) (any, error) {
	c.called("ListProjectPolicies", in)
	p := in.(*rekognition.ListProjectPoliciesInput)
	c.policies.add(sdkaws.ToString(p.ProjectArn))
	if sdkaws.ToInt32(p.MaxResults) != 5 {
		return nil, apiErr("ValidationException", "MaxResults must be at most 5")
	}
	switch sdkaws.ToString(p.ProjectArn) {
	case rekTestProjA:
		if p.NextToken == nil {
			return &rekognition.ListProjectPoliciesOutput{
				ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjA, "share-a1"), {ProjectArn: sdkaws.String(rekTestProjA)}},
				NextToken:       sdkaws.String("page2"),
			}, nil
		}
		return &rekognition.ListProjectPoliciesOutput{ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjA, "share-a2")}}, nil
	case rekTestProjB:
		return &rekognition.ListProjectPoliciesOutput{ProjectPolicies: []rekognitiontypes.ProjectPolicy{rekPolicy(rekTestProjB, "share-b1")}}, nil
	}
	return nil, rekNotFound
}

// rekHandlers answers every op the scan calls with the fixture.
func rekHandlers(c *rekCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListCollections": c.twoPages("ListCollections", func(n string, next *string) any {
			out := &rekognition.ListCollectionsOutput{CollectionIds: []string{"faces-" + n}, NextToken: next}
			if n == "t" {
				out.FaceModelVersions = []string{"7.0"}
			}
			return out
		}),
		"DescribeProjects": c.twoPages("DescribeProjects", func(n string, next *string) any {
			return &rekognition.DescribeProjectsOutput{ProjectDescriptions: []rekognitiontypes.ProjectDescription{rekProject(n)}, NextToken: next}
		}),
		"DescribeProjectVersions": c.rekVersions,
		"ListStreamProcessors": c.twoPages("ListStreamProcessors", func(n string, next *string) any {
			return &rekognition.ListStreamProcessorsOutput{StreamProcessors: []rekognitiontypes.StreamProcessor{rekStreamProcessor(n)}, NextToken: next}
		}),
		"ListProjectPolicies": c.rekPolicies,
	}
}

// rekScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func rekScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rekDenied)
	return scanRekognition(ctx, acct, testRegion, st, testScanID)
}

func rekRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type but project policies stores the tags ListTagsForResource returns
// for its ARN, nil for a resource without tags, and each such row's ARN is
// sent to the tag op once: the collection and stream processor ARNs are built
// from the listed id and name. Every listing page is read, each per-project
// listing is sent each project, and every call sends the model's page cap
// (none for ListStreamProcessors, whose model sets none). Each row keeps the
// base scan's Name, Status, Region and attributes; CreatedAt is the
// element's CreationTimestamp where it has one.
func TestScanRekognition_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newRekCalls()
	h := rekHandlers(calls)
	var versionProjects parentCalls
	versions := h["DescribeProjectVersions"]
	h["DescribeProjectVersions"] = func(in any) (any, error) {
		versionProjects.add(rekProjectOf(in))
		return versions(in)
	}

	total, inserted, err := rekScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rekFixtureRows || inserted != rekFixtureRows {
		t.Errorf("total, inserted = %d, %d; want %d", total, inserted, rekFixtureRows)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rekWants() {
		r, ok := rekRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if slices.Contains(rekTaggedNames, want.arn) {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || sv(r.Region) != testRegion {
			t.Errorf("%s Name, Region = %v, %v; want %s, %s", want.arn, r.Name, r.Region, want.name, testRegion)
		}
		if !rekSamePtr(r.Status, want.status) || !rekSamePtr(r.CreatedAt, want.created) {
			t.Errorf("%s Status, CreatedAt = %v, %v; want %v, %v", want.arn, r.Status, r.CreatedAt, want.status, want.created)
		}
		if r.AttributesJSON != want.attrs {
			t.Errorf("%s attributes = %s; want %s", want.arn, r.AttributesJSON, want.attrs)
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	if got, want := versionProjects.distinct(), []string{rekTestProjA, rekTestProjB}; !slices.Equal(got, want) {
		t.Errorf("DescribeProjectVersions projects = %v; want %v", got, want)
	}
	if got, want := calls.policies.distinct(), []string{rekTestProjA, rekTestProjB}; !slices.Equal(got, want) {
		t.Errorf("ListProjectPolicies projects = %v; want %v", got, want)
	}
	for op, want := range map[string]struct {
		calls int
		limit int32
	}{
		"ListCollections":         {2, 4096},
		"DescribeProjects":        {2, 100},
		"DescribeProjectVersions": {3, 100},
		"ListProjectPolicies":     {3, 5},
	} {
		caps := calls.maxResults[op]
		if len(caps) != want.calls || slices.ContainsFunc(caps, func(n int32) bool { return n != want.limit }) {
			t.Errorf("%s MaxResults = %v; want %d calls, each %d", op, caps, want.calls, want.limit)
		}
	}
	if n, caps := calls.count("ListStreamProcessors"), calls.maxResults["ListStreamProcessors"]; n != 2 || len(caps) != 0 {
		t.Errorf("ListStreamProcessors calls, MaxResults = %d, %v; want 2, none sent", n, caps)
	}
}

func rekSamePtr(a, b *string) bool { return (a == nil) == (b == nil) && sv(a) == sv(b) }

// An aws-us-gov account scanned in us-gov-west-1 sends the tag op the gov
// ARNs: the one its listing returns, and those built for collections and
// stream processors in the account's partition.
func TestScanRekognition_GovCloudARNsReachTagOp(t *testing.T) {
	const (
		govRegion  = "us-gov-west-1"
		collection = "arn:aws-us-gov:rekognition:us-gov-west-1:123456789012:collection/faces-gov"
		project    = "arn:aws-us-gov:rekognition:us-gov-west-1:123456789012:project/gov/1700000000"
		processor  = "arn:aws-us-gov:rekognition:us-gov-west-1:123456789012:streamprocessor/sp-gov"
	)
	st := newTestStore(t)
	calls := newRekCalls()
	for _, arn := range []string{collection, project, processor} {
		calls.tags[arn] = map[string]string{"env": "gov"}
	}
	h := rekHandlers(calls)
	h["ListCollections"] = calls.answer("ListCollections", func(any) any {
		return &rekognition.ListCollectionsOutput{CollectionIds: []string{"faces-gov"}}
	})
	h["DescribeProjects"] = calls.answer("DescribeProjects", func(any) any {
		return &rekognition.DescribeProjectsOutput{ProjectDescriptions: []rekognitiontypes.ProjectDescription{{ProjectArn: sdkaws.String(project)}}}
	})
	h["ListStreamProcessors"] = calls.answer("ListStreamProcessors", func(any) any {
		return &rekognition.ListStreamProcessorsOutput{StreamProcessors: []rekognitiontypes.StreamProcessor{{Name: sdkaws.String("sp-gov")}}}
	})
	h["ListProjectPolicies"] = calls.answer("ListProjectPolicies", func(any) any { return &rekognition.ListProjectPoliciesOutput{} })
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, rekDenied)

	total, _, err := scanRekognition(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 3 {
		t.Fatalf("scan = %d, %v; want 3, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{collection, project, processor}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeRekognitionCollection, collection},
		{TypeRekognitionProject, project},
		{TypeRekognitionStreamProcessor, processor},
	} {
		r, ok := rekRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": "gov"})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// Empty listings store nothing, make no per-project listing, read no tags
// and report nothing.
func TestScanRekognition_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newRekCalls()
	h := rekHandlers(calls)
	h["ListCollections"] = calls.answer("ListCollections", func(any) any { return &rekognition.ListCollectionsOutput{} })
	h["DescribeProjects"] = calls.answer("DescribeProjects", func(any) any { return &rekognition.DescribeProjectsOutput{} })
	h["ListStreamProcessors"] = calls.answer("ListStreamProcessors", func(any) any { return &rekognition.ListStreamProcessorsOutput{} })

	total, inserted, err := rekScan(context.Background(), t, st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("warnings, tag reads = %d, %v; want 0, none", *warns, calls.tagARNs)
	}
	if n := calls.count("DescribeProjectVersions") + calls.count("ListProjectPolicies"); n != 0 {
		t.Errorf("per-project calls = %d; want 0", n)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanRekognition_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newRekCalls()
	if _, _, err := rekScan(context.Background(), t, st, rekHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := rekScan(context.Background(), t, st, rekHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != rekFixtureRows || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, rekFixtureRows)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under rekognition:ListTagsForResource, a throttle
// after retries is a transient error, any other code a hard one; a
// not-found drops the row. A project dropped as not found is no parent: its
// datasets are not stored and its versions and policies not listed.
func TestScanRekognition_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		typ, arn string
		goneRows int
	}{
		{TypeRekognitionCollection, rekTestCollection, 1},
		{TypeRekognitionProject, rekTestProjA, 7},
		{TypeRekognitionDataset, rekTestTrainA, 1},
		{TypeRekognitionProjectVersion, rekTestVersionT, 1},
		{TypeRekognitionStreamProcessor, rekTestSPT, 1},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", rekDenied, true, 0, 1},
				{"not found", rekNotFound, false, 0, 0},
				{"other error", rekBadInput, true, outcomeError, 0},
				{"transient", rekThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, rekTagsOp)
					calls := newRekCalls()
					calls.errs[target.arn] = c.err

					total, _, err := rekScan(context.Background(), t, st, rekHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "InvalidParameterException")
					}
					r, ok := rekRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := rekFixtureRows
					if !c.kept {
						want -= target.goneRows
					}
					if total != want || countStoredRows(t, st) != want {
						t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", rekTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several phases are one warning for the scan.
func TestScanRekognition_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, rekTagsOp)
	calls := newRekCalls()
	denied := []struct{ typ, arn string }{
		{TypeRekognitionCollection, rekTestCollection},
		{TypeRekognitionDataset, rekTestTestA},
		{TypeRekognitionStreamProcessor, rekTestSPB},
	}
	for _, d := range denied {
		calls.errs[d.arn] = rekDenied
	}

	total, _, err := rekScan(context.Background(), t, st, rekHandlers(calls))
	if err != nil || total != rekFixtureRows {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, rekFixtureRows)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", rekTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if r, ok := rekRowOf(t, st, d.typ, d.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanRekognition_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, rekTagsOp)
	calls := newRekCalls()
	calls.errs[rekTestCollection] = rekThrottled
	calls.errs[rekTestVersionB] = rekBadInput

	_, _, err := rekScan(context.Background(), t, st, rekHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", rekTagsOp, *warns)
	}
}

// A denied listing warns once under its own op, for both projects of a
// per-project listing, and costs no other phase its rows; denied projects
// leave the per-project phases no project to read.
func TestScanRekognition_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListCollections", 2},
		{"DescribeProjects", 9},
		{"DescribeProjectVersions", 2},
		{"ListStreamProcessors", 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "rekognition:"+c.op)
			calls := newRekCalls()
			h := rekHandlers(calls)
			h[c.op] = func(any) (any, error) { return nil, rekDenied }

			total, _, err := rekScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (rekognition:%s: %d); want 1 under the op", *warns, c.op, *opWarns)
			}
			want := rekFixtureRows - c.lost
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
			if c.op == "DescribeProjects" && calls.count("DescribeProjectVersions")+calls.count("ListProjectPolicies") != 0 {
				t.Error("per-project listings called with no project read")
			}
		})
	}
}

// A failed write of any tagged type stops the scan before any later listing
// and sends no warning about the rows it lost, their denied tag reads
// included.
func TestScanRekognition_FailedWriteStopsWithoutWarning(t *testing.T) {
	later := []string{"DescribeProjects", "DescribeProjectVersions", "ListStreamProcessors", "ListProjectPolicies"}
	for _, c := range []struct {
		typ, deniedARN string
		later          []string
	}{
		{TypeRekognitionCollection, rekTestCollection, later},
		{TypeRekognitionProject, rekTestProjA, later[1:]},
		{TypeRekognitionDataset, rekTestTrainA, later[1:]},
		{TypeRekognitionProjectVersion, rekTestVersionT, later[2:]},
		{TypeRekognitionStreamProcessor, rekTestSPT, later[3:]},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newRekCalls()
			calls.errs[c.deniedARN] = rekDenied

			_, _, err := rekScan(context.Background(), t, st, rekHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write", op, n)
				}
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, one project's
// failed version listing costs the other none of its rows, a project deleted
// before its versions were listed is skipped silently, and no listing's
// failure stops the phases after it.
func TestScanRekognition_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newRekCalls()
	h := rekHandlers(calls)
	h["ListCollections"] = failOnCall(2, apiErr("InvalidPaginationTokenException", "page 2"), h["ListCollections"])
	h["DescribeProjects"] = failOnCall(2, apiErr("InvalidPaginationTokenException", "page 2"), h["DescribeProjects"])
	h["DescribeProjectVersions"] = func(in any) (any, error) {
		calls.called("DescribeProjectVersions", in)
		if bacField[*string](in, "NextToken") == nil {
			return &rekognition.DescribeProjectVersionsOutput{ProjectVersionDescriptions: []rekognitiontypes.ProjectVersionDescription{rekVersion(rekTestVersionT)}, NextToken: sdkaws.String("p2")}, nil
		}
		return nil, rekBadInput
	}
	h["ListStreamProcessors"] = func(any) (any, error) { return nil, apiErr("ValidationException", "boom") }

	total, _, err := rekScan(context.Background(), t, st, h)
	for _, code := range []string{"InvalidPaginationTokenException", "InvalidParameterException", "ValidationException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	// faces-t, project A, its 2 datasets, v-t, 2 policies of A.
	if stored := countStoredRows(t, st); total != 7 || stored != 7 {
		t.Errorf("total, stored = %d, %d; want 7", total, stored)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypeRekognitionCollection, rekTestCollection},
		{TypeRekognitionDataset, rekTestTestA},
		{TypeRekognitionProjectVersion, rekTestVersionT},
		{TypeRekognitionProjectPolicy, rekTestPolA2},
	} {
		if _, ok := rekRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// DescribeProjectVersions' modeled not-found says the project was deleted
// since it was listed: the project is skipped silently when none of its
// versions was read, and with one warning when its listing stopped part way.
// The other project's versions are stored either way.
func TestScanRekognition_ProjectGoneBeforeVersions(t *testing.T) {
	for _, c := range []struct {
		name      string
		failPage  int
		wantRows  []string
		wantWarns int
	}{
		{"before the first page", 1, nil, 0},
		{"after the first page", 2, []string{rekTestVersionT}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rekDescribeProjectVersionsOp)
			calls := newRekCalls()
			h := rekHandlers(calls)
			page := 0
			h["DescribeProjectVersions"] = func(in any) (any, error) {
				if rekProjectOf(in) == rekTestProjA {
					calls.mu.Lock()
					page++
					this := page
					calls.mu.Unlock()
					if this == c.failPage {
						return nil, rekNotFound
					}
				}
				return calls.rekVersions(in)
			}

			_, _, err := rekScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeRekognitionProjectVersion)); !slices.Equal(got, c.wantRows) {
				t.Errorf("versions = %v; want %v", got, c.wantRows)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d; want %d", rekDescribeProjectVersionsOp, *warns, c.wantWarns)
			}
		})
	}
}

// Elements listed without their identifier are not stored or read and are
// warned about once per listing, under a key that is never empty: a dataset
// or version names its project.
func TestScanRekognition_UnstorableItemsWarn(t *testing.T) {
	projectWithBareDataset := rekProject("t")
	projectWithBareDataset.Datasets = append(projectWithBareDataset.Datasets, rekognitiontypes.DatasetMetadata{})
	for _, c := range []struct {
		name, op, key string
		out           any
		lost          int
	}{
		{"collection without id", "ListCollections", "first (an element with no identifier)", &rekognition.ListCollectionsOutput{CollectionIds: []string{""}}, 2},
		{"project without ARN", "DescribeProjects", "first (an element with no identifier)", &rekognition.DescribeProjectsOutput{ProjectDescriptions: []rekognitiontypes.ProjectDescription{{Status: rekognitiontypes.ProjectStatusCreated}}}, 9},
		{"dataset without ARN", "DescribeProjects", "first " + rekTestProjA, &rekognition.DescribeProjectsOutput{ProjectDescriptions: []rekognitiontypes.ProjectDescription{projectWithBareDataset}}, 2},
		{"version without ARN", "DescribeProjectVersions", "first " + rekTestProjA, &rekognition.DescribeProjectVersionsOutput{ProjectVersionDescriptions: []rekognitiontypes.ProjectVersionDescription{{}}}, 2},
		{"stream processor without name", "ListStreamProcessors", "first (an element with no identifier)", &rekognition.ListStreamProcessorsOutput{StreamProcessors: []rekognitiontypes.StreamProcessor{{Status: rekognitiontypes.StreamProcessorStatusRunning}}}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			warnings := bacWarnings(st, "rekognition:"+c.op)
			calls := newRekCalls()
			h := rekHandlers(calls)
			h[c.op] = func(in any) (any, error) {
				if c.op == "DescribeProjectVersions" && rekProjectOf(in) != rekTestProjA {
					return &rekognition.DescribeProjectVersionsOutput{}, nil
				}
				return c.out, nil
			}

			total, _, err := rekScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %d, under the op %v; want one ending %q", *warns, *warnings, c.key)
			}
			if want := rekFixtureRows - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
		})
	}
}

// Each listed collection keeps the face model version at its index in
// FaceModelVersions; one past the end of that list is stored without one.
func TestScanRekognition_CollectionFaceModelVersions(t *testing.T) {
	st := newTestStore(t)
	h := rekHandlers(newRekCalls())
	h["ListCollections"] = func(any) (any, error) {
		return &rekognition.ListCollectionsOutput{CollectionIds: []string{"a", "b", "c"}, FaceModelVersions: []string{"5.0", "6.0"}}, nil
	}

	if _, _, err := rekScan(context.Background(), t, st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeRekognitionCollection)
	if len(rows) != 3 {
		t.Errorf("collections stored = %d; want 3", len(rows))
	}
	for id, want := range map[string]string{
		"a": `{"CollectionId":"a","FaceModelVersion":"5.0"}`,
		"b": `{"CollectionId":"b","FaceModelVersion":"6.0"}`,
		"c": `{"CollectionId":"c"}`,
	} {
		arn := "arn:aws:rekognition:us-east-1:123456789012:collection/" + id
		if got := rows[arn].AttributesJSON; got != want {
			t.Errorf("%s attributes = %s; want %s", arn, got, want)
		}
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanRekognition_RepeatedTokenKeepsPages(t *testing.T) {
	// out builds a new output per call: the SDK writes ResultMetadata into
	// the output, and the projects' DescribeProjectVersions calls run
	// concurrently.
	for _, c := range []struct {
		op, typ, arn string
		out          func() any
	}{
		{"ListCollections", TypeRekognitionCollection, rekTestCollection, func() any {
			return &rekognition.ListCollectionsOutput{CollectionIds: []string{"faces-t"}, NextToken: sdkaws.String("same")}
		}},
		{"DescribeProjectVersions", TypeRekognitionProjectVersion, rekTestVersionT, func() any {
			return &rekognition.DescribeProjectVersionsOutput{ProjectVersionDescriptions: []rekognitiontypes.ProjectVersionDescription{rekVersion(rekTestVersionT)}, NextToken: sdkaws.String("same")}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := rekHandlers(newRekCalls())
			h[c.op] = func(any) (any, error) { return c.out(), nil }

			_, _, err := rekScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := rekRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanRekognition_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newRekCalls()
	h := rekHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["DescribeProjectVersions"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := rekScan(ctx, t, st, h)
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
	if n := calls.count("ListStreamProcessors") + calls.count("ListProjectPolicies"); n != 0 {
		t.Errorf("ListStreamProcessors and ListProjectPolicies called %d times after ctx ended", n)
	}
}

// A region without Rekognition (NXDOMAIN on the first listing, nothing
// stored) is unavailable and makes no other listing. An NXDOMAIN after rows
// were stored is a failure of this scan: the counts are kept.
func TestScanRekognition_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "rekognition.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRekCalls()
		h := rekHandlers(calls)
		h["ListCollections"] = func(any) (any, error) { return nil, nx }
		total, _, err := rekScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("DescribeProjects") != 0 {
			t.Errorf("total, DescribeProjects calls = %d, %d; want 0, 0", total, calls.count("DescribeProjects"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRekCalls()
		calls.errs[rekTestSPB] = nx
		total, _, err := rekScan(context.Background(), t, st, rekHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != rekFixtureRows {
			t.Errorf("total = %d; want %d", total, rekFixtureRows)
		}
	})
}

// An account the service is closed to (an AccessDeniedException without a
// message) on the first page of DescribeProjects or ListStreamProcessors has
// none of them, silently. The same answer on a later page follows a page the
// op served, so it is warned about as a denial and the page read is kept.
func TestScanRekognition_ClosedToNewCustomers(t *testing.T) {
	for _, c := range []struct {
		name, op  string
		page      int
		lost      int
		wantWarns int
	}{
		{"projects first page", "DescribeProjects", 1, 9, 0},
		{"projects later page", "DescribeProjects", 2, 2, 1},
		{"stream processors first page", "ListStreamProcessors", 1, 2, 0},
		{"stream processors later page", "ListStreamProcessors", 2, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "rekognition:"+c.op)
			h := rekHandlers(newRekCalls())
			h[c.op] = failOnCall(c.page, rekClosed, h[c.op])

			total, _, err := rekScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.wantWarns || *opWarns != c.wantWarns {
				t.Errorf("warnings = %d (under the op %d); want %d", *warns, *opWarns, c.wantWarns)
			}
			if want := rekFixtureRows - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
		})
	}
}

func TestScanRekognition_StoresProjectPolicies(t *testing.T) {
	st := newTestStore(t)
	calls := newRekCalls()
	if _, _, err := rekScan(context.Background(), t, st, rekHandlers(calls)); err != nil {
		t.Fatalf("scanRekognition: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeRekognitionProjectPolicy)
	if got := nativeIDs(rows); !slices.Equal(got, []string{rekTestPolA1, rekTestPolA2, rekTestPolB1}) {
		t.Fatalf("policy rows = %v, want both pages of A and B's policy", got)
	}
	r := rows[rekTestPolA2]
	if r.Type != TypeRekognitionProjectPolicy || sv(r.Region) != testRegion || sv(r.Name) != "share-a2" {
		t.Errorf("row = type %q region %q name %q", r.Type, sv(r.Region), sv(r.Name))
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %s; want nil, policies have no tag read", *r.TagsJSON)
	}
	var attrs struct {
		PolicyRevisionID string `json:"PolicyRevisionId"`
		PolicyDocument   string `json:"PolicyDocument"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.PolicyRevisionID != "rev-share-a2" || attrs.PolicyDocument == "" {
		t.Errorf("attrs = %+v, want the SDK policy verbatim", attrs)
	}
	assertContainsNative(t, st, rekTestProjA, rekTestPolA1)
	assertContainsNative(t, st, rekTestProjA, rekTestPolA2)
	assertContainsNative(t, st, rekTestProjB, rekTestPolB1)
}

func TestScanRekognition_ProjectsWithoutPolicies(t *testing.T) {
	st := newTestStore(t)
	calls := newRekCalls()
	h := rekHandlers(calls)
	h["ListProjectPolicies"] = func(in any) (any, error) {
		calls.policies.add(sdkaws.ToString(in.(*rekognition.ListProjectPoliciesInput).ProjectArn))
		return &rekognition.ListProjectPoliciesOutput{}, nil
	}
	if _, _, err := rekScan(context.Background(), t, st, h); err != nil {
		t.Fatalf("scanRekognition: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); n != 0 {
		t.Errorf("policy rows = %d, want 0", n)
	}
	if got := calls.policies.distinct(); len(got) != 2 {
		t.Errorf("ListProjectPolicies parents = %v, want both projects", got)
	}
}

// A project deleted since listing, denied, or failing transiently after
// retries drops only its own policies: deleted is silent, a denial warns once
// however many projects hit it, and a transient error warns once. Any other
// error fails the phase (childFanOut stores none of its rows) and is returned;
// the earlier phases' rows stay.
func TestScanRekognition_FailedPolicyParent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"project gone", map[string]error{rekTestProjA: rekNotFound}, "", []string{rekTestPolB1}, 0},
		{"denied everywhere", map[string]error{rekTestProjA: rekDenied, rekTestProjB: rekDenied}, "", nil, 1},
		{"throttled everywhere", map[string]error{rekTestProjA: rekThrottled, rekTestProjB: rekThrottled}, "", nil, 1},
		{"provisioned throughput exceeded", map[string]error{rekTestProjA: apiErr("ProvisionedThroughputExceededException", "rate exceeded")}, "", []string{rekTestPolB1}, 1},
		{"other error", map[string]error{rekTestProjA: rekBadInput}, "InvalidParameterException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rekProjectPoliciesOp)
			calls := newRekCalls()
			h := rekHandlers(calls)
			h["ListProjectPolicies"] = func(in any) (any, error) {
				if err, ok := tc.fail[sdkaws.ToString(in.(*rekognition.ListProjectPoliciesInput).ProjectArn)]; ok {
					return nil, err
				}
				return calls.rekPolicies(in)
			}
			_, _, err := rekScan(context.Background(), t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("policy rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := countStoredRows(t, st); n != rekFixtureRows-3+len(tc.wantRows) {
				t.Errorf("stored rows = %d; want %d, the earlier phases' rows kept", n, rekFixtureRows-3+len(tc.wantRows))
			}
		})
	}
}

// A hard error in an earlier phase still lets the policy phase run over the
// stored projects, and is returned.
func TestScanRekognition_EarlierPhaseErrorStillRunsPolicies(t *testing.T) {
	st := newTestStore(t)
	h := rekHandlers(newRekCalls())
	h["ListStreamProcessors"] = func(any) (any, error) { return nil, rekBadInput }
	total, _, err := rekScan(context.Background(), t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeRekognitionProjectPolicy)); !slices.Equal(got, []string{rekTestPolA1, rekTestPolA2, rekTestPolB1}) {
		t.Errorf("policy rows = %v, want all three", got)
	}
	if total != rekFixtureRows-2 {
		t.Errorf("total = %d, want %d", total, rekFixtureRows-2)
	}
}

// A transient error in an earlier phase is demoted to a warning under its own
// op so that the policy phase's hard error still reaches the dispatcher as a
// hard error.
func TestScanRekognition_EarlierPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, rekListStreamProcessorsOp)
	h := rekHandlers(newRekCalls())
	h["ListStreamProcessors"] = func(any) (any, error) { return nil, rekThrottled }
	h["ListProjectPolicies"] = func(any) (any, error) { return nil, rekBadInput }
	_, _, err := rekScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the demoted transient", rekListStreamProcessorsOp, *warns)
	}
}
