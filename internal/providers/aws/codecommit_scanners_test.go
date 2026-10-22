package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/codecommit"
	codecommittypes "github.com/aws/aws-sdk-go-v2/service/codecommit/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	ccDenied       = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: codecommit:List"}
	ccTemplateGone = &smithy.GenericAPIError{Code: "ApprovalRuleTemplateDoesNotExistException", Message: "gone"}
	ccRepoGone     = &smithy.GenericAPIError{Code: "RepositoryDoesNotExistException", Message: "gone"}
	ccBadInput     = &smithy.GenericAPIError{Code: "InvalidContinuationTokenException", Message: "bad"}
	ccThrottled    = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	ccCreated      = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func ccClient(handlers map[string]func(in any) (any, error)) *codecommit.Client {
	return codecommit.NewFromConfig(opStubConfig(handlers, ccDenied))
}

func ccRepoARN(name string) string { return codeCommitRepoARN(testRegion, testAccountID, name) }

func ccTemplateARN(id string) string {
	return codeCommitTemplateNativeID(testRegion, testAccountID, id)
}

func ccTemplate(name, id string) *codecommittypes.ApprovalRuleTemplate {
	return &codecommittypes.ApprovalRuleTemplate{
		ApprovalRuleTemplateName: ptrStr(name), ApprovalRuleTemplateId: ptrStr(id),
		ApprovalRuleTemplateContent: ptrStr(`{"Version":"2018-11-08","Statements":[{"Type":"Approvers","NumberOfApprovalsNeeded":2}]}`),
		CreationDate:                &ccCreated,
	}
}

