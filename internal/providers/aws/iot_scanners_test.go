package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/store"
)

// iotTagCalls records the ResourceArn and NextToken of every
// ListTagsForResource call and answers each from pages (one page per entry,
// none for an ARN it does not hold) or errs; calls run concurrently.
type iotTagCalls struct {
	mu     sync.Mutex
	arns   []string
	tokens []string
	pages  map[string][][]iottypes.Tag
	errs   map[string]error
}

func (c *iotTagCalls) handler(in any) (any, error) {
	req := in.(*iot.ListTagsForResourceInput)
	arn := *req.ResourceArn
	c.mu.Lock()
	c.arns = append(c.arns, arn)
	c.tokens = append(c.tokens, sv(req.NextToken))
	c.mu.Unlock()
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	pages := c.pages[arn]
	if len(pages) == 0 {
		return &iot.ListTagsForResourceOutput{}, nil
	}
	page := 0
	if req.NextToken != nil {
		page = 1
	}
	out := &iot.ListTagsForResourceOutput{Tags: pages[page]}
	if page+1 < len(pages) {
		out.NextToken = ptrStr("p2")
	}
	return out, nil
}

// iotTaggedFixture serves one tagged type's listing (two resources, named
// "tagged" and "bare") and, for a listed-then-described type, its describe.
type iotTaggedFixture struct {
	typ, kind string
	handlers  map[string]func(any) (any, error)
}

var iotFixtureNames = []string{"tagged", "bare"}

// iotListed builds a list response from the fixture names.
func iotListed[T any](item func(name string) T, out func([]T) any) func(any) (any, error) {
	return func(any) (any, error) {
		var items []T
		for _, n := range iotFixtureNames {
			items = append(items, item(n))
		}
		return out(items), nil
	}
}

// iotDescribeStub answers a describe call for the name key reads from its input.
func iotDescribeStub[In any](kind string, key func(*In) *string, out func(name *string, arn string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		name := key(in.(*In))
		return out(name, iotTestARN(kind, *name)), nil
	}
}

func iotTaggedFixtures() []iotTaggedFixture {
	return []iotTaggedFixture{
		{TypeIoTCertificateProvider, "certificateprovider", map[string]func(any) (any, error){
			"ListCertificateProviders": iotListed(func(n string) iottypes.CertificateProviderSummary {
				return iottypes.CertificateProviderSummary{CertificateProviderName: ptrStr(n)}
			}, func(s []iottypes.CertificateProviderSummary) any {
				return &iot.ListCertificateProvidersOutput{CertificateProviders: s}
			}),
			"DescribeCertificateProvider": iotDescribeStub("certificateprovider", func(in *iot.DescribeCertificateProviderInput) *string { return in.CertificateProviderName },
				func(n *string, arn string) any {
					return &iot.DescribeCertificateProviderOutput{CertificateProviderName: n, CertificateProviderArn: &arn}
				}),
		}},
		{TypeIoTCACertificate, "cacert", map[string]func(any) (any, error){
			"ListCACertificates": iotListed(func(n string) iottypes.CACertificate { return iottypes.CACertificate{CertificateId: ptrStr(n)} },
				func(s []iottypes.CACertificate) any { return &iot.ListCACertificatesOutput{Certificates: s} }),
			"DescribeCACertificate": iotDescribeStub("cacert", func(in *iot.DescribeCACertificateInput) *string { return in.CertificateId },
				func(n *string, arn string) any {
					return &iot.DescribeCACertificateOutput{CertificateDescription: &iottypes.CACertificateDescription{CertificateId: n, CertificateArn: &arn}}
				}),
		}},
		{TypeIoTPolicy, "policy", map[string]func(any) (any, error){
			"ListPolicies": iotListed(func(n string) iottypes.Policy { return iottypes.Policy{PolicyName: ptrStr(n)} },
				func(s []iottypes.Policy) any { return &iot.ListPoliciesOutput{Policies: s} }),
			"GetPolicy": iotDescribeStub("policy", func(in *iot.GetPolicyInput) *string { return in.PolicyName },
				func(n *string, arn string) any { return &iot.GetPolicyOutput{PolicyName: n, PolicyArn: &arn} }),
		}},
		{TypeIoTRoleAlias, "rolealias", map[string]func(any) (any, error){
			"ListRoleAliases": func(any) (any, error) { return &iot.ListRoleAliasesOutput{RoleAliases: iotFixtureNames}, nil },
			"DescribeRoleAlias": iotDescribeStub("rolealias", func(in *iot.DescribeRoleAliasInput) *string { return in.RoleAlias },
				func(n *string, arn string) any {
					return &iot.DescribeRoleAliasOutput{RoleAliasDescription: &iottypes.RoleAliasDescription{RoleAlias: n, RoleAliasArn: &arn}}
				}),
		}},
		{TypeIoTAuthorizer, "authorizer", map[string]func(any) (any, error){
			"ListAuthorizers": iotListed(func(n string) iottypes.AuthorizerSummary {
				return iottypes.AuthorizerSummary{AuthorizerName: ptrStr(n)}
			},
				func(s []iottypes.AuthorizerSummary) any { return &iot.ListAuthorizersOutput{Authorizers: s} }),
			"DescribeAuthorizer": iotDescribeStub("authorizer", func(in *iot.DescribeAuthorizerInput) *string { return in.AuthorizerName },
				func(n *string, arn string) any {
					return &iot.DescribeAuthorizerOutput{AuthorizerDescription: &iottypes.AuthorizerDescription{AuthorizerName: n, AuthorizerArn: &arn}}
				}),
		}},
		{TypeIoTDomainConfiguration, "domainconfiguration", map[string]func(any) (any, error){
			"ListDomainConfigurations": iotListed(func(n string) iottypes.DomainConfigurationSummary {
				return iottypes.DomainConfigurationSummary{DomainConfigurationName: ptrStr(n)}
			}, func(s []iottypes.DomainConfigurationSummary) any {
				return &iot.ListDomainConfigurationsOutput{DomainConfigurations: s}
			}),
			"DescribeDomainConfiguration": iotDescribeStub("domainconfiguration", func(in *iot.DescribeDomainConfigurationInput) *string { return in.DomainConfigurationName },
				func(n *string, arn string) any {
					return &iot.DescribeDomainConfigurationOutput{DomainConfigurationName: n, DomainConfigurationArn: &arn}
				}),
		}},
		{TypeIoTScheduledAudit, "scheduledaudit", map[string]func(any) (any, error){
			"ListScheduledAudits": iotListed(func(n string) iottypes.ScheduledAuditMetadata {
				return iottypes.ScheduledAuditMetadata{ScheduledAuditName: ptrStr(n)}
			}, func(s []iottypes.ScheduledAuditMetadata) any {
				return &iot.ListScheduledAuditsOutput{ScheduledAudits: s}
			}),
			"DescribeScheduledAudit": iotDescribeStub("scheduledaudit", func(in *iot.DescribeScheduledAuditInput) *string { return in.ScheduledAuditName },
				func(n *string, arn string) any {
					return &iot.DescribeScheduledAuditOutput{ScheduledAuditName: n, ScheduledAuditArn: &arn}
				}),
		}},
		{TypeIoTMitigationAction, "mitigationaction", map[string]func(any) (any, error){
			"ListMitigationActions": iotListed(func(n string) iottypes.MitigationActionIdentifier {
				return iottypes.MitigationActionIdentifier{ActionName: ptrStr(n)}
			}, func(s []iottypes.MitigationActionIdentifier) any {
				return &iot.ListMitigationActionsOutput{ActionIdentifiers: s}
			}),
			"DescribeMitigationAction": iotDescribeStub("mitigationaction", func(in *iot.DescribeMitigationActionInput) *string { return in.ActionName },
				func(n *string, arn string) any {
					return &iot.DescribeMitigationActionOutput{ActionName: n, ActionArn: &arn}
				}),
		}},
		{TypeIoTSecurityProfile, "securityprofile", map[string]func(any) (any, error){
			"ListSecurityProfiles": iotListed(func(n string) iottypes.SecurityProfileIdentifier {
				return iottypes.SecurityProfileIdentifier{Name: ptrStr(n)}
			}, func(s []iottypes.SecurityProfileIdentifier) any {
				return &iot.ListSecurityProfilesOutput{SecurityProfileIdentifiers: s}
			}),
			"DescribeSecurityProfile": iotDescribeStub("securityprofile", func(in *iot.DescribeSecurityProfileInput) *string { return in.SecurityProfileName },
				func(n *string, arn string) any {
					return &iot.DescribeSecurityProfileOutput{SecurityProfileName: n, SecurityProfileArn: &arn}
				}),
		}},
		{TypeIoTCustomMetric, "custommetric", map[string]func(any) (any, error){
			"ListCustomMetrics": func(any) (any, error) { return &iot.ListCustomMetricsOutput{MetricNames: iotFixtureNames}, nil },
			"DescribeCustomMetric": iotDescribeStub("custommetric", func(in *iot.DescribeCustomMetricInput) *string { return in.MetricName },
				func(n *string, arn string) any {
					return &iot.DescribeCustomMetricOutput{MetricName: n, MetricArn: &arn}
				}),
		}},
		{TypeIoTDimension, "dimension", map[string]func(any) (any, error){
			"ListDimensions": func(any) (any, error) { return &iot.ListDimensionsOutput{DimensionNames: iotFixtureNames}, nil },
			"DescribeDimension": iotDescribeStub("dimension", func(in *iot.DescribeDimensionInput) *string { return in.Name },
				func(n *string, arn string) any { return &iot.DescribeDimensionOutput{Name: n, Arn: &arn} }),
		}},
		{TypeIoTCommand, "command", map[string]func(any) (any, error){
			"ListCommands": iotListed(func(n string) iottypes.CommandSummary { return iottypes.CommandSummary{CommandId: ptrStr(n)} },
				func(s []iottypes.CommandSummary) any { return &iot.ListCommandsOutput{Commands: s} }),
			"GetCommand": iotDescribeStub("command", func(in *iot.GetCommandInput) *string { return in.CommandId },
				func(n *string, arn string) any { return &iot.GetCommandOutput{CommandId: n, CommandArn: &arn} }),
		}},
		{TypeIoTJobTemplate, "jobtemplate", map[string]func(any) (any, error){
			"ListJobTemplates": iotListed(func(n string) iottypes.JobTemplateSummary {
				return iottypes.JobTemplateSummary{JobTemplateId: ptrStr(n)}
			},
				func(s []iottypes.JobTemplateSummary) any { return &iot.ListJobTemplatesOutput{JobTemplates: s} }),
			"DescribeJobTemplate": iotDescribeStub("jobtemplate", func(in *iot.DescribeJobTemplateInput) *string { return in.JobTemplateId },
				func(n *string, arn string) any {
					return &iot.DescribeJobTemplateOutput{JobTemplateId: n, JobTemplateArn: &arn}
				}),
		}},
		{TypeIoTFleetMetric, "fleetmetric", map[string]func(any) (any, error){
			"ListFleetMetrics": iotListed(func(n string) iottypes.FleetMetricNameAndArn {
				return iottypes.FleetMetricNameAndArn{MetricName: ptrStr(n)}
			},
				func(s []iottypes.FleetMetricNameAndArn) any { return &iot.ListFleetMetricsOutput{FleetMetrics: s} }),
			"DescribeFleetMetric": iotDescribeStub("fleetmetric", func(in *iot.DescribeFleetMetricInput) *string { return in.MetricName },
				func(n *string, arn string) any { return &iot.DescribeFleetMetricOutput{MetricName: n, MetricArn: &arn} }),
		}},
		{TypeIoTProvisioningTemplate, "provisioningtemplate", map[string]func(any) (any, error){
			"ListProvisioningTemplates": iotListed(func(n string) iottypes.ProvisioningTemplateSummary {
				return iottypes.ProvisioningTemplateSummary{TemplateName: ptrStr(n)}
			}, func(s []iottypes.ProvisioningTemplateSummary) any {
				return &iot.ListProvisioningTemplatesOutput{Templates: s}
			}),
			"DescribeProvisioningTemplate": iotDescribeStub("provisioningtemplate", func(in *iot.DescribeProvisioningTemplateInput) *string { return in.TemplateName },
				func(n *string, arn string) any {
					return &iot.DescribeProvisioningTemplateOutput{TemplateName: n, TemplateArn: &arn}
				}),
		}},
		{TypeIoTThingGroup, "thinggroup", map[string]func(any) (any, error){
			"ListThingGroups": iotListed(func(n string) iottypes.GroupNameAndArn { return iottypes.GroupNameAndArn{GroupName: ptrStr(n)} },
				func(s []iottypes.GroupNameAndArn) any { return &iot.ListThingGroupsOutput{ThingGroups: s} }),
			"DescribeThingGroup": iotDescribeStub("thinggroup", func(in *iot.DescribeThingGroupInput) *string { return in.ThingGroupName },
				func(n *string, arn string) any {
					return &iot.DescribeThingGroupOutput{ThingGroupName: n, ThingGroupArn: &arn}
				}),
		}},
		{TypeIoTThingType, "thingtype", map[string]func(any) (any, error){
			"ListThingTypes": iotListed(func(n string) iottypes.ThingTypeDefinition {
				return iottypes.ThingTypeDefinition{ThingTypeName: ptrStr(n)}
			},
				func(s []iottypes.ThingTypeDefinition) any { return &iot.ListThingTypesOutput{ThingTypes: s} }),
			"DescribeThingType": iotDescribeStub("thingtype", func(in *iot.DescribeThingTypeInput) *string { return in.ThingTypeName },
				func(n *string, arn string) any {
					return &iot.DescribeThingTypeOutput{ThingTypeName: n, ThingTypeArn: &arn}
				}),
		}},
		{TypeIoTBillingGroup, "billinggroup", map[string]func(any) (any, error){
			"ListBillingGroups": iotListed(func(n string) iottypes.GroupNameAndArn { return iottypes.GroupNameAndArn{GroupName: ptrStr(n)} },
				func(s []iottypes.GroupNameAndArn) any { return &iot.ListBillingGroupsOutput{BillingGroups: s} }),
			"DescribeBillingGroup": iotDescribeStub("billinggroup", func(in *iot.DescribeBillingGroupInput) *string { return in.BillingGroupName },
				func(n *string, arn string) any {
					return &iot.DescribeBillingGroupOutput{BillingGroupName: n, BillingGroupArn: &arn}
				}),
		}},
		{TypeIoTTopicRule, "rule", map[string]func(any) (any, error){
			"ListTopicRules": iotListed(func(n string) iottypes.TopicRuleListItem { return iottypes.TopicRuleListItem{RuleName: ptrStr(n)} },
				func(s []iottypes.TopicRuleListItem) any { return &iot.ListTopicRulesOutput{Rules: s} }),
			"GetTopicRule": iotDescribeStub("rule", func(in *iot.GetTopicRuleInput) *string { return in.RuleName },
				func(_ *string, arn string) any { return &iot.GetTopicRuleOutput{RuleArn: &arn} }),
		}},
		{TypeIoTJob, "job", map[string]func(any) (any, error){
			"ListJobs": iotListed(func(n string) iottypes.JobSummary {
				return iottypes.JobSummary{JobId: ptrStr(n), JobArn: ptrStr(iotTestARN("job", n))}
			}, func(s []iottypes.JobSummary) any { return &iot.ListJobsOutput{Jobs: s} }),
		}},
		{TypeIoTOTAUpdate, "otaupdate", map[string]func(any) (any, error){
			"ListOTAUpdates": iotListed(func(n string) iottypes.OTAUpdateSummary {
				return iottypes.OTAUpdateSummary{OtaUpdateId: ptrStr(n), OtaUpdateArn: ptrStr(iotTestARN("otaupdate", n))}
			}, func(s []iottypes.OTAUpdateSummary) any { return &iot.ListOTAUpdatesOutput{OtaUpdates: s} }),
		}},
		{TypeIoTStream, "stream", map[string]func(any) (any, error){
			"ListStreams": iotListed(func(n string) iottypes.StreamSummary {
				return iottypes.StreamSummary{StreamId: ptrStr(n), StreamArn: ptrStr(iotTestARN("stream", n))}
			}, func(s []iottypes.StreamSummary) any { return &iot.ListStreamsOutput{Streams: s} }),
		}},
	}
}

