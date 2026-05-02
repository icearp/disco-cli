package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/schemas"
)

func init() {
	registerService(serviceEntry{
		name: "aws:event-schemas",
		fn:   scanEventSchemas,
		emits: []coverage.TypeDecl{
			{Service: "event-schemas", DiscoType: TypeEventSchemasDiscoverer},
			{Service: "event-schemas", DiscoType: TypeEventSchemasRegistry},
			{Service: "event-schemas", DiscoType: TypeEventSchemasRegistryPolicy},
			{Service: "event-schemas", DiscoType: TypeEventSchemasSchema},
		},
	})
}

type eventSchemasAPI interface {
	ListDiscoverers(context.Context, *schemas.ListDiscoverersInput, ...func(*schemas.Options)) (*schemas.ListDiscoverersOutput, error)
	ListRegistries(context.Context, *schemas.ListRegistriesInput, ...func(*schemas.Options)) (*schemas.ListRegistriesOutput, error)
	ListSchemas(context.Context, *schemas.ListSchemasInput, ...func(*schemas.Options)) (*schemas.ListSchemasOutput, error)
	GetResourcePolicy(context.Context, *schemas.GetResourcePolicyInput, ...func(*schemas.Options)) (*schemas.GetResourcePolicyOutput, error)
}

// scanEventSchemas discovers EventBridge Schemas discoverers, registries,
// per-registry schemas, and per-registry resource policies.
func scanEventSchemas(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := schemas.NewFromConfig(acct.cfg, func(o *schemas.Options) { o.Region = region })

	t, i, ferr := scanESDiscoverers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	registryNames, t, i, ferr := scanESRegistries(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, rn := range registryNames {
		t, i, ferr = scanESSchemas(ctx, client, acct, region, st, scanID, rn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanESRegistryPolicy(ctx, client, acct, region, st, scanID, rn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanESDiscoverers(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := schemas.NewListDiscoverersPaginator(client, &schemas.ListDiscoverersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "schemas:ListDiscoverers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("schemas:ListDiscoverers: %w", err)
		}
		for _, d := range out.Discoverers {
			arn := sv(d.DiscovererArn)
			if arn == "" {
				continue
			}
			label := sv(d.DiscovererId)
			state := string(d.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasDiscoverer, NativeID: arn,
				Name: &label, Region: &region, Status: &state,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "schemas discoverers")
}

func scanESRegistries(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := schemas.NewListRegistriesPaginator(client, &schemas.ListRegistriesInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "schemas:ListRegistries", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("schemas:ListRegistries: %w", err)
		}
		for _, r := range out.Registries {
			arn := sv(r.RegistryArn)
			if arn == "" {
				continue
			}
			if n := sv(r.RegistryName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasRegistry, NativeID: arn,
				Name: r.RegistryName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "schemas registries")
	return names, t, i, err
}

func scanESSchemas(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string, registryName string) (int, int, error) {
	rn := registryName
	pager := schemas.NewListSchemasPaginator(client, &schemas.ListSchemasInput{RegistryName: &rn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "schemas:ListSchemas", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("schemas:ListSchemas: %w", err)
		}
		for _, s := range out.Schemas {
			arn := sv(s.SchemaArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasSchema, NativeID: arn,
				Name: s.SchemaName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "schemas schemas")
}

func scanESRegistryPolicy(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string, registryName string) (int, int, error) {
	rn := registryName
	out, err := client.GetResourcePolicy(ctx, &schemas.GetResourcePolicyInput{RegistryName: &rn})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("schemas:GetResourcePolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:schemas:%s:%s:registry/%s/policy", region, acct.ID, rn)
	label := rn
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEventSchemasRegistryPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "schemas registry-policies")
}
