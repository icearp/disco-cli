package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/computeoptimizerautomation"
	acotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizerautomation/types"
	"github.com/icearp/disco-cli/store"
)

const (
	acoRuleA = "arn:aws:compute-optimizer::" + testAccountID + ":automation-rule/rule-a"
	acoRuleB = "arn:aws:compute-optimizer::" + testAccountID + ":automation-rule/rule-b"
)

// acoHandlers answers ListAutomationRules with rule-a on page one and rule-b
// plus an ARN-less rule on page two, and GetAutomationRule with a full rule
// for each ARN. It records every GetAutomationRule ARN.
func acoHandlers(gets *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListAutomationRules": func(in any) (any, error) {
			if in.(*computeoptimizerautomation.ListAutomationRulesInput).NextToken == nil {
				return &computeoptimizerautomation.ListAutomationRulesOutput{
					AutomationRules: []acotypes.AutomationRule{{RuleArn: ptrStr(acoRuleA)}},
					NextToken:       ptrStr("p2"),
				}, nil
			}
			return &computeoptimizerautomation.ListAutomationRulesOutput{
				AutomationRules: []acotypes.AutomationRule{{RuleArn: ptrStr(acoRuleB)}, {Name: ptrStr("no-arn")}},
			}, nil
		},
		"GetAutomationRule": func(in any) (any, error) {
			arn := sv(in.(*computeoptimizerautomation.GetAutomationRuleInput).RuleArn)
			gets.add(arn)
			return &computeoptimizerautomation.GetAutomationRuleOutput{
				RuleArn:          &arn,
				Name:             ptrStr(map[string]string{acoRuleA: "name-a", acoRuleB: "name-b"}[arn]),
				Status:           acotypes.RuleStatusActive,
				CreatedTimestamp: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
				Criteria: &acotypes.Criteria{Region: []acotypes.StringCriteriaCondition{
					{Comparison: acotypes.ComparisonOperatorStringEquals, Values: []string{"eu-west-1"}},
				}},
				Tags: []acotypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
			}, nil
		},
	}
}

// runACOAutomationAcct runs the registered aws:aco-automation scanner, so
// each test also exercises the client wiring.
func runACOAutomationAcct(t *testing.T, st *store.Store, acct *account) (int, error) {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:aco-automation" })
	if idx < 0 {
		t.Fatal("aws:aco-automation is not registered")
	}
	total, _, err := registeredServices[idx].fn(context.Background(), acct, "", st, testScanID)
	return total, err
}

// runACOAutomation runs the scanner against handlers; an unstubbed op is denied.
func runACOAutomation(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "unstubbed op"))
	return runACOAutomationAcct(t, st, acct)
}

func TestScanACOAutomationRules_StoresEveryPageFromUSEast1(t *testing.T) {
	st := newTestStore(t)
	var gets, regions parentCalls
	h := acoHandlers(&gets)
	list := h["ListAutomationRules"]
	h["ListAutomationRules"] = func(in any) (any, error) {
		if got := in.(*computeoptimizerautomation.ListAutomationRulesInput).MaxResults; got == nil || *got != 100 {
			t.Errorf("ListAutomationRules MaxResults = %v, want 100", got)
		}
		return list(in)
	}
	acct := newTestAccount(testAccountID)
	cfg := opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	cfg.Region = "eu-west-1"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, err := runACOAutomationAcct(t, st, acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	if got := gets.distinct(); !slices.Equal(got, []string{acoRuleA, acoRuleB}) {
		t.Errorf("GetAutomationRule ARNs = %v, want [%s %s]", got, acoRuleA, acoRuleB)
	}
	rows := rowsByNativeID(t, st, TypeACOAutomationAutomationRule)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: %v", len(rows), rows)
	}
	r, ok := rows[acoRuleB]
	if !ok {
		t.Fatalf("rule %s not stored", acoRuleB)
	}
	if sv(r.Region) != "global" || sv(r.Name) != "name-b" || sv(r.Status) != "Active" {
		t.Errorf("region, name, status = %q, %q, %q; want global, name-b, Active", sv(r.Region), sv(r.Name), sv(r.Status))
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	if sv(r.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q, want {\"env\":\"prod\"}", sv(r.TagsJSON))
	}
	var attrs struct {
		Criteria struct {
			Region []struct{ Values []string }
		}
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attributes: %v", err)
	}
	if len(attrs.Criteria.Region) != 1 || !slices.Equal(attrs.Criteria.Region[0].Values, []string{"eu-west-1"}) {
		t.Errorf("attributes Criteria.Region = %+v, want one condition on eu-west-1", attrs.Criteria.Region)
	}
}

func TestScanACOAutomationRules_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "aco-automation:ListAutomationRules")
	total, err := runACOAutomation(t, st, map[string]func(in any) (any, error){
		"ListAutomationRules": func(any) (any, error) { return &computeoptimizerautomation.ListAutomationRulesOutput{}, nil },
	})
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 0, 0, nil", total, *warns, err)
	}
}