// iotFixtureFor returns the fixture of typ.
func iotFixtureFor(t *testing.T, typ string) iotTaggedFixture {
	t.Helper()
	for _, f := range iotTaggedFixtures() {
		if f.typ == typ {
			return f
		}
	}
	t.Fatalf("no fixture for %s", typ)
	return iotTaggedFixture{}
}

// iotFixtureHandlers merges every fixture's handlers with tags answering the
// tag reads; each "tagged" resource is given the tag type=<its type>.
func iotFixtureHandlers(tags *iotTagCalls) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){"ListTagsForResource": tags.handler}
	if tags.pages == nil {
		tags.pages = map[string][][]iottypes.Tag{}
	}
	for _, f := range iotTaggedFixtures() {
		maps.Copy(handlers, f.handlers)
		tags.pages[iotTestARN(f.kind, "tagged")] = [][]iottypes.Tag{{{Key: ptrStr("type"), Value: ptrStr(f.typ)}}}
	}
	return handlers
}

// Every tagged IoT type is stored with the tags ListTagsForResource returns
// for its own ARN, or none, and only those types' rows are tag-read.
func TestScanIoT_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tags := &iotTagCalls{}
	acct.cfg = opStubConfig(iotFixtureHandlers(tags), iotDenied)

	total, inserted, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	fixtures := iotTaggedFixtures()
	if total != 2*len(fixtures) || inserted != total {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, 2*len(fixtures), 2*len(fixtures))
	}
	var wantARNs []string
	for _, f := range fixtures {
		tagged, bare := iotTestARN(f.kind, "tagged"), iotTestARN(f.kind, "bare")
		wantARNs = append(wantARNs, tagged, bare)
		rows := rowsByNativeID(t, st, f.typ)
		if len(rows) != 2 {
			t.Errorf("%s: %d rows; want 2", f.typ, len(rows))
			continue
		}
		assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"type": f.typ})
		if got := rows[bare].TagsJSON; got != nil {
			t.Errorf("%s bare TagsJSON = %s; want nil", f.typ, *got)
		}
	}
	slices.Sort(wantARNs)
	if got := slices.Sorted(slices.Values(tags.arns)); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}
}

