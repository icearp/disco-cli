package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	qbtypes "github.com/aws/aws-sdk-go-v2/service/qbusiness/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	qbDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: qbusiness:Get"}
	qbGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	qbBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	qbThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
)

func qbTestClient(handlers map[string]func(in any) (any, error)) *qbusiness.Client {
	return qbusiness.NewFromConfig(opStubConfig(handlers, qbDenied))
}

func qbTestAppARN(id string) string {
	return qbARN("aws", testRegion, testAccountID, "application", id)
}

func qbTestApps(ids ...string) []qbAppRef {
	apps := make([]qbAppRef, len(ids))
	for i, id := range ids {
		apps[i] = qbAppRef{id: id, arn: qbTestAppARN(id), status: qbtypes.ApplicationStatusActive}
	}
	return apps
}

func qbTopic(name string) qbtypes.TopicConfiguration {
	return qbtypes.TopicConfiguration{Name: &name}
}

func qbAppIDOf(in any) string {
	return sv(in.(*qbusiness.GetChatControlsConfigurationInput).ApplicationId)
}

func scanQBChatControlsForTest(t *testing.T, st *store.Store, apps []qbAppRef, get func(in any) (any, error)) (int, error) {
	t.Helper()
	client := qbTestClient(map[string]func(any) (any, error){"GetChatControlsConfiguration": get})
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanQBChatControlsConfigurations(context.Background(), client, acct, testRegion, st, testScanID, apps, &p)
	total, _, err := p.result(st, acct, testRegion)
	return total, err
}

func TestScanQBChatControlsConfigurations_StoresEveryTopicPage(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	total, err := scanQBChatControlsForTest(t, st, qbTestApps("a", "b"), func(in any) (any, error) {
		req := in.(*qbusiness.GetChatControlsConfigurationInput)
		calls.add(sv(req.ApplicationId))
		if req.MaxResults == nil || *req.MaxResults != 50 {
			return nil, qbBadInput // the model's page cap is 50
		}
		if sv(req.ApplicationId) == "b" {
			return &qbusiness.GetChatControlsConfigurationOutput{ResponseScope: qbtypes.ResponseScopeExtendedKnowledgeEnabled}, nil
		}
		if sv(req.NextToken) == "" {
			return &qbusiness.GetChatControlsConfigurationOutput{
				BlockedPhrases:      &qbtypes.BlockedPhrasesConfiguration{BlockedPhrases: []string{"secret project"}},
				ResponseScope:       qbtypes.ResponseScopeEnterpriseContentOnly,
				TopicConfigurations: []qbtypes.TopicConfiguration{qbTopic("t1")},
				NextToken:           ptrStr("p2"),
			}, nil
		}
		// Only the topics are paged; a later page's other fields must not
		// replace the first page's.
		return &qbusiness.GetChatControlsConfigurationOutput{
			TopicConfigurations: []qbtypes.TopicConfiguration{qbTopic("t2")},
		}, nil
	})
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if len(calls.ids) != 3 {
		t.Errorf("calls = %v, want 3 (two pages for a, one for b)", calls.ids)
	}
	rows := rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	got, ok := rows[qbTestAppARN("a")+"/chat-controls-configuration"]
	if !ok {
		t.Fatalf("no row for app a; have %v", rows)
	}
	if got.Type != TypeQBusinessChatControlsConfiguration || sv(got.Region) != testRegion || sv(got.Name) != "a" {
		t.Errorf("row = type %q region %q name %q", got.Type, sv(got.Region), sv(got.Name))
	}
	var attrs struct {
		BlockedPhrases      struct{ BlockedPhrases []string }
		ResponseScope       string
		TopicConfigurations []struct{ Name string }
		NextToken           *string
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	var names []string
	for _, tc := range attrs.TopicConfigurations {
		names = append(names, tc.Name)
	}
	if !slices.Equal(names, []string{"t1", "t2"}) {
		t.Errorf("topics = %v, want [t1 t2] from both pages", names)
	}
	if !slices.Equal(attrs.BlockedPhrases.BlockedPhrases, []string{"secret project"}) || attrs.ResponseScope != "ENTERPRISE_CONTENT_ONLY" {
		t.Errorf("attrs = %+v; want the first page's settings", attrs)
	}
	if attrs.NextToken != nil {
		t.Errorf("NextToken = %q, want dropped", *attrs.NextToken)
	}
}

func TestScanQBChatControlsConfigurations_NoAppsStoresNothing(t *testing.T) {
	st := newTestStore(t)
	total, err := scanQBChatControlsForTest(t, st, nil, func(any) (any, error) {
		t.Error("GetChatControlsConfiguration called with no applications")
		return &qbusiness.GetChatControlsConfigurationOutput{}, nil
	})
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)) != 0 {
		t.Errorf("total, err = %d, %v; want 0 rows, nil", total, err)
	}
}