func TestScanACOAutomationRules_NotOptedInIsDisabled(t *testing.T) {
	st := newTestStore(t)
	_, err := runACOAutomation(t, st, map[string]func(in any) (any, error){
		"ListAutomationRules": func(any) (any, error) {
			return nil, apiErr("OptInRequiredException", "The account must be opted in")
		},
	})
	if !errors.Is(err, errServiceDisabled) {
		t.Errorf("err = %v, want errServiceDisabled", err)
	}
}

// A list denial warns once and keeps the rules from the pages before it.
func TestScanACOAutomationRules_ListDeniedKeepsListedRules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failPage bool // fail page two instead of page one
		want     int
	}{
		{name: "first page", want: 0},
		{name: "second page", failPage: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "aco-automation:ListAutomationRules")
			var gets parentCalls
			h := acoHandlers(&gets)
			list := h["ListAutomationRules"]
			h["ListAutomationRules"] = func(in any) (any, error) {
				if (in.(*computeoptimizerautomation.ListAutomationRulesInput).NextToken != nil) == tc.failPage {
					return nil, apiErr("AccessDeniedException", "not authorized to perform: aco-automation:ListAutomationRules")
				}
				return list(in)
			}
			total, err := runACOAutomation(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != tc.want || len(rowsByNativeID(t, st, TypeACOAutomationAutomationRule)) != tc.want || *warns != 1 {
				t.Errorf("total, rows, warnings = %d, %d, %d; want %d, %d, 1",
					total, len(rowsByNativeID(t, st, TypeACOAutomationAutomationRule)), *warns, tc.want, tc.want)
			}
		})
	}
}

func TestScanACOAutomationRules_ListErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := runACOAutomation(t, st, map[string]func(in any) (any, error){
		"ListAutomationRules": func(any) (any, error) {
			return nil, apiErr("InvalidParameterValueException", "bad filter")
		},
	})
	assertAPICode(t, err, "InvalidParameterValueException")
	if errors.Is(err, errServiceDisabled) {
		t.Errorf("err = %v, want a hard error, not errServiceDisabled", err)
	}
}

// getFails makes GetAutomationRule fail for rule-a with err and answer rule-b.
func getFails(gets *parentCalls, err error) map[string]func(in any) (any, error) {
	h := acoHandlers(gets)
	get := h["GetAutomationRule"]
	h["GetAutomationRule"] = func(in any) (any, error) {
		if sv(in.(*computeoptimizerautomation.GetAutomationRuleInput).RuleArn) == acoRuleA {
			return nil, err
		}
		return get(in)
	}
	return h
}

// A rule deleted between the list and its Get is dropped silently; its
// sibling is stored.
func TestScanACOAutomationRules_GetNotFoundSkipsRule(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "aco-automation:GetAutomationRule")
	var gets parentCalls
	total, err := runACOAutomation(t, st, getFails(&gets, apiErr("ResourceNotFoundException", "rule not found")))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeACOAutomationAutomationRule)
	if _, ok := rows[acoRuleB]; total != 1 || len(rows) != 1 || !ok || *warns != 0 {
		t.Errorf("total, rows, warnings = %d, %v, %d; want 1, only %s, 0", total, rows, *warns, acoRuleB)
	}
}

// A denied Get warns once for the op, however many rules it hits.
func TestScanACOAutomationRules_GetDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "aco-automation:GetAutomationRule")
	var gets parentCalls
	h := acoHandlers(&gets)
	h["GetAutomationRule"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: aco-automation:GetAutomationRule")
	}
	total, err := runACOAutomation(t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || *warns != 1 {
		t.Errorf("total, warnings = %d, %d; want 0, 1", total, *warns)
	}
}

func TestScanACOAutomationRules_GetErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var gets parentCalls
	_, err := runACOAutomation(t, st, getFails(&gets, apiErr("InvalidParameterValueException", "bad arn")))
	assertAPICode(t, err, "InvalidParameterValueException")
	if n := len(rowsByNativeID(t, st, TypeACOAutomationAutomationRule)); n != 0 {
		t.Errorf("rows = %d, want 0 after a failed Get", n)
	}
}

// A service that answers with the token it was sent ends the listing after
// that page instead of looping.
func TestScanACOAutomationRules_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var gets parentCalls
	h := acoHandlers(&gets)
	calls := 0
	h["ListAutomationRules"] = func(any) (any, error) {
		calls++
		if calls > 2 {
			return nil, apiErr("InvalidParameterValueException", "listing did not stop")
		}
		arn := acoRuleA
		if calls == 2 {
			arn = acoRuleB
		}
		return &computeoptimizerautomation.ListAutomationRulesOutput{
			AutomationRules: []acotypes.AutomationRule{{RuleArn: &arn}},
			NextToken:       ptrStr("same"),
		}, nil
	}
	total, err := runACOAutomation(t, st, h)
	if err != nil || total != 2 {
		t.Errorf("total, err = %d, %v; want 2, nil", total, err)
	}
}
