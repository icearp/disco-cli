package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/regions"
)

func TestRegionAvailabilityCode(t *testing.T) {
	// Derived default = name minus "aws:".
	if got := regionAvailabilityCode("aws:dynamodb"); got != "dynamodb" {
		t.Errorf("derive = %q, want dynamodb", got)
	}
	// Override wins when present.
	regionAvailabilityCodeOverrides["aws:zzz-test"] = "zzz"
	defer delete(regionAvailabilityCodeOverrides, "aws:zzz-test")
	if got := regionAvailabilityCode("aws:zzz-test"); got != "zzz" {
		t.Errorf("override = %q, want zzz", got)
	}
}

// Service names carrying a real SDK-table opinion, so these cases exercise the
// registry the scanner actually consults rather than a stand-in. Both are
// pinned independently by TestGeneratedServiceRegionsAnchors; if the generated
// table ever stops covering aws:cassandra, that test fails first and says so.
const (
	sdkListedService   = "aws:cassandra"    // SDK table lists it in sdkListedRegion
	sdkListedRegion    = "us-east-1"        //
	sdkOmittedRegion   = "ap-northeast-3"   // ...and omits it here
	sdkUnknownService  = "aws:zzz-not-real" // absent from the table entirely
	unknownServiceCode = "zzz-not-real"
)

// TestServiceAvailableInRegion walks the four-state matrix of the two
// availability sources. The load-bearing row is "SDK says no, catalog says yes":
// the shipped table can lag a region launch by an SDK release, and if that
// disagreement ever resolved toward skipping, the scanner would silently stop
// covering a brand-new region. Nothing else in the system would notice — the
// pair reports zero resources, exactly like a genuinely empty region.
func TestServiceAvailableInRegion(t *testing.T) {
	const code = "cassandra"
	listed := map[string]map[string]bool{code: {sdkListedRegion: true}}
	omitted := map[string]map[string]bool{code: {"eu-west-1": true}}

	cases := []struct {
		name    string
		avail   map[string]map[string]bool
		service string
		code    string
		region  string
		want    bool
	}{
		{"sdk lists it, catalog silent", nil, sdkListedService, code, sdkListedRegion, true},
		{"sdk omits it, catalog silent", nil, sdkListedService, code, sdkOmittedRegion, false},
		{"sdk silent, catalog lists it", listed, sdkUnknownService, code, sdkListedRegion, true},
		{"sdk silent, catalog omits it", omitted, sdkUnknownService, code, sdkListedRegion, false},
		{"both list it", listed, sdkListedService, code, sdkListedRegion, true},
		{"both omit it", omitted, sdkListedService, code, sdkOmittedRegion, false},
		{"sdk omits it, catalog lists it", map[string]map[string]bool{code: {sdkOmittedRegion: true}}, sdkListedService, code, sdkOmittedRegion, true},
		{"sdk lists it, catalog omits it", omitted, sdkListedService, code, sdkListedRegion, true},
		{"neither has an opinion", nil, sdkUnknownService, unknownServiceCode, sdkListedRegion, true},
		{"catalog known but empty is no opinion", map[string]map[string]bool{unknownServiceCode: {}}, sdkUnknownService, unknownServiceCode, sdkListedRegion, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceAvailableInRegion(c.avail, c.service, c.code, c.region); got != c.want {
				t.Errorf("serviceAvailableInRegion(%v, %q, %q, %q) = %v, want %v",
					c.avail, c.service, c.code, c.region, got, c.want)
			}
		})
	}
}

// stubSSMAvailability serves /aws/service/global-infrastructure/services/<code>/regions
// from a per-code region list, paginated 10 per page. Codes in errCodes return an
// error on first page; codes absent from regionsByCode return zero params.
type stubSSMAvailability struct {
	regionsByCode map[string][]string
	errByCode     map[string]error
}

func (s *stubSSMAvailability) GetParametersByPath(_ context.Context, in *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	// Path: /aws/service/global-infrastructure/services/<code>/regions
	const prefix = "/aws/service/global-infrastructure/services/"
	const suffix = "/regions"
	p := *in.Path
	code := p[len(prefix) : len(p)-len(suffix)]
	if err := s.errByCode[code]; err != nil {
		return nil, err
	}
	regions := s.regionsByCode[code]
	// Paginate at 10/page using NextToken as an offset.
	start := 0
	if in.NextToken != nil {
		// token is the next start index encoded as a single rune count; keep simple.
		start = int((*in.NextToken)[0])
	}
	end := start + 10
	if end > len(regions) {
		end = len(regions)
	}
	out := &ssm.GetParametersByPathOutput{}
	for _, r := range regions[start:end] {
		rr := r
		out.Parameters = append(out.Parameters, ssmtypes.Parameter{Value: &rr})
	}
	if end < len(regions) {
		tok := string(rune(end))
		out.NextToken = &tok
	}
	return out, nil
}