func TestScanQBChatControlsConfigurations_SkipsOnlyFailedApps(t *testing.T) {
	st := newTestStore(t)
	apps := []qbAppRef{}
	for id, status := range map[string]qbtypes.ApplicationStatus{
		"active": qbtypes.ApplicationStatusActive, "creating": qbtypes.ApplicationStatusCreating,
		"updating": qbtypes.ApplicationStatusUpdating, "deleting": qbtypes.ApplicationStatusDeleting,
		"failed": qbtypes.ApplicationStatusFailed, "unset": "",
	} {
		apps = append(apps, qbAppRef{id: id, arn: qbTestAppARN(id), status: status})
	}
	calls := &parentCalls{}
	total, err := scanQBChatControlsForTest(t, st, apps, func(in any) (any, error) {
		calls.add(qbAppIDOf(in))
		return &qbusiness.GetChatControlsConfigurationOutput{}, nil
	})
	if err != nil || total != 5 {
		t.Fatalf("total, err = %d, %v; want 5, nil", total, err)
	}
	if got, want := calls.distinct(), []string{"active", "creating", "deleting", "unset", "updating"}; !slices.Equal(got, want) {
		t.Errorf("apps queried %v, want %v", got, want)
	}
}

func TestScanQBChatControlsConfigurations_DeniedWarnsOnceAndKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "qbusiness:GetChatControlsConfiguration")
	total, err := scanQBChatControlsForTest(t, st, qbTestApps("a", "b", "c"), func(in any) (any, error) {
		if qbAppIDOf(in) == "a" {
			return &qbusiness.GetChatControlsConfigurationOutput{}, nil
		}
		return nil, qbDenied
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for two denied applications", *warnings)
	}
}

func TestScanQBChatControlsConfigurations_GoneAppSkippedSilently(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "qbusiness:GetChatControlsConfiguration")
	total, err := scanQBChatControlsForTest(t, st, qbTestApps("a", "b"), func(in any) (any, error) {
		if qbAppIDOf(in) == "b" {
			return nil, qbGone
		}
		return &qbusiness.GetChatControlsConfigurationOutput{}, nil
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)[qbChatControlsNativeID(qbTestAppARN("a"))]; !ok {
		t.Error("sibling app a not stored")
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d, want 0 for a deleted application", *warnings)
	}
}

