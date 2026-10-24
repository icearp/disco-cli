package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	ecrpublictypes "github.com/aws/aws-sdk-go-v2/service/ecrpublic/types"
	"github.com/icearp/disco-cli/store"
)

// ecrTagCalls records the ResourceArn of every ListTagsForResource call and
// answers each from tags (nil for none) or errs; calls run concurrently.
type ecrTagCalls struct {
	mu   sync.Mutex
	arns []string
	tags map[string][]ecrtypes.Tag
	errs map[string]error
}

func (c *ecrTagCalls) handler(in any) (any, error) {
	arn := *in.(*ecr.ListTagsForResourceInput).ResourceArn
	c.mu.Lock()
	c.arns = append(c.arns, arn)
	c.mu.Unlock()
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &ecr.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

func ecrRepo(name string) ecrtypes.Repository {
	return ecrtypes.Repository{RepositoryName: ptrStr(name), RepositoryArn: ptrStr(ecrRepoARN(name))}
}

// runECRRepos runs the repository phase over repos, one page, with tags
// answering the tag reads, and returns its parents and joined result.
func runECRRepos(t *testing.T, st *store.Store, repos []ecrtypes.Repository, tags *ecrTagCalls) ([]childParent, phaseTotals, error) {
	t.Helper()
	client := ecr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(in any) (any, error) {
			if got := in.(*ecr.DescribeRepositoriesInput).MaxResults; got == nil || *got != ecrPageLimit {
				t.Errorf("DescribeRepositories MaxResults = %v; want %d", got, ecrPageLimit)
			}
			return &ecr.DescribeRepositoriesOutput{Repositories: repos}, nil
		},
		"ListTagsForResource": tags.handler,
	}, ecrPubDenied))
	var p phaseTotals
	acct := newTestAccount(testAccountID)
	parents, stop := scanECRRepositories(context.Background(), client, acct, testRegion, st, testScanID, &p)
	if stop {
		t.Fatal("stop = true; want false (the write succeeded)")
	}
	_, _, err := p.result(st, acct, testRegion)
	return parents, p, err
}

