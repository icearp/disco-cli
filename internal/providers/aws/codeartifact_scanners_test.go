package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
	"github.com/icearp/disco-cli/store"
)

const (
	caTestPrefix = "arn:aws:codeartifact:us-east-1:123456789012:"
	caTestDomain = caTestPrefix + "domain/d1"
	caTestRepoA  = caTestPrefix + "repository/d1/ra"
	caTestGroup  = caTestPrefix + "package-group/d1/*"

	// Repository rb sits in a domain another account owns, so its ARN and its
	// packages' ARNs carry that account.
	caTestForeignOwner  = "210987654321"
	caTestForeignPrefix = "arn:aws:codeartifact:us-east-1:" + caTestForeignOwner + ":"
	caTestRepoB         = caTestForeignPrefix + "repository/d1/rb"

	caTestPkgA1 = caTestPrefix + "package/d1/ra/npm/acme/left-pad"
	caTestPkgA2 = caTestPrefix + "package/d1/ra/pypi//requests"
	caTestPkgB1 = caTestForeignPrefix + "package/d1/rb/maven/com.example/lib"
)

var (
	caDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: codeartifact:ListPackages")
	caBadInput   = apiErr("ValidationException", "bad")
	caThrottle   = apiErr("ThrottlingException", "slow down")
	caRepoGone   = apiErr("ResourceNotFoundException", "repository not found")
	caAllPkgARNs = []string{caTestPkgA1, caTestPkgA2, caTestPkgB1}
)

// caHandlers answers every CodeArtifact op the scan calls: domain d1,
// repositories ra and rb (plus one without an ARN) and one package group.
// Repository ra's packages span two pages, the first also holding an element
// without a package name; rb, whose domain caTestForeignOwner owns, has one
// package and one element without a format. ListPackages records the
// repository it was called for and rejects any page size but the model cap and
// any DomainOwner but the repository's.
func caHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListDomains": func(any) (any, error) {
			return &codeartifact.ListDomainsOutput{Domains: []catypes.DomainSummary{
				{Arn: sdkaws.String(caTestDomain), Name: sdkaws.String("d1")},
			}}, nil
		},
		"ListRepositories": func(any) (any, error) {
			return &codeartifact.ListRepositoriesOutput{Repositories: []catypes.RepositorySummary{
				{Arn: sdkaws.String(caTestRepoA), Name: sdkaws.String("ra"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(testAccountID)},
				{Name: sdkaws.String("no-arn"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(testAccountID)},
				{Arn: sdkaws.String(caTestRepoB), Name: sdkaws.String("rb"), DomainName: sdkaws.String("d1"), DomainOwner: sdkaws.String(caTestForeignOwner)},
			}}, nil
		},
		"ListPackageGroups": func(any) (any, error) {
			return &codeartifact.ListPackageGroupsOutput{PackageGroups: []catypes.PackageGroupSummary{
				{Arn: sdkaws.String(caTestGroup), Pattern: sdkaws.String("/*"), DomainName: sdkaws.String("d1")},
			}}, nil
		},
		"ListPackages": func(in any) (any, error) {
			p := in.(*codeartifact.ListPackagesInput)
			repo := sdkaws.ToString(p.Domain) + "/" + sdkaws.ToString(p.Repository)
			owner := testAccountID
			if repo == "d1/rb" {
				owner = caTestForeignOwner
			}
			if sdkaws.ToInt32(p.MaxResults) != codeArtifactPackagePageLimit || sdkaws.ToString(p.DomainOwner) != owner {
				return nil, apiErr("ValidationException", "unexpected maxResults or domainOwner")
			}
			calls.add(repo)
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

func scanCodeArtifactTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, caDenied)
	total, _, err := scanCodeArtifact(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func caPackageIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	ids := nativeIDs(rowsByNativeID(t, st, TypeCodeArtifactPackage))
	slices.Sort(ids)
	return ids
}

// caParentsStored fails the test unless the domain, both repositories and the
// package group are stored.
func caParentsStored(t *testing.T, st *store.Store) {
	t.Helper()
	for typ, id := range map[string]string{
		TypeCodeArtifactDomain:       caTestDomain,
		TypeCodeArtifactPackageGroup: caTestGroup,
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s row %s missing", typ, id)
		}
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCodeArtifactRepository)); !slices.Equal(got, []string{caTestRepoA, caTestRepoB}) {
		t.Errorf("repository rows = %v, want %v", got, []string{caTestRepoA, caTestRepoB})
	}
}