// runIoTPolicies runs the policy phase over the fixture policies, with
// handlers overriding the fixture's, and returns its joined result.
func runIoTPolicies(t *testing.T, st *store.Store, tags *iotTagCalls, handlers map[string]func(any) (any, error)) (phaseTotals, error) {
	t.Helper()
	all := map[string]func(any) (any, error){"ListTagsForResource": tags.handler}
	maps.Copy(all, iotFixtureFor(t, TypeIoTPolicy).handlers)
	maps.Copy(all, handlers)
	client := iot.NewFromConfig(opStubConfig(all, iotDenied))
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	if scanIoTPolicies(context.Background(), client, acct, testRegion, st, testScanID, &p, newIoTTags(client),
		iotListPolicies(context.Background(), client, acct, testRegion, testScanID)) {
		t.Fatal("storeWriteFailed = true; want false")
	}
	_, _, err := p.result(st, acct, testRegion)
	return p, err
}

// Tags on several pages are all read, the second page asked for with the
// first page's NextToken.
func TestIoTTagReader_ReadsEveryPage(t *testing.T) {
	st := newTestStore(t)
	arn := iotTestARN("policy", "tagged")
	tags := &iotTagCalls{pages: map[string][][]iottypes.Tag{arn: {
		{{Key: ptrStr("a"), Value: ptrStr("1")}},
		{{Key: ptrStr("b"), Value: ptrStr("2")}},
	}}}
	if _, err := runIoTPolicies(t, st, tags, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeIoTPolicy)[arn].TagsJSON, map[string]string{"a": "1", "b": "2"})
	var second []string
	for i, a := range tags.arns {
		if a == arn && tags.tokens[i] != "" {
			second = append(second, tags.tokens[i])
		}
	}
	if !slices.Equal(second, []string{"p2"}) {
		t.Errorf("follow-up NextTokens = %v; want [p2]", second)
	}
}

// A failed tag read keeps the policy without tags, except one IoT no longer
// finds, which is not stored; a denial warns once, a transient error and any
// other error are returned.
func TestScanIoTPolicies_TagReadFailures(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		kept     bool
		warns    int
		wantCode string
	}{
		{name: "not found", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "denied", err: iotDenied, kept: true, warns: 1},
		{name: "transient", err: apiErr("ThrottlingException", "Rate exceeded"), kept: true, wantCode: "ThrottlingException"},
		{name: "other error", err: iotInvalidRequest, kept: true, wantCode: "InvalidRequestException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			failing := iotTestARN("policy", "tagged")
			tags := &iotTagCalls{errs: map[string]error{failing: c.err}}
			p, err := runIoTPolicies(t, st, tags, nil)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
			}
			wantTotal := 1
			if c.kept {
				wantTotal = 2
			}
			if p.total != wantTotal {
				t.Errorf("total = %d; want %d", p.total, wantTotal)
			}
			rows := rowsByNativeID(t, st, TypeIoTPolicy)
			r, ok := rows[failing]
			if ok != c.kept {
				t.Fatalf("failing policy stored = %v; want %v", ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("failing policy TagsJSON = %s; want nil", *r.TagsJSON)
			}
			if _, ok := rows[iotTestARN("policy", "bare")]; !ok {
				t.Error("sibling policy not stored")
			}
			var got []store.ScanWarning
			for _, w := range *warns {
				if w.Service == iotListTagsOp {
					got = append(got, w)
				}
			}
			if len(got) != c.warns {
				t.Fatalf("%s warnings = %v; want %d", iotListTagsOp, got, c.warns)
			}
			if c.warns > 0 && got[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// A policy GetPolicy no longer finds is dropped silently; a denied GetPolicy
// drops the policy behind one warning; a response without an ARN is warned
// about and not stored; any other GetPolicy error drops the policy and is
// returned. The sibling is stored in every case.
func TestScanIoTPolicies_DescribeFailures(t *testing.T) {
	const getPolicy = "iot:GetPolicy"
	cases := []struct {
		name     string
		answer   func(n *string) (any, error)
		stored   int
		warns    int
		wantCode string
	}{
		{name: "not found", answer: func(*string) (any, error) { return nil, apiErr("ResourceNotFoundException", "gone") }, stored: 1},
		{name: "denied", answer: func(*string) (any, error) { return nil, iotDenied }, stored: 1, warns: 1},
		{name: "no ARN", answer: func(n *string) (any, error) { return &iot.GetPolicyOutput{PolicyName: n}, nil }, stored: 1, warns: 1},
		{name: "other error", answer: func(*string) (any, error) { return nil, iotInvalidRequest }, stored: 1, wantCode: "InvalidRequestException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, getPolicy)
			fixture := iotFixtureFor(t, TypeIoTPolicy).handlers["GetPolicy"]
			_, err := runIoTPolicies(t, st, &iotTagCalls{}, map[string]func(any) (any, error){
				"GetPolicy": func(in any) (any, error) {
					if n := in.(*iot.GetPolicyInput).PolicyName; *n == "tagged" {
						return c.answer(n)
					}
					return fixture(in)
				},
			})
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeIoTPolicy)
			if len(rows) != c.stored {
				t.Errorf("stored %d policies; want %d", len(rows), c.stored)
			}
			if _, ok := rows[iotTestARN("policy", "tagged")]; ok {
				t.Error("failing policy stored")
			}
			if *warns != c.warns {
				t.Errorf("%s warnings = %d; want %d", getPolicy, *warns, c.warns)
			}
		})
	}
}

// A job, OTA update or stream without an ARN is warned about and not stored,
// rather than dropped silently.
func TestScanIoTInventory_EmptyARNNotStored(t *testing.T) {
	cases := []struct {
		op, typ string
		out     any
	}{
		{"ListJobs", TypeIoTJob, &iot.ListJobsOutput{Jobs: []iottypes.JobSummary{{JobId: ptrStr("j")}}}},
		{"ListOTAUpdates", TypeIoTOTAUpdate, &iot.ListOTAUpdatesOutput{OtaUpdates: []iottypes.OTAUpdateSummary{{OtaUpdateId: ptrStr("o")}}}},
		{"ListStreams", TypeIoTStream, &iot.ListStreamsOutput{Streams: []iottypes.StreamSummary{{StreamId: ptrStr("s")}}}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iot:"+c.op)
			tags := &iotTagCalls{}
			client := iot.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
				"ListTagsForResource": tags.handler,
				c.op:                  func(any) (any, error) { return c.out, nil },
			}, iotDenied))
			var p phaseTotals
			acct := newTestAccount(testAccountID)
			scanIoTInventory(context.Background(), client, acct, testRegion, st, testScanID, &p, newIoTTags(client))
			if _, _, err := p.result(st, acct, testRegion); err != nil {
				t.Fatalf("err = %v", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%d rows stored; want 0", n)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d; want 1", *warns)
			}
			if len(tags.arns) != 0 {
				t.Errorf("tag reads = %v; want none", tags.arns)
			}
		})
	}
}

// ListJobs, ListOTAUpdates and ListStreams ask for the model's page cap.
func TestScanIoTInventory_ListsAtPageCap(t *testing.T) {
	var (
		mu  sync.Mutex
		got = map[string]int32{}
	)
	rec := func(op string, max *int32, out any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		got[op] = sdkaws.ToInt32(max)
		return out, nil
	}
	client := iot.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListJobs": func(in any) (any, error) {
			return rec("ListJobs", in.(*iot.ListJobsInput).MaxResults, &iot.ListJobsOutput{})
		},
		"ListOTAUpdates": func(in any) (any, error) {
			return rec("ListOTAUpdates", in.(*iot.ListOTAUpdatesInput).MaxResults, &iot.ListOTAUpdatesOutput{})
		},
		"ListStreams": func(in any) (any, error) {
			return rec("ListStreams", in.(*iot.ListStreamsInput).MaxResults, &iot.ListStreamsOutput{})
		},
	}, iotDenied))
	st := newTestStore(t)
	var p phaseTotals
	scanIoTInventory(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, &p, newIoTTags(client))
	want := map[string]int32{"ListJobs": 250, "ListOTAUpdates": 250, "ListStreams": 250}
	if !maps.Equal(got, want) {
		t.Errorf("MaxResults = %v; want %v", got, want)
	}
}

// A hard error in one phase does not stop the family's later phases or the
// other families: the later certs-family phase and another family's rows are
// stored, and the error is returned.
func TestScanIoT_PhaseErrorRunsEveryOtherPhase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	handlers := iotFixtureHandlers(&iotTagCalls{})
	handlers["ListCertificateProviders"] = func(any) (any, error) { return nil, iotInvalidRequest }
	acct.cfg = opStubConfig(handlers, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "InvalidRequestException")
	for _, typ := range []string{TypeIoTPolicy, TypeIoTDomainConfiguration, TypeIoTThingGroup, TypeIoTStream} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s: %d rows; want 2", typ, n)
		}
	}
}

