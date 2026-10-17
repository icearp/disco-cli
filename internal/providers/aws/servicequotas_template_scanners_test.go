package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/icearp/disco-cli/store"
)

const sqTemplateOp = "servicequotas:ListServiceQuotaIncreaseRequestsInTemplate"

// fakeSQTemplate serves pages in call order or fails every call with err.
type fakeSQTemplate struct {
	pages []*servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput
	err   error
	ins   []servicequotas.ListServiceQuotaIncreaseRequestsInTemplateInput
}

func (f *fakeSQTemplate) ListServiceQuotaIncreaseRequestsInTemplate(_ context.Context, in *servicequotas.ListServiceQuotaIncreaseRequestsInTemplateInput, _ ...func(*servicequotas.Options)) (*servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput, error) {
	f.ins = append(f.ins, *in)
	if f.err != nil {
		return nil, f.err
	}
	return f.pages[len(f.ins)-1], nil
}

func sqTemplateEntry(region, service, quota, name string, desired float64) sqtypes.ServiceQuotaIncreaseRequestInTemplate {
	return sqtypes.ServiceQuotaIncreaseRequestInTemplate{
		AwsRegion: sdkaws.String(region), ServiceCode: sdkaws.String(service), QuotaCode: sdkaws.String(quota),
		QuotaName: sdkaws.String(name), DesiredValue: sdkaws.Float64(desired),
	}
}

func TestScanSQTemplateEntries_PaginatesAndStoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeSQTemplate{pages: []*servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput{
		{
			ServiceQuotaIncreaseRequestInTemplateList: []sqtypes.ServiceQuotaIncreaseRequestInTemplate{
				sqTemplateEntry("us-east-1", "ec2", "L-1216C47A", "Running On-Demand Standard instances", 256),
				// No AwsRegion: no identity, so no row.
				{ServiceCode: sdkaws.String("ec2"), QuotaCode: sdkaws.String("L-0263D0A3")},
			},
			NextToken: sdkaws.String("t1"),
		},
		{ServiceQuotaIncreaseRequestInTemplateList: []sqtypes.ServiceQuotaIncreaseRequestInTemplate{
			sqTemplateEntry("eu-west-1", "ec2", "L-1216C47A", "Running On-Demand Standard instances", 128),
		}},
	}}

	total, _, err := scanSQTemplateEntries(context.Background(), fake, newTestAccount(testAccountID), st, testScanID)
	if err != nil {
		t.Fatalf("scanSQTemplateEntries: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	if len(fake.ins) != 2 || sv(fake.ins[1].NextToken) != "t1" {
		t.Fatalf("calls = %+v; want 2 calls, second with NextToken t1", fake.ins)
	}
	if got := fake.ins[0].MaxResults; got == nil || *got != 100 {
		t.Errorf("MaxResults = %v; want 100 (model cap)", got)
	}

	rows := rowsByNativeID(t, st, TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate)
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2: %v", len(rows), rows)
	}
	want := map[string]float64{
		"arn:aws:servicequotas:us-east-1:123456789012:ec2/L-1216C47A/request-template": 256,
		"arn:aws:servicequotas:eu-west-1:123456789012:ec2/L-1216C47A/request-template": 128,
	}
	for id, desired := range want {
		r, ok := rows[id]
		if !ok {
			t.Errorf("missing row %s; have %v", id, rows)
			continue
		}
		if sv(r.Region) != "global" {
			t.Errorf("%s Region = %q; want global", id, sv(r.Region))
		}
		if sv(r.Name) != "Running On-Demand Standard instances" {
			t.Errorf("%s Name = %q", id, sv(r.Name))
		}
		var attrs struct{ DesiredValue float64 }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs: %v", id, err)
		}
		if attrs.DesiredValue != desired {
			t.Errorf("%s DesiredValue = %v; want %v", id, attrs.DesiredValue, desired)
		}
	}
}

