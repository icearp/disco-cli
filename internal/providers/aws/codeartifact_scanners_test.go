package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
	"github.com/icearp/disco-cli/store"
)

const (
	caTestPrefix  = "arn:aws:codeartifact:us-east-1:123456789012:"
	caTestDomain  = caTestPrefix + "domain/d1"
	caTestDomain2 = caTestPrefix + "domain/d2"
	caTestRepoA   = caTestPrefix + "repository/d1/ra"
	caTestGroup   = caTestPrefix + "package-group/d1/*"
	caTestGroup2  = caTestPrefix + "package-group/d1/npm/*"

	// Repository rb sits in a domain another account owns, so its ARN and its
	// packages' ARNs carry that account.
	caTestForeignOwner  = "210987654321"
	caTestForeignPrefix = "arn:aws:codeartifact:us-east-1:" + caTestForeignOwner + ":"
	caTestRepoB         = caTestForeignPrefix + "repository/d1/rb"

	caTestPkgA1 = caTestPrefix + "package/d1/ra/npm/acme/left-pad"
	caTestPkgA2 = caTestPrefix + "package/d1/ra/pypi//requests"
	caTestPkgB1 = caTestForeignPrefix + "package/d1/rb/maven/com.example/lib"

	// caTestTotal is the fixture's rows: 2 domains, 2 repositories, 2 package
	// groups and 3 packages.
	caTestTotal = 9
)

var (
	caDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: codeartifact:ListPackages")
	caBadInput   = apiErr("ValidationException", "bad")
	caThrottle   = apiErr("ThrottlingException", "slow down")
	caRepoGone   = apiErr("ResourceNotFoundException", "repository not found")
	caAllPkgARNs = []string{caTestPkgA1, caTestPkgA2, caTestPkgB1}
	caCreated    = time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
)

// caCalls records a scan's calls and answers ListTagsForResource with tags,
// or errs, by ARN.
type caCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
	// repos is the repositories ListPackages was called for.
	repos parentCalls
}

// newCACalls tags domain d1, repository ra and package group /* with their
// kind; d2, rb and /npm/* have no tags.
func newCACalls() *caCalls {
	return &caCalls{ops: map[string]int{}, errs: map[string]error{}, tags: map[string]map[string]string{
		caTestDomain: {"kind": "domain"},
		caTestRepoA:  {"kind": "repository"},
		caTestGroup:  {"kind": "package-group"},
	}}
}

func (c *caCalls) caRecord(op string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
}

