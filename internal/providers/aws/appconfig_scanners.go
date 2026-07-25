package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppConfigApplication, Service: "appconfig", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppConfigConfigurationProfile, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigDeployment, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigDeploymentStrategy, Service: "appconfig", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppConfigEnvironment, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigExtension, Service: "appconfig", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppConfigExtensionAssociation, Service: "appconfig"})
	registerType(restype.Descriptor{Type: TypeAppConfigHostedConfigurationVersion, Service: "appconfig"})
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
	ListExtensions(context.Context, *appconfig.ListExtensionsInput, ...func(*appconfig.Options)) (*appconfig.ListExtensionsOutput, error)
	ListExtensionAssociations(context.Context, *appconfig.ListExtensionAssociationsInput, ...func(*appconfig.Options)) (*appconfig.ListExtensionAssociationsOutput, error)
	ListHostedConfigurationVersions(context.Context, *appconfig.ListHostedConfigurationVersionsInput, ...func(*appconfig.Options)) (*appconfig.ListHostedConfigurationVersionsOutput, error)
}

func acARN(region, acct string, segs ...string) string {
	s := fmt.Sprintf("arn:aws:appconfig:%s:%s", region, acct)
	for _, seg := range segs {
		s += "/" + seg
	}
	// Replace first '/' separator with ':' to match AWS appconfig ARN format.
	for i := len(fmt.Sprintf("arn:aws:appconfig:%s:%s", region, acct)); i < len(s); i++ {
		if s[i] == '/' {
			return s[:i] + ":" + s[i+1:]
		}
	}
	return s
}

type acAppRef struct {
	id  string
	arn string
}

type acCfgProfileRef struct {
	appID string
	id    string
	arn   string
}

type acEnvRef struct {
	appID string
	id    string
	arn   string
}