// ccTemplateHandlers lists templates t-1 and t-2 over two pages; GetApprovalRuleTemplate
// answers each with id id-<name>, and t-1 is associated with repo-b then repo-a over two
// pages. Ops in override answer instead.
func ccTemplateHandlers(calls *parentCalls, override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListApprovalRuleTemplates": func(in any) (any, error) {
			if in.(*codecommit.ListApprovalRuleTemplatesInput).NextToken == nil {
				return &codecommit.ListApprovalRuleTemplatesOutput{ApprovalRuleTemplateNames: []string{"t-1"}, NextToken: ptrStr("p2")}, nil
			}
			return &codecommit.ListApprovalRuleTemplatesOutput{ApprovalRuleTemplateNames: []string{"t-2"}}, nil
		},
		"GetApprovalRuleTemplate": func(in any) (any, error) {
			name := *in.(*codecommit.GetApprovalRuleTemplateInput).ApprovalRuleTemplateName
			calls.add(name)
			return &codecommit.GetApprovalRuleTemplateOutput{ApprovalRuleTemplate: ccTemplate(name, "id-"+name)}, nil
		},
		"ListRepositoriesForApprovalRuleTemplate": func(in any) (any, error) {
			req := in.(*codecommit.ListRepositoriesForApprovalRuleTemplateInput)
			switch {
			case *req.ApprovalRuleTemplateName != "t-1":
				return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{}, nil
			case req.NextToken == nil:
				return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{RepositoryNames: []string{"repo-b"}, NextToken: ptrStr("p2")}, nil
			default:
				return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{RepositoryNames: []string{"repo-a"}}, nil
			}
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

// ccTemplateRepoNames decodes the stored association list; ok is false when it is null.
func ccTemplateRepoNames(t *testing.T, r store.Resource) (names []string, ok bool) {
	t.Helper()
	var attrs struct{ AssociatedRepositoryNames *[]string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.AssociatedRepositoryNames == nil {
		return nil, false
	}
	return *attrs.AssociatedRepositoryNames, true
}

func TestScanCCApprovalRuleTemplates_StoresEachTemplate(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	total, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(ccTemplateHandlers(calls, nil)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if got, want := calls.distinct(), []string{"t-1", "t-2"}; !slices.Equal(got, want) {
		t.Errorf("templates fetched %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate)
	if len(rows) != 2 {
		t.Fatalf("template rows = %d, want 2", len(rows))
	}
	got, ok := rows[ccTemplateARN("id-t-1")]
	if !ok {
		t.Fatalf("template %s missing; rows %v", ccTemplateARN("id-t-1"), slices.Collect(maps.Keys(rows)))
	}
	if got.Type != TypeCodeCommitApprovalRuleTemplate || sv(got.Name) != "t-1" || sv(got.Region) != testRegion || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q name %q region %q created %q", got.Type, sv(got.Name), sv(got.Region), sv(got.CreatedAt))
	}
	var attrs struct{ ApprovalRuleTemplateContent string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.ApprovalRuleTemplateContent == "" {
		t.Errorf("attrs content = %q, %v; want the template content", attrs.ApprovalRuleTemplateContent, err)
	}
	if names, ok := ccTemplateRepoNames(t, got); !ok || !slices.Equal(names, []string{"repo-a", "repo-b"}) {
		t.Errorf("t-1 repositories = %v (present %v), want [repo-a repo-b]", names, ok)
	}
	if names, ok := ccTemplateRepoNames(t, rows[ccTemplateARN("id-t-2")]); !ok || len(names) != 0 {
		t.Errorf("t-2 repositories = %v (present %v), want present and empty", names, ok)
	}
}

func TestScanCCApprovalRuleTemplates_SkipsGoneAndIDLessTemplates(t *testing.T) {
	for name, override := range map[string]map[string]func(any) (any, error){
		"get not found": {"GetApprovalRuleTemplate": func(in any) (any, error) {
			if n := *in.(*codecommit.GetApprovalRuleTemplateInput).ApprovalRuleTemplateName; n == "t-2" {
				return nil, ccTemplateGone
			}
			return &codecommit.GetApprovalRuleTemplateOutput{ApprovalRuleTemplate: ccTemplate("t-1", "id-t-1")}, nil
		}},
		"associations not found": {"ListRepositoriesForApprovalRuleTemplate": func(in any) (any, error) {
			if n := *in.(*codecommit.ListRepositoriesForApprovalRuleTemplateInput).ApprovalRuleTemplateName; n == "t-2" {
				return nil, ccTemplateGone
			}
			return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{}, nil
		}},
		"empty id": {"GetApprovalRuleTemplate": func(in any) (any, error) {
			n := *in.(*codecommit.GetApprovalRuleTemplateInput).ApprovalRuleTemplateName
			if n == "t-2" {
				return &codecommit.GetApprovalRuleTemplateOutput{ApprovalRuleTemplate: ccTemplate("t-2", "")}, nil
			}
			return &codecommit.GetApprovalRuleTemplateOutput{ApprovalRuleTemplate: ccTemplate(n, "id-"+n)}, nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(ccTemplateHandlers(&parentCalls{}, override)), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 1 {
				t.Errorf("total, err = %d, %v; want 1, nil", total, err)
			}
			if _, ok := rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate)[ccTemplateARN("id-t-1")]; !ok {
				t.Errorf("t-1 missing")
			}
		})
	}
}

func TestScanCCApprovalRuleTemplates_Empty(t *testing.T) {
	st := newTestStore(t)
	handlers := map[string]func(any) (any, error){
		"ListApprovalRuleTemplates": func(any) (any, error) { return &codecommit.ListApprovalRuleTemplatesOutput{}, nil },
	}
	total, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
}

// A denial on any template op warns once for that op; a denied association
// listing keeps the template with a null association list.
func TestScanCCApprovalRuleTemplates_AccessDenied(t *testing.T) {
	for _, c := range []struct {
		op        string
		wantTotal int
	}{
		{"ListApprovalRuleTemplates", 0},
		{"GetApprovalRuleTemplate", 0},
		{"ListRepositoriesForApprovalRuleTemplate", 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "codecommit:"+c.op)
			handlers := ccTemplateHandlers(&parentCalls{}, nil)
			delete(handlers, c.op)
			total, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != c.wantTotal {
				t.Errorf("total, err = %d, %v; want %d, nil", total, err, c.wantTotal)
			}
			if *warned != 1 {
				t.Errorf("warnings = %d, want 1", *warned)
			}
			for native, r := range rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate) {
				if names, ok := ccTemplateRepoNames(t, r); ok {
					t.Errorf("%s repositories = %v, want null", native, names)
				}
			}
		})
	}
}

func TestScanCCApprovalRuleTemplates_OtherErrorPropagates(t *testing.T) {
	for _, op := range []string{"ListApprovalRuleTemplates", "GetApprovalRuleTemplate", "ListRepositoriesForApprovalRuleTemplate"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := ccTemplateHandlers(&parentCalls{}, map[string]func(any) (any, error){op: func(any) (any, error) { return nil, ccBadInput }})
			_, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, ccBadInput) {
				t.Errorf("err = %v, want the %s error", err, op)
			}
		})
	}
}