func (c *caCalls) caCount(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *caCalls) caListTags(in any) (any, error) {
	arn := sv(in.(*codeartifact.ListTagsForResourceInput).ResourceArn)
	c.caRecord("ListTagsForResource")
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []catypes.Tag
	for k, v := range tags {
		list = append(list, catypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &codeartifact.ListTagsForResourceOutput{Tags: list}, nil
}

// caSentARNs is the sorted ARNs sent to ListTagsForResource.
func (c *caCalls) caSentARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// caPageSize rejects a listing sent any page size but the model cap.
func caPageSize(in any) error {
	if n := bacField[*int32](in, "MaxResults"); n == nil || *n != 1000 {
		return apiErr("ValidationException", "unexpected maxResults")
	}
	return nil
}

// caHandlers answers every CodeArtifact op the scan calls: domains d1 and d2,
// repositories ra and rb (plus one without an ARN), and d1's package groups
// /* and /npm/* (d2 has none). Repository ra's packages span two pages, the
// first also holding an element without a package name; rb, whose domain
// caTestForeignOwner owns, has one package and one element without a format.
// Every listing rejects any page size but the model cap; ListPackages also
// rejects any DomainOwner but the repository's.
func caHandlers(c *caCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.caListTags,
		"ListDomains": func(in any) (any, error) {
			c.caRecord("ListDomains")
			if err := caPageSize(in); err != nil {
				return nil, err
			}
			return &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{
				{Arn: sdkaws.String(caTestDomain), Name: sdkaws.String("d1"), Owner: sdkaws.String(testAccountID), Status: catypes.DomainStatusActive, CreatedTime: &caCreated},
				{Arn: sdkaws.String(caTestDomain2), Name: sdkaws.String("d2"), Owner: sdkaws.String(testAccountID)},
			}}, nil
		},
		"ListRepositories": func(in any) (any, error) {
			c.caRecord("ListRepositories")
			if err := caPageSize(in); err != nil {
				return nil, err
			}
			return &codeartifact.ListRepositoriesOutput{Repositories: []catypes.RepositorySummary{
				{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(testAccountID), AdministratorAccount: sdkaws.String(testAccountID), CreatedTime: &caCreated},
				{Name: sdkaws.String("no-arn"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(testAccountID)},
				{Arn: sdkaws.String(caTestRepoB), Name: sdkaws.String("rb"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(caTestForeignOwner), AdministratorAccount: sdkaws.String(testAccountID)},
			}}, nil
		},
		"ListPackageGroups": func(in any) (any, error) {
			c.caRecord("ListPackageGroups")
			if err := caPageSize(in); err != nil {
				return nil, err
			}
			p := in.(*codeartifact.ListPackageGroupsInput)
			if p.DomainOwner != nil {
				return nil, apiErr("ValidationException", "unexpected domainOwner")
			}
			switch sv(p.Domain) {
			case "d1":
				return &codeartifact.ListPackageGroupsOutput{PackageGroups: []catypes.PackageGroupSummary{
					{Arn: sdkaws.String(caTestGroup), Pattern: sdkaws.String("/*"), DomainName: sdkaws.String("d1"), CreatedTime: &caCreated},
					{Arn: sdkaws.String(caTestGroup2), Pattern: sdkaws.String("/npm/*"), DomainName: sdkaws.String("d1")},
				}}, nil
			case "d2":
				return &codeartifact.ListPackageGroupsOutput{}, nil
			}
			return nil, apiErr("ResourceNotFoundException", "unknown domain "+sv(p.Domain))
		},
		"ListPackages": func(in any) (any, error) {
			c.caRecord("ListPackages")
			p := in.(*codeartifact.ListPackagesInput)
			repo := sdkaws.ToString(p.Domain) + "/" + sdkaws.ToString(p.Repository)
			owner := testAccountID
			if repo == "d1/rb" {
				owner = caTestForeignOwner
			}
			if sdkaws.ToInt32(p.MaxResults) != 1000 || sdkaws.ToString(p.DomainOwner) != owner {
				return nil, apiErr("ValidationException", "unexpected maxResults or domainOwner")
			}
			c.repos.add(repo)
			switch {
			case repo == "d1/ra" && p.NextToken == nil:
				return &codeartifact.ListPackagesOutput{NextToken: sdkaws.String("p2"), Packages: []catypes.PackageSummary{
					{Format: catypes.PackageFormatNpm, Namespace: sdkaws.String("acme"), Package: sdkaws.String("left-pad")},
					{Format: catypes.PackageFormatNpm, Namespace: sdkaws.String("acme")},
				}}, nil
			case repo == "d1/ra":
				return &codeartifact.ListPackagesOutput{Packages: []catypes.PackageSummary{
					{Format: catypes.PackageFormatPypi, Package: sdkaws.String("requests")},
				}}, nil
			case repo == "d1/rb":
				return &codeartifact.ListPackagesOutput{Packages: []catypes.PackageSummary{
					{Format: catypes.PackageFormatMaven, Namespace: sdkaws.String("com.example"), Package: sdkaws.String("lib")},
					{Namespace: sdkaws.String("com.example"), Package: sdkaws.String("no-format")},
				}}, nil
			}
			return nil, apiErr("ResourceNotFoundException", "unknown repository "+repo)
		},
	}
}

// caScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func caScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, caDenied)
	return scanCodeArtifact(ctx, acct, testRegion, st, testScanID)
}

func caPackageIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	return nativeIDs(rowsByNativeID(t, st, TypeCodeArtifactPackage))
}

// caParentsStored fails the test unless both domains, both repositories and
// both package groups are stored.
func caParentsStored(t *testing.T, st *store.Store) {
	t.Helper()
	for typ, want := range map[string][]string{
		TypeCodeArtifactDomain:       {caTestDomain, caTestDomain2},
		TypeCodeArtifactRepository:   {caTestRepoA, caTestRepoB},
		TypeCodeArtifactPackageGroup: {caTestGroup, caTestGroup2},
	} {
		slices.Sort(want)
		if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v; want %v", typ, got, want)
		}
	}
}

// caTagReadARNs is the ARN of every row whose tags the fixture's scan reads:
// every domain, repository and package group, no package.
func caTagReadARNs() []string {
	return slices.Sorted(slices.Values([]string{caTestDomain, caTestDomain2, caTestRepoA, caTestRepoB, caTestGroup, caTestGroup2}))
}

