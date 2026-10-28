package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// gdOrgConfigPages is a two-page DescribeOrganizationConfiguration answer.
// Only the first page carries the scalar settings, so a scanner that keeps
// the last page's header loses them.
func gdOrgConfigPages() []*guardduty.DescribeOrganizationConfigurationOutput {
	return []*guardduty.DescribeOrganizationConfigurationOutput{
		{
			MemberAccountLimitReached: sdkaws.Bool(true),
			AutoEnable:                sdkaws.Bool(true),
			DataSources: &gdtypes.OrganizationDataSourceConfigurationsResult{
				S3Logs: &gdtypes.OrganizationS3LogsConfigurationResult{AutoEnable: sdkaws.Bool(true)},
			},
			AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll,
			Features:                      []gdtypes.OrganizationFeatureConfigurationResult{{Name: gdtypes.OrgFeatureS3DataEvents}},
			NextToken:                     sdkaws.String("p2"),
		},
		{Features: []gdtypes.OrganizationFeatureConfigurationResult{{Name: gdtypes.OrgFeatureEksAuditLogs}}},
	}
}

// fakeGDOrgConfig serves gdOrgConfigPages for every detector, failing a page
// with errs["<detectorID>@<NextToken>"] ("" is the first page's token).
// childFanOut calls it from concurrent goroutines, so queried is mutex-guarded.
type fakeGDOrgConfig struct {
	guardDutyExtAPI
	errs    map[string]error
	mu      sync.Mutex
	queried []string
}

func (f *fakeGDOrgConfig) DescribeOrganizationConfiguration(_ context.Context, in *guardduty.DescribeOrganizationConfigurationInput, _ ...func(*guardduty.Options)) (*guardduty.DescribeOrganizationConfigurationOutput, error) {
	did, token := sdkaws.ToString(in.DetectorId), sdkaws.ToString(in.NextToken)
	f.mu.Lock()
	f.queried = append(f.queried, did+"@"+token)
	f.mu.Unlock()
	if err := f.errs[did+"@"+token]; err != nil {
		return nil, err
	}
	pages := gdOrgConfigPages()
	if token == "" {
		return pages[0], nil
	}
	return pages[1], nil
}

func seedGDDetectors(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		upsertTestResource(t, st, "aws", testAccountID, TypeGuardDutyDetector, guardDutyDetectorARN("aws", testRegion, testAccountID, id), testRegion, "{}")
	}
}

func gdOrgConfigARN(detectorID string) string {
	return guardDutyDetectorARN("aws", testRegion, testAccountID, detectorID) + "/organization-configuration"
}

func TestScanGDOrganizationConfigurations_MergesPagesPerDetector(t *testing.T) {
	st := newTestStore(t)
	seedGDDetectors(t, st, "d1", "d2")
	fake := &fakeGDOrgConfig{}
	total, _, _, err := scanGDOrganizationConfigurations(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"d1", "d2"})
	if err != nil {
		t.Fatalf("scanGDOrganizationConfigurations: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (one configuration per detector)", total)
	}
	slices.Sort(fake.queried)
	if want := []string{"d1@", "d1@p2", "d2@", "d2@p2"}; !slices.Equal(fake.queried, want) {
		t.Errorf("queried %v; want %v", fake.queried, want)
	}
	rows := qsRows(t, st, TypeGuardDutyOrganizationConfiguration)
	if len(rows) != 2 {
		t.Fatalf("stored %d rows; want 2", len(rows))
	}
	for _, did := range []string{"d1", "d2"} {
		r, ok := rows[gdOrgConfigARN(did)]
		if !ok {
			t.Fatalf("no row %s in %v", gdOrgConfigARN(did), rows)
		}
		if sv(r.Region) != testRegion || sv(r.Name) != "organization-configuration" || r.ManagedByProvider {
			t.Errorf("%s: region %q, name %q, managed %v; want %q, organization-configuration, false", did, sv(r.Region), sv(r.Name), r.ManagedByProvider, testRegion)
		}
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs: %v", did, err)
		}
		var got struct {
			AutoEnable                    *bool
			AutoEnableOrganizationMembers gdtypes.AutoEnableMembers
			DataSources                   *gdtypes.OrganizationDataSourceConfigurationsResult
			Features                      []gdtypes.OrganizationFeatureConfigurationResult
			MemberAccountLimitReached     *bool
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
			t.Fatalf("%s attrs: %v", did, err)
		}
		if len(got.Features) != 2 || got.Features[0].Name != gdtypes.OrgFeatureS3DataEvents || got.Features[1].Name != gdtypes.OrgFeatureEksAuditLogs {
			t.Errorf("%s Features = %+v; want S3_DATA_EVENTS then EKS_AUDIT_LOGS from both pages", did, got.Features)
		}
		if !sdkaws.ToBool(got.MemberAccountLimitReached) || got.AutoEnableOrganizationMembers != gdtypes.AutoEnableMembersAll {
			t.Errorf("%s header = %v / %q; want true / ALL from the first page", did, got.MemberAccountLimitReached, got.AutoEnableOrganizationMembers)
		}
		if !sdkaws.ToBool(got.AutoEnable) || got.DataSources == nil || got.DataSources.S3Logs == nil || !sdkaws.ToBool(got.DataSources.S3Logs.AutoEnable) {
			t.Errorf("%s deprecated AutoEnable / DataSources = %v / %+v; want both kept from the first page", did, got.AutoEnable, got.DataSources)
		}
		if _, hasMeta := attrs["ResultMetadata"]; string(attrs["NextToken"]) != "null" || hasMeta {
			t.Errorf("%s envelope NextToken=%s ResultMetadata=%s; want null and absent so the row is stable across scans", did, attrs["NextToken"], attrs["ResultMetadata"])
		}
		assertQSContains(t, st, guardDutyDetectorARN("aws", testRegion, testAccountID, did), gdOrgConfigARN(did))
	}
}