// ccSeedRepos stores the repository rows the triggers are linked under.
func ccSeedRepos(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	repos := make([]childParent, len(names))
	for i, n := range names {
		repos[i] = childParent{id: n, arn: ccRepoARN(n)}
		upsertTestResource(t, st, "aws", testAccountID, TypeCodeCommitRepository, repos[i].arn, testRegion, "{}")
	}
	return repos
}

func ccTrigger(name string) codecommittypes.RepositoryTrigger {
	return codecommittypes.RepositoryTrigger{
		Name: ptrStr(name), DestinationArn: ptrStr("arn:aws:sns:us-east-1:" + testAccountID + ":topic-" + name),
		Events: []codecommittypes.RepositoryTriggerEventEnum{codecommittypes.RepositoryTriggerEventEnumAll}, CustomData: ptrStr("note " + name),
	}
}

func TestScanCCRepositoryTriggers_StoresEachRepositorysTriggers(t *testing.T) {
	st := newTestStore(t)
	repos := ccSeedRepos(t, st, "r-a", "r-b", "r-c")
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){
		"GetRepositoryTriggers": func(in any) (any, error) {
			repo := *in.(*codecommit.GetRepositoryTriggersInput).RepositoryName
			calls.add(repo)
			switch repo {
			case "r-a":
				return &codecommit.GetRepositoryTriggersOutput{ConfigurationId: ptrStr("cfg"), Triggers: []codecommittypes.RepositoryTrigger{ccTrigger("tr-1"), ccTrigger("tr-2"), {}}}, nil
			case "r-b":
				return nil, ccRepoGone
			default:
				return &codecommit.GetRepositoryTriggersOutput{}, nil
			}
		},
	}
	total, _, err := scanCCRepositoryTriggers(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, repos)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if got, want := calls.distinct(), []string{"r-a", "r-b", "r-c"}; !slices.Equal(got, want) {
		t.Errorf("repositories queried %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeCodeCommitRepositoryTrigger)
	if len(rows) != 2 {
		t.Errorf("trigger rows = %d, want 2", len(rows))
	}
	for _, name := range []string{"tr-1", "tr-2"} {
		native := ccRepoARN("r-a") + "/trigger/" + name
		if _, ok := rows[native]; !ok {
			t.Errorf("trigger %s missing", native)
		}
		assertContainsNative(t, st, ccRepoARN("r-a"), native)
	}
	got := rows[ccRepoARN("r-a")+"/trigger/tr-1"]
	if got.Type != TypeCodeCommitRepositoryTrigger || sv(got.Name) != "tr-1" || sv(got.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", got.Type, sv(got.Name), sv(got.Region))
	}
	var attrs struct{ DestinationArn, CustomData string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.CustomData != "note tr-1" || attrs.DestinationArn == "" {
		t.Errorf("attrs = %+v, %v", attrs, err)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanCCRepositoryTriggers_Errors(t *testing.T) {
	t.Run("access denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		repos := ccSeedRepos(t, st, "r-a", "r-b")
		warned := countOpWarnings(st, "codecommit:GetRepositoryTriggers")
		total, _, err := scanCCRepositoryTriggers(context.Background(), ccClient(nil), newTestAccount(testAccountID), testRegion, st, testScanID, repos)
		if err != nil || total != 0 {
			t.Errorf("total, err = %d, %v; want 0, nil", total, err)
		}
		if *warned != 1 {
			t.Errorf("warnings = %d, want 1", *warned)
		}
	})
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		repos := ccSeedRepos(t, st, "r-a")
		handlers := map[string]func(any) (any, error){"GetRepositoryTriggers": func(any) (any, error) { return nil, ccBadInput }}
		_, _, err := scanCCRepositoryTriggers(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, repos)
		if !errors.Is(err, ccBadInput) {
			t.Errorf("err = %v, want the GetRepositoryTriggers error", err)
		}
	})
}

// One repository whose KMS key CodeCommit cannot use costs only that
// repository's triggers: the others are stored and one warning is recorded.
func TestScanCCRepositoryTriggers_BrokenKeySkipsOnlyThatRepository(t *testing.T) {
	for _, code := range ccEncryptionKeyCodes {
		t.Run(code, func(t *testing.T) {
			st := newTestStore(t)
			repos := ccSeedRepos(t, st, "r-a", "r-b", "r-c")
			warned := countOpWarnings(st, "codecommit:GetRepositoryTriggers")
			keyErr := &smithy.GenericAPIError{Code: code, Message: "key"}
			handlers := map[string]func(any) (any, error){
				"GetRepositoryTriggers": func(in any) (any, error) {
					repo := *in.(*codecommit.GetRepositoryTriggersInput).RepositoryName
					if repo != "r-a" {
						return nil, keyErr
					}
					return &codecommit.GetRepositoryTriggersOutput{Triggers: []codecommittypes.RepositoryTrigger{ccTrigger("tr-1")}}, nil
				},
			}
			total, _, err := scanCCRepositoryTriggers(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, repos)
			if err != nil || total != 1 {
				t.Errorf("total, err = %d, %v; want 1, nil", total, err)
			}
			if _, ok := rowsByNativeID(t, st, TypeCodeCommitRepositoryTrigger)[ccRepoARN("r-a")+"/trigger/tr-1"]; !ok {
				t.Errorf("r-a trigger missing")
			}
			if *warned != 1 {
				t.Errorf("warnings = %d, want 1", *warned)
			}
		})
	}
}

// A KMS error on one template's association listing keeps the template with a
// null association list and warns once.
func TestScanCCApprovalRuleTemplates_BrokenKeyKeepsTemplate(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "codecommit:ListRepositoriesForApprovalRuleTemplate")
	keyErr := &smithy.GenericAPIError{Code: "EncryptionKeyDisabledException", Message: "key"}
	handlers := ccTemplateHandlers(&parentCalls{}, map[string]func(any) (any, error){
		"ListRepositoriesForApprovalRuleTemplate": func(in any) (any, error) {
			if *in.(*codecommit.ListRepositoriesForApprovalRuleTemplateInput).ApprovalRuleTemplateName == "t-1" {
				return nil, keyErr
			}
			return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{RepositoryNames: []string{"repo-a"}}, nil
		},
	})
	total, _, err := scanCCApprovalRuleTemplates(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate)
	if names, ok := ccTemplateRepoNames(t, rows[ccTemplateARN("id-t-1")]); ok {
		t.Errorf("t-1 repositories = %v, want null", names)
	}
	if names, ok := ccTemplateRepoNames(t, rows[ccTemplateARN("id-t-2")]); !ok || !slices.Equal(names, []string{"repo-a"}) {
		t.Errorf("t-2 repositories = %v (present %v), want [repo-a]", names, ok)
	}
	if *warned != 1 {
		t.Errorf("warnings = %d, want 1", *warned)
	}
}