func scanAppConfig(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := appconfig.NewFromConfig(acct.cfg, func(o *appconfig.Options) { o.Region = region })

	apps, t, i, ferr := scanACApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	var envs []acEnvRef
	var cps []acCfgProfileRef
	for _, app := range apps {
		es, t, i, perr := scanACEnvironments(ctx, client, acct, region, st, scanID, app)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
		envs = append(envs, es...)

		cs, t, i, perr := scanACConfigurationProfiles(ctx, client, acct, region, st, scanID, app)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
		cps = append(cps, cs...)
	}

	for _, env := range envs {
		t, i, perr := scanACDeployments(ctx, client, acct, region, st, scanID, env)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	for _, cp := range cps {
		t, i, perr := scanACHostedConfigVersions(ctx, client, acct, region, st, scanID, cp)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanACDeploymentStrategies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanACExtensions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanACExtensionAssociations(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanACApplications(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string) ([]acAppRef, int, int, error) {
	pager := appconfig.NewListApplicationsPaginator(client, &appconfig.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []acAppRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListApplications", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appconfig:ListApplications: %w", perr)
		}
		for _, a := range out.Items {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := acARN(region, acct.ID, "application", id)
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			apps = append(apps, acAppRef{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigApplication, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appconfig applications")
	return apps, t, i, err
}

func scanACEnvironments(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, app acAppRef) ([]acEnvRef, int, int, error) {
	aid := app.id
	pager := appconfig.NewListEnvironmentsPaginator(client, &appconfig.ListEnvironmentsInput{ApplicationId: &aid})
	var batch []*store.Resource
	var envs []acEnvRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListEnvironments", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appconfig:ListEnvironments: %w", perr)
		}
		for _, e := range out.Items {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/environment/%s", app.arn, id)
			label := sv(e.Name)
			if label == "" {
				label = id
			}
			envs = append(envs, acEnvRef{appID: app.id, id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigEnvironment, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appconfig environments")
	return envs, t, i, err
}

func scanACConfigurationProfiles(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, app acAppRef) ([]acCfgProfileRef, int, int, error) {
	aid := app.id
	pager := appconfig.NewListConfigurationProfilesPaginator(client, &appconfig.ListConfigurationProfilesInput{ApplicationId: &aid})
	var batch []*store.Resource
	var cps []acCfgProfileRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListConfigurationProfiles", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appconfig:ListConfigurationProfiles: %w", perr)
		}
		for _, c := range out.Items {
			id := sv(c.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/configurationprofile/%s", app.arn, id)
			label := sv(c.Name)
			if label == "" {
				label = id
			}
			cps = append(cps, acCfgProfileRef{appID: app.id, id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigConfigurationProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appconfig configuration-profiles")
	return cps, t, i, err
}

func scanACDeployments(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, env acEnvRef) (int, int, error) {
	aid := env.appID
	eid := env.id
	pager := appconfig.NewListDeploymentsPaginator(client, &appconfig.ListDeploymentsInput{ApplicationId: &aid, EnvironmentId: &eid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListDeployments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appconfig:ListDeployments: %w", perr)
		}
		for _, d := range out.Items {
			arn := fmt.Sprintf("%s/deployment/%d", env.arn, d.DeploymentNumber)
			label := sv(d.ConfigurationName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigDeployment, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appconfig deployments")
}

func scanACHostedConfigVersions(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string, cp acCfgProfileRef) (int, int, error) {
	aid := cp.appID
	cid := cp.id
	pager := appconfig.NewListHostedConfigurationVersionsPaginator(client, &appconfig.ListHostedConfigurationVersionsInput{ApplicationId: &aid, ConfigurationProfileId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListHostedConfigurationVersions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appconfig:ListHostedConfigurationVersions: %w", perr)
		}
		for _, v := range out.Items {
			arn := fmt.Sprintf("%s/hostedconfigurationversion/%d", cp.arn, v.VersionNumber)
			label := sv(v.VersionLabel)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigHostedConfigurationVersion, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appconfig hosted-configuration-versions")
}

func scanACDeploymentStrategies(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := appconfig.NewListDeploymentStrategiesPaginator(client, &appconfig.ListDeploymentStrategiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListDeploymentStrategies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appconfig:ListDeploymentStrategies: %w", perr)
		}
		for _, d := range out.Items {
			id := sv(d.Id)
			if id == "" {
				continue
			}
			arn := acARN(region, acct.ID, "deploymentstrategy", id)
			label := sv(d.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigDeploymentStrategy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				// AWS-managed default strategies use the literal "AppConfig." name prefix
				// (AllAtOnce, Linear50PercentEvery30Seconds, Canary10Percent20Minutes, etc).
				ManagedByProvider: strings.HasPrefix(label, "AppConfig."),
			})
		}
	}
	return upsertBatch(st, batch, "appconfig deployment-strategies")
}

func scanACExtensions(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := appconfig.NewListExtensionsPaginator(client, &appconfig.ListExtensionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListExtensions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appconfig:ListExtensions: %w", perr)
		}
		for _, e := range out.Items {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			id := sv(e.Id)
			label := sv(e.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigExtension, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				// AWS-owned extensions carry an Id like "AWS.AppConfig.JiraIntegration" /
				// "AWS.AppConfig.FeatureFlags" / "AWS.SNS" / "AWS.Lambda". Customer Ids are
				// generated alphanumerics — Id-prefix is stable; Name is user-editable.
				ManagedByProvider: strings.HasPrefix(id, "AWS."),
			})
		}
	}
	return upsertBatch(st, batch, "appconfig extensions")
}

func scanACExtensionAssociations(ctx context.Context, client appconfigAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := appconfig.NewListExtensionAssociationsPaginator(client, &appconfig.ListExtensionAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appconfig:ListExtensionAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appconfig:ListExtensionAssociations: %w", perr)
		}
		for _, a := range out.Items {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := acARN(region, acct.ID, "extensionassociation", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppConfigExtensionAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appconfig extension-associations")
}