// Each repository is stored with the tags ListTagsForResource returns for its
// own ARN, or none.
func TestScanECRRepositories_StoresTags(t *testing.T) {
	st := newTestStore(t)
	tags := &ecrTagCalls{tags: map[string][]ecrtypes.Tag{
		ecrRepoARN("tagged"): {{Key: ptrStr("env"), Value: ptrStr("prod")}},
	}}
	parents, p, err := runECRRepos(t, st, []ecrtypes.Repository{ecrRepo("tagged"), ecrRepo("bare")}, tags)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p.total != 2 {
		t.Errorf("total = %d; want 2", p.total)
	}
	if want := []string{ecrRepoARN("bare"), ecrRepoARN("tagged")}; !slices.Equal(slices.Sorted(slices.Values(tags.arns)), want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", tags.arns, want)
	}
	rows := rowsByNativeID(t, st, TypeECRRepository)
	assertTagsMap(t, rows[ecrRepoARN("tagged")].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[ecrRepoARN("bare")].TagsJSON; got != nil {
		t.Errorf("bare TagsJSON = %s; want nil", *got)
	}
	if got := sv(rows[ecrRepoARN("tagged")].Name); got != "tagged" {
		t.Errorf("Name = %q; want tagged", got)
	}
	want := []childParent{{id: "tagged", arn: ecrRepoARN("tagged")}, {id: "bare", arn: ecrRepoARN("bare")}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
}

// A failed tag read keeps the repository without tags, except one ECR no
// longer finds, which is not stored and not handed on; a denial warns once,
// a transient error and any other error are returned.
func TestScanECRRepositories_TagReadFailures(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		kept     bool
		warns    int
		wantCode string
	}{
		{name: "not found", err: apiErr("RepositoryNotFoundException", "gone")},
		{name: "denied", err: ecrPubDenied, kept: true, warns: 1},
		{name: "transient", err: apiErr("ThrottlingException", "Rate exceeded"), kept: true, wantCode: "ThrottlingException"},
		{name: "other error", err: apiErr("InvalidParameterException", "bad"), kept: true, wantCode: "InvalidParameterException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			tags := &ecrTagCalls{errs: map[string]error{ecrRepoARN("x"): c.err}}
			parents, _, err := runECRRepos(t, st, []ecrtypes.Repository{ecrRepo("x"), ecrRepo("ok")}, tags)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeECRRepository)
			r, ok := rows[ecrRepoARN("x")]
			if ok != c.kept {
				t.Fatalf("x stored = %v; want %v", ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("x TagsJSON = %s; want nil", *r.TagsJSON)
			}
			if _, ok := rows[ecrRepoARN("ok")]; !ok {
				t.Error("sibling ok not stored")
			}
			wantParents := 1
			if c.kept {
				wantParents = 2
			}
			if len(parents) != wantParents {
				t.Errorf("parents = %v; want %d", parents, wantParents)
			}
			var got []store.ScanWarning
			for _, w := range *warns {
				if w.Service == ecrListTagsOp {
					got = append(got, w)
				}
			}
			if len(got) != c.warns {
				t.Fatalf("%s warnings = %v; want %d", ecrListTagsOp, got, c.warns)
			}
			if c.warns > 0 && got[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// A repository without an ARN is warned about and not stored, rather than
// stored under an empty NativeID.
func TestScanECRRepositories_EmptyARNNotStored(t *testing.T) {
	st := newTestStore(t)
	warnText := recordWarnings(st)
	warns := countOpWarnings(st, ecrRepositoriesOp)
	parents, _, err := runECRRepos(t, st, []ecrtypes.Repository{{}, ecrRepo("ok")}, &ecrTagCalls{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	rows := rowsByNativeID(t, st, TypeECRRepository)
	if _, ok := rows[""]; ok || len(rows) != 1 {
		t.Errorf("stored %v; want only %s", slices.Sorted(maps.Keys(rows)), ecrRepoARN("ok"))
	}
	if len(parents) != 1 || *warns != 1 {
		t.Fatalf("parents %v, %s warnings %d; want 1 parent, 1 warning", parents, ecrRepositoriesOp, *warns)
	}
	// The warning names the row even when it has no name to give.
	if msg := (*warnText)[0].Message; !strings.HasSuffix(msg, "first (a repository with no RepositoryName)") {
		t.Errorf("warning = %q; want it to name the nameless repository", msg)
	}
}

// A failed DescribeRepositories page keeps the repositories read before it,
// counted, and returns its error.
func TestScanECRRepositories_FailedPageKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	client := ecr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": func(in any) (any, error) {
			if in.(*ecr.DescribeRepositoriesInput).NextToken == nil {
				return &ecr.DescribeRepositoriesOutput{Repositories: []ecrtypes.Repository{ecrRepo("a")}, NextToken: ptrStr("p2")}, nil
			}
			return nil, apiErr("InvalidParameterException", "bad token")
		},
		"ListTagsForResource": (&ecrTagCalls{}).handler,
	}, ecrPubDenied))
	var p phaseTotals
	acct := newTestAccount(testAccountID)
	parents, _ := scanECRRepositories(context.Background(), client, acct, testRegion, st, testScanID, &p)
	_, _, err := p.result(st, acct, testRegion)
	assertAPICode(t, err, "InvalidParameterException")
	if _, ok := rowsByNativeID(t, st, TypeECRRepository)[ecrRepoARN("a")]; !ok || p.total != 1 || len(parents) != 1 {
		t.Errorf("a stored %v, total %d, parents %v; want true, 1, [a]", ok, p.total, parents)
	}
}

// A phase's hard error does not stop the phases after it: the scanning
// configurations are still read after the pull-through-cache rules fail.
func TestScanECR_PhaseErrorRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories":          ecrPrivateRepoPages,
		"ListTagsForResource":           (&ecrTagCalls{}).handler,
		"DescribePullThroughCacheRules": func(any) (any, error) { return nil, ecrPubServerErr },
		"BatchGetRepositoryScanningConfiguration": func(in any) (any, error) {
			return ecrScanCfgOK(in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames)
		},
	}, ecrPubDenied)

	_, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ServerException")
	for _, name := range []string{"a", "team/app"} {
		assertContainsNative(t, st, ecrRepoARN(name), ecrScanCfgNativeID(name))
	}
}

// A failed repository write stops the scan: no later phase is called.
func TestScanECR_FailedRepositoryWriteStops(t *testing.T) {
	st, closeStore := openClosingStore(t)
	acct := newTestAccount(testAccountID)
	called := false
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": ecrPrivateRepoPages,
		"ListTagsForResource":  (&ecrTagCalls{}).handler,
		"DescribePullThroughCacheRules": func(any) (any, error) {
			called = true
			return &ecr.DescribePullThroughCacheRulesOutput{}, nil
		},
	}, ecrPubDenied)
	closeStore()

	_, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if called {
		t.Error("DescribePullThroughCacheRules called after the repository write failed")
	}
}