func TestLoadServiceRegionAvailability(t *testing.T) {
	stub := &stubSSMAvailability{
		regionsByCode: map[string][]string{
			// 12 regions forces a second page (proves pagination).
			"dynamodb": {"us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-west-1", "eu-west-2", "eu-west-3", "eu-central-1", "ap-south-1", "ap-southeast-1", "ap-southeast-2", "sa-east-1"},
			"dax":      {"us-east-1", "eu-west-1"},
			"empty":    {}, // returns zero params → omitted (fail-open)
		},
		errByCode: map[string]error{
			"denied": &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no ssm"},
		},
	}
	out, denyErr := loadServiceRegionAvailability(context.Background(), stub,
		[]string{"dynamodb", "dax", "empty", "denied"})

	if denyErr == nil {
		t.Error("expected denyErr from the access-denied code")
	}
	if len(out["dynamodb"]) != 12 {
		t.Errorf("dynamodb regions = %d, want 12 (pagination)", len(out["dynamodb"]))
	}
	if !out["dynamodb"]["sa-east-1"] || !out["dax"]["eu-west-1"] {
		t.Error("expected paginated + small code sets fully collected")
	}
	if _, ok := out["empty"]; ok {
		t.Error("empty-result code must be omitted (fail-open)")
	}
	if _, ok := out["denied"]; ok {
		t.Error("errored code must be omitted (fail-open)")
	}
}

func TestLoadServiceRegionAvailability_NonDenyErrorStaysQuiet(t *testing.T) {
	stub := &stubSSMAvailability{
		regionsByCode: map[string][]string{"dax": {"us-east-1"}},
		errByCode:     map[string]error{"throttled": errors.New("rate exceeded")},
	}
	out, denyErr := loadServiceRegionAvailability(context.Background(), stub, []string{"dax", "throttled"})
	if denyErr != nil {
		t.Errorf("non-deny error should not surface as denyErr, got %v", denyErr)
	}
	if !out["dax"]["us-east-1"] {
		t.Error("sibling code must still load when another errors")
	}
}

func TestDistinctRegionAvailabilityCodes(t *testing.T) {
	svcs := []serviceEntry{
		{name: "aws:dynamodb"},
		{name: "aws:dynamodb"},          // dup
		{name: "aws:iam", global: true}, // global skipped
		{name: "aws:dax"},
	}
	codes := distinctRegionAvailabilityCodes(svcs)
	if len(codes) != 2 {
		t.Fatalf("codes = %v, want 2 (dynamodb, dax)", codes)
	}
	got := map[string]bool{codes[0]: true, codes[1]: true}
	if !got["dynamodb"] || !got["dax"] || got["iam"] {
		t.Errorf("unexpected codes %v", codes)
	}
}

// TestBedrockAgentCoreOverride pins the one populated override and the reason it
// is populated. The service is the case the SDK table cannot cover: its SDK
// package (bedrockagentcorecontrol) ships an empty endpoint table, so the
// catalog is the only source with an opinion, and the catalog is only reachable
// through the hyphenated code AWS files it under.
//
// Both halves are asserted because either alone passes while the scan still
// warns: a correct code with an SDK opinion would never consult the catalog, and
// an SDK-silent service with the derived code hits a path that does not exist.
func TestBedrockAgentCoreOverride(t *testing.T) {
	const (
		service = "aws:bedrockagentcore"
		code    = "bedrock-agentcore"
	)

	if got := regionAvailabilityCode(service); got != code {
		t.Fatalf("regionAvailabilityCode(%q) = %q, want %q", service, got, code)
	}
	if got := regions.ServiceRegions(service); len(got) != 0 {
		t.Fatalf("SDK table now covers %s (%d regions) — the override exists because it did not; "+
			"re-verify it is still needed", service, len(got))
	}

	// us-west-1 and ap-northeast-3 are enabled for the scanned account but absent
	// from the catalog list, and both fail live even for org-admin credentials.
	catalog := map[string]map[string]bool{code: {"us-east-1": true, "us-west-2": true}}
	for region, want := range map[string]bool{
		"us-east-1":      true,
		"us-west-2":      true,
		"us-west-1":      false,
		"ap-northeast-3": false,
	} {
		if got := serviceAvailableInRegion(catalog, service, code, region); got != want {
			t.Errorf("serviceAvailableInRegion(%s, %s) = %v, want %v", service, region, got, want)
		}
	}
}