func TestScanGDOrganizationConfigurations_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		token     string
		err       error
		wantCode  string
		wantRows  int
		wantWarns int
	}{
		{"first-page bad request warns once and keeps siblings", "", apiErr("BadRequestException", "synthetic-test-400"), "", 2, 1},
		{"later-page bad request propagates", "p2", apiErr("BadRequestException", "synthetic-test-400"), "BadRequestException", 0, 0},
		{"access denied warns once and keeps siblings", "", apiErr("AccessDeniedException", "synthetic-test-deny"), "", 2, 1},
		{"other error propagates", "", apiErr("InternalServerErrorException", "synthetic-test-500"), "InternalServerErrorException", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			ids := []string{"bad1", "bad2", "ok1", "ok2"}
			seedGDDetectors(t, st, ids...)
			fake := &fakeGDOrgConfig{errs: map[string]error{"bad1@" + tc.token: tc.err, "bad2@" + tc.token: tc.err}}
			_, _, _, err := scanGDOrganizationConfigurations(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, ids)
			if tc.wantCode != "" {
				if !isAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want the %s propagated", err, tc.wantCode)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			rows := qsRows(t, st, TypeGuardDutyOrganizationConfiguration)
			if len(rows) != tc.wantRows {
				t.Errorf("stored %d rows; want %d", len(rows), tc.wantRows)
			}
			if tc.wantRows > 0 {
				for _, did := range []string{"ok1", "ok2"} {
					if _, ok := rows[gdOrgConfigARN(did)]; !ok {
						t.Errorf("sibling %s not stored", did)
					}
				}
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, svc := range warned {
				if svc != "guardduty:DescribeOrganizationConfiguration" {
					t.Errorf("warning names %q; want guardduty:DescribeOrganizationConfiguration", svc)
				}
			}
		})
	}
}

func TestScanGDOrganizationConfigurations_NoDetectorsMakesNoCalls(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeGDOrgConfig{}
	total, _, _, err := scanGDOrganizationConfigurations(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Fatalf("got (%d, %v); want (0, nil)", total, err)
	}
	if len(fake.queried) != 0 || len(qsRows(t, st, TypeGuardDutyOrganizationConfiguration)) != 0 {
		t.Errorf("queried %v or stored rows with no detectors; want neither", fake.queried)
	}
}

