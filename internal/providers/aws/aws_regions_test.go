package aws

import (
	"cmp"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
)

func TestExpandAllRegions(t *testing.T) {
	full := awsregions.RegionsByPartition["aws"]
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"all sentinel", []string{"all"}, full},
		{"uppercase", []string{"ALL"}, full},
		{"padded", []string{" all "}, full},
		{"sentinel wins over specifics", []string{"all", "us-east-1"}, full},
		{"specific list unchanged", []string{"us-east-1", "us-west-2"}, []string{"us-east-1", "us-west-2"}},
		{"nil unchanged", nil, nil},
		{"empty unchanged", []string{}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandAllRegions("aws", tc.in)
			if !slices.Equal(got, tc.want) {
				t.Errorf("expandAllRegions(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestExpandAllRegionsCloneSemantics guards that the "all" expansion hands back
// a fresh slice — mutating it must not corrupt the package-level region list
// (mirrors TestRegionNamesCloneSemantics).
func TestExpandAllRegionsCloneSemantics(t *testing.T) {
	before := slices.Clone(awsregions.RegionsByPartition["aws"])
	got := expandAllRegions("aws", []string{"all"})
	if len(got) == 0 {
		t.Fatal("expandAllRegions([all]) returned empty")
	}
	got[0] = "mutated-region"
	if !slices.Equal(awsregions.RegionsByPartition["aws"], before) {
		t.Fatalf("mutating expandAllRegions result altered awsregions.RegionsByPartition[aws]: %v", awsregions.RegionsByPartition["aws"])
	}
}

// "all" expands to the account's own partition: a GovCloud account never gets
// a commercial region, and a partition the SDK does not know expands to none
// rather than to the commercial list.
func TestExpandAllRegions_AccountPartitionOnly(t *testing.T) {
	if len(awsregions.RegionsByPartition["aws-us-gov"]) == 0 {
		t.Fatal("RegionsByPartition[aws-us-gov] is empty; the cases below prove nothing")
	}
	cases := []struct {
		partition string
		want      []string
	}{
		{"aws-us-gov", awsregions.RegionsByPartition["aws-us-gov"]},
		{"aws-cn", awsregions.RegionsByPartition["aws-cn"]},
		{"aws-unknown", nil},
	}
	for _, tc := range cases {
		t.Run(tc.partition, func(t *testing.T) {
			got := expandAllRegions(tc.partition, []string{"all"})
			if !slices.Equal(got, tc.want) {
				t.Errorf("expandAllRegions(%q, [all]) = %v; want %v", tc.partition, got, tc.want)
			}
			if slices.Contains(got, "us-east-1") {
				t.Errorf("expandAllRegions(%q, [all]) includes commercial us-east-1", tc.partition)
			}
		})
	}
}

// RegionNames lists every partition, so the scan's scope column fits the
// widest region of any partition.
func TestRegionNames_EveryPartition(t *testing.T) {
	got := (&Scanner{}).RegionNames()
	if !slices.Equal(got, awsregions.Regions) {
		t.Errorf("RegionNames() = %v; want awsregions.Regions %v", got, awsregions.Regions)
	}
	if !slices.Contains(got, "us-gov-west-1") || !slices.Contains(got, "cn-north-1") {
		t.Errorf("RegionNames() = %v; lacks a GovCloud or China region", got)
	}
}

func testAccountIn(partition string) *account {
	acct := newTestAccount(testAccountID)
	acct.Partition = partition
	return acct
}

// commercialPins is, by SDK package, the region each homeRegion caller called
// for an aws account before homes were generated (read from the scanners at
// the Phase 1 commit). The aws output stays byte-identical only while
// homeRegion still gives these.
var commercialPins = map[string]string{
	"artifact":                     "us-east-1",
	"bcmdashboards":                "us-east-1",
	"bcmpricingcalculator":         "us-east-1",
	"billing":                      "us-east-1",
	"billingconductor":             "us-east-1",
	"budgets":                      "us-east-1",
	"chatbot":                      "us-east-2",
	"cloudfront":                   "us-east-1",
	"computeoptimizerautomation":   "us-east-1",
	"costandusagereportservice":    "us-east-1",
	"costexplorer":                 "us-east-1",
	"devicefarm":                   "us-west-2",
	"ecrpublic":                    "us-east-1",
	"globalaccelerator":            "us-west-2",
	"invoicing":                    "us-east-1",
	"licensemanager":               "us-east-1",
	"lightsail":                    "us-east-1",
	"networkmanager":               "us-west-2",
	"notifications":                "us-east-1",
	"notificationscontacts":        "us-east-1",
	"pricingplanmanager":           "us-east-1",
	"route53":                      "us-east-1",
	"route53globalresolver":        "us-east-2",
	"route53recoverycontrolconfig": "us-west-2",
	"route53recoveryreadiness":     "us-west-2",
	"savingsplans":                 "us-east-1",
	"servicequotas":                "us-east-1",
	"shield":                       "us-east-1",
	"supportapp":                   "us-east-1",
	"trustedadvisor":               "us-east-1",
	"uxc":                          "us-east-1",
	"waf":                          "us-east-1",
	"workspacesinstances":          "us-east-1",
}

// commercialOncePins is commercialPins for the onceRegion callers: the region
// each dedupe gate runs its once-per-account call in for an aws account (for
// the callers of the Phase 1 commit, where they ran it then).
var commercialOncePins = map[string]string{
	"cloudwatch":    "us-east-1",
	"directconnect": "us-east-1",
	"ec2":           "us-east-1",
	"groundstation": "us-west-2",
	"ram":           "us-east-1",
	"s3":            "us-east-1",
	"servicequotas": "us-east-1",
	"ssm":           "us-east-1",
}

func TestHomeRegion_CommercialMatchesPins(t *testing.T) {
	acct := testAccountIn("aws")
	for pkg, want := range commercialPins {
		got, err := acct.homeRegion(pkg)
		if err != nil || got != want {
			t.Errorf("homeRegion(%q) for aws = %q, %v; want %q, nil", pkg, got, err, want)
		}
		if !acct.isHomeRegion(pkg, want) {
			t.Errorf("isHomeRegion(%q, %q) for aws = false; want true", pkg, want)
		}
	}
	for pkg, want := range commercialOncePins {
		got, err := acct.onceRegion(pkg)
		if err != nil || got != want {
			t.Errorf("onceRegion(%q) for aws = %q, %v; want %q, nil", pkg, got, err, want)
		}
		if !acct.isOnceRegion(pkg, want) {
			t.Errorf("isOnceRegion(%q, %q) for aws = false; want true", pkg, want)
		}
	}
}

// An override exists only because the generated table disagrees with the
// commercial pin; once the SDK agrees, the entry is dead and must go.
func TestHomeRegion_OverridesStillDisagreeWithSDK(t *testing.T) {
	for _, c := range []struct {
		name      string
		overrides map[string]string
		table     map[string]map[string]string
	}{
		{"commercialHomeOverrides", commercialHomeOverrides, awsregions.Homes},
		{"commercialOnceOverrides", commercialOnceOverrides, awsregions.OnceRegions},
	} {
		for pkg, pin := range c.overrides {
			if sdk, ok := c.table[pkg]["aws"]; ok && sdk == pin {
				t.Errorf("%s[%q] = %q, which the generated table now gives too; drop the override", c.name, pkg, pin)
			}
		}
	}
}

// Homes and once-regions are keyed by SDK package, so a call naming anything
// else (a disco service name, a typo) has no region in any partition, aws
// included, and its scanner silently reports unavailable. Every lookup call
// must name, as a literal, an SDK service package the aws package imports (a
// gate may key on another service's home, as wafv2 does on CloudFront's), and
// the pin table for its kind must cover it so its aws region is pinned.
func TestHomeRegion_CallsNameAnImportedSDKPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	pinsFor := map[string]string{
		"homeRegion": "commercialPins", "isHomeRegion": "commercialPins", "homeGate": "commercialPins",
		"onceRegion": "commercialOncePins", "isOnceRegion": "commercialOncePins",
	}
	pkgArg := map[string]int{"homeGate": 1} // homeGate(st, pkg, op, region); the rest take pkg first
	pinTables := map[string]map[string]string{"commercialPins": commercialPins, "commercialOncePins": commercialOncePins}
	called := map[string]bool{} // "<pin table>:<pkg>"
	imported := map[string]bool{}
	var parsed []*ast.File
	for _, path := range files {
		// aws_regions.go defines the lookups; the is* forms and homeGate forward
		// their argument.
		if strings.HasSuffix(path, "_test.go") || path == "aws_regions.go" {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
		for _, spec := range file.Imports {
			if pkg, ok := strings.CutPrefix(strings.Trim(spec.Path.Value, `"`), "github.com/aws/aws-sdk-go-v2/service/"); ok {
				imported[pkg] = true
			}
		}
	}
	for _, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			table, isLookup := pinsFor[sel.Sel.Name]
			if !isLookup || len(call.Args) <= pkgArg[sel.Sel.Name] {
				return true
			}
			pos := fset.Position(call.Pos())
			lit, ok := call.Args[pkgArg[sel.Sel.Name]].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: %s takes a non-literal package; the guard cannot check it", pos, sel.Sel.Name)
				return true
			}
			pkg := strings.Trim(lit.Value, `"`)
			called[table+":"+pkg] = true
			if !imported[pkg] {
				t.Errorf("%s: %s(%q) names a package the aws package does not import", pos, sel.Sel.Name, pkg)
			}
			if _, ok := pinTables[table][pkg]; !ok {
				t.Errorf("%s: %s(%q) has no %s entry", pos, sel.Sel.Name, pkg, table)
			}
			return true
		})
	}
	// A pinned package nothing calls any more means a scanner went back to a
	// literal region, which no partition check covers.
	for table, pins := range pinTables {
		for pkg := range pins {
			if !called[table+":"+pkg] {
				t.Errorf("no lookup call of the kind %s pins names %q", table, pkg)
			}
		}
	}
}

