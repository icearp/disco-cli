package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	ecrpublictypes "github.com/aws/aws-sdk-go-v2/service/ecrpublic/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	ecrPubDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: ecr-public:Describe"}
	ecrPubServerErr = &smithy.GenericAPIError{Code: "ServerException", Message: "boom"}
)

const (
	ecrPubRegistryARN  = "arn:aws:ecr-public::123456789012:registry/123456789012"
	ecrPubRegistry2ARN = "arn:aws:ecr-public::123456789012:registry/other"
	ecrPubRepoA        = "arn:aws:ecr-public::123456789012:repository/a"
	ecrPubRepoB        = "arn:aws:ecr-public::123456789012:repository/b"
	ecrPubRepoNoReg    = "arn:aws:ecr-public::123456789012:repository/no-registry"
	ecrPubRepoOtherReg = "arn:aws:ecr-public::123456789012:repository/other-registry"
)

func ecrPubRepo(arn, name, registryID string) ecrpublictypes.Repository {
	r := ecrpublictypes.Repository{RepositoryArn: &arn, RepositoryName: &name}
	if registryID != "" {
		r.RegistryId = &registryID
	}
	return r
}

// ecrPubRepoHandler serves two DescribeRepositories pages: repos a and b in
// registry 123456789012, then one repo with no RegistryId and one in a
// registry DescribeRegistries never lists.
func ecrPubRepoHandler(in any) (any, error) {
	req, ok := in.(*ecrpublic.DescribeRepositoriesInput)
	if !ok {
		return nil, fmt.Errorf("ecrPubRepoHandler: got input %T, want *ecrpublic.DescribeRepositoriesInput", in)
	}
	if req.MaxResults == nil || *req.MaxResults != ecrPageLimit {
		return nil, fmt.Errorf("ecrPubRepoHandler: MaxResults = %v, want %d", req.MaxResults, ecrPageLimit)
	}
	if req.NextToken == nil {
		return &ecrpublic.DescribeRepositoriesOutput{
			Repositories: []ecrpublictypes.Repository{
				ecrPubRepo(ecrPubRepoA, "a", testAccountID),
				ecrPubRepo(ecrPubRepoB, "b", testAccountID),
			},
			NextToken: ptrStr("p2"),
		}, nil
	}
	return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{
		ecrPubRepo(ecrPubRepoNoReg, "no-registry", ""),
		ecrPubRepo(ecrPubRepoOtherReg, "other-registry", "999999999999"),
	}}, nil
}

// ecrPubRegistryPages serves DescribeRegistries page one (the account's
// registry with a primary alias) and page two (a registry with no RegistryId
// and one with no ARN), or failErr in place of page two when set.
func ecrPubRegistryPages(failErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*ecrpublic.DescribeRegistriesInput)
		if !ok {
			return nil, fmt.Errorf("ecrPubRegistryPages: got input %T, want *ecrpublic.DescribeRegistriesInput", in)
		}
		if req.MaxResults == nil || *req.MaxResults != ecrPageLimit {
			return nil, fmt.Errorf("ecrPubRegistryPages: MaxResults = %v, want %d", req.MaxResults, ecrPageLimit)
		}
		if req.NextToken == nil {
			return &ecrpublic.DescribeRegistriesOutput{
				Registries: []ecrpublictypes.Registry{{
					RegistryArn: ptrStr(ecrPubRegistryARN), RegistryId: ptrStr(testAccountID),
					RegistryUri: ptrStr("public.ecr.aws/myalias"),
					Aliases: []ecrpublictypes.RegistryAlias{
						{Name: ptrStr("fallback"), Status: ecrpublictypes.RegistryAliasStatusActive},
						{Name: ptrStr("myalias"), PrimaryRegistryAlias: true, Status: ecrpublictypes.RegistryAliasStatusActive},
					},
				}},
				NextToken: ptrStr("p2"),
			}, nil
		}
		if failErr != nil {
			return nil, failErr
		}
		return &ecrpublic.DescribeRegistriesOutput{Registries: []ecrpublictypes.Registry{
			{RegistryArn: ptrStr(ecrPubRegistry2ARN)},
			{RegistryId: ptrStr("no-arn")},
		}}, nil
	}
}

func ecrPubClient(handlers map[string]func(in any) (any, error)) *ecrpublic.Client {
	return ecrpublic.NewFromConfig(opStubConfig(handlers, ecrPubDenied))
}

// ecrPubContainsCount counts every contains edge in st.
func ecrPubContainsCount(t *testing.T, st *store.Store) int {
	t.Helper()
	rels, err := st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	n := 0
	for _, r := range rels {
		if r.Kind == store.RelContains {
			n++
		}
	}
	return n
}

