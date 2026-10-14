package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
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

	total, _, err := scanECRPublic(context.Background(), acct, "us-east-1", st, testScanID)
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
	if !r.ManagedByProvider {
		t.Error("registry row not ManagedByProvider: AWS creates it implicitly and it cannot be deleted")
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
	if total, _, err := scanECRPublic(context.Background(), acct, "eu-west-1", st, testScanID); err != nil || total != 0 || len(called) != 0 {
		t.Fatalf("eu-west-1: total %d, err %v, calls %v; want 0, nil, none", total, err, called)
	}
	if _, _, err := scanECRPublic(context.Background(), acct, "us-east-1", st, testScanID); err != nil {
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

	total, _, err := scanECRPublic(context.Background(), acct, "us-east-1", st, testScanID)
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

func TestScanECRPublic_RepositoryErrorSkipsRegistries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	called := false
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(any) (any, error) { return nil, ecrPubServerErr },
		"DescribeRegistries": func(any) (any, error) {
			called = true
			return &ecrpublic.DescribeRegistriesOutput{}, nil
		},
	}, ecrPubDenied)

	_, _, err := scanECRPublic(context.Background(), acct, "us-east-1", st, testScanID)
	assertAPICode(t, err, "ServerException")
	if called {
		t.Error("DescribeRegistries called after the repository phase failed")
	}
}

// The registry phase runs after every existing ECR phase: its hard error
// reaches the caller with the earlier private and public rows stored and
// counted.
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