func TestHomeRegion_OtherPartitions(t *testing.T) {
	cases := []struct {
		partition, pkg, want string
	}{
		// From the generated table, not the commercial pin.
		{"aws-us-gov", "route53", "us-gov-west-1"},
		{"aws-cn", "route53", "cn-northwest-1"},
		{"aws-us-gov", "networkmanager", "us-gov-west-1"},
		{"aws-cn", "cloudfront", "cn-northwest-1"},
	}
	for _, tc := range cases {
		got, err := testAccountIn(tc.partition).homeRegion(tc.pkg)
		if err != nil || got != tc.want {
			t.Errorf("homeRegion(%q) for %s = %q, %v; want %q, nil", tc.pkg, tc.partition, got, err, tc.want)
		}
	}
}

// With no SDK-confirmed home the answer is a typed unavailable error, never a
// guess: not the commercial override, not the partition's implicit region, not
// the once-region a dedupe gate would use.
func TestHomeRegion_NoHomeIsUnavailable(t *testing.T) {
	cases := []struct {
		partition, pkg string
		listed         bool // the package has a once-region there
	}{
		{"aws-us-gov", "budgets", false},           // generated table has no gov entry
		{"aws-us-gov", "globalaccelerator", false}, // commercial override must not leak
		{"aws-cn", "chatbot", false},               // override-only package
		{"aws-us-gov", "notifications", true},      // several gov regions: once-region only
		{"aws-us-gov", "licensemanager", true},
		{"aws-us-gov", "servicequotas", true},
		{"aws-cn", "servicequotas", true},
		{"aws-cn", "savingsplans", true}, // two cn regions listed: once-region only
		{"aws", "no-such-package", false},
	}
	for _, tc := range cases {
		acct := testAccountIn(tc.partition)
		got, err := acct.homeRegion(tc.pkg)
		var nr *noRegionError
		if !errors.As(err, &nr) || nr.Kind != "home" || nr.Package != tc.pkg || nr.Partition != tc.partition || nr.Listed != tc.listed {
			t.Errorf("homeRegion(%q) for %s = %q, %#v; want *noRegionError{home, %s, %s, Listed: %v}", tc.pkg, tc.partition, got, err, tc.pkg, tc.partition, tc.listed)
		}
		if !errors.Is(err, errServiceUnavailable) {
			t.Errorf("homeRegion(%q) for %s: error %v does not satisfy errors.Is(errServiceUnavailable)", tc.pkg, tc.partition, err)
		}
		home, _ := partitionHomeOf(tc.partition)
		if acct.isHomeRegion(tc.pkg, home) || acct.isHomeRegion(tc.pkg, "us-east-1") {
			t.Errorf("isHomeRegion(%q) for %s opened a gate with no confirmed home", tc.pkg, tc.partition)
		}
	}
}

