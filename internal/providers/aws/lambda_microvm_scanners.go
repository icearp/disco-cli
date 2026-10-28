package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdacore"
	lambdacoretypes "github.com/aws/aws-sdk-go-v2/service/lambdacore/types"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdamicrovmstypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// Lambda network connectors and MicroVMs are served by two SDK clients of
// their own (Lambda Core, Lambda MicroVMs) on the lambda endpoint and IAM
// namespace, so they are phases of the aws:lambda scan.
func init() {
	registerType(restype.Descriptor{Type: TypeLambdaNetworkConnector, Service: "lambda"})
	registerType(restype.Descriptor{Type: TypeLambdaMicrovmImage, Service: "lambda"})
	registerType(restype.Descriptor{
		Type: TypeLambdaMicrovmImageVersion, Service: "lambda",
		Redact: []redact.Rule{{Path: "EnvironmentVariables.*", Mode: redact.RedactScalar}},
	})
	registerType(restype.Descriptor{Type: TypeLambdaMicrovm, Service: "lambda"})
	// AWS publishes these base images, unasked and undeletable. They are stored
	// because a customer image version names one as its BaseImageArn.
	registerType(restype.Descriptor{Type: TypeLambdaManagedMicrovmImage, Service: "lambda", Managed: true})
}

type lambdaCoreAPI interface {
	ListNetworkConnectors(context.Context, *lambdacore.ListNetworkConnectorsInput, ...func(*lambdacore.Options)) (*lambdacore.ListNetworkConnectorsOutput, error)
}

type lambdaMicrovmsAPI interface {
	ListMicrovmImages(context.Context, *lambdamicrovms.ListMicrovmImagesInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImagesOutput, error)
	ListMicrovmImageVersions(context.Context, *lambdamicrovms.ListMicrovmImageVersionsInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImageVersionsOutput, error)
	ListMicrovms(context.Context, *lambdamicrovms.ListMicrovmsInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmsOutput, error)
	ListManagedMicrovmImages(context.Context, *lambdamicrovms.ListManagedMicrovmImagesInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListManagedMicrovmImagesOutput, error)
	ListTags(context.Context, *lambdamicrovms.ListTagsInput, ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListTagsOutput, error)
}

const (
	lambdaListNetworkConnectorsOp = "lambda:ListNetworkConnectors"
	lambdaListMicrovmImagesOp     = "lambda:ListMicrovmImages"
)

// Page caps: the MaxItems and maxResults range maximum of each op's model.
const (
	lambdaNetworkConnectorsPageCap int32 = 100
	lambdaMicrovmImagesPageCap     int32 = 50
)

