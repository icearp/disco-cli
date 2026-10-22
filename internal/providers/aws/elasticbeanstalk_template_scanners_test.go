package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

const (
	ebTestAppWeb     = "arn:aws:elasticbeanstalk:us-east-1:123456789012:application/web"
	ebTestAppWorker  = "arn:aws:elasticbeanstalk:us-east-1:123456789012:application/worker"
	ebTestAppBare    = "arn:aws:elasticbeanstalk:us-east-1:123456789012:application/bare"
	ebTestEnv        = "arn:aws:elasticbeanstalk:us-east-1:123456789012:environment/web/web-prod"
	ebTestVersion    = "arn:aws:elasticbeanstalk:us-east-1:123456789012:applicationversion/web/v1"
	ebTestPlatform   = "arn:aws:elasticbeanstalk:us-east-1:123456789012:platform/custom/1.0.0"
	ebTestTplBase    = "arn:aws:elasticbeanstalk:us-east-1:123456789012:configurationtemplate/web/base"
	ebTestTplProd    = "arn:aws:elasticbeanstalk:us-east-1:123456789012:configurationtemplate/web/prod/v2"
	ebTestTplWorker1 = "arn:aws:elasticbeanstalk:us-east-1:123456789012:configurationtemplate/worker/w1"
)