// A tag read error in the repository phase does not stop the later phases:
// the pull-through-cache rules are still stored and the error is returned.
func TestScanECR_TagReadErrorRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tags := &ecrTagCalls{errs: map[string]error{ecrRepoARN("a"): apiErr("InvalidParameterException", "bad")}}
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"DescribeRepositories": ecrPrivateRepoPages,
		"ListTagsForResource":  tags.handler,
		"DescribePullThroughCacheRules": func(any) (any, error) {
			return &ecr.DescribePullThroughCacheRulesOutput{PullThroughCacheRules: []ecrtypes.PullThroughCacheRule{{EcrRepositoryPrefix: ptrStr("hub")}}}, nil
		},
	}, ecrPubDenied)

	total, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "InvalidParameterException")
	if total != 3 {
		t.Errorf("total = %d; want 3 (two repositories, pull-through-cache rule)", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeECRPullThroughCacheRule)["arn:aws:ecr:us-east-1:123456789012:pull-through-cache-rule/hub"]; !ok {
		t.Error("pull-through-cache rule not stored after the tag read error")
	}
}

// ecrPhaseCalls records which ECR ops a scan called; calls run concurrently.
type ecrPhaseCalls struct {
	mu    sync.Mutex
	calls map[string]bool
}

func (c *ecrPhaseCalls) record(op string, h func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		c.mu.Lock()
		if c.calls == nil {
			c.calls = map[string]bool{}
		}
		c.calls[op] = true
		c.mu.Unlock()
		return h(in)
	}
}

func (c *ecrPhaseCalls) called(op string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

// A write that fails in a later phase stops the scan: no phase after it is
// called. closeStore runs inside the failing phase's call, after the earlier
// phases stored their rows.
func TestScanECR_FailedLaterWriteStops(t *testing.T) {
	cases := []struct {
		name, failingOp string
		later           []string
	}{
		{"private phase", "DescribePullThroughCacheRules", []string{"GetRegistryPolicy", "DescribeRegistries", "BatchGetRepositoryScanningConfiguration"}},
		{"public repositories", "ecr-public:DescribeRepositories", []string{"DescribeRegistries", "BatchGetRepositoryScanningConfiguration"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			acct := newTestAccount(testAccountID)
			var calls ecrPhaseCalls
			ok := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
			acct.cfg = opStubConfig(map[string]func(in any) (any, error){
				"DescribeRepositories": func(in any) (any, error) {
					if _, public := in.(*ecrpublic.DescribeRepositoriesInput); public && c.failingOp == "ecr-public:DescribeRepositories" {
						closeStore()
						return &ecrpublic.DescribeRepositoriesOutput{Repositories: []ecrpublictypes.Repository{ecrPubRepo(ecrPubRepoA, "a", testAccountID)}}, nil
					}
					return ecrPrivateRepoPages(in)
				},
				"ListTagsForResource": (&ecrTagCalls{}).handler,
				"DescribePullThroughCacheRules": func(any) (any, error) {
					if c.failingOp == "DescribePullThroughCacheRules" {
						closeStore()
					}
					return &ecr.DescribePullThroughCacheRulesOutput{PullThroughCacheRules: []ecrtypes.PullThroughCacheRule{{EcrRepositoryPrefix: ptrStr("hub")}}}, nil
				},
				"GetRegistryPolicy":                       calls.record("GetRegistryPolicy", ok(&ecr.GetRegistryPolicyOutput{})),
				"DescribeRegistries":                      calls.record("DescribeRegistries", ok(&ecrpublic.DescribeRegistriesOutput{})),
				"BatchGetRepositoryScanningConfiguration": calls.record("BatchGetRepositoryScanningConfiguration", ok(&ecr.BatchGetRepositoryScanningConfigurationOutput{})),
			}, ecrPubDenied)

			_, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			for _, op := range c.later {
				if calls.called(op) {
					t.Errorf("%s called after the write failed", op)
				}
			}
		})
	}
}

// Each phase's errors are reported under the op that phase calls: a throttle
// from that op, beside another phase's hard error, is demoted to one warning
// under exactly that label.
func TestScanECR_PhaseLabelsNameTheirCalls(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, label := range []string{
		ecrRepositoriesOp, ecrListTagsOp,
		"ecr:DescribePullThroughCacheRules", "ecr:ListPullTimeUpdateExclusions", "ecr:GetRegistryPolicy",
		"ecr:GetRegistryScanningConfiguration", "ecr:DescribeRegistry", "ecr:DescribeRepositoryCreationTemplates",
		"ecr:GetSigningConfiguration", "ecr-public:DescribeRepositories", ecrPublicRegistriesOp,
		"ecr:BatchGetRepositoryScanningConfiguration",
	} {
		t.Run(label, func(t *testing.T) {
			svc, opName, _ := strings.Cut(label, ":")
			hardOp := "GetRegistryPolicy"
			if opName == hardOp {
				hardOp = "GetSigningConfiguration"
			}
			fails := func(in any) bool {
				_, public := in.(*ecrpublic.DescribeRepositoriesInput)
				return opName != "DescribeRepositories" || public == (svc == "ecr-public")
			}
			handlers := map[string]func(in any) (any, error){
				"DescribeRepositories": ecrPrivateRepoPages,
				"ListTagsForResource":  (&ecrTagCalls{}).handler,
				"BatchGetRepositoryScanningConfiguration": func(in any) (any, error) {
					return ecrScanCfgOK(in.(*ecr.BatchGetRepositoryScanningConfigurationInput).RepositoryNames)
				},
				hardOp: func(any) (any, error) { return nil, ecrPubServerErr },
			}
			ok := handlers[opName]
			handlers[opName] = func(in any) (any, error) {
				if fails(in) {
					return nil, throttle
				}
				return ok(in)
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, label)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, ecrPubDenied)

			_, _, err := scanECR(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "ServerException")
			if *warns != 1 {
				t.Errorf("%s warnings = %d; want 1 (the demoted throttle)", label, *warns)
			}
		})
	}
}