// Repositories are listed over every page and stored with GetRepository's
// metadata as their attributes.
func TestScanCCRepositories_StoresEachPageWithMetadata(t *testing.T) {
	st := newTestStore(t)
	handlers := map[string]func(any) (any, error){
		"ListRepositories": func(in any) (any, error) {
			if in.(*codecommit.ListRepositoriesInput).NextToken == nil {
				return &codecommit.ListRepositoriesOutput{Repositories: []codecommittypes.RepositoryNameIdPair{{RepositoryName: ptrStr("repo-a")}}, NextToken: ptrStr("p2")}, nil
			}
			return &codecommit.ListRepositoriesOutput{Repositories: []codecommittypes.RepositoryNameIdPair{{RepositoryName: ptrStr("repo-b")}, {}}}, nil
		},
		"GetRepository": func(in any) (any, error) {
			name := *in.(*codecommit.GetRepositoryInput).RepositoryName
			return &codecommit.GetRepositoryOutput{RepositoryMetadata: &codecommittypes.RepositoryMetadata{
				RepositoryName: ptrStr(name), CloneUrlHttp: ptrStr("https://git-codecommit.us-east-1.amazonaws.com/v1/repos/" + name),
			}}, nil
		},
	}
	repos, total, _, err := scanCCRepositories(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if want := []childParent{{id: "repo-a", arn: ccRepoARN("repo-a")}, {id: "repo-b", arn: ccRepoARN("repo-b")}}; !slices.Equal(repos, want) {
		t.Errorf("repos = %v, want %v", repos, want)
	}
	rows := rowsByNativeID(t, st, TypeCodeCommitRepository)
	for _, name := range []string{"repo-a", "repo-b"} {
		got, ok := rows[ccRepoARN(name)]
		if !ok {
			t.Errorf("repository %s missing", name)
			continue
		}
		var attrs struct {
			CloneURLHTTP string `json:"CloneUrlHttp"`
		}
		if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.CloneURLHTTP == "" {
			t.Errorf("%s attrs = %s, want GetRepository's RepositoryMetadata", name, got.AttributesJSON)
		}
		if sv(got.Name) != name || sv(got.Region) != testRegion {
			t.Errorf("%s name %q region %q", name, sv(got.Name), sv(got.Region))
		}
	}
}

// ccTopLevelHandlers serves one repository, one template associated with it
// and one trigger, with the ops in override answering instead. Every other op
// is denied.
func ccTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListRepositories": func(any) (any, error) {
			return &codecommit.ListRepositoriesOutput{Repositories: []codecommittypes.RepositoryNameIdPair{{RepositoryName: ptrStr("repo-a"), RepositoryId: ptrStr("rid")}}}, nil
		},
		"GetRepository": func(any) (any, error) {
			return &codecommit.GetRepositoryOutput{RepositoryMetadata: &codecommittypes.RepositoryMetadata{RepositoryName: ptrStr("repo-a")}}, nil
		},
		"ListApprovalRuleTemplates": func(any) (any, error) {
			return &codecommit.ListApprovalRuleTemplatesOutput{ApprovalRuleTemplateNames: []string{"t-1"}}, nil
		},
		"GetApprovalRuleTemplate": func(any) (any, error) {
			return &codecommit.GetApprovalRuleTemplateOutput{ApprovalRuleTemplate: ccTemplate("t-1", "id-t-1")}, nil
		},
		"ListRepositoriesForApprovalRuleTemplate": func(any) (any, error) {
			return &codecommit.ListRepositoriesForApprovalRuleTemplateOutput{RepositoryNames: []string{"repo-a"}}, nil
		},
		"GetRepositoryTriggers": func(in any) (any, error) {
			if *in.(*codecommit.GetRepositoryTriggersInput).RepositoryName != "repo-a" {
				return nil, ccBadInput
			}
			return &codecommit.GetRepositoryTriggersOutput{Triggers: []codecommittypes.RepositoryTrigger{ccTrigger("tr-1")}}, nil
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

func TestScanCodeCommitIn_RunsNewPhasesOverListedRepositories(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(ccTopLevelHandlers(nil)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 3 {
		t.Fatalf("total, err = %d, %v; want 3, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate)[ccTemplateARN("id-t-1")]; !ok {
		t.Errorf("template missing")
	}
	assertContainsNative(t, st, ccRepoARN("repo-a"), ccRepoARN("repo-a")+"/trigger/tr-1")
	assertContainsEdgesDeclared(t, st)
}

// The new phases run after the repository phase: its row is stored and counted
// when every new phase fails, and each new phase's error is returned.
func TestScanCodeCommitIn_NewPhaseErrorsKeepRepositories(t *testing.T) {
	st := newTestStore(t)
	listErr := &smithy.GenericAPIError{Code: "InvalidMaxResultsException", Message: "templates"}
	trigErr := &smithy.GenericAPIError{Code: "InvalidRepositoryNameException", Message: "triggers"}
	handlers := ccTopLevelHandlers(map[string]func(any) (any, error){
		"ListApprovalRuleTemplates": func(any) (any, error) { return nil, listErr },
		"GetRepositoryTriggers":     func(any) (any, error) { return nil, trigErr },
	})
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, listErr) || !errors.Is(err, trigErr) {
		t.Errorf("err = %v, want both new-phase errors", err)
	}
	if total != 1 || len(rowsByNativeID(t, st, TypeCodeCommitRepository)) != 1 {
		t.Errorf("total = %d, want 1 with the repository stored", total)
	}
}

// A repository-listing error is returned and the template phase still runs.
func TestScanCodeCommitIn_RepositoryErrorKeepsTemplates(t *testing.T) {
	st := newTestStore(t)
	handlers := ccTopLevelHandlers(map[string]func(any) (any, error){
		"ListRepositories": func(any) (any, error) { return nil, ccBadInput },
	})
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, ccBadInput) || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v, want the ListRepositories error classified outcomeError", err)
	}
	if total != 1 || len(rowsByNativeID(t, st, TypeCodeCommitApprovalRuleTemplate)) != 1 {
		t.Errorf("total = %d, want 1 with the template stored", total)
	}
}

