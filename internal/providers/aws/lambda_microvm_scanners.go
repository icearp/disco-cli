package aws

import (
	"context"

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
}

func scanLambdaNetworkConnectors(ctx context.Context, client lambdaCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := lambdacore.NewListNetworkConnectorsPaginator(client, &lambdacore.ListNetworkConnectorsInput{})
	return pageScan(ctx, "lambda:ListNetworkConnectors", acct, region, st, p.HasMorePages,
		func(ctx context.Context) (*lambdacore.ListNetworkConnectorsOutput, error) { return p.NextPage(ctx) },
		func(o *lambdacore.ListNetworkConnectorsOutput) []lambdacoretypes.NetworkConnectorSummary {
			return o.NetworkConnectors
		},
		func(c lambdacoretypes.NetworkConnectorSummary) *store.Resource {
			arn := sv(c.Arn)
			if arn == "" {
				return nil
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLambdaNetworkConnector, NativeID: arn, Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
			if c.State != "" {
				status := string(c.State)
				r.Status = &status
			}
			return r
		})
}

// scanLambdaMicrovmFamily runs the MicroVM phases. Image versions need the
// images listed first; the rest are independent.
func scanLambdaMicrovmFamily(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	images, total, inserted, err := scanLambdaMicrovmImages(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	for _, scan := range []func() (int, int, error){
		func() (int, int, error) {
			return scanLambdaMicrovmImageVersions(ctx, client, acct, region, st, scanID, images)
		},
		func() (int, int, error) { return scanLambdaMicrovms(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLambdaManagedMicrovmImages(ctx, client, acct, region, st, scanID) },
	} {
		t, n, err := scan()
		total += t
		inserted += n
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// scanLambdaMicrovmImages stores the account's MicroVM images and returns the
// stored ones as parents for the image-version phase.
func scanLambdaMicrovmImages(ctx context.Context, client lambdaMicrovmsAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var images []childParent
	p := lambdamicrovms.NewListMicrovmImagesPaginator(client, &lambdamicrovms.ListMicrovmImagesInput{})
	total, inserted, err := pageScan(ctx, "lambda:ListMicrovmImages", acct, region, st, p.HasMorePages,
		func(ctx context.Context) (*lambdamicrovms.ListMicrovmImagesOutput, error) { return p.NextPage(ctx) },
		func(o *lambdamicrovms.ListMicrovmImagesOutput) []lambdamicrovmstypes.MicrovmImageSummary {
			return o.Items
		},
		func(img lambdamicrovmstypes.MicrovmImageSummary) *store.Resource {
			arn := sv(img.ImageArn)
			if arn == "" {
				return nil
			}
			images = append(images, childParent{id: arn, arn: arn})
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLambdaMicrovmImage, NativeID: arn, Name: img.Name, Region: &region,
				CreatedAt: tp(img.CreatedAt), AttributesJSON: mustJSON(img), DiscoveredBy: scanID,
			}
			if img.State != "" {
				status := string(img.State)
				r.Status = &status
			}
			return r
		})
	return images, total, inserted, err
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