// Applications still throttled after the SDK's retries have no row to keep,
// so they are one transient error, not a warning; the others are stored.
func TestScanQBChatControlsConfigurations_ThrottledAppsReturnedAndOthersKept(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	total, err := scanQBChatControlsForTest(t, st, qbTestApps("a", "b", "c"), func(in any) (any, error) {
		if qbAppIDOf(in) == "a" {
			return &qbusiness.GetChatControlsConfigurationOutput{}, nil
		}
		return nil, qbThrottle
	})
	assertGlueOutcome(t, err, outcomeTransient)
	assertAPICode(t, err, "ThrottlingException")
	if members := bacTopMembers(err); len(members) != 1 {
		t.Errorf("err members = %d; want 1 for two throttled applications (err %v)", len(members), err)
	}
	if total != 1 || len(rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)) != 1 {
		t.Errorf("total = %d; want application a stored (1)", total)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// One application's hard error costs the others none of their rows, and a
// throttled application beside it is demoted to one warning under the op.
func TestScanQBChatControlsConfigurations_HardErrorKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "qbusiness:GetChatControlsConfiguration")
	total, err := scanQBChatControlsForTest(t, st, qbTestApps("a", "b", "c", "d"), func(in any) (any, error) {
		switch qbAppIDOf(in) {
		case "b":
			return nil, qbThrottle
		case "c":
			return nil, qbBadInput
		}
		return &qbusiness.GetChatControlsConfigurationOutput{}, nil
	})
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	// a is read before the failed application, d after it.
	rows := rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)
	_, aOK := rows[qbChatControlsNativeID(qbTestAppARN("a"))]
	_, dOK := rows[qbChatControlsNativeID(qbTestAppARN("d"))]
	if !aOK || !dOK || total != 2 {
		t.Errorf("total = %d, a stored = %v, d stored = %v; want 2, true, true", total, aOK, dOK)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1 for the throttled application", *warnings)
	}
}

func TestScanQBChatControlsConfigurations_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := scanQBChatControlsForTest(t, st, qbTestApps("a"), func(any) (any, error) { return nil, qbBadInput })
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the ValidationException", err)
	}
}

// A repeated page token ends the read as errRepeatedNextToken: the topics
// would be incomplete, so that application's row is not stored.
func TestScanQBChatControlsConfigurations_RepeatedTokenIsAnError(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	_, err := scanQBChatControlsForTest(t, st, qbTestApps("a"), func(in any) (any, error) {
		calls.add(sv(in.(*qbusiness.GetChatControlsConfigurationInput).NextToken))
		if len(calls.ids) > 5 {
			return nil, qbBadInput
		}
		return &qbusiness.GetChatControlsConfigurationOutput{NextToken: ptrStr("same"), TopicConfigurations: []qbtypes.TopicConfiguration{qbTopic("t")}}, nil
	})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if got := len(calls.ids); got != 2 {
		t.Errorf("calls = %d, want 2 (first page, then the repeated token)", got)
	}
	if n := len(rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)); n != 0 {
		t.Errorf("rows = %d; want none", n)
	}
}

// qbScanHandlers answers every op scanQBusiness makes for one ACTIVE
// application "app1" with indices "idx1" and "idx2"; tests override single ops.
func qbScanHandlers() map[string]func(any) (any, error) {
	respond := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(any) (any, error){
		"ListApplications": respond(&qbusiness.ListApplicationsOutput{Applications: []qbtypes.Application{
			{ApplicationId: ptrStr("app1"), DisplayName: ptrStr("App One"), Status: qbtypes.ApplicationStatusActive},
		}}),
		"ListDataAccessors":              respond(&qbusiness.ListDataAccessorsOutput{}),
		"ListIndices":                    respond(&qbusiness.ListIndicesOutput{Indices: []qbtypes.Index{{IndexId: ptrStr("idx1")}, {IndexId: ptrStr("idx2")}}}),
		"ListDataSources":                respond(&qbusiness.ListDataSourcesOutput{}),
		"ListPlugins":                    respond(&qbusiness.ListPluginsOutput{}),
		"ListRetrievers":                 respond(&qbusiness.ListRetrieversOutput{}),
		"ListWebExperiences":             respond(&qbusiness.ListWebExperiencesOutput{}),
		"ListChatResponseConfigurations": respond(&qbusiness.ListChatResponseConfigurationsOutput{}),
		"ListSubscriptions":              respond(&qbusiness.ListSubscriptionsOutput{}),
		"GetChatControlsConfiguration":   respond(&qbusiness.GetChatControlsConfigurationOutput{}),
		"ListTagsForResource":            respond(&qbusiness.ListTagsForResourceOutput{}),
	}
}