func TestScanECRPublic_StoresRegistriesAndContainsEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": ecrPubRepoHandler,
		"DescribeRegistries":   ecrPubRegistryPages(nil),
	}, ecrPubDenied)

	total, _, err := runECRPublic(acct, "us-east-1", st)
	if err != nil {
		t.Fatalf("scanECRPublic: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d, want 6 (4 repositories, 2 registries)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeECRPublicRepository)); n != 4 {
		t.Errorf("repository rows = %d, want 4", n)
	}
	regs := rowsByNativeID(t, st, TypeECRPublicRegistry)
	if len(regs) != 2 {
		t.Fatalf("registry rows = %d, want 2 (the no-ARN registry is skipped): %v", len(regs), regs)
	}
	r := regs[ecrPubRegistryARN]
	if r.Type != TypeECRPublicRegistry || sv(r.Name) != "myalias" || sv(r.Region) != "us-east-1" {
		t.Errorf("registry row = type %q name %q region %q, want %q myalias us-east-1", r.Type, sv(r.Name), sv(r.Region), TypeECRPublicRegistry)
	}
	if r.ManagedByProvider {
		t.Error("registry row is ManagedByProvider; PutRegistryCatalogData makes it customer config")
	}
	var attrs struct {
		RegistryURI string `json:"RegistryUri"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.RegistryURI != "public.ecr.aws/myalias" {
		t.Errorf("attrs RegistryUri = %q (err %v), want public.ecr.aws/myalias", attrs.RegistryURI, err)
	}
	if got := sv(regs[ecrPubRegistry2ARN].Name); got != ecrPubRegistry2ARN {
		t.Errorf("id-less registry name = %q, want its ARN", got)
	}
	assertContainsNative(t, st, ecrPubRegistryARN, ecrPubRepoA)
	assertContainsNative(t, st, ecrPubRegistryARN, ecrPubRepoB)
	// Only the two same-RegistryId pairs: the repo without a RegistryId must not
	// join the registry without one, nor the other-registry repo any registry.
	if n := ecrPubContainsCount(t, st); n != 2 {
		t.Errorf("contains edges = %d, want 2", n)
	}
}

func TestEcrPublicRegistryLabel(t *testing.T) {
	cases := []struct {
		name string
		reg  ecrpublictypes.Registry
		want string
	}{
		{"primary alias", ecrpublictypes.Registry{RegistryId: ptrStr("1"), Aliases: []ecrpublictypes.RegistryAlias{{Name: ptrStr("x")}, {Name: ptrStr("p"), PrimaryRegistryAlias: true}}}, "p"},
		{"no primary alias", ecrpublictypes.Registry{RegistryId: ptrStr("1"), Aliases: []ecrpublictypes.RegistryAlias{{Name: ptrStr("x"), DefaultRegistryAlias: true}}}, "1"},
		{"primary alias without name", ecrpublictypes.Registry{RegistryId: ptrStr("1"), Aliases: []ecrpublictypes.RegistryAlias{{PrimaryRegistryAlias: true}}}, "1"},
		{"no id", ecrpublictypes.Registry{RegistryArn: ptrStr("arn:x")}, "arn:x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ecrPublicRegistryLabel(tc.reg); got != tc.want {
				t.Errorf("label = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanECRPublicRegistries_Empty(t *testing.T) {
	st := newTestStore(t)
	client := ecrPubClient(map[string]func(in any) (any, error){
		"DescribeRegistries": func(any) (any, error) { return &ecrpublic.DescribeRegistriesOutput{}, nil },
	})
	total, _, err := scanECRPublicRegistries(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeECRPublicRegistry)); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestScanECRPublicRegistries_Errors(t *testing.T) {
	cases := []struct {
		name      string
		handler   func(in any) (any, error)
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{"denied on first page", func(any) (any, error) { return nil, ecrPubDenied }, "", 0, 1},
		{"denied on second page keeps first", ecrPubRegistryPages(ecrPubDenied), "", 1, 1},
		{"error on first page", func(any) (any, error) { return nil, ecrPubServerErr }, "ServerException", 0, 0},
		{"error on second page keeps first", ecrPubRegistryPages(ecrPubServerErr), "ServerException", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "ecr-public:DescribeRegistries")
			client := ecrPubClient(map[string]func(in any) (any, error){"DescribeRegistries": tc.handler})

			total, _, err := scanECRPublicRegistries(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeECRPublicRegistry)); n != tc.wantTotal {
				t.Errorf("rows = %d, want %d", n, tc.wantTotal)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// ecrPublic is us-east-1-only: every other region makes no call, and the
// us-east-1 pass calls us-east-1 whatever the config's default region.
func TestScanECRPublic_RegionRouting(t *testing.T) {
	var (
		mu      sync.Mutex
		called  []string
		regions parentCalls
	)
	record := func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			mu.Lock()
			called = append(called, op)
			mu.Unlock()
			return h(in)
		}
	}
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": record("DescribeRepositories", ecrPubRepoHandler),
		"DescribeRegistries":   record("DescribeRegistries", ecrPubRegistryPages(nil)),
	}, ecrPubDenied)
	cfg.Region = "ap-southeast-2"
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(cfg, &regions)

	st := newTestStore(t)
	if total, _, err := runECRPublic(acct, "eu-west-1", st); err != nil || total != 0 || len(called) != 0 {
		t.Fatalf("eu-west-1: total %d, err %v, calls %v; want 0, nil, none", total, err, called)
	}
	if _, _, err := runECRPublic(acct, "us-east-1", st); err != nil {
		t.Fatalf("us-east-1: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	if n := len(rowsByNativeID(t, st, TypeECRPublicRegistry)); n != 2 {
		t.Errorf("registry rows = %d, want 2", n)
	}
}

// A denied repository listing still stores the registry, with no edges.
func TestScanECRPublic_RepositoriesDeniedStillScansRegistries(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "ecr-public:DescribeRepositories")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(any) (any, error) { return nil, ecrPubDenied },
		"DescribeRegistries": func(any) (any, error) {
			return &ecrpublic.DescribeRegistriesOutput{Registries: []ecrpublictypes.Registry{{
				RegistryArn: ptrStr(ecrPubRegistryARN), RegistryId: ptrStr(testAccountID),
			}}}, nil
		},
	}, ecrPubDenied)

	total, _, err := runECRPublic(acct, "us-east-1", st)
	if err != nil {
		t.Fatalf("scanECRPublic: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeECRPublicRegistry)[ecrPubRegistryARN]; !ok {
		t.Errorf("missing registry row %s", ecrPubRegistryARN)
	}
	if n := ecrPubContainsCount(t, st); n != 0 {
		t.Errorf("contains edges = %d, want 0", n)
	}
	if *warns != 1 {
		t.Errorf("DescribeRepositories warnings = %d, want 1", *warns)
	}
}

// A failed repository listing does not stop the registry phase: the registry
// is stored, linked to the repositories read before the failure, and the
// repository error is returned.
func TestScanECRPublic_RepositoryErrorStillScansRegistries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(in any) (any, error) {
			if in.(*ecrpublic.DescribeRepositoriesInput).NextToken == nil {
				return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{ecrPubRepo(ecrPubRepoA, "a", testAccountID)}, NextToken: ptrStr("p2")}, nil
			}
			return nil, ecrPubServerErr
		},
		"DescribeRegistries": func(any) (any, error) {
			return &ecrpublic.DescribeRegistriesOutput{Registries: []ecrpublictypes.Registry{{
				RegistryArn: ptrStr(ecrPubRegistryARN), RegistryId: ptrStr(testAccountID),
			}}}, nil
		},
	}, ecrPubDenied)

	total, _, err := runECRPublic(acct, "us-east-1", st)
	assertAPICode(t, err, "ServerException")
	if total != 2 {
		t.Errorf("total = %d, want 2 (repository a, registry)", total)
	}
	assertContainsNative(t, st, ecrPubRegistryARN, ecrPubRepoA)
	repo := rowsByNativeID(t, st, TypeECRPublicRepository)[ecrPubRepoA]
	var attrs struct {
		RepositoryName string
		RegistryID     string `json:"RegistryId"`
	}
	if err := json.Unmarshal([]byte(repo.AttributesJSON), &attrs); err != nil || attrs.RepositoryName != "a" || attrs.RegistryID != testAccountID || sv(repo.Name) != "a" {
		t.Errorf("repository a: Name %q, attrs %+v (err %v); want a, {a %s}", sv(repo.Name), attrs, err, testAccountID)
	}
}

// Public rows without their identifier are warned about once per phase and
// not stored.
func TestScanECRPublic_EmptyARNsWarned(t *testing.T) {
	st := newTestStore(t)
	repoWarns := countOpWarnings(st, "ecr-public:DescribeRepositories")
	regWarns := countOpWarnings(st, ecrPublicRegistriesOp)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(any) (any, error) {
			return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{{RepositoryName: ptrStr("noarn")}}}, nil
		},
		"DescribeRegistries": ecrPubRegistryPages(nil),
	}, ecrPubDenied)

	if _, _, err := runECRPublic(acct, "us-east-1", st); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeECRPublicRepository)); n != 0 {
		t.Errorf("repository rows = %d, want 0", n)
	}
	if *repoWarns != 1 || *regWarns != 1 {
		t.Errorf("warnings: repositories %d, registries %d; want 1 each", *repoWarns, *regWarns)
	}
}

// runECRPublic runs the public phases and returns their joined result.
func runECRPublic(acct *account, region string, st *store.Store) (int, int, error) {
	var p phaseTotals
	scanECRPublic(context.Background(), acct, region, st, testScanID, &p)
	return p.result(st, acct, region)
}

// The public registry phase runs after the private ECR phases and the public
// repository phase: its hard error reaches the caller with those rows stored
// and counted.
func TestScanECR_RegistryErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	called := false
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribePullThroughCacheRules": func(any) (any, error) {
			return &ecr.DescribePullThroughCacheRulesOutput{PullThroughCacheRules: []ecrtypes.PullThroughCacheRule{{EcrRepositoryPrefix: ptrStr("hub")}}}, nil
		},
		// ecr and ecrpublic share the op name; only the public input is served.
		"DescribeRepositories": func(in any) (any, error) {
			if _, ok := in.(*ecrpublic.DescribeRepositoriesInput); !ok {
				return nil, ecrPubDenied
			}
			return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{ecrPubRepo(ecrPubRepoA, "a", testAccountID)}}, nil
		},
		"DescribeRegistries": func(any) (any, error) {
			called = true
			return nil, ecrPubServerErr
		},
	}, ecrPubDenied)

	total, _, err := scanECR(context.Background(), acct, "us-east-1", st, testScanID)
	if !called {
		t.Fatal("DescribeRegistries never called")
	}
	assertAPICode(t, err, "ServerException")
	if total != 2 {
		t.Errorf("total = %d, want 2 (pull-through-cache rule, public repository)", total)
	}
	for typ, want := range map[string]string{
		TypeECRPullThroughCacheRule: "arn:aws:ecr:us-east-1:123456789012:pull-through-cache-rule/hub",
		TypeECRPublicRepository:     ecrPubRepoA,
	} {
		if _, ok := rowsByNativeID(t, st, typ)[want]; !ok {
			t.Errorf("missing %s row %s", typ, want)
		}
	}
}

const ecrScanCfgOp = "ecr:BatchGetRepositoryScanningConfiguration"

func ecrRepoARN(name string) string {
	return "arn:aws:ecr:us-east-1:123456789012:repository/" + name
}

func ecrScanCfgNativeID(name string) string {
	return "arn:aws:ecr:us-east-1:123456789012:repository-scanning-configuration/" + name
}

func ecrScanCfg(name string) ecrtypes.RepositoryScanningConfiguration {
	return ecrtypes.RepositoryScanningConfiguration{
		RepositoryName: ptrStr(name), RepositoryArn: ptrStr(ecrRepoARN(name)),
		ScanFrequency: ecrtypes.ScanFrequencyContinuousScan, ScanOnPush: true,
		AppliedScanFilters: []ecrtypes.ScanningRepositoryFilter{{Filter: ptrStr("prod-*"), FilterType: ecrtypes.ScanningRepositoryFilterTypeWildcard}},
	}
}

func ecrScanCfgOK(names []string) (any, error) {
	out := &ecr.BatchGetRepositoryScanningConfigurationOutput{}
	for _, n := range names {
		out.ScanningConfigurations = append(out.ScanningConfigurations, ecrScanCfg(n))
	}
	return out, nil
}

// ecrRepoNames returns n distinct repository names.
func ecrRepoNames(n int) []string {
	names := make([]string, n)
	for i := range n {
		names[i] = fmt.Sprintf("repo-%02d", i)
	}
	return names
}

// ecrSeedRepos stores a repository row per name, as the repository phase
// would, and returns them as that phase hands them on.
func ecrSeedRepos(t *testing.T, st *store.Store, names []string) []childParent {
	t.Helper()
	repos := make([]childParent, len(names))
	for i, n := range names {
		upsertTestResource(t, st, "aws", testAccountID, TypeECRRepository, ecrRepoARN(n), testRegion, "{}")
		repos[i] = childParent{id: n, arn: ecrRepoARN(n)}
	}
	return repos
}

// ecrScanCfgBatches records the repositoryNames of every
// BatchGetRepositoryScanningConfiguration call; calls run concurrently.
type ecrScanCfgBatches struct {
	mu      sync.Mutex
	batches [][]string
}

func (b *ecrScanCfgBatches) handler(respond func([]string) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		names := in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames
		b.mu.Lock()
		b.batches = append(b.batches, slices.Clone(names))
		b.mu.Unlock()
		return respond(names)
	}
}

func runECRScanCfg(st *store.Store, repos []childParent, h func(any) (any, error)) (int, int, error) {
	client := ecr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"BatchGetRepositoryScanningConfiguration": h,
	}, ecrPubDenied))
	return scanECRRepositoryScanningConfigs(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, repos)
}

func TestScanECRRepositoryScanningConfigs_BatchesAndStores(t *testing.T) {
	st := newTestStore(t)
	names := append(ecrRepoNames(25), "team/app")
	repos := ecrSeedRepos(t, st, names)
	var calls ecrScanCfgBatches

	total, _, err := runECRScanCfg(st, repos, calls.handler(ecrScanCfgOK))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 26 {
		t.Errorf("total = %d, want 26", total)
	}
	var sizes []int
	var requested []string
	for _, b := range calls.batches {
		sizes = append(sizes, len(b))
		requested = append(requested, b...)
	}
	slices.Sort(sizes)
	if !slices.Equal(sizes, []int{1, 25}) {
		t.Errorf("batch sizes = %v, want [1 25]", sizes)
	}
	slices.Sort(requested)
	want := slices.Clone(names)
	slices.Sort(want)
	if !slices.Equal(requested, want) {
		t.Errorf("requested = %v, want each of %v once", requested, want)
	}

	rows := rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)
	if len(rows) != 26 {
		t.Errorf("stored %d rows, want 26", len(rows))
	}
	r, ok := rows[ecrScanCfgNativeID("team/app")]
	if !ok {
		t.Fatalf("missing row %s; have %d rows", ecrScanCfgNativeID("team/app"), len(rows))
	}
	if r.Type != TypeECRRepositoryScanningConfig || sv(r.Name) != "team/app" || sv(r.Region) != testRegion || r.ManagedByProvider {
		t.Errorf("row = type %q name %q region %q managed %v; want %q team/app %q false", r.Type, sv(r.Name), sv(r.Region), r.ManagedByProvider, TypeECRRepositoryScanningConfig, testRegion)
	}
	var attrs struct{ ScanFrequency string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.ScanFrequency != "CONTINUOUS_SCAN" {
		t.Errorf("attrs ScanFrequency = %q (err %v), want CONTINUOUS_SCAN", attrs.ScanFrequency, err)
	}
	assertContainsNative(t, st, ecrRepoARN("team/app"), ecrScanCfgNativeID("team/app"))
	assertContainsNative(t, st, ecrRepoARN("repo-00"), ecrScanCfgNativeID("repo-00"))
}

func TestScanECRRepositoryScanningConfigs_NoRepositories(t *testing.T) {
	st := newTestStore(t)
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, nil, calls.handler(ecrScanCfgOK))
	if err != nil || total != 0 || len(calls.batches) != 0 {
		t.Errorf("total = %d, err = %v, calls = %d; want 0, nil, 0", total, err, len(calls.batches))
	}
}

// A denial of every batch warns once for the op and fails nothing.
func TestScanECRRepositoryScanningConfigs_DeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := ecrSeedRepos(t, st, ecrRepoNames(30))
	var calls ecrScanCfgBatches

	total, _, err := runECRScanCfg(st, repos, calls.handler(func([]string) (any, error) { return nil, ecrPubDenied }))
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
	if len(calls.batches) != 2 {
		t.Errorf("calls = %d, want 2", len(calls.batches))
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// A failure entry drops only its repository: REPOSITORY_NOT_FOUND silently,
// a code the SDK does not model with a warning.
func TestScanECRRepositoryScanningConfigs_Failures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      ecrtypes.ScanningConfigurationFailureCode
		wantWarns int
	}{
		{"repository not found", ecrtypes.ScanningConfigurationFailureCodeRepositoryNotFound, 0},
		{"unmodeled code", "SOME_NEW_CODE", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ecrScanCfgOp)
			repos := ecrSeedRepos(t, st, []string{"kept", "gone"})
			var calls ecrScanCfgBatches
			total, _, err := runECRScanCfg(st, repos, calls.handler(func([]string) (any, error) {
				return &ecr.BatchGetRepositoryScanningConfigurationOutput{
					ScanningConfigurations: []ecrtypes.RepositoryScanningConfiguration{ecrScanCfg("kept")},
					Failures: []ecrtypes.RepositoryScanningConfigurationFailure{{
						RepositoryName: ptrStr("gone"), FailureCode: tc.code, FailureReason: ptrStr("reason"),
					}},
				}, nil
			}))
			if err != nil || total != 1 {
				t.Errorf("total = %d, err = %v; want 1, nil", total, err)
			}
			rows := rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)
			if _, ok := rows[ecrScanCfgNativeID("kept")]; !ok || len(rows) != 1 {
				t.Errorf("rows = %v, want only %s", slices.Collect(maps.Keys(rows)), ecrScanCfgNativeID("kept"))
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

var ecrRepoNotFound = &smithy.GenericAPIError{Code: "RepositoryNotFoundException", Message: "gone"}

// A batch failing with RepositoryNotFoundException is asked again name by
// name: the repository still not found is dropped silently, a single-name
// failure entry with an unrecognised code warns, and the rest of the batch
// and the other batch are stored.
func TestScanECRRepositoryScanningConfigs_BatchNotFoundRetriesSingly(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := ecrSeedRepos(t, st, ecrRepoNames(26))
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(func(names []string) (any, error) {
		switch {
		case len(names) == ecrScanningConfigBatchMax || names[0] == "repo-07":
			return nil, ecrRepoNotFound
		case names[0] == "repo-11":
			return &ecr.BatchGetRepositoryScanningConfigurationOutput{
				Failures: []ecrtypes.RepositoryScanningConfigurationFailure{{RepositoryName: ptrStr("repo-11"), FailureCode: "SOME_NEW_CODE"}},
			}, nil
		}
		return ecrScanCfgOK(names)
	}))
	if err != nil || total != 24 {
		t.Errorf("total = %d, err = %v; want 24, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)
	for _, name := range []string{"repo-07", "repo-11"} {
		if _, ok := rows[ecrScanCfgNativeID(name)]; ok {
			t.Errorf("stored row for dropped %s", name)
		}
	}
	for _, name := range []string{"repo-00", "repo-24", "repo-25"} {
		if _, ok := rows[ecrScanCfgNativeID(name)]; !ok {
			t.Errorf("missing row %s", ecrScanCfgNativeID(name))
		}
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 (repo-11's failure code)", *warns)
	}
}

// A single-name batch failing with RepositoryNotFoundException is dropped
// silently without being sent again.
func TestScanECRRepositoryScanningConfigs_SingleNameBatchNotFound(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := ecrSeedRepos(t, st, []string{"a"})
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(func([]string) (any, error) { return nil, ecrRepoNotFound }))
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
	if len(calls.batches) != 1 || *warns != 0 {
		t.Errorf("calls = %d, warnings = %d; want 1, 0", len(calls.batches), *warns)
	}
}

// After a batch's RepositoryNotFoundException, a single-name call failing
// otherwise fails the phase.
func TestScanECRRepositoryScanningConfigs_SingleRetryErrorFails(t *testing.T) {
	st := newTestStore(t)
	repos := ecrSeedRepos(t, st, []string{"a", "b"})
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(func(names []string) (any, error) {
		switch {
		case len(names) > 1:
			return nil, ecrRepoNotFound
		case names[0] == "a":
			return ecrScanCfgOK(names)
		}
		return nil, ecrPubServerErr
	}))
	assertAPICode(t, err, "ServerException")
	// The single-name call read before the failing one is kept.
	if _, ok := rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)[ecrScanCfgNativeID("a")]; !ok || total != 1 {
		t.Errorf("a stored %v, total %d; want true, 1", ok, total)
	}
}

// When ctx ends during the fan-out, the batches it cut short and those never
// read add one stop member between them.
func TestScanECRRepositoryScanningConfigs_ContextEndedMidFanOut(t *testing.T) {
	for _, batches := range []int{3, fanoutMed + 2} {
		t.Run(strconv.Itoa(batches)+" batches", func(t *testing.T) {
			st := newTestStore(t)
			repos := ecrSeedRepos(t, st, ecrRepoNames(batches*ecrScanningConfigBatchMax))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			running := min(batches, fanoutMed)
			var started atomic.Int32
			// The last batch to start of those that fit cancels ctx, so every
			// one of them is in flight when it ends.
			client := ecr.NewFromConfig(opStubConfigCtx(map[string]func(context.Context, any) (any, error){
				"BatchGetRepositoryScanningConfiguration": func(ctx context.Context, _ any) (any, error) {
					if started.Add(1) == int32(running) {
						cancel()
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}, ecrPubDenied))
			_, _, err := scanECRRepositoryScanningConfigs(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, repos)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v; want context.Canceled", err)
			}
			if j, ok := err.(interface{ Unwrap() []error }); !ok || len(j.Unwrap()) != 1 {
				t.Errorf("err = %v; want exactly one stop member", err)
			}
		})
	}
}

// Once ctx has ended no batch is read, and the stop is returned.
func TestScanECRRepositoryScanningConfigs_ContextEnded(t *testing.T) {
	st := newTestStore(t)
	repos := ecrSeedRepos(t, st, []string{"a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := ecr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"BatchGetRepositoryScanningConfiguration": func(in any) (any, error) {
			return ecrScanCfgOK(in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames)
		},
	}, ecrPubDenied))
	_, _, err := scanECRRepositoryScanningConfigs(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, repos)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}

// After a batch's RepositoryNotFoundException, denials of the single-name
// calls warn once and fail nothing.
func TestScanECRRepositoryScanningConfigs_SingleRetryDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := ecrSeedRepos(t, st, []string{"a", "b"})
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(func(names []string) (any, error) {
		if len(names) > 1 {
			return nil, ecrRepoNotFound
		}
		return nil, ecrPubDenied
	}))
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// A repository whose stored ARN does not parse gives its scanning
// configuration no NativeID: it is dropped with a warning, its sibling stored.
func TestScanECRRepositoryScanningConfigs_UnusableParentARNSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := append(ecrSeedRepos(t, st, []string{"a"}), childParent{id: "garbled", arn: "not-an-arn"})
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(ecrScanCfgOK))
	if err != nil || total != 1 {
		t.Errorf("total = %d, err = %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)[ecrScanCfgNativeID("a")]; !ok {
		t.Errorf("missing row %s", ecrScanCfgNativeID("a"))
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

func TestECRRepoScanningConfigNativeID(t *testing.T) {
	for _, tc := range []struct{ repoARN, name, want string }{
		{"arn:aws:ecr:us-east-1:123456789012:repository/team/app", "team/app", "arn:aws:ecr:us-east-1:123456789012:repository-scanning-configuration/team/app"},
		{"arn:aws-cn:ecr:cn-north-1:123456789012:repository/a", "a", "arn:aws-cn:ecr:cn-north-1:123456789012:repository-scanning-configuration/a"},
	} {
		got, err := ecrRepoScanningConfigNativeID(tc.repoARN, tc.name)
		if err != nil || got != tc.want {
			t.Errorf("ecrRepoScanningConfigNativeID(%q, %q) = %q, %v; want %q", tc.repoARN, tc.name, got, err, tc.want)
		}
	}
	if _, err := ecrRepoScanningConfigNativeID("not-an-arn", "a"); err == nil {
		t.Error("ecrRepoScanningConfigNativeID(not-an-arn) err = nil, want error")
	}
}

func TestScanECRRepositoryScanningConfigs_OtherErrorFails(t *testing.T) {
	for _, code := range []string{"ServerException", "ValidationException"} {
		t.Run(code, func(t *testing.T) {
			st := newTestStore(t)
			repos := ecrSeedRepos(t, st, []string{"a"})
			var calls ecrScanCfgBatches
			total, _, err := runECRScanCfg(st, repos, calls.handler(func([]string) (any, error) {
				return nil, &smithy.GenericAPIError{Code: code, Message: "boom"}
			}))
			assertAPICode(t, err, code)
			if total != 0 || len(rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)) != 0 {
				t.Errorf("total = %d, want 0 and no rows", total)
			}
		})
	}
}

// An element without repositoryName has no NativeID: it is dropped with a
// warning and its siblings are stored.
func TestScanECRRepositoryScanningConfigs_UnnamedElementSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ecrScanCfgOp)
	repos := ecrSeedRepos(t, st, []string{"a", "b"})
	var calls ecrScanCfgBatches
	total, _, err := runECRScanCfg(st, repos, calls.handler(func([]string) (any, error) {
		unnamed := ecrScanCfg("b")
		unnamed.RepositoryName = nil
		return &ecr.BatchGetRepositoryScanningConfigurationOutput{
			ScanningConfigurations: []ecrtypes.RepositoryScanningConfiguration{ecrScanCfg("a"), unnamed},
		}, nil
	}))
	if err != nil || total != 1 {
		t.Errorf("total = %d, err = %v; want 1, nil", total, err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// ecrPrivateRepoPages serves the private DescribeRepositories in two pages
// (repositories a, then team/app) and the public one empty.
func ecrPrivateRepoPages(in any) (any, error) {
	priv, ok := in.(*ecr.DescribeRepositoriesInput)
	if !ok {
		return &ecrpublic.DescribeRepositoriesOutput{}, nil
	}
	if priv.NextToken == nil {
		return &ecr.DescribeRepositoriesOutput{
			Repositories: []ecrtypes.Repository{{RepositoryName: ptrStr("a"), RepositoryArn: ptrStr(ecrRepoARN("a"))}},
			NextToken:    ptrStr("p2"),
		}, nil
	}
	return &ecr.DescribeRepositoriesOutput{
		Repositories: []ecrtypes.Repository{{RepositoryName: ptrStr("team/app"), RepositoryArn: ptrStr(ecrRepoARN("team/app"))}},
	}, nil
}

// scanECR hands every repository it listed, across pages, to the scanning
// configuration phase.
func TestScanECR_StoresRepositoryScanningConfigs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": ecrPrivateRepoPages,
		"BatchGetRepositoryScanningConfiguration": func(in any) (any, error) {
			return ecrScanCfgOK(in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames)
		},
	}, ecrPubDenied)

	if _, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, name := range []string{"a", "team/app"} {
		assertContainsNative(t, st, ecrRepoARN(name), ecrScanCfgNativeID(name))
	}
}

// The scanning configuration phase runs after every existing ECR phase: its
// hard error reaches the caller with the earlier rows stored and counted.
func TestScanECR_ScanningConfigErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	called := false
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(in any) (any, error) {
			if _, ok := in.(*ecrpublic.DescribeRepositoriesInput); ok {
				return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{ecrPubRepo(ecrPubRepoA, "a", testAccountID)}}, nil
			}
			return ecrPrivateRepoPages(in)
		},
		"DescribePullThroughCacheRules": func(any) (any, error) {
			return &ecr.DescribePullThroughCacheRulesOutput{PullThroughCacheRules: []ecrtypes.PullThroughCacheRule{{EcrRepositoryPrefix: ptrStr("hub")}}}, nil
		},
		"DescribeRegistries": func(any) (any, error) { return &ecrpublic.DescribeRegistriesOutput{}, nil },
		"BatchGetRepositoryScanningConfiguration": func(any) (any, error) {
			called = true
			return nil, ecrPubServerErr
		},
	}, ecrPubDenied)

	total, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
	if !called {
		t.Fatal("BatchGetRepositoryScanningConfiguration never called")
	}
	assertAPICode(t, err, "ServerException")
	if total != 4 {
		t.Errorf("total = %d, want 4 (two repositories, pull-through-cache rule, public repository)", total)
	}
	for typ, want := range map[string]string{
		TypeECRPullThroughCacheRule: "arn:aws:ecr:us-east-1:123456789012:pull-through-cache-rule/hub",
		TypeECRPublicRepository:     ecrPubRepoA,
	} {
		if _, ok := rowsByNativeID(t, st, typ)[want]; !ok {
			t.Errorf("missing %s row %s", typ, want)
		}
	}
}

// A failed registry write records no contains pairs: the registry rows they
// would name were not stored. The write fails on the scan id, which no scan
// row holds, while the store stays open.
func TestScanECRPublicRegistries_FailedWriteRecordsNoPairs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeECRPublicRepository, ecrPubRepoA, "us-east-1", "{}")
	warns := countWarnings(st)
	client := ecrPubClient(map[string]func(in any) (any, error){"DescribeRegistries": ecrPubRegistryPages(nil)})

	_, _, err := scanECRPublicRegistries(context.Background(), client, newTestAccount(testAccountID), "us-east-1", st, "no-such-scan",
		map[string][]string{testAccountID: {ecrPubRepoA}})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := ecrPubContainsCount(t, st); n != 0 {
		t.Errorf("contains edges = %d after a failed write; want 0", n)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d after a failed write; want 0 (no pair was offered to the closure)", *warns)
	}
}

// One failed batch does not drop the others: their rows, and those its
// per-name retry read before the error, are stored and the error returned.
// The warnings wait for the write, and a failed write sends none.
func TestScanECRRepositoryScanningConfigs_FailedBatchKeepsOthers(t *testing.T) {
	names := ecrRepoNames(ecrScanningConfigBatchMax + 1)
	failing := names[ecrScanningConfigBatchMax]
	handler := func(in any) (any, error) {
		batch := in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames
		if slices.Contains(batch, failing) {
			return nil, ecrPubServerErr
		}
		out := &ecr.BatchGetRepositoryScanningConfigurationOutput{
			Failures: []ecrtypes.RepositoryScanningConfigurationFailure{{RepositoryName: ptrStr(batch[0]), FailureCode: "SOME_NEW_CODE"}},
		}
		for _, n := range batch[1:] {
			out.ScanningConfigurations = append(out.ScanningConfigurations, ecrScanCfg(n))
		}
		return out, nil
	}
	t.Run("write succeeds", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, ecrScanCfgOp)
		repos := ecrSeedRepos(t, st, names)
		total, _, err := runECRScanCfg(st, repos, handler)
		assertAPICode(t, err, "ServerException")
		if want := ecrScanningConfigBatchMax - 1; total != want || len(rowsByNativeID(t, st, TypeECRRepositoryScanningConfig)) != want {
			t.Errorf("total = %d; want %d (the good batch less its failure entry)", total, want)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d; want 1 (the unrecognised failure code)", *warns)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countWarnings(st)
		repos := ecrSeedRepos(t, st, names)
		closeStore()
		_, _, err := runECRScanCfg(st, repos, handler)
		if !errors.Is(err, store.ErrStoreWrite) || !errors.Is(err, ecrPubServerErr) {
			t.Errorf("err = %v; want store.ErrStoreWrite with the batch error", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d after a failed write; want 0", *warns)
		}
	})
}
