package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// apigatewayv2PortalAPI lists the API Gateway developer-portal ops. None has
// an SDK paginator; every loop pages by NextToken.
type apigatewayv2PortalAPI interface {
	ListPortals(context.Context, *apigatewayv2.ListPortalsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListPortalsOutput, error)
	ListPortalProducts(context.Context, *apigatewayv2.ListPortalProductsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListPortalProductsOutput, error)
	ListProductPages(context.Context, *apigatewayv2.ListProductPagesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListProductPagesOutput, error)
	ListProductRestEndpointPages(context.Context, *apigatewayv2.ListProductRestEndpointPagesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListProductRestEndpointPagesOutput, error)
}

func init() {
	registerType(restype.Descriptor{Type: TypeAPIGatewayV2Portal, Service: "apigatewayv2"})
	registerType(restype.Descriptor{Type: TypeAPIGatewayV2PortalProduct, Service: "apigatewayv2"})
	registerType(restype.Descriptor{Type: TypeAPIGatewayV2ProductPage, Service: "apigatewayv2"})
	registerType(restype.Descriptor{Type: TypeAPIGatewayV2ProductRestEndpointPage, Service: "apigatewayv2"})
}

// scanAPIGatewayV2Portals stores developer portals and this account's portal
// products, then each product's pages and REST endpoint pages.
func scanAPIGatewayV2Portals(ctx context.Context, client apigatewayv2PortalAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	total, inserted, err = scanAPIGatewayV2PortalList(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, n, products, err := scanAPIGatewayV2PortalProducts(ctx, client, acct, region, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanAPIGatewayV2ProductPages(ctx, client, acct, region, st, scanID, products)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanAPIGatewayV2ProductRestEndpointPages(ctx, client, acct, region, st, scanID, products)
	return total + t, inserted + n, err
}

// skipPortalListError reports whether a region-wide portal list error is
// skipped. skipIfAccessDenied drops the gateway's "Unable to determine
// service/operation name" region gap silently and warns on a denial. Portals
// are newer than the rest of API Gateway and no other region-gap shape has
// been observed, so the modeled 400 warns rather than being silenced or
// failing the service. skipIfAccessDenied is only its warning sink: the silent
// drops there match AccessDenied/NotFound codes, so a BadRequest always warns.
func skipPortalListError(st *store.Store, op, accountID, region string, err error) bool {
	if !isAccessDenied(err) && !isServiceNotAvailableInRegion(err) && !isAPIErrorCode(err, "BadRequestException") {
		return false
	}
	_ = skipIfAccessDenied(st, op, accountID, region, err)
	return true
}

func scanAPIGatewayV2PortalList(ctx context.Context, client apigatewayv2PortalAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "apigatewayv2:ListPortals"
	input := &apigatewayv2.ListPortalsInput{}
	var batch []*store.Resource
	for {
		page, err := client.ListPortals(ctx, input)
		if err != nil {
			if !skipPortalListError(st, op, acct.ID, region, err) {
				return 0, 0, fmt.Errorf("%s: %w", op, err)
			}
			break
		}
		for _, p := range page.Items {
			portalARN := sv(p.PortalArn)
			if portalARN == "" {
				continue
			}
			var name *string
			if p.PortalContent != nil {
				name = p.PortalContent.DisplayName
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Portal, NativeID: portalARN,
				Name: name, Region: &region, Status: nonEmptyPtr(string(p.PublishStatus)),
				AttributesJSON: mustJSON(p), TagsJSON: mapTagsJSON(p.Tags), DiscoveredBy: scanID,
			})
		}
		// Mirrors the SDK paginators' StopOnDuplicateToken; a repeated token would loop forever.
		if next := sv(page.NextToken); next == "" || next == sv(input.NextToken) {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, op)
}

// scanAPIGatewayV2PortalProducts stores the portal products this account
// owns and returns them as parents for the page phases. A product another
// account shares here is part of its owner's inventory, so a product whose
// ARN names a different account is not stored.
func scanAPIGatewayV2PortalProducts(ctx context.Context, client apigatewayv2PortalAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	const op = "apigatewayv2:ListPortalProducts"
	input := &apigatewayv2.ListPortalProductsInput{}
	var (
		batch    []*store.Resource
		products []childParent
	)
	for {
		page, err := client.ListPortalProducts(ctx, input)
		if err != nil {
			if !skipPortalListError(st, op, acct.ID, region, err) {
				return 0, 0, nil, fmt.Errorf("%s: %w", op, err)
			}
			break
		}
		for _, p := range page.Items {
			productARN, id := sv(p.PortalProductArn), sv(p.PortalProductId)
			if productARN == "" || id == "" {
				continue
			}
			parsed, err := arn.Parse(productARN)
			if err != nil {
				return 0, 0, nil, fmt.Errorf("%s: PortalProductArn %q: %w", op, productARN, err)
			}
			if parsed.AccountID == "" {
				return 0, 0, nil, fmt.Errorf("%s: PortalProductArn %q names no account", op, productARN)
			}
			if parsed.AccountID != acct.ID {
				continue
			}
			products = append(products, childParent{id: id, arn: productARN})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2PortalProduct, NativeID: productARN,
				Name: p.DisplayName, Region: &region,
				AttributesJSON: mustJSON(p), TagsJSON: mapTagsJSON(p.Tags), DiscoveredBy: scanID,
			})
		}
		if next := sv(page.NextToken); next == "" || next == sv(input.NextToken) {
			break
		}
		input.NextToken = page.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, op)
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, products, nil
}