var (
	ebDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: elasticbeanstalk:DescribeConfigurationSettings")
	ebBadInput = apiErr("TooManyBucketsException", "bad")
	ebGone     = apiErr("InvalidParameterValue", "No Configuration Template named 'x' found.")
	ebCreated  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// ebSettingCases are the "base" template's option settings and the value the
// store must hold for each. In a strict namespace (environment properties,
// JVM options, SNS topics), in any letter case, or with no namespace, only
// references, booleans and EC2 resource ids are kept and every other value,
// however innocuous, is replaced by its reason-tagged placeholder. In any other
// namespace a value is kept unless its OptionName is secret-looking or it
// holds a credential shape (JSON included).
var ebSettingCases = []struct {
	setting ebtypes.ConfigurationOptionSetting
	want    string
}{
	{ebSetting("aws:elasticbeanstalk:application:environment", "UPLOADER", "AKIAIOSFODNN7EXAMPLE"), redact.PlaceholderValueShape},
	{ebSetting("aws:elasticbeanstalk:application:environment", "UPSTREAM_HEADER", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"), redact.PlaceholderValueShape},
	{ebSetting("aws:elasticbeanstalk:application:environment", "TLS_BUNDLE", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----"), redact.PlaceholderValueShape},
	{ebSetting("aws:elasticbeanstalk:application:environment", "DB_PASS", "hunter2"), redact.PlaceholderKeyName},
	{ebSetting("aws:rds:dbinstance", "DBPassword", "hunter3"), redact.PlaceholderKeyName},
	{ebSetting("aws:elasticbeanstalk:application:environment", "WEBHOOK_ID", "q2Wx8zPl0vB7nR4tY6uK3mHs"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:eks:environment", "env-variables", `{"API_TOKEN":"tok-eks-secret"}`), redact.PlaceholderValueShape},
	{ebtypes.ConfigurationOptionSetting{
		Namespace: sdkaws.String("aws:elasticbeanstalk:sns:topics"), OptionName: sdkaws.String("Notification Endpoint"),
		ResourceName: sdkaws.String("res-1"), Value: sdkaws.String("https://u:pw-sns@hooks.example.com/x"),
	}, redact.PlaceholderValueShape},
	{ebSetting("aws:elasticbeanstalk:application:environmentsecrets", "DB_PASSWORD", "arn:aws:secretsmanager:us-east-1:123456789012:secret:db-AbCdEf"), "arn:aws:secretsmanager:us-east-1:123456789012:secret:db-AbCdEf"},
	{ebSetting("aws:elasticbeanstalk:application:environment", "API_TOKEN", "{{resolve:ssm-secure:/web/api-token:1}}"), "{{resolve:ssm-secure:/web/api-token:1}}"},
	{ebSetting("aws:elasticbeanstalk:cloudwatch:logs", "StreamLogs", "true"), "true"},
	{ebSetting("aws:autoscaling:asg", "MinSize", "1"), "1"},
	{ebSetting("aws:elasticbeanstalk:application:environment", "ADMIN_PASSCODE", "482915"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environment", "STRIPE_KEY_LIVE", "4111111111111111"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environment", "BACKUP_ROLE", "arn:aws:x:::Hunter2!pass"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environment", "LEGACY", "{{resolve:ssm:Hunter2!pass}}"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environment", "DB_PW", "app-cafe1234"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environment", "ROLE", "arn:aws:a:::xAKIAIOSFODNN7EXAMPLE"), redact.PlaceholderValueShape},
	{ebtypes.ConfigurationOptionSetting{Namespace: sdkaws.String("aws:elasticbeanstalk:application:environment"), Value: sdkaws.String("998877")}, redact.PlaceholderKeyName},
	{ebSetting("aws:autoscaling:launchconfiguration", "SecurityGroups", "sg-0123456789abcdef0"), "sg-0123456789abcdef0"},
	{ebSetting("aws:autoscaling:launchconfiguration", "InstanceType", "t3.micro"), "t3.micro"},
	{ebSetting("aws:autoscaling:launchconfiguration", "EC2KeyName", "deploy-key"), "deploy-key"},
	{ebSetting("aws:elasticbeanstalk:application", "Application Healthcheck URL", "/health"), "/health"},
	{ebSetting("aws:elasticbeanstalk:cloudwatch:logs", "LogGroupName", "/aws/elasticbeanstalk/web"), "/aws/elasticbeanstalk/web"},
	{ebSetting("aws:elasticbeanstalk:container:tomcat:jvmoptions", "Xmx", "512m"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:container:nodejs", "NodeCommand", "node app.js --db-password hunter5"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:application:environmentsecrets", "DB_HOST", "secrets-host.internal"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:customoption", "Stage", "prod-custom"), redact.PlaceholderRule},
	{ebSetting("aws:cloudformation:template:parameter", "Stage", "prod-cfn"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:sns:topics", "Notification Endpoint", "https://hooks.slack.com/services/T0000/B0000/XXXXwebhook"), redact.PlaceholderRule},
	{ebSetting("aws:elasticbeanstalk:sns:topics", "Notification Topic ARN", "arn:aws:sns:us-east-1:123456789012:alerts"), "arn:aws:sns:us-east-1:123456789012:alerts"},
	{ebSetting("aws:elasticbeanstalk:sns:topics", "Notification Protocol", "https"), redact.PlaceholderRule},
	{ebSetting("AWS:ElasticBeanstalk:Application:Environment", "DB_HOST", "db.internal"), redact.PlaceholderRule},
	{ebtypes.ConfigurationOptionSetting{OptionName: sdkaws.String("DB_HOST"), Value: sdkaws.String("db.internal")}, redact.PlaceholderRule},
}

func ebSetting(namespace, name, value string) ebtypes.ConfigurationOptionSetting {
	return ebtypes.ConfigurationOptionSetting{Namespace: sdkaws.String(namespace), OptionName: sdkaws.String(name), Value: sdkaws.String(value)}
}

func ebSettings() []ebtypes.ConfigurationOptionSetting {
	out := make([]ebtypes.ConfigurationOptionSetting, 0, len(ebSettingCases))
	for _, c := range ebSettingCases {
		out = append(out, c.setting)
	}
	return out
}

func ebTemplate(app, name string) ebtypes.ConfigurationSettingsDescription {
	return ebtypes.ConfigurationSettingsDescription{
		ApplicationName: sdkaws.String(app), TemplateName: sdkaws.String(name),
		DateCreated: &ebCreated, Description: sdkaws.String(name + " settings"),
		SolutionStackName: sdkaws.String("64bit Amazon Linux 2023 v4.0.0 running Python 3.11"),
	}
}

// ebHandlers serves three applications (web: base, prod/v2; worker: "" and
// w1; bare: none), one row for each older phase, and each template's
// settings; prod/v2's response also carries an element without a name and
// one naming another template, both of which must be dropped.
// templateCalls records "app/template" per DescribeConfigurationSettings call.
func ebHandlers(templateCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeApplications": func(any) (any, error) {
			return &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
				{ApplicationArn: sdkaws.String(ebTestAppWeb), ApplicationName: sdkaws.String("web"), ConfigurationTemplates: []string{"base", "prod/v2"}},
				{ApplicationArn: sdkaws.String(ebTestAppWorker), ApplicationName: sdkaws.String("worker"), ConfigurationTemplates: []string{"", "w1"}},
				{ApplicationArn: sdkaws.String(ebTestAppBare), ApplicationName: sdkaws.String("bare")},
			}}, nil
		},
		"DescribeEnvironments": func(any) (any, error) {
			return &elasticbeanstalk.DescribeEnvironmentsOutput{Environments: []ebtypes.EnvironmentDescription{
				{EnvironmentArn: sdkaws.String(ebTestEnv), EnvironmentName: sdkaws.String("web-prod"), ApplicationName: sdkaws.String("web")},
			}}, nil
		},
		"DescribeApplicationVersions": func(any) (any, error) {
			return &elasticbeanstalk.DescribeApplicationVersionsOutput{ApplicationVersions: []ebtypes.ApplicationVersionDescription{
				{ApplicationVersionArn: sdkaws.String(ebTestVersion), VersionLabel: sdkaws.String("v1")},
			}}, nil
		},
		"ListPlatformVersions": func(any) (any, error) {
			return &elasticbeanstalk.ListPlatformVersionsOutput{PlatformSummaryList: []ebtypes.PlatformSummary{
				{PlatformArn: sdkaws.String(ebTestPlatform)},
			}}, nil
		},
		"DescribeConfigurationSettings": func(in any) (any, error) {
			p := in.(*elasticbeanstalk.DescribeConfigurationSettingsInput)
			app, tpl := sdkaws.ToString(p.ApplicationName), sdkaws.ToString(p.TemplateName)
			templateCalls.add(app + "/" + tpl)
			if p.EnvironmentName != nil {
				return nil, apiErr("InvalidParameterCombination", "template and environment both set")
			}
			d := ebTemplate(app, tpl)
			switch app + "/" + tpl {
			case "web/base":
				d.OptionSettings = ebSettings()
				return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{d}}, nil
			case "web/prod/v2":
				return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{
					d, {ApplicationName: sdkaws.String(app)}, ebTemplate(app, "other"),
				}}, nil
			case "worker/w1":
				return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{d}}, nil
			}
			return nil, ebBadInput
		},
	}
}

func scanEBTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ebDenied)
	total, _, err := scanElasticBeanstalk(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

var ebAllTemplates = []string{ebTestTplBase, ebTestTplProd, ebTestTplWorker1}

// assertEBOlderRows fails unless every older phase's row was stored.
func assertEBOlderRows(t *testing.T, st *store.Store) {
	t.Helper()
	for typ, id := range map[string]string{
		TypeBeanstalkApplication:        ebTestAppWeb,
		TypeBeanstalkEnvironment:        ebTestEnv,
		TypeBeanstalkApplicationVersion: ebTestVersion,
		TypeBeanstalkPlatform:           ebTestPlatform,
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s row %s missing; the template phase must not stop the older phases", typ, id)
		}
	}
}

func TestScanElasticBeanstalk_StoresConfigurationTemplates(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanEBTopLevel(t, st, ebHandlers(&calls))
	if err != nil {
		t.Fatalf("scanElasticBeanstalk: %v", err)
	}
	// 3 applications + 1 environment + 1 version + 1 platform + 3 templates
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"web/base", "web/prod/v2", "worker/w1"}) {
		t.Errorf("DescribeConfigurationSettings calls = %v, want one per named template", got)
	}
	rows := rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)
	if got := nativeIDs(rows); !slices.Equal(got, ebAllTemplates) {
		t.Fatalf("template rows = %v, want %v", got, ebAllTemplates)
	}
	r := rows[ebTestTplProd]
	if r.Type != TypeBeanstalkConfigurationTemplate || sv(r.Region) != testRegion || sv(r.Name) != "prod/v2" ||
		sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("row = type %q region %q name %q created %q", r.Type, sv(r.Region), sv(r.Name), sv(r.CreatedAt))
	}
	var attrs struct {
		ApplicationName   string `json:"ApplicationName"`
		Description       string `json:"Description"`
		SolutionStackName string `json:"SolutionStackName"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.ApplicationName != "web" || attrs.Description != "prod/v2 settings" || !strings.HasPrefix(attrs.SolutionStackName, "64bit") {
		t.Errorf("attrs = %s, want the SDK description verbatim", r.AttributesJSON)
	}
	assertContainsNative(t, st, ebTestAppWeb, ebTestTplBase)
	assertContainsNative(t, st, ebTestAppWeb, ebTestTplProd)
	assertContainsNative(t, st, ebTestAppWorker, ebTestTplWorker1)
	assertEBOlderRows(t, st)
}

// Each option value is kept or replaced by its reason-tagged placeholder as
// its case says, no replaced value is stored as any string of the row, and each
// setting's Namespace, OptionName and ResourceName stay and no setting is lost.
func TestScanElasticBeanstalk_RedactsOptionValues(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	if _, err := scanEBTopLevel(t, st, ebHandlers(&calls)); err != nil {
		t.Fatalf("scanElasticBeanstalk: %v", err)
	}
	raw := rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)[ebTestTplBase].AttributesJSON
	var attrs ebtypes.ConfigurationSettingsDescription
	if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if len(attrs.OptionSettings) != len(ebSettingCases) {
		t.Fatalf("stored %d settings, want %d", len(attrs.OptionSettings), len(ebSettingCases))
	}
	for i, c := range ebSettingCases {
		want := c.setting
		t.Run(sv(want.Namespace)+"/"+sv(want.OptionName), func(t *testing.T) {
			got := attrs.OptionSettings[i]
			if sv(got.Namespace) != sv(want.Namespace) || sv(got.OptionName) != sv(want.OptionName) || sv(got.ResourceName) != sv(want.ResourceName) {
				t.Errorf("keys = %q/%q/%q, want %q/%q/%q", sv(got.Namespace), sv(got.OptionName), sv(got.ResourceName),
					sv(want.Namespace), sv(want.OptionName), sv(want.ResourceName))
			}
			if sv(got.Value) != c.want {
				t.Errorf("value of %q = %q, want %q", sv(want.Value), sv(got.Value), c.want)
			}
			if c.want != sv(want.Value) && strings.Contains(raw, `"`+jsonText(t, sv(want.Value))+`"`) {
				t.Errorf("stored attributes still contain %q", sv(want.Value))
			}
		})
	}
}

// jsonText is s as it appears inside a JSON string, so a value with
// newlines or quotes is found in a stored row.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return strings.Trim(string(b), `"`)
}

// A response without the requested template stores nothing for it and is
// reported in one warning however many templates hit it; a full response
// warns nothing.
func TestScanElasticBeanstalk_ResponseWithoutRequestedTemplate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		empty     []string
		wantRows  []string
		wantWarns int
	}{
		{"none", nil, ebAllTemplates, 0},
		{"one", []string{"w1"}, []string{ebTestTplBase, ebTestTplProd}, 1},
		{"two", []string{"base", "w1"}, []string{ebTestTplProd}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, beanstalkDescribeConfigurationSettingsOp)
			var calls parentCalls
			h := ebHandlers(&calls)
			next := h["DescribeConfigurationSettings"]
			h["DescribeConfigurationSettings"] = func(in any) (any, error) {
				p := in.(*elasticbeanstalk.DescribeConfigurationSettingsInput)
				if slices.Contains(tc.empty, sdkaws.ToString(p.TemplateName)) {
					return &elasticbeanstalk.DescribeConfigurationSettingsOutput{ConfigurationSettings: []ebtypes.ConfigurationSettingsDescription{
						ebTemplate(sdkaws.ToString(p.ApplicationName), "other"),
					}}, nil
				}
				return next(in)
			}
			if _, err := scanEBTopLevel(t, st, h); err != nil {
				t.Fatalf("scanElasticBeanstalk: %v", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("template rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanElasticBeanstalk_ApplicationsWithoutTemplates(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := ebHandlers(&calls)
	h["DescribeApplications"] = func(any) (any, error) {
		return &elasticbeanstalk.DescribeApplicationsOutput{Applications: []ebtypes.ApplicationDescription{
			{ApplicationArn: sdkaws.String(ebTestAppBare), ApplicationName: sdkaws.String("bare")},
		}}, nil
	}
	if _, err := scanEBTopLevel(t, st, h); err != nil {
		t.Fatalf("scanElasticBeanstalk: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); n != 0 {
		t.Errorf("template rows = %d, want 0", n)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("DescribeConfigurationSettings calls = %v, want none", got)
	}
}

// A template denied, answering InvalidParameterValue (deleted since listing)
// or failing transiently after retries drops only itself, with one warning
// per reason however many templates hit it. Any other error fails the
// phase (childFanOut stores none of its rows) and is returned as a hard error;
// the older phases' rows stay.
func TestScanElasticBeanstalk_FailedTemplate(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"denied everywhere", map[string]error{"base": ebDenied, "prod/v2": ebDenied, "w1": ebDenied}, "", nil, 1},
		{"denied once", map[string]error{"base": ebDenied}, "", []string{ebTestTplProd, ebTestTplWorker1}, 1},
		{"throttled everywhere", map[string]error{"base": throttled, "prod/v2": throttled, "w1": throttled}, "", nil, 1},
		{"throttled once", map[string]error{"w1": throttled}, "", []string{ebTestTplBase, ebTestTplProd}, 1},
		{"other error", map[string]error{"base": ebBadInput}, "TooManyBucketsException", nil, 0},
		{"gone once", map[string]error{"prod/v2": ebGone}, "", []string{ebTestTplBase, ebTestTplWorker1}, 1},
		{"gone everywhere", map[string]error{"base": ebGone, "prod/v2": ebGone, "w1": ebGone}, "", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, beanstalkDescribeConfigurationSettingsOp)
			var calls parentCalls
			h := ebHandlers(&calls)
			next := h["DescribeConfigurationSettings"]
			h["DescribeConfigurationSettings"] = func(in any) (any, error) {
				if err, ok := tc.fail[sdkaws.ToString(in.(*elasticbeanstalk.DescribeConfigurationSettingsInput).TemplateName)]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanEBTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if got := classifyServiceError(err); got != outcomeError {
					t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
				}
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("template rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			assertEBOlderRows(t, st)
		})
	}
}

// A hard error in any older phase after the applications were stored still
// lets the template phase run over them, and is returned.
func TestScanElasticBeanstalk_OlderPhaseErrorStillRunsTemplates(t *testing.T) {
	for _, op := range []string{"DescribeEnvironments", "DescribeApplicationVersions", "ListPlatformVersions"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := ebHandlers(&calls)
			h[op] = func(any) (any, error) { return nil, ebBadInput }
			_, err := scanEBTopLevel(t, st, h)
			assertAPICode(t, err, "TooManyBucketsException")
			if got := nativeIDs(rowsByNativeID(t, st, TypeBeanstalkConfigurationTemplate)); !slices.Equal(got, ebAllTemplates) {
				t.Errorf("template rows = %v, want all three", got)
			}
		})
	}
}

// A transient error in an older phase is demoted to one warning so that the
// template phase's hard error still reaches the dispatcher as a hard error.
func TestScanElasticBeanstalk_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "elasticbeanstalk")
	var calls parentCalls
	h := ebHandlers(&calls)
	h["ListPlatformVersions"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["DescribeConfigurationSettings"] = func(any) (any, error) { return nil, ebBadInput }
	_, err := scanEBTopLevel(t, st, h)
	assertAPICode(t, err, "TooManyBucketsException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// A denied DescribeApplications leaves the template phase no parents: no
// template call, no error.
func TestScanElasticBeanstalk_ApplicationsDenied(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := ebHandlers(&calls)
	delete(h, "DescribeApplications")
	if _, err := scanEBTopLevel(t, st, h); err != nil {
		t.Fatalf("scanElasticBeanstalk: %v", err)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("DescribeConfigurationSettings calls = %v, want none", got)
	}
}
