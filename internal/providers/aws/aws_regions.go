package aws

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
	"github.com/icearp/disco-cli/regions"
	"github.com/icearp/disco-cli/store"
)

// RegionNames implements providers.RegionNamer with the commercial partition's
// regions, cloned so callers can't mutate the package-level list. `disco
// coverage regions` diffs it against ec2:DescribeRegions, which lists only the
// caller's own partition; run with another partition's credentials, that
// command reports mismatches, as it would with the unified list against
// commercial credentials.
func (s *Scanner) RegionNames() []string { return slices.Clone(awsregions.RegionsByPartition["aws"]) }

// expandAllRegions replaces the "all" sentinel (any case) with every region of
// partition; enabledScanRegions later trims it to the account's opted-in set.
// Returns a clone so callers can't mutate the package list. Non-sentinel input
// is returned unchanged. A partition the pinned SDK does not know has no
// regions, so "all" expands to none; loadAccounts rejects such a partition
// before it gets here.
func expandAllRegions(partition string, regions []string) []string {
	for _, r := range regions {
		if strings.EqualFold(strings.TrimSpace(r), "all") {
			return slices.Clone(awsregions.RegionsByPartition[partition])
		}
	}
	return regions
}

// sdkTableAllows reports whether the shipped SDK endpoint table lets service be
// scanned in region: it lists the region, or it has no table for the service,
// which fails open (regions.ServiceAvailable).
func sdkTableAllows(service, region string) bool {
	known, avail := regions.ServiceAvailable(service, region)
	return !known || avail
}

// partitionHomeOf returns the partition's implicit global region from
// partitions.json (us-east-1 for "aws"): the region an SDK config is given
// when neither the profile nor the environment names one, and the region an
// account scans when nothing configures its regions.
func partitionHomeOf(partition string) (string, error) {
	p, ok := lookupPartition(partition)
	if !ok || p.ImplicitGlobalRegion == "" {
		return "", fmt.Errorf("aws: partition %q has no implicit global region in the pinned SDK's partitions.json", partition)
	}
	return p.ImplicitGlobalRegion, nil
}

// noRegionError reports that an SDK service package has no region of the kind
// Kind names ("home" or "once") in the account's partition. It satisfies
// errors.Is(err, errServiceUnavailable), so a global scanner that returns it,
// before building a client, makes no call and the dispatcher reports
// "(region: unavailable)".
//
// Listed is set for a missing home when the package's table does list regions
// in the partition (it has a once-region): the service may well run there, the
// SDK just confirms no single home, so the dispatcher also warns rather than
// let the skip read as "not offered".
type noRegionError struct {
	Kind      string
	Package   string
	Partition string
	Listed    bool
}

func (e *noRegionError) Error() string {
	if e.Listed {
		return fmt.Sprintf("SDK service package %s is listed in partition %s but has no SDK-confirmed home region, so its account-wide call is not made", e.Package, e.Partition)
	}
	return fmt.Sprintf("SDK service package %s has no %s region in partition %s: its pinned endpoint table confirms none", e.Package, e.Kind, e.Partition)
}

func (e *noRegionError) Is(target error) bool { return target == errServiceUnavailable }

// homeRegion returns the home region of the aws-sdk-go-v2 service package pkg
// in the account's partition: the one region its account-wide data is read
// from. It is awsregions.Homes, which holds only what the package's endpoint
// table confirms (a partition-global endpoint's signing region, or the sole
// region the table lists), except that commercialHomeOverrides keeps the
// regions the scanners called for aws accounts before homes were generated.
// Every other partition has no fallback: without a confirmed home it returns a
// *noRegionError and the caller makes no call there.
func (a *account) homeRegion(pkg string) (string, error) {
	return a.lookupRegion("home", pkg, awsregions.Homes, commercialHomeOverrides)
}

// isHomeRegion reports whether region is pkg's home region in the account's
// partition. A per-op gate keyed on it opens only there, so a partition with
// no confirmed home never makes the gated call.
func (a *account) isHomeRegion(pkg, region string) bool {
	home, err := a.homeRegion(pkg)
	return err == nil && region == home
}

