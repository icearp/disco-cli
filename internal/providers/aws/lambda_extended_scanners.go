package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// scanLambdaPermissions emits one row per function with a non-empty
// resource policy. NativeID synth: {functionArn}/policy.
func scanLambdaPermissions(ctx context.Context, client lambdaAPI, acct *account, fns []lambdaFunctionSummary, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	for _, fn := range fns {
		name := fn.name
		out, perr := client.GetPolicy(ctx, &lambda.GetPolicyInput{FunctionName: &name})
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "ResourceNotFoundException") {
				continue
			}
			return total, inserted, fmt.Errorf("lambda:GetPolicy %s: %w", name, perr)
		}
		if sv(out.Policy) == "" {
			continue
		}
		arn := fn.arn + "/policy"
		label := name
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeLambdaPermission, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "lambda permissions")
}

// scanLambdaLayerVersionPermissions emits one row per layer-version with a
// non-empty resource policy. Walks ListLayers → ListLayerVersions →
// GetLayerVersionPolicy. NativeID synth: {layerVersionArn}/policy.
func scanLambdaLayerVersionPermissions(ctx context.Context, client lambdaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	type lvRef struct {
		layer   string
		version int64
		arn     string
	}
	var refs []lvRef
	lpager := lambda.NewListLayersPaginator(client, &lambda.ListLayersInput{})
	for lpager.HasMorePages() {
		page, lerr := lpager.NextPage(ctx)
		if lerr != nil {
			if isAccessDenied(lerr) {
				_ = skipIfAccessDenied(st, "lambda:ListLayers(perm)", acct.ID, region, lerr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("lambda:ListLayers(perm): %w", lerr)
		}
		for _, l := range page.Layers {
			if l.LayerName == nil {
				continue
			}
			vpager := lambda.NewListLayerVersionsPaginator(client, &lambda.ListLayerVersionsInput{LayerName: l.LayerName})
			for vpager.HasMorePages() {
				vpage, verr := vpager.NextPage(ctx)
				if verr != nil {
					if isAccessDenied(verr) {
						break
					}
					return 0, 0, fmt.Errorf("lambda:ListLayerVersions(perm) %s: %w", *l.LayerName, verr)
				}
				for _, v := range vpage.LayerVersions {
					refs = append(refs, lvRef{layer: *l.LayerName, version: v.Version, arn: sv(v.LayerVersionArn)})
				}
			}
		}
	}
	var batch []*store.Resource
	for _, r := range refs {
		layer := r.layer
		ver := r.version
		out, perr := client.GetLayerVersionPolicy(ctx, &lambda.GetLayerVersionPolicyInput{
			LayerName:     &layer,
			VersionNumber: &ver,
		})
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "ResourceNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("lambda:GetLayerVersionPolicy %s:%d: %w", layer, ver, perr)
		}
		if sv(out.Policy) == "" {
			continue
		}
		arn := r.arn + "/policy"
		label := fmt.Sprintf("%s:%d", layer, ver)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeLambdaLayerVersionPermission, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "lambda layer-version-permissions")
}
