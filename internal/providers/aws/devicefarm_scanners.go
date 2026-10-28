package aws

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeviceFarmProject, Service: "devicefarm", Redact: []redact.Rule{{Path: "EnvironmentVariables[*].Value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeDeviceFarmDevicePool, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmNetworkProfile, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmInstanceProfile, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmDeviceInstance, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmVPCEConfiguration, Service: "devicefarm"})
	registerType(restype.Descriptor{Type: TypeDeviceFarmTestGridProject, Service: "devicefarm"})
	// Url is a presigned S3 PUT URL: its signature is a bearer credential, and a
	// presigned URL re-minted per read would version-split an unchanged upload.
	// Redacting it to a constant covers both, so no Volatile rule is needed.
	registerType(restype.Descriptor{Type: TypeDeviceFarmUpload, Service: "devicefarm", Redact: []redact.Rule{{Path: "Url", Mode: redact.RedactScalar}}, Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:devicefarm",
		fn:     scanDeviceFarm,
		global: true, // Device Farm has a single regional endpoint (us-west-2 for aws).
	})
}

type deviceFarmAPI interface {
	ListProjects(context.Context, *devicefarm.ListProjectsInput, ...func(*devicefarm.Options)) (*devicefarm.ListProjectsOutput, error)
	ListDevicePools(context.Context, *devicefarm.ListDevicePoolsInput, ...func(*devicefarm.Options)) (*devicefarm.ListDevicePoolsOutput, error)
	ListNetworkProfiles(context.Context, *devicefarm.ListNetworkProfilesInput, ...func(*devicefarm.Options)) (*devicefarm.ListNetworkProfilesOutput, error)
	ListInstanceProfiles(context.Context, *devicefarm.ListInstanceProfilesInput, ...func(*devicefarm.Options)) (*devicefarm.ListInstanceProfilesOutput, error)
	ListDeviceInstances(context.Context, *devicefarm.ListDeviceInstancesInput, ...func(*devicefarm.Options)) (*devicefarm.ListDeviceInstancesOutput, error)
	ListVPCEConfigurations(context.Context, *devicefarm.ListVPCEConfigurationsInput, ...func(*devicefarm.Options)) (*devicefarm.ListVPCEConfigurationsOutput, error)
	ListTestGridProjects(context.Context, *devicefarm.ListTestGridProjectsInput, ...func(*devicefarm.Options)) (*devicefarm.ListTestGridProjectsOutput, error)
	ListUploads(context.Context, *devicefarm.ListUploadsInput, ...func(*devicefarm.Options)) (*devicefarm.ListUploadsOutput, error)
	ListTagsForResource(context.Context, *devicefarm.ListTagsForResourceInput, ...func(*devicefarm.Options)) (*devicefarm.ListTagsForResourceOutput, error)
}

const (
	dfTagsOp = "devicefarm:ListTagsForResource"
	// dfTestGridPageCap is the maxResult maximum of ListTestGridProjects. The
	// other listings read here model no page size maximum, so they are sent
	// none.
	dfTestGridPageCap = 1000
	// dfNoIdentifier names, in a warning, a listed element with neither an
	// ARN nor a name.
	dfNoIdentifier = "(an element with no identifier)"
)

// dfGone is the not-found code ListTagsForResource models, and the one
// ListDevicePools and ListNetworkProfiles model for a project deleted since
// it was listed.
var dfGone = []string{"NotFoundException"}

// scanDeviceFarm discovers Device Farm projects (with device pools, network
// profiles and uploaded app/test packages), account-level instance profiles,
// device instances, VPCE configs, and Selenium test-grid projects. Not scanned:
// device catalog; test runs and their job/suite/test/sample/artifact/session
// children.
//
// Every listing runs as its own phase, one at a time: a phase's error does
// not stop the later ones; a failed write and ctx's end do (runHostPhases).
// The per-project phases read the projects stored this scan. Every type but
// the upload, and curated device pools and network profiles, has its tags
// read per row (dfTags), whose denials are one warning for the whole scan.
func scanDeviceFarm(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("devicefarm")
	if err != nil {
		return 0, 0, err
	}
	client := devicefarm.NewFromConfig(acct.cfg, func(o *devicefarm.Options) { o.Region = region })
	tags := dfTags(client).sharingDenials()
	var projects []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "devicefarm", 0, []hostPhase{
		{op: "devicefarm:ListProjects", run: func(p *phaseTotals) (failed bool) {
			projects, failed = scanDeviceFarmProjects(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "devicefarm:ListDevicePools", run: func(p *phaseTotals) bool {
			return scanDeviceFarmDevicePools(ctx, client, acct, region, st, scanID, projects, tags, p)
		}},
		{op: "devicefarm:ListNetworkProfiles", run: func(p *phaseTotals) bool {
			return scanDeviceFarmNetworkProfiles(ctx, client, acct, region, st, scanID, projects, tags, p)
		}},
		{op: "devicefarm:ListInstanceProfiles", run: func(p *phaseTotals) bool {
			return scanDeviceFarmInstanceProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "devicefarm:ListDeviceInstances", run: func(p *phaseTotals) bool {
			return scanDeviceFarmDeviceInstances(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "devicefarm:ListVPCEConfigurations", run: func(p *phaseTotals) bool {
			return scanDeviceFarmVPCEConfigurations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "devicefarm:ListTestGridProjects", run: func(p *phaseTotals) bool {
			return scanDeviceFarmTestGridProjects(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "devicefarm:ListUploads", func() (int, int, error) {
			return scanDeviceFarmUploads(ctx, client, acct, region, st, scanID, childParentIDs(projects))
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// dfTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists every
// type read here under ListTagsForResource and TagResource (deviceinstance,
// devicepool, instanceprofile, networkprofile, project, testgrid-project,
// vpceconfiguration). The op returns every tag in one response.
// NotFoundException, its modeled not-found, drops a resource deleted after
// it was listed.
//
// A device pool or network profile of type CURATED is one AWS provides, not
// this account's (ManagedByProvider), so its tags are not read and it is
// stored untagged.
func dfTags(client deviceFarmAPI) rowTagReader {
	return rowTagReader{
		op:       dfTagsOp,
		notFound: dfGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return nil, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &devicefarm.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t dftypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// dfRow is the row of a listed element stored under its ARN and named name.
// An element without an ARN is not stored: one empty NativeID would merge
// every such element into one resource. what names the elements in that
// warning, keyed by key.
func dfRow(acct *account, region, scanID, typ, what string, arn *string, name, key string, element any) (*store.Resource, string, string) {
	a := sv(arn)
	if a == "" {
		return nil, what + " have no ARN", cmp.Or(key, dfNoIdentifier)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: a,
		Name: &name, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// scanDeviceFarmProjects stores the projects with their tags and returns
// the stored ones, keyed by ARN, as the parents of the per-project phases.
// The listing is held whole because every per-project phase needs all the
// projects as its parents. It reports whether the write failed, after which
// the scan must stop.
func scanDeviceFarmProjects(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "devicefarm:ListProjects", "projects", "Arn",
		func(ctx context.Context, token *string) ([]dftypes.Project, *string, error) {
			out, err := client.ListProjects(ctx, &devicefarm.ListProjectsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Projects, out.NextToken, nil
		},
		func(pr dftypes.Project) (*store.Resource, string, string) {
			return dfRow(acct, region, scanID, TypeDeviceFarmProject, "projects", pr.Arn, sv(pr.Name), sv(pr.Name), pr)
		},
		func(pr dftypes.Project) string { return sv(pr.Arn) },
		tags, p)
}

// scanDeviceFarmDevicePools stores each project's device pools, curated ones
// included, with the tags of this account's own (dfTags). A curated pool is
// one AWS provides, so it is ManagedByProvider. Each project is listed on its
// own, so one project's failure costs the others none of theirs, and each
// page is written as soon as its tags are read (storeEachParentPagesTags).
func scanDeviceFarmDevicePools(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, projects []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "devicefarm:ListDevicePools", dfGone, projects,
		func(ctx context.Context, projectARN string, token *string) ([]dftypes.DevicePool, *string, error) {
			out, err := client.ListDevicePools(ctx, &devicefarm.ListDevicePoolsInput{Arn: &projectARN, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DevicePools, out.NextToken, nil
		},
		func(_ string, dp dftypes.DevicePool) (*store.Resource, string, string) {
			r, why, key := dfRow(acct, region, scanID, TypeDeviceFarmDevicePool, "device pools", dp.Arn, sv(dp.Name), sv(dp.Name), dp)
			if r != nil {
				r.ManagedByProvider = dp.Type == dftypes.DevicePoolTypeCurated
			}
			return r, why, key
		},
		tags, p)
}

// scanDeviceFarmNetworkProfiles stores each project's network profiles,
// curated ones included, with the tags of this account's own (dfTags). A
// curated profile is one AWS provides, so it is ManagedByProvider. Each
// project is listed on its own, so one project's failure costs the others
// none of theirs, and each page is written as soon as its tags are read
// (storeEachParentPagesTags).
func scanDeviceFarmNetworkProfiles(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, projects []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "devicefarm:ListNetworkProfiles", dfGone, projects,
		func(ctx context.Context, projectARN string, token *string) ([]dftypes.NetworkProfile, *string, error) {
			out, err := client.ListNetworkProfiles(ctx, &devicefarm.ListNetworkProfilesInput{Arn: &projectARN, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkProfiles, out.NextToken, nil
		},
		func(_ string, np dftypes.NetworkProfile) (*store.Resource, string, string) {
			r, why, key := dfRow(acct, region, scanID, TypeDeviceFarmNetworkProfile, "network profiles", np.Arn, sv(np.Name), sv(np.Name), np)
			if r != nil {
				r.ManagedByProvider = np.Type == dftypes.NetworkProfileTypeCurated
			}
			return r, why, key
		},
		tags, p)
}

// scanDeviceFarmInstanceProfiles stores the account's instance profiles with
// their tags.
func scanDeviceFarmInstanceProfiles(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "devicefarm:ListInstanceProfiles",
		func(ctx context.Context, token *string) ([]dftypes.InstanceProfile, *string, error) {
			out, err := client.ListInstanceProfiles(ctx, &devicefarm.ListInstanceProfilesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InstanceProfiles, out.NextToken, nil
		},
		func(ip dftypes.InstanceProfile) (*store.Resource, string, string) {
			return dfRow(acct, region, scanID, TypeDeviceFarmInstanceProfile, "instance profiles", ip.Arn, sv(ip.Name), sv(ip.Name), ip)
		},
		tags, p)
}

// scanDeviceFarmDeviceInstances stores the account's private device
// instances with their tags. An instance has no name, so it is named by its
// ARN.
func scanDeviceFarmDeviceInstances(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "devicefarm:ListDeviceInstances",
		func(ctx context.Context, token *string) ([]dftypes.DeviceInstance, *string, error) {
			out, err := client.ListDeviceInstances(ctx, &devicefarm.ListDeviceInstancesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeviceInstances, out.NextToken, nil
		},
		func(d dftypes.DeviceInstance) (*store.Resource, string, string) {
			r, why, key := dfRow(acct, region, scanID, TypeDeviceFarmDeviceInstance, "device instances", d.Arn, sv(d.Arn), sv(d.Udid), d)
			if r != nil {
				status := string(d.Status)
				r.Status = &status
			}
			return r, why, key
		},
		tags, p)
}

// dfVPCENotAllowlisted reports the answer of ListVPCEConfigurations to an
// account not allowlisted for VPC endpoint configurations, an opt-in
// feature: the account has none, which is no error.
func dfVPCENotAllowlisted(err error) bool {
	return isAPIErrorWithMessage(err, "ServiceAccountException", "not allowlisted")
}

// scanDeviceFarmVPCEConfigurations stores the account's VPC endpoint
// configurations with their tags; an account not allowlisted for them has
// none (dfVPCENotAllowlisted).
func scanDeviceFarmVPCEConfigurations(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "devicefarm:ListVPCEConfigurations",
		firstPageGapPages(dfVPCENotAllowlisted, func(ctx context.Context, token *string) ([]dftypes.VPCEConfiguration, *string, error) {
			out, err := client.ListVPCEConfigurations(ctx, &devicefarm.ListVPCEConfigurationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VpceConfigurations, out.NextToken, nil
		}),
		func(c dftypes.VPCEConfiguration) (*store.Resource, string, string) {
			name := sv(c.VpceConfigurationName)
			return dfRow(acct, region, scanID, TypeDeviceFarmVPCEConfiguration, "VPC endpoint configurations", c.Arn, name, name, c)
		},
		tags, p)
}

// scanDeviceFarmTestGridProjects stores the account's test-grid projects with
// their tags.
func scanDeviceFarmTestGridProjects(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "devicefarm:ListTestGridProjects",
		func(ctx context.Context, token *string) ([]dftypes.TestGridProject, *string, error) {
			out, err := client.ListTestGridProjects(ctx, &devicefarm.ListTestGridProjectsInput{MaxResult: aws.Int32(dfTestGridPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TestGridProjects, out.NextToken, nil
		},
		func(tg dftypes.TestGridProject) (*store.Resource, string, string) {
			return dfRow(acct, region, scanID, TypeDeviceFarmTestGridProject, "test-grid projects", tg.Arn, sv(tg.Name), sv(tg.Name), tg)
		},
		tags, p)
}

// scanDeviceFarmUploads lists the app and test packages uploaded to each
// project the projects phase returned.
func scanDeviceFarmUploads(ctx context.Context, client deviceFarmAPI, acct *account, region string, st *store.Store, scanID string, projectARNs []string) (int, int, error) {
	var batch []*store.Resource
	// An upload ARN listed under more than one project (e.g. an AWS-curated
	// upload) must be upserted once per scan, or the second upsert version-splits it.
	seen := make(map[string]bool)
	warnedDenied := false
	for _, projectARN := range projectARNs {
		pager := devicefarm.NewListUploadsPaginator(client, &devicefarm.ListUploadsInput{Arn: &projectARN})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					// A project-scoped deny may cover only some projects: warn
					// once, keep listing the others.
					if !warnedDenied {
						warnedDenied = true
						_ = skipIfAccessDenied(st, "devicefarm:ListUploads", acct.ID, region, perr)
					}
					break
				}
				// Project deleted between ListProjects and ListUploads.
				if isAPIErrorCode(perr, "NotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("devicefarm:ListUploads %s: %w", projectARN, perr)
			}
			for _, u := range out.Uploads {
				arn := sv(u.Arn)
				if arn == "" || seen[arn] {
					continue
				}
				seen[arn] = true
				r := &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDeviceFarmUpload, NativeID: arn,
					Name: u.Name, Region: &region, CreatedAt: tp(u.Created),
					AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
					ManagedByProvider: u.Category == dftypes.UploadCategoryCurated,
				}
				if status := string(u.Status); status != "" {
					r.Status = &status
				}
				batch = append(batch, r)
			}
		}
	}
	return upsertBatch(st, batch, "devicefarm uploads")
}