// isPortalProductGone matches a product deleted between ListPortalProducts
// and the per-product page list.
func isPortalProductGone(err error) bool {
	return isAPIErrorCode(err, "NotFoundException")
}

// scanAPIGatewayV2ProductPages stores each product's documentation pages. The
// list returns summaries without the page body, so no page content is stored.
func scanAPIGatewayV2ProductPages(ctx context.Context, client apigatewayv2PortalAPI, acct *account, region string, st *store.Store, scanID string, products []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "apigatewayv2:ListProductPages", products, isPortalProductGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			input := &apigatewayv2.ListProductPagesInput{PortalProductId: &p.id}
			var rows []*store.Resource
			for {
				page, err := client.ListProductPages(ctx, input)
				if err != nil {
					return nil, err
				}
				for _, pp := range page.Items {
					pageARN := sv(pp.ProductPageArn)
					if pageARN == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAPIGatewayV2ProductPage, NativeID: pageARN,
						Name: pp.PageTitle, Region: &region,
						AttributesJSON: mustJSON(pp), DiscoveredBy: scanID,
					})
				}
				if next := sv(page.NextToken); next == "" || next == sv(input.NextToken) {
					return rows, nil
				}
				input.NextToken = page.NextToken
			}
		})
}

// scanAPIGatewayV2ProductRestEndpointPages stores each product's REST
// endpoint pages from the body-less list summaries.
func scanAPIGatewayV2ProductRestEndpointPages(ctx context.Context, client apigatewayv2PortalAPI, acct *account, region string, st *store.Store, scanID string, products []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "apigatewayv2:ListProductRestEndpointPages", products, isPortalProductGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			input := &apigatewayv2.ListProductRestEndpointPagesInput{PortalProductId: &p.id}
			var rows []*store.Resource
			for {
				page, err := client.ListProductRestEndpointPages(ctx, input)
				if err != nil {
					return nil, err
				}
				for _, ep := range page.Items {
					pageARN := sv(ep.ProductRestEndpointPageArn)
					if pageARN == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAPIGatewayV2ProductRestEndpointPage, NativeID: pageARN,
						Name: ep.Endpoint, Region: &region, Status: nonEmptyPtr(string(ep.Status)),
						AttributesJSON: mustJSON(ep), DiscoveredBy: scanID,
					})
				}
				if next := sv(page.NextToken); next == "" || next == sv(input.NextToken) {
					return rows, nil
				}
				input.NextToken = page.NextToken
			}
		})
}