// Every domain, repository and package group is stored under its ARN, named
// by its name (a package group by its pattern), with the listed element as
// attributes and the tags ListTagsForResource returns for that ARN, nil for
// a resource without tags. Exactly those ARNs are sent to the tag op, no
// package's. A domain's Status is its status, none when it has none; no row
// has a CreatedAt. The element without an ARN is warned about once.
func TestScanCodeArtifact_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCACalls()

	counted, upserts := countUpserts(st)
	total, err := caScan(context.Background(), counted, caHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != caTestTotal || inserted != caTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, caTestTotal, caTestTotal)
	}
	if w := warns(); len(w) != 1 || w[0].Service != "codeartifact:ListRepositories" || !strings.Contains(w[0].Message, "no-arn") {
		t.Errorf("warnings = %v; want one under codeartifact:ListRepositories naming no-arn", w)
	}
	caParentsStored(t, st)
	for _, c := range []struct {
		typ, arn, name, status, attr string
		tags                         map[string]string
	}{
		{TypeCodeArtifactDomain, caTestDomain, "d1", "Active", `"Owner":"` + testAccountID + `"`, map[string]string{"kind": "domain"}},
		{TypeCodeArtifactDomain, caTestDomain2, "d2", "", `"Name":"d2"`, nil},
		{TypeCodeArtifactRepository, caTestRepoA, "ra", "", `"AdministratorAccount":"` + testAccountID + `"`, map[string]string{"kind": "repository"}},
		{TypeCodeArtifactRepository, caTestRepoB, "rb", "", `"DomainOwner":"` + caTestForeignOwner + `"`, nil},
		{TypeCodeArtifactPackageGroup, caTestGroup, "/*", "", `"Pattern":"/*"`, map[string]string{"kind": "package-group"}},
		{TypeCodeArtifactPackageGroup, caTestGroup2, "/npm/*", "", `"Pattern":"/npm/*"`, nil},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s %s missing", c.typ, c.arn)
			continue
		}
		if sv(r.Name) != c.name || sv(r.Region) != testRegion || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s Name, Region, CreatedAt, Managed = %q, %q, %v, %v; want %q, %q, nil, false", c.arn, sv(r.Name), sv(r.Region), r.CreatedAt, r.ManagedByProvider, c.name, testRegion)
		}
		if c.status == "" && r.Status != nil {
			t.Errorf("%s Status = %q; want none", c.arn, *r.Status)
		} else if c.status != "" && sv(r.Status) != c.status {
			t.Errorf("%s Status = %q; want %q", c.arn, sv(r.Status), c.status)
		}
		if !strings.Contains(r.AttributesJSON, `"Arn":"`+c.arn+`"`) || !strings.Contains(r.AttributesJSON, c.attr) {
			t.Errorf("%s attrs = %s; want the listed element with %s", c.arn, r.AttributesJSON, c.attr)
		}
		if c.tags != nil {
			assertTagsMap(t, r.TagsJSON, c.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
		}
	}
	if got := calls.caSentARNs(); !slices.Equal(got, caTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, caTagReadARNs())
	}
	for _, r := range rowsByNativeID(t, st, TypeCodeArtifactPackage) {
		if r.TagsJSON != nil {
			t.Errorf("package %s TagsJSON = %q; want nil", r.NativeID, *r.TagsJSON)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanCodeArtifact_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := caScan(context.Background(), st, caHandlers(newCACalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	counted, upserts := countUpserts(st)
	total, err := caScan(context.Background(), counted, caHandlers(newCACalls()))
	inserted := upserts()
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != caTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, caTestTotal)
	}
}

// An account without domains or repositories stores nothing, reads no tags
// and lists no package groups or packages.
func TestScanCodeArtifact_NoDomains(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCACalls()
	h := caHandlers(calls)
	h["ListDomains"] = func(any) (any, error) { return &codeartifact.ListDomainsOutput{}, nil }
	h["ListRepositories"] = func(any) (any, error) { return &codeartifact.ListRepositoriesOutput{}, nil }
	total, err := caScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
	for _, op := range []string{"ListTagsForResource", "ListPackageGroups", "ListPackages"} {
		if n := calls.caCount(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
}

func TestScanCodeArtifact_StoresPackages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCACalls()
	if _, err := caScan(context.Background(), st, caHandlers(calls)); err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if got := calls.repos.distinct(); !slices.Equal(got, []string{"d1/ra", "d1/rb"}) {
		t.Errorf("ListPackages repositories = %v, want d1/ra and d1/rb", got)
	}
	if got := caPackageIDs(t, st); !slices.Equal(got, caAllPkgARNs) {
		t.Errorf("package rows = %v, want %v", got, caAllPkgARNs)
	}
	rows := rowsByNativeID(t, st, TypeCodeArtifactPackage)
	for id, r := range rows {
		if r.Type != TypeCodeArtifactPackage || sv(r.Region) != testRegion {
			t.Errorf("%s: type %q region %q", id, r.Type, sv(r.Region))
		}
	}
	if _, ok := rows[caTestPkgB1]; !ok {
		t.Errorf("package of rb missing under %s; its ARN must carry the repository ARN's account %s", caTestPkgB1, caTestForeignOwner)
	}
	if got := sv(rows[caTestPkgA2].Name); got != "requests" {
		t.Errorf("Name = %q, want requests", got)
	}
	var attrs struct {
		Format    string `json:"Format"`
		Namespace string `json:"Namespace"`
		Package   string `json:"Package"`
	}
	if err := json.Unmarshal([]byte(rows[caTestPkgA1].AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.Format != "npm" || attrs.Namespace != "acme" || attrs.Package != "left-pad" {
		t.Errorf("attrs = %+v, want the SDK element verbatim", attrs)
	}

	assertContainsNative(t, st, caTestRepoA, caTestPkgA1)
	assertContainsNative(t, st, caTestRepoA, caTestPkgA2)
	assertContainsNative(t, st, caTestRepoB, caTestPkgB1)
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, caTestRepoB), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("repository rb contains %d rows, want only its own package", len(rels))
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanCodeArtifact_NoPackages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := caHandlers(newCACalls())
	h["ListPackages"] = func(any) (any, error) { return &codeartifact.ListPackagesOutput{}, nil }
	total, err := caScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if got := caPackageIDs(t, st); len(got) != 0 {
		t.Errorf("package rows = %v, want none", got)
	}
	if total != caTestTotal-3 {
		t.Errorf("total = %d, want %d", total, caTestTotal-3)
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently, and a
// dropped domain or repository is no parent of the later phases; a denial
// keeps the row untagged and warns once under the tag op, naming it; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanCodeArtifact_TagReadFailures(t *testing.T) {
	t.Parallel()
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
	for _, k := range []struct {
		typ, arn string
		// lost is the rows a not-found costs besides the row itself: its
		// children, which are then not listed.
		lost int
	}{
		{TypeCodeArtifactDomain, caTestDomain, 2},
		{TypeCodeArtifactRepository, caTestRepoA, 2},
		{TypeCodeArtifactPackageGroup, caTestGroup, 0},
	} {
		cases = append(cases,
			tagCase{k.typ + "/not found", k.arn, k.typ, caRepoGone, -1, "", caTestTotal - 1 - k.lost, false, 0},
			tagCase{k.typ + "/denied", k.arn, k.typ, caDenied, -1, "", caTestTotal, true, 1},
			tagCase{k.typ + "/transient", k.arn, k.typ, caThrottle, outcomeTransient, "ThrottlingException", caTestTotal, true, 0},
			tagCase{k.typ + "/other error", k.arn, k.typ, caBadInput, outcomeError, "ValidationException", caTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, caTagsOp)
			calls := newCACalls()
			calls.errs[c.arn] = c.err

			total, err := caScan(context.Background(), st, caHandlers(calls))
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
			if len(*warns) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", caTagsOp, *warns, c.wantWarns)
			}
			for _, x := range *warns {
				if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one in %s/%s naming %s", x, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in every phase send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanCodeArtifact_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, caTagsOp)
	calls := newCACalls()
	denied := []string{caTestDomain, caTestRepoA, caTestGroup}
	for _, arn := range denied {
		calls.errs[arn] = caDenied
	}

	total, err := caScan(context.Background(), st, caHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != caTestTotal {
		t.Errorf("total = %d; want %d", total, caTestTotal)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, caTestDomain) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", caTagsOp, *warnings, testAccountID, testRegion, caTestDomain)
	}
	for _, arn := range denied {
		found := false
		for _, typ := range []string{TypeCodeArtifactDomain, TypeCodeArtifactRepository, TypeCodeArtifactPackageGroup} {
			if r, ok := rowsByNativeID(t, st, typ)[arn]; ok {
				found = r.TagsJSON == nil
			}
		}
		if !found {
			t.Errorf("%s not stored untagged", arn)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanCodeArtifact_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, caTagsOp)
	calls := newCACalls()
	calls.errs[caTestDomain] = caThrottle
	calls.errs[caTestGroup2] = caBadInput

	_, err := caScan(context.Background(), st, caHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", caTagsOp, *warns)
	}
}

// A repository whose ARN names another account (rb, in a domain that account
// owns) is kept untagged without a warning or an error when its tag read is
// denied or finds no such resource; a throttle is still transient. The same
// denial on a repository whose ARN names this account warns, also when its
// element names another account as its administrator (rc).
func TestScanCodeArtifact_OtherAccountRepository(t *testing.T) {
	t.Parallel()
	adminElsewhere := caTestPrefix + "repository/d1/rc"
	for _, c := range []struct {
		name, arn string
		err       error
		outcome   serviceOutcome // -1: no error
		wantWarns int
	}{
		{"domain owner elsewhere/denied", caTestRepoB, caDenied, -1, 0},
		{"domain owner elsewhere/not found", caTestRepoB, caRepoGone, -1, 0},
		{"domain owner elsewhere/throttled", caTestRepoB, caThrottle, outcomeTransient, 0},
		{"administrator elsewhere/denied", adminElsewhere, caDenied, -1, 1},
		{"own/denied", caTestRepoA, caDenied, -1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, caTagsOp)
			calls := newCACalls()
			calls.errs[c.arn] = c.err
			h := caHandlers(calls)
			list := h["ListRepositories"]
			h["ListRepositories"] = func(in any) (any, error) {
				out, err := list(in)
				if err != nil {
					return nil, err
				}
				o := out.(*codeartifact.ListRepositoriesOutput)
				o.Repositories = append(o.Repositories, catypes.RepositorySummary{
					Arn: sdkaws.String(adminElsewhere), Name: sdkaws.String("rc"), DomainName: sdkaws.String("d1"),
					DomainOwner: sdkaws.String(testAccountID), AdministratorAccount: sdkaws.String(caTestForeignOwner),
				})
				return o, nil
			}
			h["ListPackages"] = func(any) (any, error) { return &codeartifact.ListPackagesOutput{}, nil }

			total, err := caScan(context.Background(), st, h)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
			}
			if want := caTestTotal - 3 + 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if r, ok := rowsByNativeID(t, st, TypeCodeArtifactRepository)[c.arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", c.arn, ok)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d; want %d", caTagsOp, *warns, c.wantWarns)
			}
		})
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phase before it keeps its rows.
func TestScanCodeArtifact_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeCodeArtifactRepository)
	calls := newCACalls()
	calls.errs[caTestRepoA] = caDenied

	total, err := caScan(context.Background(), st, caHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListPackageGroups", "ListPackages"} {
		if n := calls.caCount(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 domains", total)
	}
}

// caFirstPage answers every call of a listing with page.
func caFirstPage(page any) func(any) (any, error) {
	return func(any) (any, error) { return page, nil }
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the later phases, which read the parents it stored.
func TestScanCodeArtifact_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	t.Run("domains", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCACalls()
		h := caHandlers(calls)
		h["ListDomains"] = failOnCall(2, caBadInput, caFirstPage(
			&codeartifact.ListDomainsOutput{NextToken: sdkaws.String("t2"), Domains: []catypes.DomainSummary{{Arn: sdkaws.String(caTestDomain), Name: sdkaws.String("d1")}}}))
		total, err := caScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "ValidationException")
		if total != caTestTotal-1 {
			t.Errorf("total = %d; want %d", total, caTestTotal-1)
		}
		if got := nativeIDs(rowsByNativeID(t, st, TypeCodeArtifactPackageGroup)); len(got) != 2 {
			t.Errorf("package groups of the domain read = %v; want 2", got)
		}
	})
	t.Run("repositories", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCACalls()
		h := caHandlers(calls)
		h["ListRepositories"] = failOnCall(2, caBadInput, caFirstPage(
			&codeartifact.ListRepositoriesOutput{NextToken: sdkaws.String("t2"), Repositories: []catypes.RepositorySummary{
				{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra"), DomainOwner: sdkaws.String(testAccountID)},
			}}))
		total, err := caScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "ValidationException")
		// rb and its package are lost.
		if total != caTestTotal-2 {
			t.Errorf("total = %d; want %d", total, caTestTotal-2)
		}
		if got := calls.repos.distinct(); !slices.Equal(got, []string{"d1/ra"}) {
			t.Errorf("ListPackages repositories = %v; want d1/ra", got)
		}
	})
}

// The repository listing and each domain's package-group listing write
// every page, with its tags, before they request the next: ra and /* are
// stored tagged when page 2 of their listing is requested.
func TestScanCodeArtifact_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCACalls()
	h := caHandlers(calls)
	type lookup struct {
		row *store.Resource
		err error
	}
	lookups := map[string]lookup{} // per listing: its page-1 row, read when page 2 was requested
	lookUp := func(op, arn string) {
		r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
		lookups[op] = lookup{r, err}
	}
	h["ListRepositories"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") == nil {
			return &codeartifact.ListRepositoriesOutput{NextToken: sdkaws.String("t2"), Repositories: []catypes.RepositorySummary{
				{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(testAccountID)},
			}}, nil
		}
		lookUp("ListRepositories", caTestRepoA)
		return &codeartifact.ListRepositoriesOutput{Repositories: []catypes.RepositorySummary{
			{Arn: sdkaws.String(caTestRepoB), Name: sdkaws.String("rb"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(caTestForeignOwner)},
		}}, nil
	}
	listGroups := h["ListPackageGroups"]
	h["ListPackageGroups"] = func(in any) (any, error) {
		g := in.(*codeartifact.ListPackageGroupsInput)
		switch {
		case sv(g.Domain) != "d1":
			return listGroups(in)
		case g.NextToken == nil:
			return &codeartifact.ListPackageGroupsOutput{NextToken: sdkaws.String("t2"), PackageGroups: []catypes.PackageGroupSummary{
				{Arn: sdkaws.String(caTestGroup), Pattern: sdkaws.String("/*"), DomainName: sdkaws.String("d1")},
			}}, nil
		}
		lookUp("ListPackageGroups", caTestGroup)
		return &codeartifact.ListPackageGroupsOutput{PackageGroups: []catypes.PackageGroupSummary{
			{Arn: sdkaws.String(caTestGroup2), Pattern: sdkaws.String("/npm/*"), DomainName: sdkaws.String("d1")},
		}}, nil
	}

	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, c := range map[string]struct {
		arn  string
		tags map[string]string
	}{
		"ListRepositories":  {caTestRepoA, map[string]string{"kind": "repository"}},
		"ListPackageGroups": {caTestGroup, map[string]string{"kind": "package-group"}},
	} {
		got, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if got.err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, c.arn, got.err)
			continue
		}
		assertTagsMap(t, got.row.TagsJSON, c.tags)
	}
	caParentsStored(t, st)
}