// gdServiceStub serves every op scanGuardDuty calls for one detector "d"
// with one publishing destination; override replaces an op's queue.
func gdServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"ListDetectors":              one(&guardduty.ListDetectorsOutput{DetectorIds: []string{"d"}}),
		"GetDetector":                one(&guardduty.GetDetectorOutput{Status: gdtypes.DetectorStatusEnabled}),
		"ListFilters":                one(&guardduty.ListFiltersOutput{}),
		"ListMembers":                one(&guardduty.ListMembersOutput{}),
		"ListIPSets":                 one(&guardduty.ListIPSetsOutput{}),
		"ListMalwareProtectionPlans": one(&guardduty.ListMalwareProtectionPlansOutput{}),
		"ListPublishingDestinations": one(&guardduty.ListPublishingDestinationsOutput{Destinations: []gdtypes.Destination{
			{DestinationId: sdkaws.String("pd"), DestinationType: gdtypes.DestinationTypeS3},
		}}),
		"ListThreatEntitySets":  one(&guardduty.ListThreatEntitySetsOutput{}),
		"ListThreatIntelSets":   one(&guardduty.ListThreatIntelSetsOutput{}),
		"ListTrustedEntitySets": one(&guardduty.ListTrustedEntitySetsOutput{}),
		"DescribeOrganizationConfiguration": one(&guardduty.DescribeOrganizationConfigurationOutput{
			AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll,
		}),
		"ListCustomDetectionRuleAssociations":      one(&guardduty.ListCustomDetectionRuleAssociationsOutput{}),
		"ListCustomDetectionRuleOrgConfigurations": one(&guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{}),
		"ListTagsForResource":                      one(&guardduty.ListTagsForResourceOutput{}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
}

// TestScanGuardDuty_StoresOrganizationConfigurationUnderDetector runs the whole
// service against the real SDK client (its input validators included): the
// detector is stored and contains its organization configuration.
func TestScanGuardDuty_StoresOrganizationConfigurationUnderDetector(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = gdServiceStub(t, nil)
	total, _, err := scanGuardDuty(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanGuardDuty: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (detector, publishing destination, organization configuration)", total)
	}
	assertQSContains(t, st, guardDutyDetectorARN("aws", testRegion, testAccountID, "d"), gdOrgConfigARN("d"))
}

// TestScanGuardDuty_OrganizationConfigurationErrorPropagates pins that the
// phase's hard error reaches the service result. The code is not a transient
// one: a transient error is demoted to a warning where the custom detection
// rule phases join theirs.
func TestScanGuardDuty_OrganizationConfigurationErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = gdServiceStub(t, map[string][]stubCall{
		"DescribeOrganizationConfiguration": {{Err: apiErr("ValidationException", "synthetic-test-hard")}},
	})
	_, _, err := scanGuardDuty(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the DescribeOrganizationConfiguration ValidationException", err)
	}
}

// TestScanGuardDuty_OrganizationConfigurationRefusedKeepsDetectorRows pins that
// a first-page BadRequestException costs only the organization configuration:
// the service succeeds, warns once naming the op, and keeps the detector's
// other rows.
func TestScanGuardDuty_OrganizationConfigurationRefusedKeepsDetectorRows(t *testing.T) {
	st := newTestStore(t)
	var warned []string
	st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
	acct := newTestAccount(testAccountID)
	acct.cfg = gdServiceStub(t, map[string][]stubCall{
		"DescribeOrganizationConfiguration": {{Err: apiErr("BadRequestException", "synthetic-test-400")}},
	})
	if _, _, err := scanGuardDuty(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGuardDuty: %v; want nil", err)
	}
	if want := []string{"guardduty:DescribeOrganizationConfiguration"}; !slices.Equal(warned, want) {
		t.Errorf("warnings %v; want %v", warned, want)
	}
	if got := len(qsRows(t, st, TypeGuardDutyOrganizationConfiguration)); got != 0 {
		t.Errorf("stored %d organization configurations; want 0", got)
	}
	for _, rtype := range []string{TypeGuardDutyDetector, TypeGuardDutyPublishingDestination} {
		if got := len(qsRows(t, st, rtype)); got != 1 {
			t.Errorf("stored %d %s rows; want 1", got, rtype)
		}
	}
}

func TestIsGuardDutyMemberRestricted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "member-restricted bad request",
			err: &smithy.GenericAPIError{
				Code:    "BadRequestException",
				Message: "The request is rejected because member accounts cannot manage specified resources or properties.",
			},
			want: true,
		},
		{
			name: "vanilla bad request",
			err:  &smithy.GenericAPIError{Code: "BadRequestException", Message: "Validation failed"},
			want: false,
		},
		{
			name: "access denied",
			err:  &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: ... not authorized"},
			want: false,
		},
		{
			name: "non-api error",
			err:  errors.New("network unreachable"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGuardDutyMemberRestricted(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