// A family's error does not cancel the families still running: an op of
// another family, called after the failing op, is handed a ctx that nothing
// can cancel.
func TestScanIoT_FamilyErrorDoesNotCancelOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	failed := make(chan struct{})
	handlers := map[string]func(context.Context, any) (any, error){
		"ListCertificates": func(context.Context, any) (any, error) {
			close(failed)
			return nil, iotInvalidRequest
		},
		"ListThingGroups": func(ctx context.Context, in any) (any, error) {
			select {
			case <-failed:
			case <-time.After(10 * time.Second):
				t.Error("ListCertificates not called within 10s")
			}
			if ctx.Done() != nil {
				t.Error("the families share a cancellable ctx: a family's error can cancel the others")
			}
			return iotFixtureFor(t, TypeIoTThingGroup).handlers["ListThingGroups"](in)
		},
	}
	for op, h := range iotFixtureFor(t, TypeIoTThingGroup).handlers {
		if _, ok := handlers[op]; !ok {
			handlers[op] = func(_ context.Context, in any) (any, error) { return h(in) }
		}
	}
	tags := &iotTagCalls{}
	handlers["ListTagsForResource"] = func(_ context.Context, in any) (any, error) { return tags.handler(in) }
	acct.cfg = opStubConfigCtx(handlers, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "InvalidRequestException")
	if n := len(rowsByNativeID(t, st, TypeIoTThingGroup)); n != 2 {
		t.Errorf("thing groups stored = %d; want 2", n)
	}
}

// A tag throttle beside another phase's hard error is demoted to one
// warning under the tag op, so the service is reported as failed with the
// hard error, not as transient.
func TestScanIoT_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warns := countOpWarnings(st, iotListTagsOp)
	tags := &iotTagCalls{errs: map[string]error{iotTestARN("policy", "tagged"): apiErr("ThrottlingException", "Rate exceeded")}}
	handlers := iotFixtureHandlers(tags)
	handlers["ListDimensions"] = func(any) (any, error) { return nil, iotInvalidRequest }
	acct.cfg = opStubConfig(handlers, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
	}
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotListTagsOp, *warns)
	}
}

// A failed write stops the family it happens in and the phases after the
// families: no later certs-family op and no ListAuditSuppressions is called.
func TestScanIoT_FailedWriteStops(t *testing.T) {
	st, closeStore := openClosingStore(t)
	acct := newTestAccount(testAccountID)
	var (
		mu     sync.Mutex
		called = map[string]bool{}
	)
	handlers := iotFixtureHandlers(&iotTagCalls{})
	getPolicy := handlers["GetPolicy"]
	handlers["GetPolicy"] = func(in any) (any, error) {
		closeStore()
		return getPolicy(in)
	}
	for _, op := range []string{"ListRoleAliases", "ListAuditSuppressions"} {
		next := handlers[op]
		handlers[op] = func(in any) (any, error) {
			mu.Lock()
			called[op] = true
			mu.Unlock()
			if next == nil {
				return &iot.ListAuditSuppressionsOutput{}, nil
			}
			return next(in)
		}
	}
	acct.cfg = opStubConfig(handlers, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	for op, ok := range called {
		if ok {
			t.Errorf("%s called after the write failed", op)
		}
	}
}

// Provider-managed domain configurations (named iot:...) are stored without a
// tag read; a customer's one is tag-read.
func TestScanIoTDomainConfigurations_ManagedNotTagRead(t *testing.T) {
	st := newTestStore(t)
	managed, custom := iotTestARN("domainconfiguration", "iot:Data-ATS"), iotTestARN("domainconfiguration", "custom")
	tagged := [][]iottypes.Tag{{{Key: ptrStr("k"), Value: ptrStr("v")}}}
	tags := &iotTagCalls{pages: map[string][][]iottypes.Tag{managed: tagged, custom: tagged}}
	client := iot.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": tags.handler,
		"ListDomainConfigurations": func(any) (any, error) {
			return &iot.ListDomainConfigurationsOutput{DomainConfigurations: []iottypes.DomainConfigurationSummary{
				{DomainConfigurationName: ptrStr("iot:Data-ATS")}, {DomainConfigurationName: ptrStr("custom")},
			}}, nil
		},
		"DescribeDomainConfiguration": iotFixtureFor(t, TypeIoTDomainConfiguration).handlers["DescribeDomainConfiguration"],
	}, iotDenied))
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	if scanIoTDomainConfigurations(context.Background(), client, acct, testRegion, st, testScanID, &p, newIoTTags(client)) {
		t.Fatal("storeWriteFailed = true; want false")
	}
	if _, _, err := p.result(st, acct, testRegion); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(tags.arns, []string{custom}) {
		t.Errorf("ListTagsForResource ARNs = %v; want [%s]", tags.arns, custom)
	}
	rows := rowsByNativeID(t, st, TypeIoTDomainConfiguration)
	r, ok := rows[managed]
	if !ok || !r.ManagedByProvider {
		t.Fatalf("managed row stored = %v, ManagedByProvider = %v; want true, true", ok, r.ManagedByProvider)
	}
	if r.TagsJSON != nil {
		t.Errorf("managed row TagsJSON = %s; want nil", *r.TagsJSON)
	}
	assertTagsMap(t, rows[custom].TagsJSON, map[string]string{"k": "v"})
}

// A ListPolicies or ListThingGroups page that fails keeps the resources of the
// pages read before it and returns the error; every page asks for the model's
// page cap.
func TestScanIoT_ListPageErrorKeepsEarlierPages(t *testing.T) {
	cases := []struct {
		op, typ, kind string
		page          func(in any) (token *string, size int32)
		first         func(next *string) any
	}{
		{
			"ListPolicies", TypeIoTPolicy, "policy",
			func(in any) (*string, int32) {
				r := in.(*iot.ListPoliciesInput)
				return r.Marker, sdkaws.ToInt32(r.PageSize)
			},
			func(next *string) any {
				return &iot.ListPoliciesOutput{Policies: []iottypes.Policy{{PolicyName: ptrStr("tagged")}}, NextMarker: next}
			},
		},
		{
			"ListThingGroups", TypeIoTThingGroup, "thinggroup",
			func(in any) (*string, int32) {
				r := in.(*iot.ListThingGroupsInput)
				return r.NextToken, sdkaws.ToInt32(r.MaxResults)
			},
			func(next *string) any {
				return &iot.ListThingGroupsOutput{ThingGroups: []iottypes.GroupNameAndArn{{GroupName: ptrStr("tagged")}}, NextToken: next}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var (
				mu    sync.Mutex
				sizes []int32
			)
			handlers := iotFixtureHandlers(&iotTagCalls{})
			handlers[c.op] = func(in any) (any, error) {
				token, size := c.page(in)
				mu.Lock()
				sizes = append(sizes, size)
				mu.Unlock()
				if token == nil {
					return c.first(ptrStr("page2")), nil
				}
				return nil, iotInvalidRequest
			}
			acct.cfg = opStubConfig(handlers, iotDenied)

			_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "InvalidRequestException")
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[iotTestARN(c.kind, "tagged")]; !ok || len(rows) != 1 {
				t.Errorf("stored %d rows, first page's stored = %v; want 1, true", len(rows), ok)
			}
			if len(sizes) < 2 {
				t.Fatalf("%s called %d times; want at least 2", c.op, len(sizes))
			}
			for _, n := range sizes {
				if n != iotListPageCap {
					t.Errorf("%s page size = %v; want %d", c.op, sizes, iotListPageCap)
					break
				}
			}
		})
	}
}

// iotDenyA answers a describe for key "a" with a denial and any other key
// with out.
func iotDenyA[In any](key func(*In) *string, out func(n *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		n := key(in.(*In))
		if *n == "a" {
			return nil, iotDenied
		}
		return out(n), nil
	}
}

// A denied describe in an untagged phase, of an item whose list element has
// no ARN, drops that resource behind one warning under the describe op and
// stores its sibling.
func TestScanIoT_UntaggedDescribeDenialWarns(t *testing.T) {
	arn := func(n *string) *string { return ptrStr(iotTestARN("x", *n)) }
	cases := []struct {
		describeOp, typ string
		handlers        map[string]func(any) (any, error)
	}{
		{"DescribeCertificate", TypeIoTCertificate, map[string]func(any) (any, error){
			"ListCertificates": func(any) (any, error) {
				return &iot.ListCertificatesOutput{Certificates: []iottypes.Certificate{{CertificateId: ptrStr("a")}, {CertificateId: ptrStr("b")}}}, nil
			},
			"DescribeCertificate": iotDenyA(func(in *iot.DescribeCertificateInput) *string { return in.CertificateId }, func(n *string) any {
				return &iot.DescribeCertificateOutput{CertificateDescription: &iottypes.CertificateDescription{CertificateId: n, CertificateArn: arn(n)}}
			}),
		}},
		{"DescribeThing", TypeIoTThing, map[string]func(any) (any, error){
			"ListThings": func(any) (any, error) {
				return &iot.ListThingsOutput{Things: []iottypes.ThingAttribute{{ThingName: ptrStr("a")}, {ThingName: ptrStr("b")}}}, nil
			},
			"DescribeThing": iotDenyA(func(in *iot.DescribeThingInput) *string { return in.ThingName }, func(n *string) any {
				return &iot.DescribeThingOutput{ThingName: n, ThingArn: arn(n)}
			}),
		}},
		{"GetPackage", TypeIoTSoftwarePackage, map[string]func(any) (any, error){
			"ListPackages": func(any) (any, error) {
				return &iot.ListPackagesOutput{PackageSummaries: []iottypes.PackageSummary{{PackageName: ptrStr("a")}, {PackageName: ptrStr("b")}}}, nil
			},
			"GetPackage": iotDenyA(func(in *iot.GetPackageInput) *string { return in.PackageName }, func(n *string) any {
				return &iot.GetPackageOutput{PackageName: n, PackageArn: arn(n)}
			}),
		}},
		{"GetPackageVersion", TypeIoTSoftwarePackageVersion, map[string]func(any) (any, error){
			"ListPackages": func(any) (any, error) {
				return &iot.ListPackagesOutput{PackageSummaries: []iottypes.PackageSummary{{PackageName: ptrStr("p")}}}, nil
			},
			"ListPackageVersions": func(any) (any, error) {
				return &iot.ListPackageVersionsOutput{PackageVersionSummaries: []iottypes.PackageVersionSummary{{VersionName: ptrStr("a")}, {VersionName: ptrStr("b")}}}, nil
			},
			"GetPackageVersion": iotDenyA(func(in *iot.GetPackageVersionInput) *string { return in.VersionName }, func(n *string) any {
				return &iot.GetPackageVersionOutput{VersionName: n, PackageVersionArn: arn(n)}
			}),
		}},
		{"DescribeIndex", TypeIoTIndex, map[string]func(any) (any, error){
			"ListIndices": func(any) (any, error) { return &iot.ListIndicesOutput{IndexNames: []string{"a", "b"}}, nil },
			"DescribeIndex": iotDenyA(func(in *iot.DescribeIndexInput) *string { return in.IndexName }, func(n *string) any {
				return &iot.DescribeIndexOutput{IndexName: n}
			}),
		}},
	}
	for _, c := range cases {
		t.Run(c.describeOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iot:"+c.describeOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(c.handlers, iotDenied)

			if _, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("err = %v", err)
			}
			if *warns != 1 {
				t.Errorf("iot:%s warnings = %d; want 1", c.describeOp, *warns)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 1 {
				t.Errorf("%s rows = %d; want 1 (the sibling)", c.typ, n)
			}
		})
	}
}