// A repeated token ends a listing as an error after its second call, keeping
// both pages, and the later phases still run.
func TestScanCodeArtifact_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	same := sdkaws.String("same")
	for _, c := range []struct {
		op            string
		first, second any
	}{
		{
			"ListDomains",
			&codeartifact.ListDomainsOutput{NextToken: same, Domains: []catypes.DomainSummary{{Arn: sdkaws.String(caTestDomain), Name: sdkaws.String("d1")}}},
			&codeartifact.ListDomainsOutput{NextToken: same, Domains: []catypes.DomainSummary{{Arn: sdkaws.String(caTestDomain2), Name: sdkaws.String("d2")}}},
		},
		{
			"ListRepositories",
			&codeartifact.ListRepositoriesOutput{NextToken: same, Repositories: []catypes.RepositorySummary{{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra"), DomainOwner: sdkaws.String(testAccountID)}}},
			&codeartifact.ListRepositoriesOutput{NextToken: same, Repositories: []catypes.RepositorySummary{{Arn: sdkaws.String(caTestRepoB), Name: sdkaws.String("rb"), DomainOwner: sdkaws.String(caTestForeignOwner)}}},
		},
		{
			"ListPackageGroups",
			&codeartifact.ListPackageGroupsOutput{NextToken: same, PackageGroups: []catypes.PackageGroupSummary{{Arn: sdkaws.String(caTestGroup), Pattern: sdkaws.String("/*")}}},
			&codeartifact.ListPackageGroupsOutput{NextToken: same, PackageGroups: []catypes.PackageGroupSummary{{Arn: sdkaws.String(caTestGroup2), Pattern: sdkaws.String("/npm/*")}}},
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCACalls()
			h := caHandlers(calls)
			list := h[c.op]
			// Only domain d1's package groups repeat the token; d2's are
			// answered as the fixture answers them.
			h[c.op] = func(in any) (any, error) {
				if g, ok := in.(*codeartifact.ListPackageGroupsInput); ok && sv(g.Domain) != "d1" {
					return list(in)
				}
				calls.caRecord("repeating " + c.op)
				if bacField[*string](in, "NextToken") == nil {
					return c.first, nil
				}
				return c.second, nil
			}
			total, err := caScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if n := calls.caCount("repeating " + c.op); n != 2 {
				t.Errorf("%s calls = %d; want 2", c.op, n)
			}
			if total != caTestTotal {
				t.Errorf("total = %d; want %d", total, caTestTotal)
			}
			caParentsStored(t, st)
			if got := caPackageIDs(t, st); !slices.Equal(got, caAllPkgARNs) {
				t.Errorf("package rows = %v; want %v", got, caAllPkgARNs)
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or pattern, or by
// the fallback text when it has none.
func TestScanCodeArtifact_ElementWithoutARNWarns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op, key string
		answer        any
	}{
		{"domain", "ListDomains", noIdentifier, &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{{}}}},
		{"package group", "ListPackageGroups", "/x/*", &codeartifact.ListPackageGroupsOutput{PackageGroups: []catypes.PackageGroupSummary{{Pattern: sdkaws.String("/x/*")}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "codeartifact:"+c.op)
			calls := newCACalls()
			h := caHandlers(calls)
			h[c.op] = func(any) (any, error) { return c.answer, nil }
			if _, err := caScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if w := *warnings; len(w) != 1 || !strings.Contains(w[0].Message, c.key) {
				t.Errorf("codeartifact:%s warnings = %v; want one naming %q", c.op, w, c.key)
			}
			if slices.Contains(calls.caSentARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// A domain whose name is missing is stored with its tags but lists no
// package groups, and is warned about under ListDomains.
func TestScanCodeArtifact_DomainWithoutNameHasNoChildren(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListDomains")
	calls := newCACalls()
	h := caHandlers(calls)
	h["ListDomains"] = func(any) (any, error) {
		return &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{{Arn: sdkaws.String(caTestDomain)}}}, nil
	}
	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeCodeArtifactDomain)[caTestDomain]
	if !ok {
		t.Fatalf("domain %s missing", caTestDomain)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"kind": "domain"})
	if !slices.Contains(calls.caSentARNs(), caTestDomain) {
		t.Errorf("tag op ARNs = %v; want %s among them", calls.caSentARNs(), caTestDomain)
	}
	if n := calls.caCount("ListPackageGroups"); n != 0 {
		t.Errorf("ListPackageGroups calls = %d; want 0", n)
	}
	if *warns != 1 {
		t.Errorf("codeartifact:ListDomains warnings = %d; want 1", *warns)
	}
}

// Each domain's package-group listing stands alone: a domain deleted since
// it was listed is skipped silently, a denied one warns once for the op, and
// any other error is returned; the other domain's groups are stored in every
// case.
func TestScanCodeArtifact_PackageGroupListingFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"not found", caRepoGone, false, 0},
		{"denied", caDenied, false, 1},
		{"other error", caBadInput, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "codeartifact:ListPackageGroups")
			calls := newCACalls()
			h := caHandlers(calls)
			list := h["ListPackageGroups"]
			h["ListDomains"] = func(any) (any, error) {
				return &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{
					{Arn: sdkaws.String(caTestDomain), Name: sdkaws.String("d1")},
					{Arn: sdkaws.String(caTestDomain2), Name: sdkaws.String("d2")},
					{Arn: sdkaws.String(caTestPrefix + "domain/d3"), Name: sdkaws.String("d3")},
				}}, nil
			}
			h["ListPackageGroups"] = func(in any) (any, error) {
				if sv(in.(*codeartifact.ListPackageGroupsInput).Domain) == "d3" {
					return nil, c.err
				}
				return list(in)
			}
			total, err := caScan(context.Background(), st, h)
			if c.wantErr {
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != caTestTotal+1 {
				t.Errorf("total = %d; want %d", total, caTestTotal+1)
			}
			if got, want := nativeIDs(rowsByNativeID(t, st, TypeCodeArtifactPackageGroup)), []string{caTestGroup, caTestGroup2}; !slices.Equal(got, want) {
				t.Errorf("package groups = %v; want %v", got, want)
			}
			if *warns != c.wantWarns {
				t.Errorf("codeartifact:ListPackageGroups warnings = %d; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// A denied domain listing warns under its op and the later phases still run.
func TestScanCodeArtifact_DomainsDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListDomains")
	calls := newCACalls()
	h := caHandlers(calls)
	delete(h, "ListDomains")
	total, err := caScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("codeartifact:ListDomains warnings = %d; want 1", *warns)
	}
	// The repositories and their packages; no domain lists package groups.
	if total != 5 {
		t.Errorf("total = %d; want 5", total)
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanCodeArtifact_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCACalls()
			h := caHandlers(calls)
			domain := "arn:" + c.partition + ":codeartifact:" + c.region + ":" + testAccountID + ":domain/dp"
			calls.tags[domain] = map[string]string{"k": "v"}
			h["ListDomains"] = func(any) (any, error) {
				return &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{{Arn: &domain, Name: sdkaws.String("d2")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(h, caDenied)
			if _, err := scanCodeArtifact(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.caSentARNs(), domain) {
				t.Errorf("ListTagsForResource ARNs %v lack %s", calls.caSentARNs(), domain)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeCodeArtifactDomain)[domain].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanCodeArtifact_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCACalls()
	h := caHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListRepositories"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return &codeartifact.ListRepositoriesOutput{NextToken: sdkaws.String("t2"), Repositories: []catypes.RepositorySummary{{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra")}}}, nil
	}

	_, err := caScan(ctx, st, h)
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
	for _, op := range []string{"ListPackageGroups", "ListPackages"} {
		if n := calls.caCount(op); n != 0 {
			t.Errorf("%s called %d times after the stop; want 0", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanCodeArtifact_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "codeartifact.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		h := caHandlers(newCACalls())
		h["ListPackageGroups"] = func(any) (any, error) { return nil, nx }
		total, err := caScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != caTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, caTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCACalls()
		calls.errs[caTestDomain2] = nx
		_, err := caScan(context.Background(), st, caHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanCodeArtifact(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// AccessDenied on ListPackages warns once for the op, not once per repository,
// and keeps every other row.
func TestScanCodeArtifact_PackagesAccessDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	h := caHandlers(newCACalls())
	delete(h, "ListPackages")
	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
	if got := caPackageIDs(t, st); len(got) != 0 {
		t.Errorf("package rows = %v, want none", got)
	}
	caParentsStored(t, st)
}

// A repository deleted after ListRepositories is skipped silently; its
// siblings' packages are stored.
func TestScanCodeArtifact_RepositoryGone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	h := caHandlers(newCACalls())
	list := h["ListPackages"]
	h["ListPackages"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*codeartifact.ListPackagesInput).Repository) == "ra" {
			return nil, caRepoGone
		}
		return list(in)
	}
	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if got := caPackageIDs(t, st); !slices.Equal(got, []string{caTestPkgB1}) {
		t.Errorf("package rows = %v, want only %s", got, caTestPkgB1)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// A repository whose listing still fails transiently after retries is skipped
// with one warning; its siblings' packages are stored.
func TestScanCodeArtifact_RepositoryTransientSkipped(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	h := caHandlers(newCACalls())
	list := h["ListPackages"]
	h["ListPackages"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*codeartifact.ListPackagesInput).Repository) == "ra" {
			return nil, caThrottle
		}
		return list(in)
	}
	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if got := caPackageIDs(t, st); !slices.Equal(got, []string{caTestPkgB1}) {
		t.Errorf("package rows = %v, want only %s", got, caTestPkgB1)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// Any other ListPackages error is returned, and the rows of every earlier
// phase stay stored: the package phase runs after all of them.
func TestScanCodeArtifact_PackagesErrorKeepsParents(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := caHandlers(newCACalls())
	h["ListPackages"] = func(any) (any, error) { return nil, caBadInput }
	total, err := caScan(context.Background(), st, h)
	assertAPICode(t, err, "ValidationException")
	caParentsStored(t, st)
	if total != caTestTotal-3 {
		t.Errorf("total = %d, want %d", total, caTestTotal-3)
	}
}

// A hard error in an earlier phase is returned, and the package phase still
// lists the repositories stored before it.
func TestScanCodeArtifact_ParentErrorStillListsPackages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := caHandlers(newCACalls())
	h["ListPackageGroups"] = func(any) (any, error) { return nil, caBadInput }
	total, err := caScan(context.Background(), st, h)
	assertAPICode(t, err, "ValidationException")
	if got := caPackageIDs(t, st); !slices.Equal(got, caAllPkgARNs) {
		t.Errorf("package rows = %v, want %v", got, caAllPkgARNs)
	}
	if total != caTestTotal-2 {
		t.Errorf("total = %d, want %d", total, caTestTotal-2)
	}
}

// A transient error in an earlier phase is demoted to a warning under its op
// so that the package phase's hard error still reaches the dispatcher as a
// hard error.
func TestScanCodeArtifact_ParentTransientDoesNotMaskHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackageGroups")
	h := caHandlers(newCACalls())
	h["ListPackageGroups"] = func(any) (any, error) { return nil, caThrottle }
	h["ListPackages"] = func(any) (any, error) { return nil, caBadInput }
	_, err := caScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// ListPackages stops when the service repeats the token it was sent.
func TestScanCodeArtifact_PackagesRepeatedTokenStops(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h := caHandlers(newCACalls())
	var count atomic.Int32
	h["ListPackages"] = func(any) (any, error) {
		if count.Add(1) > 10 {
			return nil, apiErr("LoopGuard", "the repeated token was not detected")
		}
		return &codeartifact.ListPackagesOutput{NextToken: sdkaws.String("same")}, nil
	}
	if _, err := caScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	// Per repository: the first call, then one repeat of "same".
	if got := count.Load(); got != 4 {
		t.Errorf("ListPackages calls = %d, want 4", got)
	}
}

// A repository ARN not of the form repository/<domain>/<name> fails the
// package phase rather than listing packages of a guessed repository.
func TestScanCodeArtifact_MalformedRepositoryARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCACalls()
	h := caHandlers(calls)
	h["ListRepositories"] = func(any) (any, error) {
		return &codeartifact.ListRepositoriesOutput{Repositories: []catypes.RepositorySummary{
			{Arn: sdkaws.String(caTestPrefix + "repository/ra"), Name: sdkaws.String("ra")},
		}}, nil
	}
	if _, err := caScan(context.Background(), st, h); err == nil {
		t.Fatal("scanCodeArtifact err = nil, want the malformed repository ARN")
	}
	if got := calls.repos.distinct(); len(got) != 0 {
		t.Errorf("ListPackages repositories = %v, want none", got)
	}
}

func TestCARepositoryPackageARN(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		repo string
		pkg  catypes.PackageSummary
		want string
	}{
		{caTestRepoA, catypes.PackageSummary{Format: catypes.PackageFormatNpm, Namespace: sdkaws.String("acme"), Package: sdkaws.String("left-pad")}, caTestPkgA1},
		{caTestRepoA, catypes.PackageSummary{Format: catypes.PackageFormatPypi, Package: sdkaws.String("requests")}, caTestPkgA2},
		{
			"arn:aws-cn:codeartifact:cn-north-1:210987654321:repository/d2/r.2",
			catypes.PackageSummary{Format: catypes.PackageFormatGeneric, Namespace: sdkaws.String("ns"), Package: sdkaws.String("tool")},
			"arn:aws-cn:codeartifact:cn-north-1:210987654321:package/d2/r.2/generic/ns/tool",
		},
	} {
		repo, err := parseCARepositoryARN(tc.repo)
		if err != nil {
			t.Fatalf("parseCARepositoryARN(%q): %v", tc.repo, err)
		}
		if got := repo.packageARN(tc.pkg); got != tc.want {
			t.Errorf("packageARN(%q, %s/%s/%s) = %q, want %q", tc.repo, tc.pkg.Format, sv(tc.pkg.Namespace), sv(tc.pkg.Package), got, tc.want)
		}
	}
}

func TestParseCARepositoryARN_Rejects(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"not-an-arn",
		caTestPrefix + "repository/d1",
		caTestPrefix + "repository/d1/ra/extra",
		caTestPrefix + "repository//ra",
		caTestPrefix + "repository/d1/",
		caTestPrefix + "domain/d1/ra",
	} {
		if _, err := parseCARepositoryARN(in); err == nil {
			t.Errorf("parseCARepositoryARN(%q) err = nil, want an error", in)
		}
	}
}