// lambdaMicrovmTags reads a MicroVM image's tags with the Lambda MicroVMs
// client's ListTags, sent the image ARN its listing returned; the op does not
// page. The Lambda client's ListTags does not take a microvm-image ARN (its
// Resource pattern omits the kind); this one's does. The Service Reference
// lists microvmImage among the resources lambda:ListTags takes. MicroVMs
// themselves have no ARN and no Service Reference resource, so they are not
// read.
func lambdaMicrovmTags(client lambdaMicrovmsAPI) rowTagReader {
	return rowTagReader{
		op:       lambdaListTagsOp,
		notFound: lambdaNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTags(ctx, &lambdamicrovms.ListTagsInput{Resource: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanLambdaNetworkConnectors stores the region's network connectors a page
// at a time with their tags, read with the Lambda client's ListTags
// (lambdaTags): Lambda Core has no tag op.
func scanLambdaNetworkConnectors(ctx context.Context, client lambdaCoreAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, lambdaListNetworkConnectorsOp,
		func(ctx context.Context, token *string) ([]lambdacoretypes.NetworkConnectorSummary, *string, error) {
			out, err := client.ListNetworkConnectors(ctx, &lambdacore.ListNetworkConnectorsInput{Marker: token, MaxItems: sdkaws.Int32(lambdaNetworkConnectorsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkConnectors, out.NextMarker, nil
		},
		func(c lambdacoretypes.NetworkConnectorSummary) (*store.Resource, string, string) {
			return lambdaRow(acct, region, scanID, TypeLambdaNetworkConnector, "network connectors", sv(c.Arn), sv(c.Id), c.Name, nonEmptyPtr(string(c.State)), c)
		}, tags, p)
}

// scanLambdaMicrovmImages stores the account's MicroVM images a page at a
// time with their tags and returns the stored ones as parents for the
// image-version phase.
func scanLambdaMicrovmImages(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (images []childParent, storeWriteFailed bool) {
	steps := tokenPageSteps{stored: func(rows []*store.Resource) {
		for _, r := range rows {
			images = append(images, childParent{id: r.NativeID, arn: r.NativeID})
		}
	}}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, lambdaListMicrovmImagesOp,
		func(ctx context.Context, token *string) ([]lambdamicrovmstypes.MicrovmImageSummary, *string, error) {
			out, err := client.ListMicrovmImages(ctx, &lambdamicrovms.ListMicrovmImagesInput{NextToken: token, MaxResults: sdkaws.Int32(lambdaMicrovmImagesPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(img lambdamicrovmstypes.MicrovmImageSummary) (*store.Resource, string, string) {
			r, why, key := lambdaRow(acct, region, scanID, TypeLambdaMicrovmImage, "MicroVM images", sv(img.ImageArn), sv(img.Name), img.Name, nonEmptyPtr(string(img.State)), img)
			if r != nil {
				r.CreatedAt = tp(img.CreatedAt)
			}
			return r, why, key
		}, steps, tags, p)
	return images, failed
}

// scanLambdaMicrovmImageVersions lists each image's versions. The API issues
// no version ARN, so a version is keyed {imageArn}/version/{imageVersion}.
func scanLambdaMicrovmImageVersions(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string, images []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOut(ctx, st, acct, region, "lambda:ListMicrovmImageVersions", images, isGone,
		func(ctx context.Context, img childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			p := lambdamicrovms.NewListMicrovmImageVersionsPaginator(client, &lambdamicrovms.ListMicrovmImageVersionsInput{ImageIdentifier: &img.id})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range page.Items {
					version := sv(v.ImageVersion)
					if version == "" {
						continue
					}
					r := &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeLambdaMicrovmImageVersion, NativeID: img.arn + "/version/" + version,
						Name: &version, Region: &region, CreatedAt: tp(v.CreatedAt),
						TagsJSON: mapTagsJSON(v.Tags), AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					}
					if v.State != "" {
						status := string(v.State)
						r.Status = &status
					}
					rows = append(rows, r)
				}
			}
			return rows, nil
		})
}

// scanLambdaMicrovms stores every MicroVM in the region. The API issues no
// MicroVM ARN, so one is keyed {imageArn}/microvm/{microvmId} from the image it
// runs.
func scanLambdaMicrovms(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := lambdamicrovms.NewListMicrovmsPaginator(client, &lambdamicrovms.ListMicrovmsInput{})
	return pageScan(ctx, "lambda:ListMicrovms", acct, region, st, p.HasMorePages,
		func(ctx context.Context) (*lambdamicrovms.ListMicrovmsOutput, error) { return p.NextPage(ctx) },
		func(o *lambdamicrovms.ListMicrovmsOutput) []lambdamicrovmstypes.MicrovmItem { return o.Items },
		func(m lambdamicrovmstypes.MicrovmItem) *store.Resource {
			imageArn, id := sv(m.ImageArn), sv(m.MicrovmId)
			if imageArn == "" || id == "" {
				return nil
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLambdaMicrovm, NativeID: imageArn + "/microvm/" + id, Name: &id, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}
			if m.State != "" {
				status := string(m.State)
				r.Status = &status
			}
			return r
		})
}

func scanLambdaManagedMicrovmImages(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := lambdamicrovms.NewListManagedMicrovmImagesPaginator(client, &lambdamicrovms.ListManagedMicrovmImagesInput{})
	return pageScan(ctx, "lambda:ListManagedMicrovmImages", acct, region, st, p.HasMorePages,
		func(ctx context.Context) (*lambdamicrovms.ListManagedMicrovmImagesOutput, error) {
			return p.NextPage(ctx)
		},
		func(o *lambdamicrovms.ListManagedMicrovmImagesOutput) []lambdamicrovmstypes.ManagedMicrovmImageSummary {
			return o.Items
		},
		func(img lambdamicrovmstypes.ManagedMicrovmImageSummary) *store.Resource {
			arn := sv(img.ImageArn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLambdaManagedMicrovmImage, NativeID: arn, Name: &arn, Region: &region,
				CreatedAt: tp(img.CreatedAt), AttributesJSON: mustJSON(img), DiscoveredBy: scanID,
			}
		})
}
