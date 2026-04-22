package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

func init() { registerService(serviceEntry{name: "aws:lambda", fn: scanLambda}) }

// lambdaFunctionSummary holds the minimal per-function data reused by per-function
// sub-scanners (aliases, versions, event invoke configs, function URLs).
type lambdaFunctionSummary struct {
	name string // FunctionName
	arn  string // FunctionArn (unqualified)
}

// scanLambda is the dispatcher. It scans functions first to collect names for
// per-function sub-scanners, then scans account-level resources.
func scanLambda(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lambda.NewFromConfig(acct.cfg, func(o *lambda.Options) { o.Region = region })

	// Scan functions; collect summaries for per-function sub-scanners.
	fns, tt, nn, err := scanLambdaFunctions(ctx, client, acct, region, st, scanID)
	if err != nil {
		return 0, 0, err
	}
	total += tt
	inserted += nn

	// Per-function resources. Most functions have none of these; the paginator
	// returns immediately, so the overhead per function is minimal.
	type perFnScanner func(context.Context, *lambda.Client, *account, []lambdaFunctionSummary, string, *store.Store, string) (int, int, error)
	for _, scan := range []perFnScanner{
		scanLambdaAliases,
		scanLambdaVersions,
		scanLambdaEventInvokeConfigs,
		scanLambdaFunctionURLs,
	} {
		tt, nn, err := scan(ctx, client, acct, fns, region, st, scanID)
		if err != nil {
			return total, inserted, err
		}
		total += tt
		inserted += nn
	}

	// Account-level resources (not per-function).
	for _, scan := range []func(context.Context, *lambda.Client, *account, string, *store.Store, string) (int, int, error){
		scanLambdaCodeSigningConfigs,
		scanLambdaCapacityProviders,
		scanLambdaEventSourceMappings,
		scanLambdaLayerVersions,
	} {
		tt, nn, err := scan(ctx, client, acct, region, st, scanID)
		if err != nil {
			return total, inserted, err
		}
		total += tt
		inserted += nn
	}
	return
}