func runScanQBusiness(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, qbDenied)
	total, _, err := scanQBusiness(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanQBusiness_StoresChatControlsPerApplication(t *testing.T) {
	st := newTestStore(t)
	total, err := runScanQBusiness(t, st, qbScanHandlers())
	if err != nil || total != 4 {
		t.Fatalf("total, err = %d, %v; want 4 (app, two indices, chat controls), nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)[qbTestAppARN("app1")+"/chat-controls-configuration"]; !ok {
		t.Error("chat controls for app1 not stored")
	}
}

func TestScanQBusiness_ChildPhaseErrorStillStoresChatControls(t *testing.T) {
	st := newTestStore(t)
	handlers := qbScanHandlers()
	handlers["ListSubscriptions"] = func(any) (any, error) { return nil, qbBadInput }
	_, err := runScanQBusiness(t, st, handlers)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the ListSubscriptions ValidationException", err)
	}
	if len(rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)) != 1 {
		t.Error("chat controls not stored after an earlier phase failed")
	}
}

func TestScanQBusiness_ChatControlsErrorKeepsEarlierPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := qbScanHandlers()
	handlers["GetChatControlsConfiguration"] = func(any) (any, error) { return nil, qbBadInput }
	total, err := runScanQBusiness(t, st, handlers)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the GetChatControlsConfiguration ValidationException", err)
	}
	if total != 3 || len(rowsByNativeID(t, st, TypeQBusinessIndex)) != 2 || len(rowsByNativeID(t, st, TypeQBusinessApplication)) != 1 {
		t.Errorf("total = %d; want the application and both indices stored and counted (3)", total)
	}
}

// TestScanQBusiness_TransientPhaseErrorDoesNotMaskHardError throttles one phase
// and fails the other: the throttle is one warning, and the returned error
// classifies as the hard error it carries.
func TestScanQBusiness_TransientPhaseErrorDoesNotMaskHardError(t *testing.T) {
	throttled := func(any) (any, error) { return nil, qbThrottle }
	hard := func(any) (any, error) { return nil, qbBadInput }
	for _, tc := range []struct {
		name, throttledOp, hardOp, warnedAs string
	}{
		{name: "child list throttled, chat controls fail", throttledOp: "ListSubscriptions", hardOp: "GetChatControlsConfiguration", warnedAs: "qbusiness:ListSubscriptions"},
		{name: "chat controls throttled, child list fails", throttledOp: "GetChatControlsConfiguration", hardOp: "ListSubscriptions", warnedAs: "qbusiness:GetChatControlsConfiguration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, tc.warnedAs)
			handlers := qbScanHandlers()
			handlers[tc.throttledOp] = throttled
			handlers[tc.hardOp] = hard
			_, err := runScanQBusiness(t, st, handlers)
			if got := classifyServiceError(err); got != outcomeError || !joinCarriesAPIErrorCode(err, "ValidationException") {
				t.Errorf("classifyServiceError(%v) = %d; want outcomeError (%d) carrying ValidationException", err, got, outcomeError)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", tc.warnedAs, *warnings)
			}
		})
	}
}

// Once ctx ends no further application is read, and that is an error even
// when every read already started succeeded. The first read cancels ctx and
// releases the others only then, so the reads past the fan-out bound never
// start.
func TestScanQBChatControlsConfigurations_ContextEndIsAnError(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	released := make(chan struct{})
	client := qbTestClient(map[string]func(any) (any, error){"GetChatControlsConfiguration": func(any) (any, error) {
		once.Do(func() {
			cancel()
			close(released)
		})
		<-released
		return &qbusiness.GetChatControlsConfigurationOutput{}, nil
	}})
	ids := make([]string, fanoutMed+2)
	for i := range ids {
		ids[i] = fmt.Sprintf("app%02d", i)
	}
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanQBChatControlsConfigurations(ctx, client, acct, testRegion, st, testScanID, qbTestApps(ids...), &p)
	_, _, err := p.result(st, acct, testRegion)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}