// A dedupe gate opens in one region of the account's partition, not in the
// commercial region; a home gate stays shut where the SDK confirms no home,
// even in that once-region.
func TestIsOnceRegion_PerPartition(t *testing.T) {
	gov := testAccountIn("aws-us-gov")
	if !gov.isOnceRegion("directconnect", "us-gov-west-1") {
		t.Error("isOnceRegion(directconnect, us-gov-west-1) for aws-us-gov = false; want true")
	}
	if gov.isOnceRegion("directconnect", "us-east-1") || gov.isOnceRegion("directconnect", "us-gov-east-1") {
		t.Error("isOnceRegion(directconnect) for aws-us-gov opened outside us-gov-west-1")
	}
	if gov.isHomeRegion("directconnect", "us-gov-west-1") {
		t.Error("isHomeRegion(directconnect, us-gov-west-1) for aws-us-gov = true; the SDK confirms no gov home")
	}
	// groundstation lists no gov region, so there is no once-region either.
	_, err := gov.onceRegion("groundstation")
	var nr *noRegionError
	if !errors.As(err, &nr) || nr.Kind != "once" {
		t.Errorf("onceRegion(groundstation) for aws-us-gov err = %v; want *noRegionError{Kind: once}", err)
	}
}

func TestPartitionHomeOf(t *testing.T) {
	for partition, want := range map[string]string{"aws": "us-east-1", "aws-us-gov": "us-gov-west-1", "aws-cn": "cn-northwest-1"} {
		if got, err := partitionHomeOf(partition); err != nil || got != want {
			t.Errorf("partitionHomeOf(%q) = %q, %v; want %q, nil", partition, got, err, want)
		}
	}
	if got, err := partitionHomeOf("aws-unknown"); err == nil {
		t.Errorf("partitionHomeOf(aws-unknown) = %q, nil; want an error", got)
	}
}