// scanLambdaFunctions discovers Lambda functions in one region and returns a
// summary list for use by per-function sub-scanners.
func scanLambdaFunctions(ctx context.Context, client *lambda.Client, acct *account, region string, st *store.Store, scanID string) (fns []lambdaFunctionSummary, total, inserted int, err error) {
	pager := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied("lambda:ListFunctions", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("lambda:ListFunctions: %w", err)
		}
		var batch []*store.Resource
		for _, fn := range page.Functions {
			name := sv(fn.FunctionName)
			fns = append(fns, lambdaFunctionSummary{name: name, arn: sv(fn.FunctionArn)})
			// Tags are not included in ListFunctions; fetch separately if needed.
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLambdaFunction,
				NativeID:       sv(fn.FunctionArn),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(fn),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("upsert Lambda functions: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return fns, total, inserted, nil
}

// scanLambdaAliases discovers all aliases for each function and upserts them as
// aws:lambda:alias resources. The NativeID is the AliasArn (qualified ARN).
func scanLambdaAliases(ctx context.Context, client *lambda.Client, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, fn := range fns {
		pager := lambda.NewListAliasesPaginator(client, &lambda.ListAliasesInput{FunctionName: &fn.name})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break // skip this function's aliases
				}
				return total, inserted, fmt.Errorf("lambda:ListAliases (%s): %w", fn.name, err)
			}
			var batch []*store.Resource
			for _, a := range page.Aliases {
				name := sv(a.Name)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLambdaAlias,
					NativeID:       sv(a.AliasArn),
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert Lambda aliases (%s): %w", fn.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return
}

// scanLambdaVersions discovers all published versions for each function and
// upserts them as aws:lambda:version resources. $LATEST is skipped — it is a
// mutable pseudo-version, not a stable published version.
func scanLambdaVersions(ctx context.Context, client *lambda.Client, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, fn := range fns {
		pager := lambda.NewListVersionsByFunctionPaginator(client, &lambda.ListVersionsByFunctionInput{FunctionName: &fn.name})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return total, inserted, fmt.Errorf("lambda:ListVersionsByFunction (%s): %w", fn.name, err)
			}
			var batch []*store.Resource
			for _, v := range page.Versions {
				ver := sv(v.Version)
				if ver == "$LATEST" {
					continue // mutable pseudo-version; not a real published version
				}
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLambdaVersion,
					NativeID:       sv(v.FunctionArn), // qualified: arn:...:function:name:N
					Name:           &ver,
					Region:         &region,
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert Lambda versions (%s): %w", fn.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return
}

// scanLambdaEventInvokeConfigs discovers async invocation configuration for
// each function and upserts them as aws:lambda:event-invoke-config resources.
// The NativeID is the qualified FunctionArn from the config (may include a
// version or alias qualifier).
func scanLambdaEventInvokeConfigs(ctx context.Context, client *lambda.Client, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, fn := range fns {
		pager := lambda.NewListFunctionEventInvokeConfigsPaginator(client, &lambda.ListFunctionEventInvokeConfigsInput{FunctionName: &fn.name})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return total, inserted, fmt.Errorf("lambda:ListFunctionEventInvokeConfigs (%s): %w", fn.name, err)
			}
			var batch []*store.Resource
			for _, c := range page.FunctionEventInvokeConfigs {
				name := fn.name // use function name as the display name
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLambdaEventInvokeConfig,
					NativeID:       sv(c.FunctionArn),
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert Lambda event invoke configs (%s): %w", fn.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return
}

// scanLambdaFunctionURLs discovers function URL configurations for each function
// and upserts them as aws:lambda:url resources. The NativeID is the qualified
// FunctionArn; the Name is the human-readable function URL.
func scanLambdaFunctionURLs(ctx context.Context, client *lambda.Client, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, fn := range fns {
		pager := lambda.NewListFunctionUrlConfigsPaginator(client, &lambda.ListFunctionUrlConfigsInput{FunctionName: &fn.name})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return total, inserted, fmt.Errorf("lambda:ListFunctionUrlConfigs (%s): %w", fn.name, err)
			}
			var batch []*store.Resource
			for _, u := range page.FunctionUrlConfigs {
				name := sv(u.FunctionUrl)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLambdaURL,
					NativeID:       sv(u.FunctionArn),
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(u),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert Lambda function URLs (%s): %w", fn.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return
}

// scanLambdaCodeSigningConfigs discovers all code signing configurations in the
// region and upserts them as aws:lambda:code-signing-config resources.
func scanLambdaCodeSigningConfigs(ctx context.Context, client *lambda.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := lambda.NewListCodeSigningConfigsPaginator(client, &lambda.ListCodeSigningConfigsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("lambda:ListCodeSigningConfigs", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("lambda:ListCodeSigningConfigs: %w", err)
		}
		var batch []*store.Resource
		for _, c := range page.CodeSigningConfigs {
			name := sv(c.CodeSigningConfigId)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLambdaCodeSigningConfig,
				NativeID:       sv(c.CodeSigningConfigArn),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert Lambda code signing configs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLambdaCapacityProviders discovers all Lambda capacity providers in the
// region and upserts them as aws:lambda:capacity-provider resources.
func scanLambdaCapacityProviders(ctx context.Context, client *lambda.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := lambda.NewListCapacityProvidersPaginator(client, &lambda.ListCapacityProvidersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("lambda:ListCapacityProviders", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("lambda:ListCapacityProviders: %w", err)
		}
		var batch []*store.Resource
		for _, p := range page.CapacityProviders {
			arn := sv(p.CapacityProviderArn)
			status := string(p.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLambdaCapacityProvider,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert Lambda capacity providers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLambdaEventSourceMappings discovers all event source mappings in the
// region and upserts them as aws:lambda:event-source-mapping resources.
func scanLambdaEventSourceMappings(ctx context.Context, client *lambda.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := lambda.NewListEventSourceMappingsPaginator(client, &lambda.ListEventSourceMappingsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("lambda:ListEventSourceMappings", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("lambda:ListEventSourceMappings: %w", err)
		}
		var batch []*store.Resource
		for _, m := range page.EventSourceMappings {
			name := sv(m.UUID)
			status := sv(m.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLambdaESM,
				NativeID:       sv(m.EventSourceMappingArn),
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert Lambda event source mappings: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanLambdaLayerVersions discovers all Lambda layer versions in the region and
// upserts them as aws:lambda:layer-version resources. It first lists all layers,
// then paginates layer versions per layer.
func scanLambdaLayerVersions(ctx context.Context, client *lambda.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// lambdaLayerSummary holds the minimal data needed to list a layer's versions.
	type lambdaLayerSummary struct {
		name string
	}
	var layers []lambdaLayerSummary

	lpager := lambda.NewListLayersPaginator(client, &lambda.ListLayersInput{})
	for lpager.HasMorePages() {
		page, err := lpager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("lambda:ListLayers", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("lambda:ListLayers: %w", err)
		}
		for _, l := range page.Layers {
			if l.LayerName != nil {
				layers = append(layers, lambdaLayerSummary{name: *l.LayerName})
			}
		}
	}

	// For each layer, paginate its versions and upsert them.
	for _, l := range layers {
		vpager := lambda.NewListLayerVersionsPaginator(client, &lambda.ListLayerVersionsInput{LayerName: &l.name})
		for vpager.HasMorePages() {
			page, err := vpager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return total, inserted, fmt.Errorf("lambda:ListLayerVersions (%s): %w", l.name, err)
			}
			var batch []*store.Resource
			for _, v := range page.LayerVersions {
				name := fmt.Sprintf("%s:%d", l.name, v.Version)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeLambdaLayerVersion,
					NativeID:       sv(v.LayerVersionArn),
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				n, err := st.UpsertResources(batch)
				if err != nil {
					return total, inserted, fmt.Errorf("upsert Lambda layer versions (%s): %w", l.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
	}
	return
}
