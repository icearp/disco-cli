package aws

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	actypes "github.com/aws/aws-sdk-go-v2/service/appconfig/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppConfigApplication, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigConfigurationProfile, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigDeployment, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigDeploymentStrategy, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigEnvironment, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigExperimentDefinition, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigExtension, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigExtensionAssociation, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigHostedConfigurationVersion, Service: "appconfig", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:appconfig",
		fn:   scanAppConfig,
	})
}

type appconfigAPI interface {
	ListApplications(context.Context, *appconfig.ListApplicationsInput, ...func(*appconfig.Options)) (*appconfig.ListApplicationsOutput, error)
	ListConfigurationProfiles(context.Context, *appconfig.ListConfigurationProfilesInput, ...func(*appconfig.Options)) (*appconfig.ListConfigurationProfilesOutput, error)
	ListDeployments(context.Context, *appconfig.ListDeploymentsInput, ...func(*appconfig.Options)) (*appconfig.ListDeploymentsOutput, error)
	ListDeploymentStrategies(context.Context, *appconfig.ListDeploymentStrategiesInput, ...func(*appconfig.Options)) (*appconfig.ListDeploymentStrategiesOutput, error)
	ListEnvironments(context.Context, *appconfig.ListEnvironmentsInput, ...func(*appconfig.Options)) (*appconfig.ListEnvironmentsOutput, error)
	ListExperimentDefinitions(context.Context, *appconfig.ListExperimentDefinitionsInput, ...func(*appconfig.Options)) (*appconfig.ListExperimentDefinitionsOutput, error)
	ListExtensions(context.Context, *appconfig.ListExtensionsInput, ...func(*appconfig.Options)) (*appconfig.ListExtensionsOutput, error)
	ListExtensionAssociations(context.Context, *appconfig.ListExtensionAssociationsInput, ...func(*appconfig.Options)) (*appconfig.ListExtensionAssociationsOutput, error)
	ListHostedConfigurationVersions(context.Context, *appconfig.ListHostedConfigurationVersionsInput, ...func(*appconfig.Options)) (*appconfig.ListHostedConfigurationVersionsOutput, error)
	ListTagsForResource(context.Context, *appconfig.ListTagsForResourceInput, ...func(*appconfig.Options)) (*appconfig.ListTagsForResourceOutput, error)
}

// acARN builds arn:{p}:appconfig:{region}:{acct}:{seg1}/{seg2}/….
func acARN(partition, region, acct string, segs ...string) string {
	return arnFor(partition, "appconfig", region, acct, strings.Join(segs, "/"))
}

const (
	acTagsOp = "appconfig:ListTagsForResource"
	// acPageCap is the MaxResults maximum of every AppConfig listing read
	// here.
	acPageCap = 50
	// acNoIdentifier names, in a warning, a listed element with no id.
	acNoIdentifier = "(an element with no identifier)"
)

// acGone is the not-found code ListTagsForResource models, and the one
// ListEnvironments, ListConfigurationProfiles, ListDeployments and
// ListHostedConfigurationVersions model for a parent deleted since it was
// listed.
var acGone = []string{"ResourceNotFoundException"}

// acChild is a stored environment or configuration profile: its
// application's id, its own id, and its ARN, which its child listing is keyed
// by.
type acChild struct{ appID, id, arn string }