// GetTopicRuleDestination models no ResourceNotFoundException, so one is
// returned rather than read as a destination IoT no longer finds.
func TestScanIoT_TopicRuleDestinationNotFoundReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTopicRuleDestinations": func(any) (any, error) {
			return &iot.ListTopicRuleDestinationsOutput{DestinationSummaries: []iottypes.TopicRuleDestinationSummary{{Arn: ptrStr("a")}}}, nil
		},
		"GetTopicRuleDestination": func(any) (any, error) { return nil, apiErr("ResourceNotFoundException", "gone") },
	}, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ResourceNotFoundException")
}

// One package's version listing failing does not stop the others': a denial
// warns once, any other error is returned, and the other package's versions
// are stored.
func TestScanIoTSoftware_PackageVersionListFailures(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warns := countOpWarnings(st, "iot:ListPackageVersions")
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListPackages": func(any) (any, error) {
			return &iot.ListPackagesOutput{PackageSummaries: []iottypes.PackageSummary{
				{PackageName: ptrStr("denied")}, {PackageName: ptrStr("failing")}, {PackageName: ptrStr("ok")},
			}}, nil
		},
		"GetPackage": func(in any) (any, error) {
			n := in.(*iot.GetPackageInput).PackageName
			return &iot.GetPackageOutput{PackageName: n, PackageArn: ptrStr(iotTestARN("package", *n))}, nil
		},
		"ListPackageVersions": func(in any) (any, error) {
			switch *in.(*iot.ListPackageVersionsInput).PackageName {
			case "denied":
				return nil, iotDenied
			case "failing":
				return nil, iotInvalidRequest
			}
			return &iot.ListPackageVersionsOutput{PackageVersionSummaries: []iottypes.PackageVersionSummary{{VersionName: ptrStr("v")}}}, nil
		},
		"GetPackageVersion": func(in any) (any, error) {
			r := in.(*iot.GetPackageVersionInput)
			return &iot.GetPackageVersionOutput{
				PackageName: r.PackageName, VersionName: r.VersionName,
				PackageVersionArn: ptrStr(iotTestARN("package", *r.PackageName+"/versions/"+*r.VersionName)),
			}, nil
		},
	}, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("iot:ListPackageVersions warnings = %d; want 1", *warns)
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTSoftwarePackageVersion)[iotTestARN("package", "ok/versions/v")]; !ok {
		t.Error("version of the listed package not stored")
	}
}

// A throttled ListPolicies or ListThings beside a hard error is demoted to one
// warning under that op: the phase and its principal phase share the one
// listing, and nothing is warned about under the principal op.
func TestScanIoT_PrincipalPhaseListErrorsLabelledByListOp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	want := map[string]int{"iot:ListPolicies": 1, "iot:ListThings": 1, "iot:ListPolicyPrincipals": 0, "iot:ListThingPrincipals": 0}
	got := map[string]*int{}
	for op := range want {
		got[op] = countOpWarnings(st, op)
	}
	throttle := func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
	handlers := iotFixtureHandlers(&iotTagCalls{})
	handlers["ListPolicies"] = throttle
	handlers["ListThings"] = throttle
	handlers["ListDimensions"] = func(any) (any, error) { return nil, iotInvalidRequest }
	acct.cfg = opStubConfig(handlers, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "InvalidRequestException")
	for op, n := range want {
		if *got[op] != n {
			t.Errorf("%s warnings = %d; want %d", op, *got[op], n)
		}
	}
}

// Every tag read of every family holds a slot of the one bound
// scanIoTFamilies is handed: while a read runs, fewer than fanoutMed slots
// are free. Each subtest gives one type rows and denies every other op, so
// the read checked is that type's family's; a family given a bound of its
// own leaves every slot free.
func TestScanIoT_TagReadsHoldTheSharedBound(t *testing.T) {
	for _, f := range iotTaggedFixtures() {
		t.Run(f.typ, func(t *testing.T) {
			var (
				mu    sync.Mutex
				calls int
				tags  iotTags
			)
			handlers := maps.Clone(f.handlers)
			handlers["ListTagsForResource"] = func(any) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if tags.slots.TryAcquire(fanoutMed) {
					tags.slots.Release(fanoutMed)
					t.Error("every slot of the shared bound free during a tag read")
				}
				return &iot.ListTagsForResourceOutput{}, nil
			}
			client := iot.NewFromConfig(opStubConfig(handlers, iotDenied))
			tags = newIoTTags(client)
			st := newTestStore(t)
			if _, _, err := scanIoTFamilies(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, tags); err != nil {
				t.Fatalf("err = %v", err)
			}
			if calls != len(iotFixtureNames) {
				t.Errorf("tag reads = %d; want %d", calls, len(iotFixtureNames))
			}
		})
	}
}

// iotKeyed answers a call, for the key that key reads from its input, with the error
// fail gives for it, or with out when it gives none.
func iotKeyed[In any](key func(*In) *string, fail func(string) error, out func(n *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		n := key(in.(*In))
		if err := fail(*n); err != nil {
			return nil, err
		}
		return out(n), nil
	}
}

// iotPerKeyCase is a per-key read over the listed keys "a", "b" and "c":
// handlers answer the read of each key with fail's error for it, and typ is
// the type of the rows the read gives.
type iotPerKeyCase struct {
	op, typ  string
	handlers func(fail func(string) error) map[string]func(any) (any, error)
}