// Each private listing stores its item under the exact NativeID, Name and
// attributes, keeps the rows read before a failed page, warns once about an
// item without its identifier, and asks for the model's page cap.
func TestScanECRPrivateLists_FailedPageAndEmptyIDs(t *testing.T) {
	cases := []struct {
		op, typ                   string
		wantID, wantName, attrKey string
		page                      func(in any) (any, *int32)
		scan                      func(ecrExtAPI, *store.Store) (int, int, error)
	}{
		{
			"ecr:DescribePullThroughCacheRules", TypeECRPullThroughCacheRule,
			"arn:aws:ecr:us-east-1:123456789012:pull-through-cache-rule/hub", "hub", "EcrRepositoryPrefix",
			func(in any) (any, *int32) {
				return &ecr.DescribePullThroughCacheRulesOutput{PullThroughCacheRules: []ecrtypes.PullThroughCacheRule{{EcrRepositoryPrefix: ptrStr("hub")}, {}}, NextToken: ptrStr("p2")}, in.(*ecr.DescribePullThroughCacheRulesInput).MaxResults
			},
			func(c ecrExtAPI, st *store.Store) (int, int, error) {
				return scanECRPullThroughCacheRules(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
		},
		{
			"ecr:ListPullTimeUpdateExclusions", TypeECRPullTimeUpdateExclusion,
			ecrRepoARN("a") + "/pull-time-update-exclusion", ecrRepoARN("a"), "EcrRepositoryArn",
			func(in any) (any, *int32) {
				return &ecr.ListPullTimeUpdateExclusionsOutput{PullTimeUpdateExclusions: []string{ecrRepoARN("a"), ""}, NextToken: ptrStr("p2")}, in.(*ecr.ListPullTimeUpdateExclusionsInput).MaxResults
			},
			func(c ecrExtAPI, st *store.Store) (int, int, error) {
				return scanECRPullTimeUpdateExclusions(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
		},
		{
			"ecr:DescribeRepositoryCreationTemplates", TypeECRRepositoryCreationTemplate,
			"arn:aws:ecr:us-east-1:123456789012:repository-creation-template/team", "team", "Prefix",
			func(in any) (any, *int32) {
				return &ecr.DescribeRepositoryCreationTemplatesOutput{RepositoryCreationTemplates: []ecrtypes.RepositoryCreationTemplate{{Prefix: ptrStr("team")}, {}}, NextToken: ptrStr("p2")}, in.(*ecr.DescribeRepositoryCreationTemplatesInput).MaxResults
			},
			func(c ecrExtAPI, st *store.Store) (int, int, error) {
				return scanECRRepositoryCreationTemplates(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			_, opName, _ := strings.Cut(c.op, ":")
			st := newTestStore(t)
			warns := countOpWarnings(st, c.op)
			calls := 0
			client := ecr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
				opName: func(in any) (any, error) {
					calls++
					if calls > 1 {
						return nil, apiErr("InvalidParameterException", "bad token")
					}
					out, limit := c.page(in)
					if limit == nil || *limit != ecrPageLimit {
						t.Errorf("MaxResults = %v; want %d", limit, ecrPageLimit)
					}
					return out, nil
				},
			}, ecrPubDenied))
			total, _, err := c.scan(client, st)
			assertAPICode(t, err, "InvalidParameterException")
			rows := rowsByNativeID(t, st, c.typ)
			if total != 1 || len(rows) != 1 {
				t.Errorf("total %d, rows %d; want 1, 1 (the first page's item with an id)", total, len(rows))
			}
			r, ok := rows[c.wantID]
			if !ok {
				t.Fatalf("no row %s; have %v", c.wantID, slices.Collect(maps.Keys(rows)))
			}
			if sv(r.Name) != c.wantName {
				t.Errorf("Name = %q; want %q", sv(r.Name), c.wantName)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[c.attrKey] != c.wantName {
				t.Errorf("attrs[%s] = %v (err %v); want %q", c.attrKey, attrs[c.attrKey], err, c.wantName)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d; want 1 (the item without an id)", c.op, *warns)
			}
		})
	}
}
