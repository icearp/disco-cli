package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTSoftwarePackage, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTSoftwarePackageVersion, Service: "iot"})
}

type iotSoftwareAPI interface {
	ListPackages(context.Context, *iot.ListPackagesInput, ...func(*iot.Options)) (*iot.ListPackagesOutput, error)
	GetPackage(context.Context, *iot.GetPackageInput, ...func(*iot.Options)) (*iot.GetPackageOutput, error)
	ListPackageVersions(context.Context, *iot.ListPackageVersionsInput, ...func(*iot.Options)) (*iot.ListPackageVersionsOutput, error)
	GetPackageVersion(context.Context, *iot.GetPackageVersionInput, ...func(*iot.Options)) (*iot.GetPackageVersionOutput, error)
}

// iotPackagesPageCap is the maxResults cap (1-100) the model documents for
// ListPackages and ListPackageVersions.
const iotPackagesPageCap = 100

// scanIoTSoftware lists the packages once, then runs the package phase and
// the version phase over them, adding each to p, and reports whether a
// write failed.
func scanIoTSoftware(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	pkgs := iotList(ctx, "iot:ListPackages", "iot software packages", "PackageName",
		func(ctx context.Context, token *string) ([]iottypes.PackageSummary, *string, error) {
			out, err := client.ListPackages(ctx, &iot.ListPackagesInput{NextToken: token, MaxResults: sdkaws.Int32(iotPackagesPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.PackageSummaries, out.NextToken, nil
		},
		func(x iottypes.PackageSummary) *string { return x.PackageName }, nil)
	return runIoTPhases(p,
		func(p *phaseTotals) bool {
			return scanIoTSoftwarePackages(ctx, client, acct, region, st, scanID, p, pkgs)
		},
		func(p *phaseTotals) bool {
			return scanIoTSoftwarePackageVersions(ctx, client, acct, region, st, scanID, p, pkgs.keys)
		},
	)
}

// scanIoTSoftwarePackages describes and stores the packages list holds.
func scanIoTSoftwarePackages(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, list *iotListing) (storeWriteFailed bool) {
	d := list.describe(ctx, st, acct, region, "iot:GetPackage", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.GetPackage(ctx, &iot.GetPackageInput{PackageName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.PackageArn)
			if arn == "" {
				return nil, nil
			}
			pname := sv(out.PackageName)
			// No tag read: the Service Reference lists neither package nor
			// packageversion among ListTagsForResource's resources.
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTSoftwarePackage,
				NativeID:       arn,
				Name:           &pname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStore(st, acct, region, p, list, d)
}

// iotPackageVersion is one listed version of a software package.
type iotPackageVersion struct{ pkg, ver string }

// scanIoTSoftwarePackageVersions lists the versions of each package in pkgs
// and describes each with GetPackageVersion (iotDescribe). One package's
// version listing failing does not stop the others': its versions read
// before the failure are kept, a denial is warned about once after the
// write, and every other failure is added to p under ListPackageVersions,
// the transient ones apart so that phaseErrs can demote them. An NXDOMAIN
// is flattened (lateNXDOMAIN): ListPackages reached the endpoint. A listing
// cut short because ctx ended stops the loop, and one error says how many
// packages were not listed in full; every later package would fail the same
// way.
func scanIoTSoftwarePackageVersions(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, pkgs []string) (storeWriteFailed bool) {
	const listOp = "iot:ListPackageVersions"
	var (
		keys       []iotPackageVersion
		denied     skipTally
		transients transientTally
		errs       []error
	)
	for i, pkg := range pkgs {
		versions, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]iottypes.PackageVersionSummary, *string, error) {
			out, err := client.ListPackageVersions(ctx, &iot.ListPackageVersionsInput{
				PackageName: &pkg, NextToken: token, MaxResults: sdkaws.Int32(iotPackagesPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.PackageVersionSummaries, out.NextToken, nil
		})
		for _, v := range versions {
			if v.VersionName != nil {
				keys = append(keys, iotPackageVersion{pkg, *v.VersionName})
			}
		}
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			errs = append(errs, fmt.Errorf("%s: versions of %d of %d package(s) not listed, or listed in part: %w", listOp, len(pkgs)-i, len(pkgs), err))
			break
		}
		switch err := lateNXDOMAIN(listOp, err); {
		case err == nil:
		case isAccessDenied(err):
			denied.skipped(pkg, err)
		case transients.drop(pkg, err):
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", listOp, pkg, err))
		}
	}
	d := iotDescribe(ctx, st, acct, region, "iot:GetPackageVersion", "iot software package versions", keys,
		func(k iotPackageVersion) string { return k.pkg + "/" + k.ver }, isIoTNotFound, nil,
		func(ctx context.Context, k iotPackageVersion) (*store.Resource, error) {
			out, err := client.GetPackageVersion(ctx, &iot.GetPackageVersionInput{PackageName: &k.pkg, VersionName: &k.ver})
			if err != nil {
				return nil, err
			}
			arn := sv(out.PackageVersionArn)
			if arn == "" {
				return nil, nil
			}
			label := fmt.Sprintf("%s:%s", k.pkg, k.ver)
			status := string(out.Status)
			// No tag read: the Service Reference lists neither package nor
			// packageversion among ListTagsForResource's resources.
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTSoftwarePackageVersion,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	total, inserted, err := storeReadRows(st, acct, region, listOp, d.rows, nil, func() {
		d.warn()
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, listOp, acct.ID, region, fmt.Errorf("versions of %d package(s) not listed, first %s: %w", denied.count, denied.first, denied.err))
		}
	})
	stop := p.addPhase(listOp, total, inserted, err)
	p.errs.add(listOp, errors.Join(errs...))
	if t := &transients.skipTally; t.count > 0 {
		p.errs.add(listOp, fmt.Errorf("%s: versions of %d package(s) not listed, first %s: %w", listOp, t.count, t.first, t.err))
	}
	p.errs.add(d.op, d.hard)
	p.errs.add(d.op, d.transient)
	return stop
}