func TestScanSQTemplateEntries_EmptyTemplate(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeSQTemplate{pages: []*servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput{{}}}
	total, _, err := scanSQTemplateEntries(context.Background(), fake, newTestAccount(testAccountID), st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("got (%d, %v); want (0, nil)", total, err)
	}
	if rows := rowsByNativeID(t, st, TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate); len(rows) != 0 {
		t.Errorf("rows = %v; want none", rows)
	}
}

func TestScanSQTemplateEntries_Errors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		warnings int
		fatal    bool
	}{
		{"not in an organization is silent", &sqtypes.NoAvailableOrganizationException{Message: sdkaws.String("x")}, 0, false},
		{"trusted access not enabled is silent", &sqtypes.AWSServiceAccessNotEnabledException{Message: sdkaws.String("x")}, 0, false},
		{"dependency denied warns", &sqtypes.DependencyAccessDeniedException{Message: sdkaws.String("x")}, 1, false},
		{"access denied warns", &sqtypes.AccessDeniedException{Message: sdkaws.String("User: x is not authorized to perform")}, 1, false},
		{"wrong pinned region is returned", &sqtypes.TemplatesNotAvailableInRegionException{Message: sdkaws.String("x")}, 0, true},
		{"service error is returned", &sqtypes.ServiceException{Message: sdkaws.String("x")}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == sqTemplateOp {
					scopes = append(scopes, w.Scope)
				}
			}
			_, _, err := scanSQTemplateEntries(context.Background(), &fakeSQTemplate{err: tc.err}, newTestAccount(testAccountID), st, testScanID)
			if tc.fatal {
				if !errors.Is(err, tc.err) {
					t.Errorf("err = %v; want wrapping %v", err, tc.err)
				}
			} else if err != nil {
				t.Errorf("err = %v; want nil", err)
			}
			if len(scopes) != tc.warnings {
				t.Errorf("warnings = %d; want %d", len(scopes), tc.warnings)
			}
			// The template is organization-wide, so the warning names no region.
			for _, sc := range scopes {
				if sc != testAccountID {
					t.Errorf("warning scope = %q; want %q", sc, testAccountID)
				}
			}
		})
	}
}

// The registered entry runs once per account (global) and must call the
// pinned us-east-1 endpoint whatever region the dispatcher passes.
func TestScanServiceQuotasTemplate_GlobalPinnedRegion(t *testing.T) {
	var entry *serviceEntry
	for i := range registeredServices {
		if registeredServices[i].name == "aws:servicequotas-template" {
			entry = &registeredServices[i]
		}
	}
	if entry == nil || !entry.global || !entry.optIn {
		t.Fatalf("aws:servicequotas-template entry = %+v; want registered, global, opt-in", entry)
	}
	if slices.Contains(filteredServiceNames(nil, false), entry.name) {
		t.Error("default scan must exclude opt-in aws:servicequotas-template")
	}
	if !slices.Contains(filteredServiceNames(nil, true), entry.name) {
		t.Error("--include-service-quotas must include aws:servicequotas-template")
	}

	st := newTestStore(t)
	cfg := opStubConfig(map[string]func(any) (any, error){
		"ListServiceQuotaIncreaseRequestsInTemplate": func(any) (any, error) {
			return &servicequotas.ListServiceQuotaIncreaseRequestsInTemplateOutput{
				ServiceQuotaIncreaseRequestInTemplateList: []sqtypes.ServiceQuotaIncreaseRequestInTemplate{
					sqTemplateEntry("ap-south-1", "lambda", "L-B99A9384", "Concurrent executions", 3000),
				},
			}, nil
		},
	}, errors.New("unexpected op"))
	var regions parentCalls
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := entry.fn(context.Background(), acct, "", st, testScanID); err != nil {
		t.Fatalf("fn: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("call regions = %v; want [us-east-1]", got)
	}
	rows := rowsByNativeID(t, st, TypeServiceQuotasServiceQuotaIncreaseRequestInTemplate)
	if _, ok := rows["arn:aws:servicequotas:ap-south-1:123456789012:lambda/L-B99A9384/request-template"]; !ok || len(rows) != 1 {
		t.Errorf("rows = %v; want the one ap-south-1 entry", rows)
	}
}