// commercialGlobalCallRegions is where each global scanner calls for an aws
// account when the base config region is none of these (for the scanners of
// the Phase 1 commit, where they called then); a scanner absent here calls
// the config region.
var commercialGlobalCallRegions = map[string]string{
	"aws:aco-automation":             "us-east-1",
	"aws:artifact":                   "us-east-1",
	"aws:bcmdashboards":              "us-east-1",
	"aws:bcmpricingcalculator":       "us-east-1",
	"aws:billing":                    "us-east-1",
	"aws:billingconductor":           "us-east-1",
	"aws:budgets":                    "us-east-1",
	"aws:ce":                         "us-east-1",
	"aws:chatbot":                    "us-east-2",
	"aws:cloudfront":                 "us-east-1",
	"aws:cloudwatch-dashboards":      "us-east-1",
	"aws:cur":                        "us-east-1",
	"aws:devicefarm":                 "us-west-2",
	"aws:global-accelerator":         "us-west-2",
	"aws:ground-station-satellites":  "us-west-2",
	"aws:invoicing":                  "us-east-1",
	"aws:license-manager":            "us-east-1",
	"aws:networkmanager":             "us-west-2",
	"aws:notifications":              "us-east-1",
	"aws:notifications-contacts":     "us-east-1",
	"aws:pricingplanmanager":         "us-east-1",
	"aws:route53":                    "us-east-1",
	"aws:route53-recovery-control":   "us-west-2",
	"aws:route53-recovery-readiness": "us-west-2",
	"aws:route53globalresolver":      "us-east-2",
	"aws:s3":                         "us-east-1",
	"aws:savingsplans":               "us-east-1",
	"aws:servicequotas-template":     "us-east-1",
	"aws:shield":                     "us-east-1",
	"aws:support-app":                "us-east-1",
	"aws:trustedadvisor":             "us-east-1",
	"aws:uxc":                        "us-east-1",
	"aws:waf":                        "us-east-1",
}

// runGlobalDenied runs a global scanner for an account in partition whose base
// config region is cfgRegion, denying every call, and returns the distinct
// regions it called and its error.
func runGlobalDenied(t *testing.T, svc serviceEntry, partition, cfgRegion string) ([]string, error) {
	t.Helper()
	var regions parentCalls
	acct := testAccountIn(partition)
	acct.Regions = []string{cfgRegion}
	acct.scanRegions = []string{cfgRegion}
	cfg := opStubConfig(nil, apiErr("AccessDeniedException", "denied"))
	cfg.Region = cfgRegion
	acct.cfg = recordCallRegions(cfg, &regions)
	_, _, err := svc.fn(context.Background(), acct, "", newTestStore(t), testScanID)
	return regions.distinct(), err
}

