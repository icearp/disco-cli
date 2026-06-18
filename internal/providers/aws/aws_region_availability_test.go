package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
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

func TestServiceAvailableInRegion(t *testing.T) {
	avail := map[string]map[string]bool{
		"dax": {"us-east-1": true, "eu-west-1": true},
	}
	cases := []struct {
		name   string
		m      map[string]map[string]bool
		code   string
		region string
		want   bool
	}{
		{"nil map fails open", nil, "dax", "ap-south-1", true},
		{"unknown code fails open", avail, "memorydb", "ap-south-1", true},
		{"known code present in region", avail, "dax", "us-east-1", true},
		{"known code absent from region skips", avail, "dax", "ap-south-1", false},
		{"empty region set fails open", map[string]map[string]bool{"x": {}}, "x", "us-east-1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceAvailableInRegion(c.m, c.code, c.region); got != c.want {
				t.Errorf("serviceAvailableInRegion(%q,%q) = %v, want %v", c.code, c.region, got, c.want)
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