// scanAppConfig discovers the AppConfig resources of one region. Every
// listing runs as its own phase, one at a time: a phase's error does not stop
// the later ones; a failed write and ctx's end do (runHostPhases). The child
// phases read the parents stored this scan, those parents concurrently.
// Every type but the hosted configuration version, which is not taggable, has
// its tags read per row (acTags), whose denials are one warning for the whole
// scan.
func scanAppConfig(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appconfig.NewFromConfig(acct.cfg, func(o *appconfig.Options) { o.Region = region })
	tags := acTags(client).sharingDenials()
	var (
		apps           []childParent
		envs, profiles []acChild
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "appconfig", 0, []hostPhase{
		{op: "appconfig:ListApplications", run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanACApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "appconfig:ListEnvironments", run: func(p *phaseTotals) (failed bool) {
			envs, failed = scanACEnvironments(ctx, client, acct, region, st, scanID, apps, tags, p)
			return failed
		}},
		{op: "appconfig:ListConfigurationProfiles", run: func(p *phaseTotals) (failed bool) {
			profiles, failed = scanACConfigurationProfiles(ctx, client, acct, region, st, scanID, apps, tags, p)
			return failed
		}},
		{op: "appconfig:ListDeployments", run: func(p *phaseTotals) bool {
			return scanACDeployments(ctx, client, acct, region, st, scanID, envs, tags, p)
		}},
		countedPhase(0, "appconfig:ListHostedConfigurationVersions", func() (int, int, error) {
			return scanACHostedConfigVersions(ctx, client, acct, region, st, scanID, profiles)
		}),
		{op: "appconfig:ListDeploymentStrategies", run: func(p *phaseTotals) bool {
			return scanACDeploymentStrategies(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appconfig:ListExtensions", run: func(p *phaseTotals) bool {
			return scanACExtensions(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appconfig:ListExtensionAssociations", run: func(p *phaseTotals) bool {
			return scanACExtensionAssociations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appconfig:ListExperimentDefinitions", run: func(p *phaseTotals) bool {
			return scanACExperimentDefinitions(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// acTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN ListExtensions returned for an extension, and for every
// other type the ARN built in the Service Reference's format for it. The
// Service Reference lists every ARN resource type read here under
// ListTagsForResource (application, configurationprofile, deployment,
// deploymentstrategy, environment, experimentdefinition, extension,
// extensionassociation). The op does not page its tags.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
//
// An AWS-owned row (ManagedByProvider: a predefined deployment strategy, an
// AWS-authored extension) is not read: whether the op accepts its ARN is
// unverified, a refusal would fail every scan (every region lists them), and
// a not-found answer would drop the row. Such a row keeps the TagsJSON its
// builder set.
func acTags(client appconfigAPI) rowTagReader {
	return rowTagReader{
		op:       acTagsOp,
		notFound: acGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &appconfig.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// acRow is the row of a listed element stored under nativeID and named name.
func acRow(acct *account, region, scanID, typ, nativeID, name string, element any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &name, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

// acNoID is the not-stored result of an element without the id its NativeID
// is built from: one empty id would merge every such element into one
// resource. what names the elements in the warning, name the element.
func acNoID(what string, name *string) (*store.Resource, string, string) {
	return nil, what + " have no Id", cmp.Or(sv(name), acNoIdentifier)
}

// scanACApplications stores the applications with their tags and returns the
// stored ones as the parents of the environment and configuration profile
// phases. Applications are few (a per-region quota), so their listing is held
// whole. It reports whether the write failed, after which the scan must stop.
func scanACApplications(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "appconfig:ListApplications", "applications", "Id",
		func(ctx context.Context, token *string) ([]actypes.Application, *string, error) {
			out, err := client.ListApplications(ctx, &appconfig.ListApplicationsInput{MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(a actypes.Application) (*store.Resource, string, string) {
			id := sv(a.Id)
			if id == "" {
				return acNoID("applications", a.Name)
			}
			return acRow(acct, region, scanID, TypeAppConfigApplication, acARN(acct.partition(), region, acct.ID, "application", id), cmp.Or(sv(a.Name), id), a), "", ""
		},
		func(a actypes.Application) string { return sv(a.Id) },
		tags, p)
}

// acChildren returns the stored rows as children, each looked up in ids.
func acChildren(stored []*store.Resource, ids map[*store.Resource]acChild) []acChild {
	children := make([]acChild, len(stored))
	for i, r := range stored {
		children[i] = ids[r]
	}
	return children
}

// scanACEnvironments stores each application's environments with their tags
// and returns the stored ones, the parents of the deployment phase. One
// application's failure costs the others none of theirs.
func scanACEnvironments(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent, tags rowTagReader, p *phaseTotals) ([]acChild, bool) {
	ids := map[*store.Resource]acChild{}
	stored, failed := storeEachParentTags(ctx, st, acct, region, "appconfig:ListEnvironments", acGone, apps,
		func(ctx context.Context, appID string, token *string) ([]actypes.Environment, *string, error) {
			out, err := client.ListEnvironments(ctx, &appconfig.ListEnvironmentsInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(appID string, e actypes.Environment) (*store.Resource, string, string) {
			id := sv(e.Id)
			if id == "" {
				return acNoID("environments", e.Name)
			}
			arn := acEnvironmentNativeID(acARN(acct.partition(), region, acct.ID, "application", appID), id)
			r := acRow(acct, region, scanID, TypeAppConfigEnvironment, arn, cmp.Or(sv(e.Name), id), e)
			ids[r] = acChild{appID: appID, id: id, arn: arn}
			return r, "", ""
		},
		tags, p)
	return acChildren(stored, ids), failed
}

// scanACConfigurationProfiles stores each application's configuration
// profiles with their tags and returns the stored ones, the parents of the
// hosted configuration version phase. One application's failure costs the
// others none of theirs.
func scanACConfigurationProfiles(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent, tags rowTagReader, p *phaseTotals) ([]acChild, bool) {
	ids := map[*store.Resource]acChild{}
	stored, failed := storeEachParentTags(ctx, st, acct, region, "appconfig:ListConfigurationProfiles", acGone, apps,
		func(ctx context.Context, appID string, token *string) ([]actypes.ConfigurationProfileSummary, *string, error) {
			out, err := client.ListConfigurationProfiles(ctx, &appconfig.ListConfigurationProfilesInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(appID string, c actypes.ConfigurationProfileSummary) (*store.Resource, string, string) {
			id := sv(c.Id)
			if id == "" {
				return acNoID("configuration profiles", c.Name)
			}
			arn := acProfileNativeID(acARN(acct.partition(), region, acct.ID, "application", appID), id)
			r := acRow(acct, region, scanID, TypeAppConfigConfigurationProfile, arn, cmp.Or(sv(c.Name), id), c)
			ids[r] = acChild{appID: appID, id: id, arn: arn}
			return r, "", ""
		},
		tags, p)
	return acChildren(stored, ids), failed
}

// acChildParents is children as the parents their child listings key on,
// each by its ARN, and the children by that ARN.
func acChildParents(children []acChild) ([]childParent, map[string]acChild) {
	parents := make([]childParent, len(children))
	byARN := make(map[string]acChild, len(children))
	for i, c := range children {
		parents[i] = childParent{id: c.arn, arn: c.arn}
		byARN[c.arn] = c
	}
	return parents, byARN
}

// scanACDeployments stores each environment's deployments with their tags.
// Deployments accumulate with every rollout, so each page is written as soon
// as its tags are read (storeEachParentPagesTags); one environment's failure
// costs the others none of theirs. A deployment has no ARN in its listing;
// its NativeID is the Service Reference's
// application/{ApplicationId}/environment/{EnvironmentId}/deployment/{DeploymentNumber}.
func scanACDeployments(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, envs []acChild, tags rowTagReader, p *phaseTotals) bool {
	parents, byARN := acChildParents(envs)
	return storeEachParentPagesTags(ctx, st, acct, region, "appconfig:ListDeployments", acGone, parents,
		func(ctx context.Context, envARN string, token *string) ([]actypes.DeploymentSummary, *string, error) {
			env := byARN[envARN]
			out, err := client.ListDeployments(ctx, &appconfig.ListDeploymentsInput{
				ApplicationId: &env.appID, EnvironmentId: &env.id, MaxResults: sdkaws.Int32(acPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(envARN string, d actypes.DeploymentSummary) (*store.Resource, string, string) {
			arn := fmt.Sprintf("%s/deployment/%d", envARN, d.DeploymentNumber)
			return acRow(acct, region, scanID, TypeAppConfigDeployment, arn, cmp.Or(sv(d.ConfigurationName), arn), d), "", ""
		},
		tags, p)
}

// scanACHostedConfigVersions stores each configuration profile's hosted
// configuration versions, which are not taggable. The listing returns
// version metadata only; the configuration content is never fetched. One
// profile's failure costs the others none of theirs. Every profile's versions
// are held until one write: a summary is a few small fields.
func scanACHostedConfigVersions(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, profiles []acChild) (int, int, error) {
	parents, byARN := acChildParents(profiles)
	return storeTokenEachParent(ctx, st, acct, region, "appconfig:ListHostedConfigurationVersions", acGone, childParentIDs(parents),
		func(ctx context.Context, profileARN string, token *string) ([]actypes.HostedConfigurationVersionSummary, *string, error) {
			cp := byARN[profileARN]
			out, err := client.ListHostedConfigurationVersions(ctx, &appconfig.ListHostedConfigurationVersionsInput{
				ApplicationId: &cp.appID, ConfigurationProfileId: &cp.id, MaxResults: sdkaws.Int32(acPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(profileARN string, v actypes.HostedConfigurationVersionSummary) (*store.Resource, string, string) {
			arn := fmt.Sprintf("%s/hostedconfigurationversion/%d", profileARN, v.VersionNumber)
			return acRow(acct, region, scanID, TypeAppConfigHostedConfigurationVersion, arn, cmp.Or(sv(v.VersionLabel), arn), v), "", ""
		})
}

// acPredefinedStrategyPrefix starts the id of every AWS-predefined deployment
// strategy (AppConfig.AllAtOnce, AppConfig.Linear50PercentEvery30Seconds,
// …): the model's DeploymentStrategyId pattern allows the prefix only on
// those, a customer's id being 4 to 7 lowercase letters and digits. The name
// cannot decide it: a customer may name a strategy anything.
const acPredefinedStrategyPrefix = "AppConfig."

func scanACDeploymentStrategies(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appconfig:ListDeploymentStrategies",
		func(ctx context.Context, token *string) ([]actypes.DeploymentStrategy, *string, error) {
			out, err := client.ListDeploymentStrategies(ctx, &appconfig.ListDeploymentStrategiesInput{MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(d actypes.DeploymentStrategy) (*store.Resource, string, string) {
			id := sv(d.Id)
			if id == "" {
				return acNoID("deployment strategies", d.Name)
			}
			r := acRow(acct, region, scanID, TypeAppConfigDeploymentStrategy, acARN(acct.partition(), region, acct.ID, "deploymentstrategy", id), cmp.Or(sv(d.Name), id), d)
			r.ManagedByProvider = strings.HasPrefix(id, acPredefinedStrategyPrefix)
			return r, "", ""
		},
		tags, p)
}

func scanACExtensions(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appconfig:ListExtensions",
		func(ctx context.Context, token *string) ([]actypes.ExtensionSummary, *string, error) {
			out, err := client.ListExtensions(ctx, &appconfig.ListExtensionsInput{MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(e actypes.ExtensionSummary) (*store.Resource, string, string) {
			arn := sv(e.Arn)
			if arn == "" {
				return nil, "extensions have no ARN", cmp.Or(sv(e.Id), sv(e.Name), acNoIdentifier)
			}
			r := acRow(acct, region, scanID, TypeAppConfigExtension, arn, cmp.Or(sv(e.Name), arn), e)
			// AWS-owned extensions carry an Id like "AWS.AppConfig.JiraIntegration" /
			// "AWS.AppConfig.FeatureFlags" / "AWS.SNS" / "AWS.Lambda". Customer Ids are
			// generated alphanumerics — Id-prefix is stable; Name is user-editable.
			r.ManagedByProvider = strings.HasPrefix(sv(e.Id), "AWS.")
			return r, "", ""
		},
		tags, p)
}

// scanACExtensionAssociations stores the extension associations with their
// tags, a page at a time: an association can be made per application,
// environment and configuration profile, so the listing can be large.
func scanACExtensionAssociations(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appconfig:ListExtensionAssociations",
		func(ctx context.Context, token *string) ([]actypes.ExtensionAssociationSummary, *string, error) {
			out, err := client.ListExtensionAssociations(ctx, &appconfig.ListExtensionAssociationsInput{MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(a actypes.ExtensionAssociationSummary) (*store.Resource, string, string) {
			id := sv(a.Id)
			if id == "" {
				return nil, "extension associations have no Id", cmp.Or(sv(a.ResourceArn), acNoIdentifier)
			}
			return acRow(acct, region, scanID, TypeAppConfigExtensionAssociation, acARN(acct.partition(), region, acct.ID, "extensionassociation", id), id, a), "", ""
		},
		tags, p)
}

// scanACExperimentDefinitions stores every experiment definition in the
// region, with its tags, a page at a time; the op's input has no required
// member and none is sent. A summary carries no ARN, so the NativeID is the
// Service Reference format
// application/{ApplicationId}/experimentdefinition/{Id}.
//
// ListExperimentDefinitions is newer than the appconfig endpoints it shares, so
// a region may serve appconfig without it. On the first call, the shapes an
// unrouted operation takes (isOpNotDeployed) are a sub-feature gap, skipped
// silently. ResourceNotFoundException, the op's modeled not-found, is included
// because an unfiltered list names nothing that could be missing. Once a page
// has succeeded the op is deployed: a later error of any shape except a denial
// is returned, after the rows listed so far are stored.
func scanACExperimentDefinitions(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	isGap := func(err error) bool { return isOpNotDeployed(err) || isAPIErrorCode(err, "ResourceNotFoundException") }
	return storeTokenPagesTags(ctx, st, acct, region, "appconfig:ListExperimentDefinitions",
		firstPageGapPages(isGap, func(ctx context.Context, token *string) ([]actypes.ExperimentDefinitionSummary, *string, error) {
			out, err := client.ListExperimentDefinitions(ctx, &appconfig.ListExperimentDefinitionsInput{MaxResults: sdkaws.Int32(acPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(d actypes.ExperimentDefinitionSummary) (*store.Resource, string, string) {
			appID, id := sv(d.ApplicationId), sv(d.Id)
			if appID == "" || id == "" {
				return nil, "experiment definitions have no ApplicationId or Id", cmp.Or(id, sv(d.Name), acNoIdentifier)
			}
			r := acRow(acct, region, scanID, TypeAppConfigExperimentDefinition, acARN(acct.partition(), region, acct.ID, "application", appID, "experimentdefinition", id), cmp.Or(sv(d.Name), id), d)
			r.CreatedAt = tp(d.CreatedAt)
			if d.Status != "" {
				status := string(d.Status)
				r.Status = &status
			}
			return r, "", ""
		},
		tags, p)
}

func acEnvironmentNativeID(appARN, envID string) string {
	return appARN + "/environment/" + envID
}

func acProfileNativeID(appARN, profileID string) string {
	return appARN + "/configurationprofile/" + profileID
}
