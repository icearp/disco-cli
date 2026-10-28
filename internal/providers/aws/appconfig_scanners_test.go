package aws

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	actypes "github.com/aws/aws-sdk-go-v2/service/appconfig/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	acDenied     = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: appconfig:List"}
	acNotFound   = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	acBadRequest = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
)

const (
	acExpDef1ARN = "arn:aws:appconfig:us-east-1:123456789012:application/a1/experimentdefinition/x1"
	acExpDef2ARN = "arn:aws:appconfig:us-east-1:123456789012:application/a2/experimentdefinition/x2"
)

// acExpDefHandler serves ListExperimentDefinitions over two pages: x1 (named,
// ACTIVE) under a1; then x2 (no name, no status) under a2 and two elements
// missing the application id or the id. pageTwoErr, when set, replaces the
// second page. Every call's input is recorded.
func acExpDefHandler(inputs *[]appconfig.ListExperimentDefinitionsInput, mu *sync.Mutex, pageTwoErr error) func(in any) (any, error) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return func(in any) (any, error) {
		req := in.(*appconfig.ListExperimentDefinitionsInput)
		mu.Lock()
		*inputs = append(*inputs, *req)
		mu.Unlock()
		if req.NextToken == nil {
			return &appconfig.ListExperimentDefinitionsOutput{Items: []actypes.ExperimentDefinitionSummary{{
				ApplicationId: ptrStr("a1"), Id: ptrStr("x1"), Name: ptrStr("checkout-test"),
				Status: actypes.ExperimentDefinitionStatusActive, CreatedAt: &created,
				EnvironmentId: ptrStr("env1"), ConfigurationProfileId: ptrStr("cp1"), FlagKey: ptrStr("new-checkout"),
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &appconfig.ListExperimentDefinitionsOutput{Items: []actypes.ExperimentDefinitionSummary{
			{ApplicationId: ptrStr("a2"), Id: ptrStr("x2")},
			{Id: ptrStr("no-app")},
			{ApplicationId: ptrStr("a1"), Name: ptrStr("no-id")},
		}}, nil
	}
}

func scanACExpDefs(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	client := appconfig.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListExperimentDefinitions": handler,
		"ListTagsForResource":       acNoTags,
	}, acDenied))
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanACExperimentDefinitions(context.Background(), client, acct, testRegion, st, testScanID, acTags(client), &p)
	total, _, err := p.result(st, acct, testRegion)
	return total, err
}

func acNoTags(any) (any, error) { return &appconfig.ListTagsForResourceOutput{}, nil }

// Both pages are stored, elements missing either half of the NativeID are
// skipped, and the list carries no filter.
func TestScanACExperimentDefinitions_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var (
		mu     sync.Mutex
		inputs []appconfig.ListExperimentDefinitionsInput
	)
	total, err := scanACExpDefs(t, st, acExpDefHandler(&inputs, &mu, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("list calls = %d, want 2", len(inputs))
	}
	for _, in := range inputs {
		if in.ApplicationIdentifier != nil || in.ConfigurationProfileIdentifier != nil || in.EnvironmentIdentifier != nil || in.Status != "" {
			t.Errorf("list input carries a filter: %+v", in)
		}
	}
	rows := rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want x1 and x2", total, rows)
	}
	x1, ok := rows[acExpDef1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", acExpDef1ARN, rows)
	}
	if sv(x1.Name) != "checkout-test" || sv(x1.Status) != "ACTIVE" || sv(x1.Region) != "us-east-1" || sv(x1.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("x1 name/status/region/created = %q/%q/%q/%q", sv(x1.Name), sv(x1.Status), sv(x1.Region), sv(x1.CreatedAt))
	}
	if !strings.Contains(x1.AttributesJSON, `"FlagKey":"new-checkout"`) {
		t.Errorf("x1 attrs = %s", x1.AttributesJSON)
	}
	x2, ok := rows[acExpDef2ARN]
	if !ok {
		t.Fatalf("missing %s in %v", acExpDef2ARN, rows)
	}
	if sv(x2.Name) != "x2" || x2.Status != nil {
		t.Errorf("x2 name = %q, status = %v; want x2, nil", sv(x2.Name), x2.Status)
	}
}

func TestScanACExperimentDefinitions_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanACExpDefs(t, st, func(any) (any, error) { return &appconfig.ListExperimentDefinitionsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// On the first call, every shape an unrouted operation takes is a silent
// feature gap; any other error is returned.
func TestScanACExperimentDefinitions_FirstPageNotDeployed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "resource not found", err: acNotFound},
		{name: "other", err: acBadRequest, wantCode: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "appconfig:ListExperimentDefinitions")
			total, err := scanACExpDefs(t, st, func(any) (any, error) { return nil, tc.err })
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != 0 {
				t.Errorf("total = %d, warnings = %d; want 0, 0", total, *warns)
			}
		})
	}
}

