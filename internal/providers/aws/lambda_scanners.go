package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// lambdaFunctionAttrs wraps SDK FunctionConfiguration with an enriched Code
// block so image-package functions surface their ECR image URI in
// AttributesJSON. Embedding promotes all FunctionConfiguration fields to top
// level, so resolvers reading `Role`, `KMSKeyArn`, `VpcConfig`, etc. keep
// working unchanged. `Code` is populated via GetFunction, PackageType=Image only.
type lambdaFunctionAttrs struct {
	lambdatypes.FunctionConfiguration
	Code *lambdaFunctionCodeAttrs `json:"Code,omitempty"`
}

// lambdaFunctionCodeAttrs holds the verbatim Code fields disco needs.
// ImageURI is the only edge-bearing field; ResolvedImageUri / RepositoryType
// aren't needed for graph relationships.
type lambdaFunctionCodeAttrs struct {
	ImageURI *string `json:"ImageUri,omitempty"`
}

func init() {
	registerType(restype.Descriptor{Type: TypeLambdaFunction, Service: "lambda", Redact: []redact.Rule{{Path: "Environment.Variables.*", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeLambdaAlias, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaVersion, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaURL, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaESM, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaLayerVersion, Service: "lambda", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeLambdaCodeSigningConfig, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaEventInvokeConfig, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaCapacityProvider, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaPermission, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaLayerVersionPermission, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaProvisionedConcurrencyConfig, Service: "lambda"})
	registerService(serviceEntry{
		name: "aws:lambda",
		fn:   scanLambda,
	})
}

// lambdaAPI is the narrow set of Lambda ops called by scanLambda's
// sub-phases: functions, their aliases / versions / URLs / event-invoke
// configs / provisioned concurrency, plus event-source mappings, code-signing
// configs, capacity providers, layers, layer-versions, and the tags of the
// types ListTags takes.
type lambdaAPI interface {
	ListProvisionedConcurrencyConfigs(context.Context, *lambda.ListProvisionedConcurrencyConfigsInput, ...func(*lambda.Options)) (*lambda.ListProvisionedConcurrencyConfigsOutput, error)
	ListFunctions(context.Context, *lambda.ListFunctionsInput, ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	ListAliases(context.Context, *lambda.ListAliasesInput, ...func(*lambda.Options)) (*lambda.ListAliasesOutput, error)
	ListVersionsByFunction(context.Context, *lambda.ListVersionsByFunctionInput, ...func(*lambda.Options)) (*lambda.ListVersionsByFunctionOutput, error)
	ListFunctionEventInvokeConfigs(context.Context, *lambda.ListFunctionEventInvokeConfigsInput, ...func(*lambda.Options)) (*lambda.ListFunctionEventInvokeConfigsOutput, error)
	ListFunctionUrlConfigs(context.Context, *lambda.ListFunctionUrlConfigsInput, ...func(*lambda.Options)) (*lambda.ListFunctionUrlConfigsOutput, error)
	ListCodeSigningConfigs(context.Context, *lambda.ListCodeSigningConfigsInput, ...func(*lambda.Options)) (*lambda.ListCodeSigningConfigsOutput, error)
	ListCapacityProviders(context.Context, *lambda.ListCapacityProvidersInput, ...func(*lambda.Options)) (*lambda.ListCapacityProvidersOutput, error)
	ListEventSourceMappings(context.Context, *lambda.ListEventSourceMappingsInput, ...func(*lambda.Options)) (*lambda.ListEventSourceMappingsOutput, error)
	ListLayers(context.Context, *lambda.ListLayersInput, ...func(*lambda.Options)) (*lambda.ListLayersOutput, error)
	ListLayerVersions(context.Context, *lambda.ListLayerVersionsInput, ...func(*lambda.Options)) (*lambda.ListLayerVersionsOutput, error)
	GetFunction(context.Context, *lambda.GetFunctionInput, ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
	GetLayerVersionByArn(context.Context, *lambda.GetLayerVersionByArnInput, ...func(*lambda.Options)) (*lambda.GetLayerVersionByArnOutput, error)
	GetPolicy(context.Context, *lambda.GetPolicyInput, ...func(*lambda.Options)) (*lambda.GetPolicyOutput, error)
	GetLayerVersionPolicy(context.Context, *lambda.GetLayerVersionPolicyInput, ...func(*lambda.Options)) (*lambda.GetLayerVersionPolicyOutput, error)
	ListTags(context.Context, *lambda.ListTagsInput, ...func(*lambda.Options)) (*lambda.ListTagsOutput, error)
}

const (
	lambdaListFunctionsOp          = "lambda:ListFunctions"
	lambdaGetFunctionOp            = "lambda:GetFunction"
	lambdaListCodeSigningConfigsOp = "lambda:ListCodeSigningConfigs"
	lambdaListCapacityProvidersOp  = "lambda:ListCapacityProviders"
	lambdaListESMsOp               = "lambda:ListEventSourceMappings"
	lambdaListTagsOp               = "lambda:ListTags"
)

// Page caps of the account-wide listings. ListFunctions and
// ListEventSourceMappings document returning at most 50 and 100 items a
// page whatever is asked; the others are the MaxItems range maximum of
// their model.
const (
	lambdaFunctionsPageCap          int32 = 50
	lambdaCodeSigningConfigsPageCap int32 = 10000
	lambdaCapacityProvidersPageCap  int32 = 50
	lambdaESMsPageCap               int32 = 100
)

// lambdaNotFound is the not-found code ListTags (of both the Lambda and the
// Lambda MicroVMs clients) and GetFunction model.
var lambdaNotFound = []string{"ResourceNotFoundException"}

// lambdaFunctionSummary holds minimal per-function data reused by the
// per-function sub-scanners (aliases, versions, event invoke configs, URLs).
type lambdaFunctionSummary struct {
	name string // FunctionName
	arn  string // FunctionArn (unqualified)
}

// lambdaTags reads a row's tags with ListTags, sent the row's NativeID, the
// ARN its listing returned; the op does not page. The Service Reference
// lists function, code signing config, event source mapping, capacity
// provider and network connector as the Lambda resources ListTags takes,
// and the op's Resource pattern accepts each of their ARNs. Versions and
// aliases are not read: the op's documentation says Lambda does not support
// tags on them, so they carry none of their own to store.
func lambdaTags(client lambdaAPI) rowTagReader {
	return rowTagReader{
		op:       lambdaListTagsOp,
		notFound: lambdaNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTags(ctx, &lambda.ListTagsInput{Resource: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanLambda runs every Lambda listing as its own phase, one at a time
// (runHostPhases), in the order below: a child phase gets the parents its
// parent phase stored, and a phase's error does not stop the later ones; a
// failed write and ctx's end do. Lambda Core and Lambda MicroVMs are served
// on the same lambda endpoint, so every phase is on one host. The tags of
// the types ListTags takes are read with their listing (lambdaTags,
// lambdaMicrovmTags); both clients' ListTags is the one IAM action
// lambda:ListTags, so every tag denial of the scan is one warning.
func scanLambda(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := lambda.NewFromConfig(acct.cfg, func(o *lambda.Options) { o.Region = region })
	core := lambdacore.NewFromConfig(acct.cfg, func(o *lambdacore.Options) { o.Region = region })
	microvms := lambdamicrovms.NewFromConfig(acct.cfg, func(o *lambdamicrovms.Options) { o.Region = region })
	tags := lambdaTags(client).sharingDenials()
	imageTags := lambdaMicrovmTags(microvms)
	imageTags.denials = tags.denials

	var (
		fns, pcCandidates []lambdaFunctionSummary
		images            []childParent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "lambda", 0, []hostPhase{
		{op: lambdaListFunctionsOp, run: func(p *phaseTotals) (failed bool) {
			fns, failed = scanLambdaFunctions(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "lambda:ListVersionsByFunction", func() (t, n int, err error) {
			pcCandidates, t, n, err = scanLambdaVersions(ctx, client, acct, fns, region, st, scanID)
			return t, n, err
		}),
		countedPhase(0, "lambda:ListAliases", func() (int, int, error) {
			return scanLambdaAliases(ctx, client, acct, fns, region, st, scanID)
		}),
		countedPhase(0, "lambda:ListFunctionEventInvokeConfigs", func() (int, int, error) {
			return scanLambdaEventInvokeConfigs(ctx, client, acct, fns, region, st, scanID)
		}),
		countedPhase(0, "lambda:ListFunctionUrlConfigs", func() (int, int, error) {
			return scanLambdaFunctionURLs(ctx, client, acct, fns, region, st, scanID)
		}),
		countedPhase(0, "lambda:GetPolicy", func() (int, int, error) {
			return scanLambdaPermissions(ctx, client, acct, fns, region, st, scanID)
		}),
		{op: lambdaListCodeSigningConfigsOp, run: func(p *phaseTotals) bool {
			return scanLambdaCodeSigningConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: lambdaListCapacityProvidersOp, run: func(p *phaseTotals) bool {
			return scanLambdaCapacityProviders(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: lambdaListESMsOp, run: func(p *phaseTotals) bool {
			return scanLambdaEventSourceMappings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "lambda:ListLayerVersions", func() (int, int, error) {
			return scanLambdaLayerVersions(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "lambda:GetLayerVersionPolicy", func() (int, int, error) {
			return scanLambdaLayerVersionPermissions(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "lambda:GetLayerVersionByArn", func() (int, int, error) {
			return scanLambdaForeignLayers(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "lambda:ListProvisionedConcurrencyConfigs", func() (int, int, error) {
			return scanLambdaProvisionedConcurrencyConfigs(ctx, client, acct, pcCandidates, region, st, scanID)
		}),
		{op: lambdaListNetworkConnectorsOp, run: func(p *phaseTotals) bool {
			return scanLambdaNetworkConnectors(ctx, core, acct, region, st, scanID, tags, p)
		}},
		{op: lambdaListMicrovmImagesOp, run: func(p *phaseTotals) (failed bool) {
			images, failed = scanLambdaMicrovmImages(ctx, microvms, acct, region, st, scanID, imageTags, p)
			return failed
		}},
		countedPhase(0, "lambda:ListMicrovmImageVersions", func() (int, int, error) {
			return scanLambdaMicrovmImageVersions(ctx, microvms, acct, region, st, scanID, images)
		}),
		countedPhase(0, "lambda:ListMicrovms", func() (int, int, error) {
			return scanLambdaMicrovms(ctx, microvms, acct, region, st, scanID)
		}),
		countedPhase(0, "lambda:ListManagedMicrovmImages", func() (int, int, error) {
			return scanLambdaManagedMicrovmImages(ctx, microvms, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// lambdaRow is the row of a listed element keyed by its ARN. An element
// without an ARN is not stored; what names the elements in the warning and
// key names the element.
func lambdaRow(acct *account, region, scanID, typ, what, arn, key string, name, status *string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(key, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn, Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanLambdaFunctions stores the region's functions a page at a time with
// their tags (storeTokenPagesTagsWith) and returns the stored ones for the
// per-function phases. GetFunction is read for every function: its Tags are
// the function's tags, and for an image-package function its Code.ImageUri,
// which ListFunctions omits, is stored beside the listed element
// (lambdaFunctionAttrs). Nothing else of the GetFunction body is stored:
// Code.Location is a presigned URL carrying a signature. A function
// GetFunction no longer finds was deleted since the listing and is dropped;
// any other GetFunction failure, or a TagsError in its body, keeps the listed
// element and reads its tags with ListTags.
//
// Tags come from GetFunction rather than ListTags for speed: the AWS Lambda
// quotas page counts ListTags in the 15 requests a second of "control plane
// API requests", which it says "excludes GetFunction" (GetFunction has a
// quota of its own). Unverified live.
func scanLambdaFunctions(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (fns []lambdaFunctionSummary, storeWriteFailed bool) {
	// The page's listed elements, for its GetFunction calls. The row builder
	// fills the map and the reads only look entries up; it is emptied before
	// the next page is fetched, which follows that page's write, so one page
	// is held.
	listed := map[*store.Resource]lambdatypes.FunctionConfiguration{}
	steps := tokenPageSteps{
		details: rowDetailReader{op: lambdaGetFunctionOp, what: "function(s)", notFound: lambdaNotFound},
		detailTags: func(ctx context.Context, r *store.Resource) (taggedDetail, error) {
			fn := listed[r]
			out, err := client.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: fn.FunctionArn})
			if err != nil {
				return taggedDetail{}, err
			}
			var d taggedDetail
			if out.TagsError == nil {
				d.tags, d.tagged = mapTagsJSON(out.Tags), true
			}
			if fn.PackageType != lambdatypes.PackageTypeImage {
				return d, nil
			}
			attrs := lambdaFunctionAttrs{FunctionConfiguration: fn}
			if out.Code != nil && sv(out.Code.ImageUri) != "" {
				attrs.Code = &lambdaFunctionCodeAttrs{ImageURI: out.Code.ImageUri}
			}
			d.body = attrs
			return d, nil
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				fns = append(fns, lambdaFunctionSummary{name: sv(r.Name), arn: r.NativeID})
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, lambdaListFunctionsOp,
		func(ctx context.Context, token *string) ([]lambdatypes.FunctionConfiguration, *string, error) {
			clear(listed)
			out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: token, MaxItems: sdkaws.Int32(lambdaFunctionsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Functions, out.NextMarker, nil
		},
		func(fn lambdatypes.FunctionConfiguration) (*store.Resource, string, string) {
			name := sv(fn.FunctionName)
			r, why, key := lambdaRow(acct, region, scanID, TypeLambdaFunction, "functions", sv(fn.FunctionArn), name, &name, nil, lambdaFunctionAttrs{FunctionConfiguration: fn})
			if r != nil {
				listed[r] = fn
			}
			return r, why, key
		}, steps, tags, p)
	return fns, failed
}

// scanLambdaAliases discovers all aliases for each function and upserts them as
// aws:lambda:alias resources. The NativeID is the AliasArn (qualified ARN).
func scanLambdaAliases(ctx context.Context, client lambdaAPI, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
//
// pcCandidates holds the functions that have a published version, plus those
// whose versions could not be listed: provisioned concurrency can only sit on
// a published version (or an alias of one), so the provisioned concurrency
// phase skips every function this rules out instead of calling once per function.
func scanLambdaVersions(ctx context.Context, client lambdaAPI, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (pcCandidates []lambdaFunctionSummary, total, inserted int, err error) {
	for i, fn := range fns {
		mayCarryPC := false
		pager := lambda.NewListVersionsByFunctionPaginator(client, &lambda.ListVersionsByFunctionInput{FunctionName: &fn.name})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					mayCarryPC = true
					break
				}
				// Deleted since ListFunctions: nothing left to list for it.
				if isAPIErrorCode(err, "ResourceNotFoundException") {
					break
				}
				return append(pcCandidates, fns[i:]...), total, inserted, fmt.Errorf("lambda:ListVersionsByFunction (%s): %w", fn.name, err)
			}
			var batch []*store.Resource
			for _, v := range page.Versions {
				ver := sv(v.Version)
				if ver == "$LATEST" {
					continue // mutable pseudo-version; not a real published version
				}
				mayCarryPC = true
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
					return nil, total, inserted, fmt.Errorf("upsert Lambda versions (%s): %w", fn.name, err)
				}
				total += len(batch)
				inserted += n
			}
		}
		if mayCarryPC {
			pcCandidates = append(pcCandidates, fn)
		}
	}
	return pcCandidates, total, inserted, nil
}

// scanLambdaEventInvokeConfigs discovers async invocation configuration for
// each function and upserts them as aws:lambda:event-invoke-config resources.
// The NativeID is the qualified FunctionArn from the config (may include a
// version or alias qualifier).
func scanLambdaEventInvokeConfigs(ctx context.Context, client lambdaAPI, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
func scanLambdaFunctionURLs(ctx context.Context, client lambdaAPI, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

// scanLambdaCodeSigningConfigs stores the region's code signing configs a
// page at a time with their tags.
func scanLambdaCodeSigningConfigs(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, lambdaListCodeSigningConfigsOp,
		func(ctx context.Context, token *string) ([]lambdatypes.CodeSigningConfig, *string, error) {
			out, err := client.ListCodeSigningConfigs(ctx, &lambda.ListCodeSigningConfigsInput{Marker: token, MaxItems: sdkaws.Int32(lambdaCodeSigningConfigsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CodeSigningConfigs, out.NextMarker, nil
		},
		func(c lambdatypes.CodeSigningConfig) (*store.Resource, string, string) {
			name := sv(c.CodeSigningConfigId)
			return lambdaRow(acct, region, scanID, TypeLambdaCodeSigningConfig, "code signing configs", sv(c.CodeSigningConfigArn), name, &name, nil, c)
		}, tags, p)
}

// scanLambdaCapacityProviders stores the region's capacity providers a page
// at a time with their tags. A region whose endpoint does not route the op
// answers the first page with "Unable to determine service/operation name",
// read as an empty listing (firstPageGapPages).
func scanLambdaCapacityProviders(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, lambdaListCapacityProvidersOp,
		firstPageGapPages(isServiceNotAvailableInRegion, func(ctx context.Context, token *string) ([]lambdatypes.CapacityProvider, *string, error) {
			out, err := client.ListCapacityProviders(ctx, &lambda.ListCapacityProvidersInput{Marker: token, MaxItems: sdkaws.Int32(lambdaCapacityProvidersPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.CapacityProviders, out.NextMarker, nil
		}),
		func(c lambdatypes.CapacityProvider) (*store.Resource, string, string) {
			arn, status := sv(c.CapacityProviderArn), string(c.State)
			return lambdaRow(acct, region, scanID, TypeLambdaCapacityProvider, "capacity providers", arn, arn, &arn, &status, c)
		}, tags, p)
}

// scanLambdaEventSourceMappings stores the region's event source mappings a
// page at a time with their tags.
func scanLambdaEventSourceMappings(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, lambdaListESMsOp,
		func(ctx context.Context, token *string) ([]lambdatypes.EventSourceMappingConfiguration, *string, error) {
			out, err := client.ListEventSourceMappings(ctx, &lambda.ListEventSourceMappingsInput{Marker: token, MaxItems: sdkaws.Int32(lambdaESMsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.EventSourceMappings, out.NextMarker, nil
		},
		func(m lambdatypes.EventSourceMappingConfiguration) (*store.Resource, string, string) {
			name, status := sv(m.UUID), sv(m.State)
			return lambdaRow(acct, region, scanID, TypeLambdaESM, "event source mappings", sv(m.EventSourceMappingArn), name, &name, &status, m)
		}, tags, p)
}

// scanLambdaLayerVersions discovers all Lambda layer versions in the region and
// upserts them as aws:lambda:layer-version resources. Lists layers first, then
// paginates each layer's versions.
func scanLambdaLayerVersions(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
				return 0, 0, skipIfAccessDenied(st, "lambda:ListLayers", acct.ID, region, err)
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

// scanLambdaForeignLayers enriches layer-version coverage with rows for
// AWS-managed (or otherwise cross-account) layers that customer functions
// reference via Layers[].Arn. ListLayers only returns caller-account layers,
// so without this phase those references would FK-fail in
// resolveLambdaLayerRelationships. Each foreign layer upserts with
// ManagedByProvider=true, hiding it from default `disco resources` /
// `disco graph`.
func scanLambdaForeignLayers(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// Read the function rows the function-scanner just upserted (this
	// region only — region match guards against cross-region duplicate
	// fan-out when scanning multiple regions).
	rfilter := store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Regions: []string{region}, Limit: util.AllResources,
	}
	fns, err := st.ListResources(rfilter)
	if err != nil {
		return 0, 0, fmt.Errorf("list lambda functions for foreign-layer scan: %w", err)
	}
	// Dedupe foreign-acct layer ARNs across all functions in this region.
	pending := make(map[string]struct{})
	for _, r := range fns {
		var attrs struct {
			Layers []struct {
				Arn *string `json:"Arn"`
			} `json:"Layers"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, layer := range attrs.Layers {
			arn := sv(layer.Arn)
			if arn == "" || !lambdaLayerIsForeign(arn, acct.ID) {
				continue
			}
			pending[arn] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return 0, 0, nil
	}
	// Fan-out GetLayerVersionByArn at fanoutMed. Per-call AccessDenied /
	// ResourceNotFoundException tolerated — those ARNs simply don't get
	// upserted and the resolver's FK-safe check skips the edge.
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for arn := range pending {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		layerARN := arn
		g.Go(func() error {
			defer sem.Release(1)
			out, gerr := client.GetLayerVersionByArn(gctx, &lambda.GetLayerVersionByArnInput{Arn: &layerARN})
			if gerr != nil {
				if isAccessDenied(gerr) || isAPIErrorCode(gerr, "ResourceNotFoundException") {
					return nil
				}
				return fmt.Errorf("lambda:GetLayerVersionByArn %s: %w", layerARN, gerr)
			}
			parts := strings.Split(layerARN, ":")
			if len(parts) < 8 {
				return nil
			}
			layerRegion := parts[3]
			name := parts[6] + ":" + parts[7]
			mu.Lock()
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLambdaLayerVersion, NativeID: layerARN,
				Name: &name, Region: &layerRegion,
				AttributesJSON:    mustJSON(out),
				ManagedByProvider: true,
				DiscoveredBy:      scanID,
			})
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "lambda foreign layer-versions")
}

// lambdaLayerIsForeign reports whether a layer ARN's owner-account differs
// from the caller account. Layer ARN shape:
// arn:aws:lambda:{region}:{ownerAcct}:layer:{name}:{version}.
func lambdaLayerIsForeign(arn, callerAcct string) bool {
	parts := strings.Split(arn, ":")
	if len(parts) < 8 {
		return false
	}
	return parts[4] != callerAcct
}