func iotPerKeyCases() []iotPerKeyCase {
	arn := func(kind string) func(n *string) *string {
		return func(n *string) *string { return ptrStr(iotTestARN(kind, *n)) }
	}
	ok := func(string) error { return nil }
	noTags := func(any) (any, error) { return &iot.ListTagsForResourceOutput{}, nil }
	policies := func(any) (any, error) {
		return &iot.ListPoliciesOutput{Policies: []iottypes.Policy{{PolicyName: ptrStr("a")}, {PolicyName: ptrStr("b")}, {PolicyName: ptrStr("c")}}}, nil
	}
	getPolicy := func(fail func(string) error) func(any) (any, error) {
		return iotKeyed(func(in *iot.GetPolicyInput) *string { return in.PolicyName }, fail, func(n *string) any {
			return &iot.GetPolicyOutput{PolicyName: n, PolicyArn: arn("policy")(n)}
		})
	}
	things := func(any) (any, error) {
		return &iot.ListThingsOutput{Things: []iottypes.ThingAttribute{{ThingName: ptrStr("a")}, {ThingName: ptrStr("b")}, {ThingName: ptrStr("c")}}}, nil
	}
	describeThing := iotKeyed(func(in *iot.DescribeThingInput) *string { return in.ThingName }, ok, func(n *string) any {
		return &iot.DescribeThingOutput{ThingName: n, ThingArn: arn("thing")(n)}
	})
	packages := func(names ...string) func(any) (any, error) {
		return func(any) (any, error) {
			var s []iottypes.PackageSummary
			for _, n := range names {
				s = append(s, iottypes.PackageSummary{PackageName: ptrStr(n)})
			}
			return &iot.ListPackagesOutput{PackageSummaries: s}, nil
		}
	}
	getPackage := iotKeyed(func(in *iot.GetPackageInput) *string { return in.PackageName }, ok, func(n *string) any {
		return &iot.GetPackageOutput{PackageName: n, PackageArn: arn("package")(n)}
	})
	getPackageVersion := func(fail func(string) error) func(any) (any, error) {
		return func(in any) (any, error) {
			r := in.(*iot.GetPackageVersionInput)
			if err := fail(*r.VersionName); err != nil {
				return nil, err
			}
			return &iot.GetPackageVersionOutput{
				PackageName: r.PackageName, VersionName: r.VersionName,
				PackageVersionArn: ptrStr(iotTestARN("package", *r.PackageName+"/versions/"+*r.VersionName)),
			}, nil
		}
	}
	principals := func(n *string) []string { return []string{"cert-" + *n} }
	return []iotPerKeyCase{
		{"GetPolicy", TypeIoTPolicy, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){"ListPolicies": policies, "GetPolicy": getPolicy(fail), "ListTagsForResource": noTags}
		}},
		{"DescribeCertificate", TypeIoTCertificate, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListCertificates": func(any) (any, error) {
					return &iot.ListCertificatesOutput{Certificates: []iottypes.Certificate{{CertificateId: ptrStr("a")}, {CertificateId: ptrStr("b")}, {CertificateId: ptrStr("c")}}}, nil
				},
				"DescribeCertificate": iotKeyed(func(in *iot.DescribeCertificateInput) *string { return in.CertificateId }, fail, func(n *string) any {
					return &iot.DescribeCertificateOutput{CertificateDescription: &iottypes.CertificateDescription{CertificateId: n, CertificateArn: arn("cert")(n)}}
				}),
			}
		}},
		{"GetPackageVersion", TypeIoTSoftwarePackageVersion, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListPackages": packages("p"), "GetPackage": getPackage,
				"ListPackageVersions": func(any) (any, error) {
					return &iot.ListPackageVersionsOutput{PackageVersionSummaries: []iottypes.PackageVersionSummary{
						{VersionName: ptrStr("a")}, {VersionName: ptrStr("b")}, {VersionName: ptrStr("c")},
					}}, nil
				},
				"GetPackageVersion": getPackageVersion(fail),
			}
		}},
		{"ListPackageVersions", TypeIoTSoftwarePackageVersion, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListPackages": packages("a", "b", "c"), "GetPackage": getPackage,
				"ListPackageVersions": iotKeyed(func(in *iot.ListPackageVersionsInput) *string { return in.PackageName }, fail, func(*string) any {
					return &iot.ListPackageVersionsOutput{PackageVersionSummaries: []iottypes.PackageVersionSummary{{VersionName: ptrStr("v")}}}
				}),
				"GetPackageVersion": getPackageVersion(ok),
			}
		}},
		{"ListThingPrincipals", TypeIoTThingPrincipalAttachment, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListThings": things, "DescribeThing": describeThing,
				"ListThingPrincipals": iotKeyed(func(in *iot.ListThingPrincipalsInput) *string { return in.ThingName }, fail, func(n *string) any {
					return &iot.ListThingPrincipalsOutput{Principals: principals(n)}
				}),
			}
		}},
		{"ListPolicyPrincipals", TypeIoTPolicyPrincipalAttachment, func(fail func(string) error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListPolicies": policies, "GetPolicy": getPolicy(ok), "ListTagsForResource": noTags,
				"ListPolicyPrincipals": iotKeyed(func(in *iot.ListPolicyPrincipalsInput) *string { return in.PolicyName }, fail, func(n *string) any {
					return &iot.ListPolicyPrincipalsOutput{Principals: principals(n)}
				}),
			}
		}},
	}
}

// iotPerKeyCaseNamed is the case of op.
func iotPerKeyCaseNamed(op string) iotPerKeyCase {
	for _, c := range iotPerKeyCases() {
		if c.op == op {
			return c
		}
	}
	panic("no per-key case " + op)
}

// iotFailOn fails the keys in errs with their error.
func iotFailOn(errs map[string]error) func(string) error {
	return func(n string) error { return errs[n] }
}

// A per-key read still throttled after the SDK's retries is returned as a
// transient error, so the scan is reported partial, and the other keys are
// stored; beside a hard error of another key it is demoted to one warning
// under its op, and the hard error is returned.
func TestScanIoT_PerKeyTransient(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, c := range iotPerKeyCases() {
		t.Run(c.op+"/alone", func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(c.handlers(iotFailOn(map[string]error{"a": throttle})), iotDenied)

			_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeTransient {
				t.Errorf("classifyServiceError = %v; want outcomeTransient (err %v)", got, err)
			}
			assertAPICode(t, err, "ThrottlingException")
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2", c.typ, n)
			}
		})
		t.Run(c.op+"/beside a hard error", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iot:"+c.op)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(c.handlers(iotFailOn(map[string]error{"a": throttle, "b": iotInvalidRequest})), iotDenied)

			_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
			}
			assertAPICode(t, err, "InvalidRequestException")
			if *warns != 1 {
				t.Errorf("iot:%s warnings = %d; want 1", c.op, *warns)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 1 {
				t.Errorf("%s rows = %d; want 1", c.typ, n)
			}
		})
	}
}

// A denial of a per-key read warns only after the rows are written: when the
// write fails, nothing is warned about under the op.
func TestScanIoT_FailedWriteSendsNoDenialWarning(t *testing.T) {
	for _, c := range iotPerKeyCases() {
		t.Run(c.op, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := countOpWarnings(st, "iot:"+c.op)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(c.handlers(func(n string) error {
				switch n {
				case "a":
					return iotDenied
				case "c":
					closeStore()
				}
				return nil
			}), iotDenied)

			_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("iot:%s warnings = %d; want 0", c.op, *warns)
			}
		})
	}
}

// iotMembers counts the members of a joined error; nil has none.
func iotMembers(err error) int {
	if err == nil {
		return 0
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return len(j.Unwrap())
	}
	return 1
}

// A ctx that ends during the per-key reads adds exactly one member to the
// hard error, whether keys were left unread (and reads cut short) or only
// reads were cut short, and every key, read or not, keeps its fallback row.
func TestIoTReadEach_CtxEndIsOneMember(t *testing.T) {
	cases := []struct {
		name string
		keys int
	}{
		{"keys left unread", fanoutMed + 1},
		{"reads cut short", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			keys := make([]string, c.keys)
			for i := range keys {
				keys[i] = fmt.Sprint(i)
			}
			d := iotReadEach(ctx, st, newTestAccount(testAccountID), testRegion, "iot:Op", "items", keys,
				func(k string) string { return k }, neverSkip, "kept",
				func(k string) *store.Resource { return &store.Resource{NativeID: k} },
				func(ctx context.Context, k string) iotRead {
					if k == "0" {
						cancel()
					}
					<-ctx.Done()
					return iotRead{err: ctx.Err()}
				})
			if n := iotMembers(d.hard); n != 1 || !errors.Is(d.hard, context.Canceled) {
				t.Errorf("hard = %v (%d members); want one member wrapping context.Canceled", d.hard, n)
			}
			if len(d.rows) != c.keys {
				t.Errorf("rows = %d; want %d, one per key", len(d.rows), c.keys)
			}
		})
	}
}

// Once ctx ends, no further package's versions are listed, and one error
// says how many were not.
func TestScanIoTSoftwarePackageVersions_CtxEndIsOneMember(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var listed []string
	client := iot.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListPackageVersions": func(in any) (any, error) {
			listed = append(listed, *in.(*iot.ListPackageVersionsInput).PackageName)
			cancel()
			return nil, context.Canceled
		},
	}, iotDenied))
	var p phaseTotals
	scanIoTSoftwarePackageVersions(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, &p, []string{"a", "b", "c"})
	if !slices.Equal(listed, []string{"a"}) {
		t.Errorf("packages listed = %v; want [a]", listed)
	}
	n := 0
	for _, f := range p.errs {
		if f.op == "iot:ListPackageVersions" {
			n += iotMembers(f.err)
		}
	}
	if n != 1 {
		t.Errorf("iot:ListPackageVersions error members = %d; want 1 (%v)", n, p.errs)
	}
}