// A later phase's transient error alone is returned, so the scan is marked
// partial rather than complete, with the earlier phase's rows counted.
func TestScanCodeCommitIn_LoneTransientPhaseMarksPartial(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "codecommit:ListApprovalRuleTemplates")
	handlers := ccTopLevelHandlers(map[string]func(any) (any, error){
		"ListApprovalRuleTemplates": func(any) (any, error) { return nil, ccThrottled },
	})
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if *warned != 0 {
		t.Errorf("transient warnings = %d, want 0", *warned)
	}
}

// A later phase's NXDOMAIN is flattened to a plain error, so the dispatcher
// does not report the region unavailable and drop the rows already stored.
func TestScanCodeCommitIn_LatePhaseNXDOMAINKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	handlers := ccTopLevelHandlers(map[string]func(any) (any, error){
		"ListApprovalRuleTemplates": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "codecommit.us-east-1.amazonaws.com", IsNotFound: true}
		},
	})
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

// In a region with no codecommit endpoint the first phase's NXDOMAIN is
// returned alone, so the dispatcher reports the region unavailable.
func TestScanCodeCommitIn_UnreachableEndpointStopsTheScan(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){}
	for op := range ccTopLevelHandlers(nil) {
		handlers[op] = func(any) (any, error) {
			calls.add(op)
			return nil, &net.DNSError{Err: "no such host", Name: "codecommit.us-east-1.amazonaws.com", IsNotFound: true}
		}
	}
	total, _, err := scanCodeCommitIn(context.Background(), ccClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if got, want := calls.distinct(), []string{"ListRepositories"}; !slices.Equal(got, want) || total != 0 {
		t.Errorf("ops called %v total %d, want %v and 0", got, total, want)
	}
}
