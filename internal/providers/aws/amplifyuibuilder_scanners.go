package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/amplify"
	"github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder"
)

func init() {
	registerService(serviceEntry{
		name: "aws:amplify-ui-builder",
		fn:   scanAmplifyUIBuilder,
		emits: []coverage.TypeDecl{
			{Service: "amplify-ui-builder", DiscoType: TypeAmplifyUIBuilderComponent, Leaf: true},
			{Service: "amplify-ui-builder", DiscoType: TypeAmplifyUIBuilderForm, Leaf: true},
			{Service: "amplify-ui-builder", DiscoType: TypeAmplifyUIBuilderTheme, Leaf: true},
		},
	})
}

type amplifyUIBuilderAPI interface {
	ListComponents(context.Context, *amplifyuibuilder.ListComponentsInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListComponentsOutput, error)
	ListForms(context.Context, *amplifyuibuilder.ListFormsInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListFormsOutput, error)
	ListThemes(context.Context, *amplifyuibuilder.ListThemesInput, ...func(*amplifyuibuilder.Options)) (*amplifyuibuilder.ListThemesOutput, error)
}

type amplifyAppEnvRef struct {
	AppID string
	Env   string
}

// scanAmplifyUIBuilder discovers UI components, forms, and themes per
// (Amplify app, backend environment). NativeIDs synth from app/env/id.
func scanAmplifyUIBuilder(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	uib := amplifyuibuilder.NewFromConfig(acct.cfg, func(o *amplifyuibuilder.Options) { o.Region = region })
	amp := amplify.NewFromConfig(acct.cfg, func(o *amplify.Options) { o.Region = region })

	envs, lerr := loadAmplifyAppEnvs(ctx, amp, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}

	t, i, ferr := scanAUIBComponents(ctx, uib, envs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanAUIBForms(ctx, uib, envs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanAUIBThemes(ctx, uib, envs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func loadAmplifyAppEnvs(ctx context.Context, client *amplify.Client, acct *account, region string, st *store.Store) ([]amplifyAppEnvRef, error) {
	var apps []string
	var nextToken *string
	for {
		out, err := client.ListApps(ctx, &amplify.ListAppsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "amplify:ListApps(uib-fanout)", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("amplify:ListApps(uib-fanout): %w", err)
		}
		for _, a := range out.Apps {
			if id := sv(a.AppId); id != "" {
				apps = append(apps, id)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	var refs []amplifyAppEnvRef
	for _, app := range apps {
		appID := app
		var beToken *string
		for {
			out, err := client.ListBackendEnvironments(ctx, &amplify.ListBackendEnvironmentsInput{
				AppId:     &appID,
				NextToken: beToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				if isAPIErrorCode(err, "NotFoundException", "BadRequestException") {
					break
				}
				return nil, fmt.Errorf("amplify:ListBackendEnvironments app=%s: %w", appID, err)
			}
			for _, be := range out.BackendEnvironments {
				if env := sv(be.EnvironmentName); env != "" {
					refs = append(refs, amplifyAppEnvRef{AppID: appID, Env: env})
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			beToken = out.NextToken
		}
	}
	return refs, nil
}

func scanAUIBComponents(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, e := range envs {
		appID, env := e.AppID, e.Env
		var nextToken *string
		for {
			out, err := client.ListComponents(ctx, &amplifyuibuilder.ListComponentsInput{
				AppId:           &appID,
				EnvironmentName: &env,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "amplify-ui-builder:ListComponents", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("amplify-ui-builder:ListComponents app=%s env=%s: %w", appID, env, err)
			}
			for _, c := range out.Entities {
				id := sv(c.Id)
				if id == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:amplifyuibuilder:%s:%s:app/%s/environment/%s/components/%s", region, acct.ID, appID, env, id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAmplifyUIBuilderComponent, NativeID: arn,
					Name: c.Name, Region: &region,
					AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "amplify-ui-builder components")
}

func scanAUIBForms(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, e := range envs {
		appID, env := e.AppID, e.Env
		var nextToken *string
		for {
			out, err := client.ListForms(ctx, &amplifyuibuilder.ListFormsInput{
				AppId:           &appID,
				EnvironmentName: &env,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "amplify-ui-builder:ListForms", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("amplify-ui-builder:ListForms app=%s env=%s: %w", appID, env, err)
			}
			for _, f := range out.Entities {
				id := sv(f.Id)
				if id == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:amplifyuibuilder:%s:%s:app/%s/environment/%s/forms/%s", region, acct.ID, appID, env, id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAmplifyUIBuilderForm, NativeID: arn,
					Name: f.Name, Region: &region,
					AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "amplify-ui-builder forms")
}

func scanAUIBThemes(ctx context.Context, client amplifyUIBuilderAPI, envs []amplifyAppEnvRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, e := range envs {
		appID, env := e.AppID, e.Env
		var nextToken *string
		for {
			out, err := client.ListThemes(ctx, &amplifyuibuilder.ListThemesInput{
				AppId:           &appID,
				EnvironmentName: &env,
				NextToken:       nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "amplify-ui-builder:ListThemes", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("amplify-ui-builder:ListThemes app=%s env=%s: %w", appID, env, err)
			}
			for _, t := range out.Entities {
				id := sv(t.Id)
				if id == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:amplifyuibuilder:%s:%s:app/%s/environment/%s/themes/%s", region, acct.ID, appID, env, id)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAmplifyUIBuilderTheme, NativeID: arn,
					Name: t.Name, Region: &region,
					AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "amplify-ui-builder themes")
}