// For an aws account every global scanner still calls where it did before
// homes were generated. runGlobalDenied denies every call, so a scanner stops
// at its first; that observes every client region only because each global
// scanner file builds one SDK client (TestGlobalScanners_OneClientPerFile).
// s3 is the exception: after ListBuckets it builds a client per bucket region
// (read from GetBucketLocation, not a pinned region), which TestScanS3_* cover.
func TestGlobalScanners_CommercialCallRegionsUnchanged(t *testing.T) {
	const cfgRegion = "eu-west-1"
	seen := 0
	for _, svc := range registeredServices {
		if !svc.global {
			continue
		}
		seen++
		want := cmp.Or(commercialGlobalCallRegions[svc.name], cfgRegion)
		t.Run(svc.name, func(t *testing.T) {
			got, err := runGlobalDenied(t, svc, "aws", cfgRegion)
			if errors.Is(err, errServiceUnavailable) {
				t.Fatalf("aws account: %v; want a call in %s", err, want)
			}
			if len(got) == 0 {
				t.Fatalf("aws account made no call; want one in %s", want)
			}
			for _, r := range got {
				if r != want {
					t.Errorf("aws account called %s; want only %s (all: %v)", r, want, got)
				}
			}
		})
	}
	if seen == 0 {
		t.Fatal("no global scanner is registered")
	}
	global := map[string]bool{}
	for _, svc := range registeredServices {
		global[svc.name] = svc.global
	}
	for name := range commercialGlobalCallRegions {
		if !global[name] {
			t.Errorf("commercialGlobalCallRegions names %q, which is not a registered global service", name)
		}
	}
}

// For an account in another partition every global scanner calls only that
// partition's regions, and one that returns unavailable has made no call.
func TestGlobalScanners_StayInAccountPartition(t *testing.T) {
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		home, err := partitionHomeOf(partition)
		if err != nil {
			t.Fatal(err)
		}
		own := awsregions.RegionsByPartition[partition]
		for _, svc := range registeredServices {
			if !svc.global {
				continue
			}
			t.Run(partition+"/"+svc.name, func(t *testing.T) {
				got, err := runGlobalDenied(t, svc, partition, home)
				for _, r := range got {
					if !slices.Contains(own, r) {
						t.Errorf("called %s, outside %s (all: %v)", r, partition, got)
					}
				}
				if errors.Is(err, errServiceUnavailable) && len(got) > 0 {
					t.Errorf("returned %v after calling %v; an unavailable scanner must make no call", err, got)
				}
			})
		}
	}
}

// Route53 in GovCloud goes to the gov home; Budgets, which the SDK lists in no
// gov region, makes no call and is dispatched as unavailable.
func TestGlobalScanners_GovCloudHomes(t *testing.T) {
	byName := map[string]serviceEntry{}
	for _, svc := range registeredServices {
		byName[svc.name] = svc
	}
	got, err := runGlobalDenied(t, byName["aws:route53"], "aws-us-gov", "us-gov-east-1")
	if err != nil || !slices.Equal(got, []string{"us-gov-west-1"}) {
		t.Errorf("route53 for aws-us-gov called %v, err %v; want [us-gov-west-1], nil", got, err)
	}
	got, err = runGlobalDenied(t, byName["aws:budgets"], "aws-us-gov", "us-gov-east-1")
	if len(got) != 0 || classifyServiceError(err) != outcomeUnavailable {
		t.Errorf("budgets for aws-us-gov called %v, err %v; want no call, classified unavailable", got, err)
	}
}