// A denied describe of a listed item that carries its ARN stores the item as
// its list element, verbatim, behind one warning under the describe op, and
// a tagged type still has its tags read; an item without an ARN in its list
// element is dropped behind the warning.
func TestScanIoT_DescribeDeniedKeepsListedElement(t *testing.T) {
	cases := []struct {
		describeOp, typ, kind string
		tagged                bool
		listed                any
		handlers              map[string]func(any) (any, error)
	}{
		{
			"GetPolicy", TypeIoTPolicy, "policy", true,
			iottypes.Policy{PolicyName: ptrStr("a"), PolicyArn: ptrStr(iotTestARN("policy", "a"))},
			map[string]func(any) (any, error){
				"ListPolicies": func(any) (any, error) {
					return &iot.ListPoliciesOutput{Policies: []iottypes.Policy{{PolicyName: ptrStr("a"), PolicyArn: ptrStr(iotTestARN("policy", "a"))}}}, nil
				},
			},
		},
		{
			"DescribeThingGroup", TypeIoTThingGroup, "thinggroup", true,
			iottypes.GroupNameAndArn{GroupName: ptrStr("a"), GroupArn: ptrStr(iotTestARN("thinggroup", "a"))},
			map[string]func(any) (any, error){
				"ListThingGroups": func(any) (any, error) {
					return &iot.ListThingGroupsOutput{ThingGroups: []iottypes.GroupNameAndArn{{GroupName: ptrStr("a"), GroupArn: ptrStr(iotTestARN("thinggroup", "a"))}}}, nil
				},
			},
		},
		{
			"DescribeThing", TypeIoTThing, "thing", false,
			iottypes.ThingAttribute{ThingName: ptrStr("a"), ThingArn: ptrStr(iotTestARN("thing", "a"))},
			map[string]func(any) (any, error){
				"ListThings": func(any) (any, error) {
					return &iot.ListThingsOutput{Things: []iottypes.ThingAttribute{{ThingName: ptrStr("a"), ThingArn: ptrStr(iotTestARN("thing", "a"))}}}, nil
				},
			},
		},
		{
			"GetTopicRuleDestination", TypeIoTTopicRuleDestination, "destination", false,
			iottypes.TopicRuleDestinationSummary{Arn: ptrStr(iotTestARN("destination", "a"))},
			map[string]func(any) (any, error){
				"ListTopicRuleDestinations": func(any) (any, error) {
					return &iot.ListTopicRuleDestinationsOutput{DestinationSummaries: []iottypes.TopicRuleDestinationSummary{{Arn: ptrStr(iotTestARN("destination", "a"))}}}, nil
				},
			},
		},
		{
			"DescribeRoleAlias", TypeIoTRoleAlias, "rolealias", true, nil,
			map[string]func(any) (any, error){
				"ListRoleAliases": func(any) (any, error) { return &iot.ListRoleAliasesOutput{RoleAliases: []string{"a"}}, nil },
			},
		},
	}
	for _, c := range cases {
		t.Run(c.describeOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iot:"+c.describeOp)
			arn := iotTestARN(c.kind, "a")
			tags := &iotTagCalls{pages: map[string][][]iottypes.Tag{arn: {{{Key: ptrStr("k"), Value: ptrStr("v")}}}}}
			handlers := maps.Clone(c.handlers)
			handlers["ListTagsForResource"] = tags.handler
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(handlers, iotDenied)

			if _, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("err = %v", err)
			}
			if *warns != 1 {
				t.Errorf("iot:%s warnings = %d; want 1", c.describeOp, *warns)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if c.listed == nil {
				if len(rows) != 0 {
					t.Errorf("%s rows = %d; want 0", c.typ, len(rows))
				}
				return
			}
			r, ok := rows[arn]
			if !ok {
				t.Fatalf("listed %s not stored", c.typ)
			}
			assertJSONEqual(t, c.typ+" attributes", r.AttributesJSON, mustJSON(c.listed))
			if c.tagged {
				assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
			}
		})
	}
}

// A principal listing: a key IoT no longer finds is dropped silently; a
// denied first page drops the key's principals behind one warning; a denied
// later page keeps the principals read before it behind one warning; any
// other failure keeps them too and is returned. The keys are the earlier
// phase's listing, not listed again, and every page asks for the model's
// page cap.
func TestScanIoT_PrincipalListing(t *testing.T) {
	type principalOp struct {
		op, listOp, typ, kind string
		handlers              func(page func(key string, token *string, size int32) (any, error)) map[string]func(any) (any, error)
	}
	ops := []principalOp{
		{
			"ListThingPrincipals", "ListThings", TypeIoTThingPrincipalAttachment, "thing",
			func(page func(string, *string, int32) (any, error)) map[string]func(any) (any, error) {
				c := iotPerKeyCaseNamed("ListThingPrincipals")
				h := c.handlers(func(string) error { return nil })
				h["ListThingPrincipals"] = func(in any) (any, error) {
					r := in.(*iot.ListThingPrincipalsInput)
					return page(*r.ThingName, r.NextToken, sdkaws.ToInt32(r.MaxResults))
				}
				return h
			},
		},
		{
			"ListPolicyPrincipals", "ListPolicies", TypeIoTPolicyPrincipalAttachment, "policy",
			func(page func(string, *string, int32) (any, error)) map[string]func(any) (any, error) {
				c := iotPerKeyCaseNamed("ListPolicyPrincipals")
				h := c.handlers(func(string) error { return nil })
				h["ListPolicyPrincipals"] = func(in any) (any, error) {
					r := in.(*iot.ListPolicyPrincipalsInput)
					return page(*r.PolicyName, r.Marker, sdkaws.ToInt32(r.PageSize))
				}
				return h
			},
		},
	}
	cases := []struct {
		name     string
		second   error // the error of key a's second page; nil for none
		first    error // the error of key a's first page
		keptA    int
		warns    int
		wantCode string
	}{
		{name: "not found", first: apiErr("ResourceNotFoundException", "gone")},
		{name: "not found on a later page", second: apiErr("ResourceNotFoundException", "gone")},
		{name: "denied", first: iotDenied, warns: 1},
		{name: "denied on a later page", second: iotDenied, keptA: 1, warns: 1},
		{name: "other error on a later page", second: iotInvalidRequest, keptA: 1, wantCode: "InvalidRequestException"},
		{name: "every page read", keptA: 2},
	}
	for _, o := range ops {
		for _, c := range cases {
			t.Run(o.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "iot:"+o.op)
				var (
					mu    sync.Mutex
					sizes []int32
				)
				listCalls := 0
				handlers := o.handlers(func(key string, token *string, size int32) (any, error) {
					mu.Lock()
					sizes = append(sizes, size)
					mu.Unlock()
					if key != "a" {
						return principalsOut(o.op, []string{"cert-" + key}, nil), nil
					}
					if token == nil {
						if c.first != nil {
							return nil, c.first
						}
						return principalsOut(o.op, []string{"cert-a1"}, ptrStr("p2")), nil
					}
					if c.second != nil {
						return nil, c.second
					}
					return principalsOut(o.op, []string{"cert-a2"}, nil), nil
				})
				list := handlers[o.listOp]
				handlers[o.listOp] = func(in any) (any, error) {
					mu.Lock()
					listCalls++
					mu.Unlock()
					return list(in)
				}
				acct := newTestAccount(testAccountID)
				acct.cfg = opStubConfig(handlers, iotDenied)

				_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
				if c.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertAPICode(t, err, c.wantCode)
				}
				if *warns != c.warns {
					t.Errorf("iot:%s warnings = %d; want %d", o.op, *warns, c.warns)
				}
				keptA := 0
				rows := rowsByNativeID(t, st, o.typ)
				for _, p := range []string{"cert-a1", "cert-a2"} {
					if _, ok := rows[fmt.Sprintf("arn:aws:iot:%s:%s:%s/a/principal/%s", testRegion, testAccountID, o.kind, p)]; ok {
						keptA++
					}
				}
				if keptA != c.keptA || len(rows) != c.keptA+2 {
					t.Errorf("rows = %d, of key a %d; want %d, %d", len(rows), keptA, c.keptA+2, c.keptA)
				}
				if listCalls != 1 {
					t.Errorf("%s calls = %d; want 1", o.listOp, listCalls)
				}
				for _, n := range sizes {
					if n != iotListPageCap {
						t.Errorf("%s page sizes = %v; want %d", o.op, sizes, iotListPageCap)
						break
					}
				}
			})
		}
	}
}

// principalsOut is op's output page of principals, next its continuation.
func principalsOut(op string, principals []string, next *string) any {
	if op == "ListThingPrincipals" {
		return &iot.ListThingPrincipalsOutput{Principals: principals, NextToken: next}
	}
	return &iot.ListPolicyPrincipalsOutput{Principals: principals, NextMarker: next}
}

// An NXDOMAIN after an earlier call to the endpoint succeeded is a failure of
// the scan, not a region IoT is absent from, so it is not reported as one:
// from a describe, from a package's version listing with nothing stored, and
// from any phase's listing once rows were stored, whose counts are kept.
func TestScanIoT_LateNXDOMAINNotRegionUnavailable(t *testing.T) {
	nx := func(any) (any, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "iot.us-east-1.amazonaws.com", IsNotFound: true}
	}
	cases := []struct {
		name      string
		handlers  func() map[string]func(any) (any, error)
		wantTotal int
	}{
		{"describe", func() map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListPolicies": func(any) (any, error) {
					return &iot.ListPoliciesOutput{Policies: []iottypes.Policy{{PolicyName: ptrStr("a")}}}, nil
				},
				"GetPolicy": nx,
			}
		}, 0},
		{"package versions", func() map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){
				"ListPackages": func(any) (any, error) {
					return &iot.ListPackagesOutput{PackageSummaries: []iottypes.PackageSummary{{PackageName: ptrStr("p")}}}, nil
				},
				"ListPackageVersions": nx,
			}
		}, 0},
		{"a later listing", func() map[string]func(any) (any, error) {
			h := iotFixtureHandlers(&iotTagCalls{})
			h["ListDimensions"] = nx
			return h
		}, 2*len(iotTaggedFixtures()) - 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(c.handlers(), iotDenied)

			total, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// The listings with a cap other than 250 ask for their own.
func TestScanIoT_ListsAtTheirPageCaps(t *testing.T) {
	var (
		mu  sync.Mutex
		got = map[string]int32{}
	)
	rec := func(op string, size *int32, out any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		got[op] = sdkaws.ToInt32(size)
		return out, nil
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTopicRules": func(in any) (any, error) {
			return rec("ListTopicRules", in.(*iot.ListTopicRulesInput).MaxResults, &iot.ListTopicRulesOutput{})
		},
		"ListTopicRuleDestinations": func(in any) (any, error) {
			return rec("ListTopicRuleDestinations", in.(*iot.ListTopicRuleDestinationsInput).MaxResults, &iot.ListTopicRuleDestinationsOutput{})
		},
		"ListCommands": func(in any) (any, error) {
			return rec("ListCommands", in.(*iot.ListCommandsInput).MaxResults, &iot.ListCommandsOutput{})
		},
		"ListPackages": func(in any) (any, error) {
			return rec("ListPackages", in.(*iot.ListPackagesInput).MaxResults, &iot.ListPackagesOutput{
				PackageSummaries: []iottypes.PackageSummary{{PackageName: ptrStr("p")}},
			})
		},
		"ListPackageVersions": func(in any) (any, error) {
			return rec("ListPackageVersions", in.(*iot.ListPackageVersionsInput).MaxResults, &iot.ListPackageVersionsOutput{})
		},
		"ListIndices": func(in any) (any, error) {
			return rec("ListIndices", in.(*iot.ListIndicesInput).MaxResults, &iot.ListIndicesOutput{})
		},
	}, iotDenied)

	if _, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := map[string]int32{
		"ListTopicRules": 10000, "ListTopicRuleDestinations": 1000, "ListCommands": 100,
		"ListPackages": 100, "ListPackageVersions": 100, "ListIndices": 500,
	}
	if !maps.Equal(got, want) {
		t.Errorf("page sizes = %v; want %v", got, want)
	}
}