func TestScanCodeArtifact_StoresPackages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanCodeArtifactTopLevel(t, st, caHandlers(&calls))
	if err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	// 1 domain + 2 repositories + 1 package group + 3 packages
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"d1/ra", "d1/rb"}) {
		t.Errorf("ListPackages repositories = %v, want d1/ra and d1/rb", got)
	}
	caParentsStored(t, st)
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
	st := newTestStore(t)
	var calls parentCalls
	h := caHandlers(&calls)
	h["ListPackages"] = func(any) (any, error) { return &codeartifact.ListPackagesOutput{}, nil }
	total, err := scanCodeArtifactTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanCodeArtifact: %v", err)
	}
	if got := caPackageIDs(t, st); len(got) != 0 {
		t.Errorf("package rows = %v, want none", got)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

// AccessDenied on ListPackages warns once for the op, not once per repository,
// and keeps every other row.
func TestScanCodeArtifact_PackagesAccessDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	var calls parentCalls
	h := caHandlers(&calls)
	delete(h, "ListPackages")
	if _, err := scanCodeArtifactTopLevel(t, st, h); err != nil {
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
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	var calls parentCalls
	h := caHandlers(&calls)
	list := h["ListPackages"]
	h["ListPackages"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*codeartifact.ListPackagesInput).Repository) == "ra" {
			return nil, caRepoGone
		}
		return list(in)
	}
	if _, err := scanCodeArtifactTopLevel(t, st, h); err != nil {
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
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact:ListPackages")
	var calls parentCalls
	h := caHandlers(&calls)
	list := h["ListPackages"]
	h["ListPackages"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*codeartifact.ListPackagesInput).Repository) == "ra" {
			return nil, caThrottle
		}
		return list(in)
	}
	if _, err := scanCodeArtifactTopLevel(t, st, h); err != nil {
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
	st := newTestStore(t)
	var calls parentCalls
	h := caHandlers(&calls)
	h["ListPackages"] = func(any) (any, error) { return nil, caBadInput }
	total, err := scanCodeArtifactTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	caParentsStored(t, st)
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

// A hard error in an earlier phase is returned, and the package phase still
// lists the repositories stored before it.
func TestScanCodeArtifact_ParentErrorStillListsPackages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := caHandlers(&calls)
	h["ListPackageGroups"] = func(any) (any, error) { return nil, caBadInput }
	total, err := scanCodeArtifactTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := caPackageIDs(t, st); !slices.Equal(got, caAllPkgARNs) {
		t.Errorf("package rows = %v, want %v", got, caAllPkgARNs)
	}
	// 1 domain + 2 repositories + 3 packages
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
}

// A transient error in an earlier phase is demoted to a warning so that the
// package phase's hard error still reaches the dispatcher as a hard error.
func TestScanCodeArtifact_ParentTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "codeartifact")
	var calls parentCalls
	h := caHandlers(&calls)
	h["ListPackageGroups"] = func(any) (any, error) { return nil, caThrottle }
	h["ListPackages"] = func(any) (any, error) { return nil, caBadInput }
	_, err := scanCodeArtifactTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// ListPackages stops when the service repeats the token it was sent.
func TestScanCodeArtifact_PackagesRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := caHandlers(&calls)
	var count atomic.Int32
	h["ListPackages"] = func(any) (any, error) {
		if count.Add(1) > 10 {
			return nil, apiErr("LoopGuard", "the repeated token was not detected")
		}
		return &codeartifact.ListPackagesOutput{NextToken: sdkaws.String("same")}, nil
	}
	if _, err := scanCodeArtifactTopLevel(t, st, h); err != nil {
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
	st := newTestStore(t)
	var calls parentCalls
	h := caHandlers(&calls)
	h["ListRepositories"] = func(any) (any, error) {
		return &codeartifact.ListRepositoriesOutput{Repositories: []catypes.RepositorySummary{
			{Arn: sdkaws.String(caTestPrefix + "repository/ra"), Name: sdkaws.String("ra")},
		}}, nil
	}
	if _, err := scanCodeArtifactTopLevel(t, st, h); err == nil {
		t.Fatal("scanCodeArtifact err = nil, want the malformed repository ARN")
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("ListPackages repositories = %v, want none", got)
	}
}

func TestCARepositoryPackageARN(t *testing.T) {
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