// A global scanner whose package has no home in the account's partition makes
// no call there and is dispatched as unavailable, even where a once-region
// exists.
func TestGlobalScanners_NoHomeMakesNoCall(t *testing.T) {
	byName := map[string]serviceEntry{}
	for _, svc := range registeredServices {
		byName[svc.name] = svc
	}
	cases := []struct{ partition, service string }{
		{"aws-us-gov", "aws:notifications"},
		{"aws-us-gov", "aws:license-manager"},
		{"aws-us-gov", "aws:servicequotas-template"},
		{"aws-cn", "aws:servicequotas-template"},
		{"aws-cn", "aws:savingsplans"},
	}
	for _, c := range cases {
		t.Run(c.partition+"/"+c.service, func(t *testing.T) {
			svc, ok := byName[c.service]
			if !ok {
				t.Fatalf("%s is not registered", c.service)
			}
			home, err := partitionHomeOf(c.partition)
			if err != nil {
				t.Fatal(err)
			}
			got, err := runGlobalDenied(t, svc, c.partition, home)
			if len(got) != 0 || classifyServiceError(err) != outcomeUnavailable {
				t.Errorf("called %v, err %v; want no call, classified unavailable", got, err)
			}
			var nr *noRegionError
			if !errors.As(err, &nr) || !nr.Listed {
				t.Errorf("err = %#v; want *noRegionError with Listed set (the SDK lists the package there)", err)
			}
		})
	}
}

// The dispatcher warns, under the service's name, when it skips a package the
// SDK lists in the partition but gives no home; a package with no listing
// there is skipped silently, as an absent service.
func TestReportServiceOutcome_ListedWithoutHomeWarns(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{"listed without home", &noRegionError{Kind: "home", Package: "notifications", Partition: "aws-us-gov", Listed: true}, 1},
		{"not listed", &noRegionError{Kind: "home", Package: "budgets", Partition: "aws-us-gov"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "aws:svc")
			reportServiceOutcome(st, "aws:svc", "global", testAccountID+"/global", 0, 0, 0, c.err)
			if *warns != c.wantWarns {
				t.Errorf("warnings under aws:svc = %d; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// sdkTableAllows fails open for a service with no SDK table and otherwise
// follows the table.
func TestSDKTableAllows(t *testing.T) {
	cases := []struct {
		service, region string
		want            bool
	}{
		{"aws:no-such-service", "us-east-1", true}, // no table: fail open
		{"aws:greengrass-v2", "us-gov-west-1", true},
		{"aws:greengrass-v2", "eu-west-3", false},
	}
	for _, c := range cases {
		if got := sdkTableAllows(c.service, c.region); got != c.want {
			t.Errorf("sdkTableAllows(%s, %s) = %v; want %v", c.service, c.region, got, c.want)
		}
	}
}

// Each file registering a global scanner builds one SDK client, so the first
// call TestGlobalScanners_CommercialCallRegionsUnchanged observes is in every
// client's region. s3 builds per-bucket clients (see that test). A new
// multi-client global scanner fails here until that test covers its clients.
func TestGlobalScanners_OneClientPerFile(t *testing.T) {
	files, err := filepath.Glob("*_scanners.go")
	if err != nil {
		t.Fatal(err)
	}
	global := regexp.MustCompile(`global:\s+true`)
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !global.Match(src) || path == "s3_scanners.go" {
			continue
		}
		if n := strings.Count(string(src), "NewFromConfig("); n > 1 {
			t.Errorf("%s registers a global scanner and builds %d SDK clients; check each client's region in TestGlobalScanners_CommercialCallRegionsUnchanged", path, n)
		}
	}
}

// A per-op home gate that stays shut for a package the SDK lists in the
// partition without a confirmed home warns once per scan under the op's name,
// however many regions ask; an unlisted package stays silent, and an open
// gate never warns.
func TestHomeGate_ListedWithoutHomeWarnsOnce(t *testing.T) {
	const op = "svc:GetThing"
	cases := []struct {
		name, partition, pkg string
		regions              []string
		wantOpen             bool
		wantWarns            int
	}{
		{"listed without home", "aws-us-gov", "notifications", []string{"us-gov-west-1", "us-gov-east-1"}, false, 1},
		{"not listed", "aws-us-gov", "budgets", []string{"us-gov-west-1", "us-gov-east-1"}, false, 0},
		{"commercial home", "aws", "lightsail", []string{"us-east-1"}, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, op)
			acct := testAccountIn(c.partition)
			for _, r := range c.regions {
				if got := acct.homeGate(st, c.pkg, op, r); got != c.wantOpen {
					t.Errorf("homeGate(%s, %s) = %v; want %v", c.pkg, r, got, c.wantOpen)
				}
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings under %s = %d; want %d", op, *warns, c.wantWarns)
			}
		})
	}
}