// A rescan of unchanged resources reports them all and none as inserted.
func TestScanIoT_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(iotFixtureHandlers(&iotTagCalls{}), iotDenied)
	want := 2 * len(iotTaggedFixtures())
	for i, wantInserted := range []int{want, 0} {
		total, inserted, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
		if err != nil {
			t.Fatalf("scan %d: err = %v", i+1, err)
		}
		if total != want || inserted != wantInserted {
			t.Errorf("scan %d: total, inserted = %d, %d; want %d, %d", i+1, total, inserted, want, wantInserted)
		}
	}
}

// A denied describe of an AWS-default domain configuration stores its list
// element as a provider-managed row, without a tag read.
func TestScanIoT_DescribeDeniedManagedDomainKeptUntagged(t *testing.T) {
	st := newTestStore(t)
	arn := iotTestARN("domainconfiguration", "iot:Data-ATS")
	tags := &iotTagCalls{pages: map[string][][]iottypes.Tag{arn: {{{Key: ptrStr("k"), Value: ptrStr("v")}}}}}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": tags.handler,
		"ListDomainConfigurations": func(any) (any, error) {
			return &iot.ListDomainConfigurationsOutput{DomainConfigurations: []iottypes.DomainConfigurationSummary{
				{DomainConfigurationName: ptrStr("iot:Data-ATS"), DomainConfigurationArn: &arn},
			}}, nil
		},
	}, iotDenied)

	if _, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeIoTDomainConfiguration)[arn]
	if !ok || !r.ManagedByProvider || r.TagsJSON != nil {
		t.Errorf("stored = %v, ManagedByProvider = %v, TagsJSON = %v; want both true and no tags", ok, r.ManagedByProvider, r.TagsJSON)
	}
	if len(tags.arns) != 0 {
		t.Errorf("tag reads = %v; want none", tags.arns)
	}
}

// A write that fails beside a listing's NXDOMAIN, in one error, is still
// reported as a failed write, not flattened into a plain error, though an
// earlier phase stored rows. Only the CA-certificate phase, which runs before
// the policy phase in the same family, and the policy phase have rows, so the
// policy write is the only one that fails.
func TestScanIoT_FailedWriteBesideNXDOMAIN(t *testing.T) {
	st, closeStore := openClosingStore(t)
	acct := newTestAccount(testAccountID)
	policy := iotFixtureFor(t, TypeIoTPolicy).handlers
	cacert := iotFixtureFor(t, TypeIoTCACertificate).handlers
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListCACertificates":    cacert["ListCACertificates"],
		"DescribeCACertificate": cacert["DescribeCACertificate"],
		"ListTagsForResource":   func(any) (any, error) { return &iot.ListTagsForResourceOutput{}, nil },
		"ListPolicies": func(in any) (any, error) {
			if in.(*iot.ListPoliciesInput).Marker != nil {
				return nil, &net.DNSError{Err: "no such host", Name: "iot.us-east-1.amazonaws.com", IsNotFound: true}
			}
			out, err := policy["ListPolicies"](in)
			out.(*iot.ListPoliciesOutput).NextMarker = ptrStr("m2")
			return out, err
		},
		"GetPolicy": func(in any) (any, error) {
			closeStore()
			return policy["GetPolicy"](in)
		},
	}, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
}

// A describe that fails transiently or any other way, of a listed item that
// carries its ARN, still stores the item as its list element, tags read, and
// the error is returned; a not-found drops it.
func TestScanIoT_DescribeFailureKeepsListedElement(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		kept     bool
		outcome  serviceOutcome
		wantCode string
	}{
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), true, outcomeTransient, "ThrottlingException"},
		{"other error", iotInvalidRequest, true, outcomeError, "InvalidRequestException"},
		{"not found", apiErr("ResourceNotFoundException", "gone"), false, outcomeError, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			arn := iotTestARN("thinggroup", "a")
			listed := iottypes.GroupNameAndArn{GroupName: ptrStr("a"), GroupArn: &arn}
			tags := &iotTagCalls{pages: map[string][][]iottypes.Tag{arn: {{{Key: ptrStr("k"), Value: ptrStr("v")}}}}}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(map[string]func(any) (any, error){
				"ListTagsForResource": tags.handler,
				"ListThingGroups": func(any) (any, error) {
					return &iot.ListThingGroupsOutput{ThingGroups: []iottypes.GroupNameAndArn{listed}}, nil
				},
				"DescribeThingGroup": func(any) (any, error) { return nil, c.err },
			}, iotDenied)

			_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
				if got := classifyServiceError(err); got != c.outcome {
					t.Errorf("classifyServiceError = %v; want %v", got, c.outcome)
				}
			}
			r, ok := rowsByNativeID(t, st, TypeIoTThingGroup)[arn]
			if ok != c.kept {
				t.Fatalf("stored = %v; want %v", ok, c.kept)
			}
			if ok {
				assertJSONEqual(t, "thing group attributes", r.AttributesJSON, mustJSON(listed))
				if sv(r.Name) != "a" {
					t.Errorf("Name = %q; want a", sv(r.Name))
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
			}
		})
	}
}

// An NXDOMAIN with nothing stored is left as is, so the dispatcher reports
// the region unavailable.
func TestScanIoT_NXDOMAINWithNothingStoredIsRegionUnavailable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(nil, &net.DNSError{Err: "no such host", Name: "iot.us-east-1.amazonaws.com", IsNotFound: true})

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError = %v; want outcomeUnavailable (err %v)", got, err)
	}
}

// A rescan that stores only rows it already had still keeps its counts
// beside a late NXDOMAIN.
func TestScanIoT_RescanKeepsCountsBesideLateNXDOMAIN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	handlers := iotFixtureHandlers(&iotTagCalls{})
	handlers["ListDimensions"] = func(any) (any, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "iot.us-east-1.amazonaws.com", IsNotFound: true}
	}
	acct.cfg = opStubConfig(handlers, iotDenied)
	want := 2*len(iotTaggedFixtures()) - 2
	for i := range 2 {
		total, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("scan %d: classifyServiceError = %v; want outcomeError (err %v)", i+1, got, err)
		}
		if total != want {
			t.Errorf("scan %d: total = %d; want %d", i+1, total, want)
		}
	}
}

// A describe answered without an ARN, of a listed item that carries its ARN,
// stores the item as its list element behind one warning.
func TestScanIoT_DescribeWithoutARNKeepsListedElement(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iot:DescribeThingGroup")
	arn := iotTestARN("thinggroup", "a")
	listed := iottypes.GroupNameAndArn{GroupName: ptrStr("a"), GroupArn: &arn}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &iot.ListTagsForResourceOutput{}, nil },
		"ListThingGroups": func(any) (any, error) {
			return &iot.ListThingGroupsOutput{ThingGroups: []iottypes.GroupNameAndArn{listed}}, nil
		},
		"DescribeThingGroup": func(any) (any, error) { return &iot.DescribeThingGroupOutput{ThingGroupName: ptrStr("a")}, nil },
	}, iotDenied)

	if _, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeIoTThingGroup)[arn]
	if !ok {
		t.Fatal("thing group not stored")
	}
	assertJSONEqual(t, "thing group attributes", r.AttributesJSON, mustJSON(listed))
	if *warns != 1 {
		t.Errorf("iot:DescribeThingGroup warnings = %d; want 1", *warns)
	}
}

// A throttled describe whose item is stored as its list element, beside
// another key's hard error, is demoted to one warning, and the hard error is
// returned; both items are stored.
func TestScanIoT_FallbackThrottleBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iot:DescribeThingGroup")
	var groups []iottypes.GroupNameAndArn
	for _, n := range []string{"a", "b"} {
		groups = append(groups, iottypes.GroupNameAndArn{GroupName: ptrStr(n), GroupArn: ptrStr(iotTestARN("thinggroup", n))})
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &iot.ListTagsForResourceOutput{}, nil },
		"ListThingGroups":     func(any) (any, error) { return &iot.ListThingGroupsOutput{ThingGroups: groups}, nil },
		"DescribeThingGroup": iotKeyed(func(in *iot.DescribeThingGroupInput) *string { return in.ThingGroupName },
			iotFailOn(map[string]error{"a": apiErr("ThrottlingException", "Rate exceeded"), "b": iotInvalidRequest}),
			func(*string) any { return nil }),
	}, iotDenied)

	_, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
	}
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("iot:DescribeThingGroup warnings = %d; want 1", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeIoTThingGroup)); n != 2 {
		t.Errorf("thing groups stored = %d; want 2", n)
	}
}