// homeGate is isHomeRegion for a per-op gate inside a regional scanner. When
// the gate stays shut because pkg is listed in the account's partition but has
// no SDK-confirmed home (noRegionError.Listed), it warns once per scan per
// (pkg, op), naming op, so the op's missing data is visible while the service
// reports OK; a package with no listing there stays silent.
func (a *account) homeGate(st *store.Store, pkg, op, region string) bool {
	if a.isHomeRegion(pkg, region) {
		return true
	}
	_, err := a.homeRegion(pkg)
	var nr *noRegionError
	if errors.As(err, &nr) && nr.Listed {
		if _, warned := a.listedGateWarned.LoadOrStore(pkg+" "+op, true); !warned {
			st.ReportWarning(store.ScanWarning{Provider: "aws", Service: op, Scope: a.ID, Message: nr.Error()})
		}
	}
	return false
}

// onceRegion returns the region a call is pinned to when its answer is the
// same from every region the service is offered in, so it runs once per
// account rather than once per scanned region. It is not a home: it is
// awsregions.OnceRegions, one region the package's endpoint table lists (the
// home when one is confirmed, otherwise the partition's implicit global
// region when the table lists it), except where commercialOnceOverrides keeps
// an earlier commercial choice. With none it returns a *noRegionError.
func (a *account) onceRegion(pkg string) (string, error) {
	return a.lookupRegion("once", pkg, awsregions.OnceRegions, commercialOnceOverrides)
}

// isOnceRegion reports whether region is pkg's once-region in the account's
// partition. A per-op gate keyed on it runs the op in one region only; where
// the table lists no region to pick, the op is not run.
func (a *account) isOnceRegion(pkg, region string) bool {
	once, err := a.onceRegion(pkg)
	return err == nil && region == once
}

func (a *account) lookupRegion(kind, pkg string, table map[string]map[string]string, overrides map[string]string) (string, error) {
	partition := a.partition()
	if partition == "aws" {
		if r, ok := overrides[pkg]; ok {
			return r, nil
		}
	}
	if r, ok := table[pkg][partition]; ok {
		return r, nil
	}
	_, listed := awsregions.OnceRegions[pkg][partition]
	return "", &noRegionError{Kind: kind, Package: pkg, Partition: partition, Listed: kind == "home" && listed}
}

// commercialHomeOverrides holds, by SDK package, the commercial homes the
// generated table does not give: the scanners called these regions for aws
// accounts before homes were generated, and the SDK either confirms no home or
// names a different one. They apply to the "aws" partition only.
// TestHomeRegion_OverridesStillDisagreeWithSDK fails once the SDK agrees with
// an entry, so it can be dropped.
var commercialHomeOverrides = map[string]string{
	// The SDK table lists Global Accelerator in every commercial region, so it
	// confirms no home. The Global Accelerator API reference
	// (https://docs.aws.amazon.com/global-accelerator/latest/api/Welcome.html)
	// directs control-plane calls to us-west-2.
	"globalaccelerator": "us-west-2",

	// The endpoint tables of these packages list the service in several
	// commercial regions, so they confirm no home; each region is the one the
	// scanner called before homes were generated, and its scanner records why.
	"ecrpublic":      "us-east-1",
	"licensemanager": "us-east-1",
	"lightsail":      "us-east-1",
	"notifications":  "us-east-1",
	"servicequotas":  "us-east-1",
	"supportapp":     "us-east-1",
	"trustedadvisor": "us-east-1",

	// The endpoint tables of these packages list no endpoint in any partition
	// (their clients resolve through endpoint rules alone), so the SDK data the
	// generator reads confirms no home. Each region is the one its scanner's
	// comment records.
	"artifact":                     "us-east-1",
	"bcmdashboards":                bcmDashboardsRegion,
	"bcmpricingcalculator":         bcmPricingCalculatorRegion,
	"billing":                      billingRegion,
	"chatbot":                      "us-east-2",
	"computeoptimizerautomation":   acoAutomationRegion,
	"invoicing":                    "us-east-1",
	"pricingplanmanager":           pricingPlanManagerRegion,
	"route53globalresolver":        "us-east-2",
	"route53recoverycontrolconfig": "us-west-2",
	"route53recoveryreadiness":     "us-west-2",
	"uxc":                          "us-east-1",
	"workspacesinstances":          "us-east-1",
}

// commercialOnceOverrides holds, by SDK package, commercial once-regions that
// differ from the generated choice, for the "aws" partition only.
var commercialOnceOverrides = map[string]string{
	// The generated choice is us-east-1, which hosts no ground station;
	// groundstation_satellites_scanners.go records why us-west-2 is used.
	"groundstation": groundStationSatelliteRegion,
}