// After a page has succeeded, a denial warns once and keeps page one's row;
// every other error, the not-deployed shapes included, is returned with page
// one's row stored.
func TestScanACExperimentDefinitions_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: acDenied, wantWarns: 1},
		{name: "not found", err: acNotFound, wantCode: "ResourceNotFoundException"},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "other", err: acBadRequest, wantCode: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "appconfig:ListExperimentDefinitions")
			var (
				mu     sync.Mutex
				inputs []appconfig.ListExperimentDefinitionsInput
			)
			total, err := scanACExpDefs(t, st, acExpDefHandler(&inputs, &mu, tc.err))
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantHTTP != 0:
				if c, ok := httpStatusCode(err); !ok || c != tc.wantHTTP {
					t.Fatalf("err = %v, want HTTP %d", err, tc.wantHTTP)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)
			if _, ok := rows[acExpDef1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want x1 only", total, rows)
			}
		})
	}
}

// acTopLevelHandlers answers every appconfig op: one application with one
// environment and one configuration profile, no deployments or hosted
// versions, one deployment strategy, no extensions, one extension association,
// and the experiment definitions of acExpDefHandler.
func acTopLevelHandlers(expDefs func(in any) (any, error)) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(any) (any, error) {
			return &appconfig.ListApplicationsOutput{Items: []actypes.Application{{Id: ptrStr("a1"), Name: ptrStr("shop")}}}, nil
		},
		"ListEnvironments": func(any) (any, error) {
			return &appconfig.ListEnvironmentsOutput{Items: []actypes.Environment{{Id: ptrStr("env1")}}}, nil
		},
		"ListConfigurationProfiles": func(any) (any, error) {
			return &appconfig.ListConfigurationProfilesOutput{Items: []actypes.ConfigurationProfileSummary{{Id: ptrStr("cp1")}}}, nil
		},
		"ListDeployments": func(any) (any, error) { return &appconfig.ListDeploymentsOutput{}, nil },
		"ListHostedConfigurationVersions": func(any) (any, error) {
			return &appconfig.ListHostedConfigurationVersionsOutput{}, nil
		},
		"ListDeploymentStrategies": func(any) (any, error) {
			return &appconfig.ListDeploymentStrategiesOutput{Items: []actypes.DeploymentStrategy{{Id: ptrStr("ds1"), Name: ptrStr("mine")}}}, nil
		},
		"ListExtensions": func(any) (any, error) { return &appconfig.ListExtensionsOutput{}, nil },
		"ListExtensionAssociations": func(any) (any, error) {
			return &appconfig.ListExtensionAssociationsOutput{Items: []actypes.ExtensionAssociationSummary{{Id: ptrStr("ea1")}}}, nil
		},
		"ListExperimentDefinitions": expDefs,
		"ListTagsForResource":       acNoTags,
	}
}

func TestScanAppConfig_StoresExperimentDefinitions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var (
		mu     sync.Mutex
		inputs []appconfig.ListExperimentDefinitionsInput
	)
	acct.cfg = opStubConfig(acTopLevelHandlers(acExpDefHandler(&inputs, &mu, nil)), acDenied)

	total, _, err := scanAppConfig(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanAppConfig: %v", err)
	}
	// 1 app, 1 env, 1 profile, 1 strategy, 1 association, 2 experiment definitions.
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	rows := rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)
	for _, id := range []string{acExpDef1ARN, acExpDef2ARN} {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
}

func TestScanAppConfig_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var (
		mu      sync.Mutex
		inputs  []appconfig.ListExperimentDefinitionsInput
		regions parentCalls
	)
	cfg := opStubConfig(acTopLevelHandlers(acExpDefHandler(&inputs, &mu, nil)), acDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanAppConfig(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanAppConfig: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)); n != 2 {
		t.Errorf("experiment definition rows = %d, want 2", n)
	}
}

// The experiment definition phase runs after every existing phase: its hard
// error reaches the caller with every earlier row stored and counted.
func TestScanAppConfig_ExperimentDefinitionErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	called := false
	acct.cfg = opStubConfig(acTopLevelHandlers(func(any) (any, error) {
		called = true
		return nil, acBadRequest
	}), acDenied)

	total, _, err := scanAppConfig(context.Background(), acct, testRegion, st, testScanID)
	if !called {
		t.Fatal("ListExperimentDefinitions never called")
	}
	assertAPICode(t, err, "BadRequestException")
	if total != 5 {
		t.Errorf("total = %d, want 5 (app, env, profile, strategy, association)", total)
	}
	for typ, want := range map[string]string{
		TypeAppConfigApplication:          "arn:aws:appconfig:us-east-1:123456789012:application/a1",
		TypeAppConfigEnvironment:          "arn:aws:appconfig:us-east-1:123456789012:application/a1/environment/env1",
		TypeAppConfigConfigurationProfile: "arn:aws:appconfig:us-east-1:123456789012:application/a1/configurationprofile/cp1",
		TypeAppConfigDeploymentStrategy:   "arn:aws:appconfig:us-east-1:123456789012:deploymentstrategy/ds1",
		TypeAppConfigExtensionAssociation: "arn:aws:appconfig:us-east-1:123456789012:extensionassociation/ea1",
	} {
		if _, ok := rowsByNativeID(t, st, typ)[want]; !ok {
			t.Errorf("missing %s row %s", typ, want)
		}
	}
}
