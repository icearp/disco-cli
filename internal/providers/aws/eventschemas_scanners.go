package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/schemas"
	schemastypes "github.com/aws/aws-sdk-go-v2/service/schemas/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEventSchemasDiscoverer, Service: "event-schemas"})
	registerType(restype.Descriptor{Type: TypeEventSchemasRegistry, Service: "event-schemas"})
	registerType(restype.Descriptor{Type: TypeEventSchemasRegistryPolicy, Service: "event-schemas"})
	registerType(restype.Descriptor{Type: TypeEventSchemasSchema, Service: "event-schemas"})
	registerService(serviceEntry{
		name: "aws:event-schemas",
		fn:   scanEventSchemas,
	})
}

type eventSchemasAPI interface {
	ListDiscoverers(context.Context, *schemas.ListDiscoverersInput, ...func(*schemas.Options)) (*schemas.ListDiscoverersOutput, error)
	ListRegistries(context.Context, *schemas.ListRegistriesInput, ...func(*schemas.Options)) (*schemas.ListRegistriesOutput, error)
	ListSchemas(context.Context, *schemas.ListSchemasInput, ...func(*schemas.Options)) (*schemas.ListSchemasOutput, error)
	GetResourcePolicy(context.Context, *schemas.GetResourcePolicyInput, ...func(*schemas.Options)) (*schemas.GetResourcePolicyOutput, error)
}

// The list ops leave Limit unset: the model gives it no range, so no page
// size is known to be accepted and the service's default is used.
const (
	esDiscoverersOp    = "schemas:ListDiscoverers"
	esRegistriesOp     = "schemas:ListRegistries"
	esSchemasOp        = "schemas:ListSchemas"
	esRegistryPolicyOp = "schemas:GetResourcePolicy"
)

// scanEventSchemas discovers EventBridge Schemas discoverers, registries,
// per-registry schemas, and per-registry resource policies. Every phase runs;
// the schema and policy phases read only the registries the registry phase
// stored, and only a store write failure stops the scan.
func scanEventSchemas(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := schemas.NewFromConfig(acct.cfg, func(o *schemas.Options) { o.Region = region })
	var registries []childParent
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{esDiscoverersOp, func() (int, int, error) {
			return scanESDiscoverers(ctx, client, acct, region, st, scanID)
		}},
		{esRegistriesOp, func() (t, i int, err error) {
			t, i, registries, err = scanESRegistries(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{esSchemasOp, func() (int, int, error) {
			return scanESSchemas(ctx, client, acct, region, st, scanID, registries)
		}},
		{esRegistryPolicyOp, func() (int, int, error) {
			return scanESRegistryPolicies(ctx, client, acct, region, st, scanID, registries)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

func scanESDiscoverers(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, esDiscoverersOp,
		func(ctx context.Context, token *string) ([]schemastypes.DiscovererSummary, *string, error) {
			out, err := client.ListDiscoverers(ctx, &schemas.ListDiscoverersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Discoverers, out.NextToken, nil
		},
		func(d schemastypes.DiscovererSummary) (*store.Resource, string, string) {
			if sv(d.DiscovererArn) == "" {
				return nil, "discoverers have an empty DiscovererArn", sv(d.DiscovererId)
			}
			label := sv(d.DiscovererId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasDiscoverer, NativeID: *d.DiscovererArn,
				Name: &label, Region: &region, Status: nonEmptyPtr(string(d.State)),
				AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanESRegistries lists every registry the account sees, AWS-provided ones
// included (no Scope filter), and also returns the stored ones that carry a
// RegistryName, for the schema and policy phases.
func scanESRegistries(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, esRegistriesOp, "registries", "RegistryName",
		func(ctx context.Context, token *string) ([]schemastypes.RegistrySummary, *string, error) {
			out, err := client.ListRegistries(ctx, &schemas.ListRegistriesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Registries, out.NextToken, nil
		},
		func(r schemastypes.RegistrySummary) (*store.Resource, string, string) {
			if sv(r.RegistryArn) == "" {
				return nil, "registries have an empty RegistryArn", sv(r.RegistryName)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasRegistry, NativeID: *r.RegistryArn,
				Name: r.RegistryName, Region: &region,
				AttributesJSON: mustJSON(r), TagsJSON: mapTagsJSON(r.Tags), DiscoveredBy: scanID,
			}, "", ""
		},
		func(r schemastypes.RegistrySummary) string { return sv(r.RegistryName) })
}

// scanESSchemas lists each registry's schemas concurrently. ListSchemas
// models no not-found code, so a registry deleted since it was listed fails
// the phase rather than being skipped.
func scanESSchemas(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string, registries []childParent) (int, int, error) {
	names := make([]string, len(registries))
	for i, r := range registries {
		names[i] = r.id
	}
	return storeTokenEachParent(ctx, st, acct, region, esSchemasOp, nil, names,
		func(ctx context.Context, name string, token *string) ([]schemastypes.SchemaSummary, *string, error) {
			out, err := client.ListSchemas(ctx, &schemas.ListSchemasInput{RegistryName: &name, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Schemas, out.NextToken, nil
		},
		func(_ string, s schemastypes.SchemaSummary) (*store.Resource, string, string) {
			if sv(s.SchemaArn) == "" {
				return nil, "schemas have an empty SchemaArn", sv(s.SchemaName)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEventSchemasSchema, NativeID: *s.SchemaArn,
				Name: s.SchemaName, Region: &region,
				AttributesJSON: mustJSON(s), TagsJSON: mapTagsJSON(s.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// esPolicyRead is one registry's policy read: its row (nil when the registry
// has none), the error of a denied call, and any other error.
type esPolicyRead struct {
	row         *store.Resource
	denied, err error
}

// scanESRegistryPolicies reads each registry's resource policy concurrently.
// NotFoundException, the code GetResourcePolicy models, is the registry
// having no policy (or being deleted since it was listed) and stores
// nothing. A denied registry is warned about once for the op; any other
// error, a transient one included, is returned after the other policies are
// stored, since there is no listed row to keep in its place.
func scanESRegistryPolicies(ctx context.Context, client eventSchemasAPI, acct *account, region string, st *store.Store, scanID string, registries []childParent) (int, int, error) {
	reads, stopped := readEachKey(ctx, registries, func(ctx context.Context, r childParent) esPolicyRead {
		return readESRegistryPolicy(ctx, client, acct, region, scanID, r.id)
	})
	var (
		rows   []*store.Resource
		errs   []error
		denied skipTally
	)
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: %w", esRegistryPolicyOp, stopped))
	}
	for i, r := range reads {
		if r.row != nil {
			rows = append(rows, r.row)
		}
		if r.denied != nil {
			denied.skipped(registries[i].id, r.denied)
		}
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return storeReadRows(st, acct, region, esRegistryPolicyOp, rows, nil, func() {
		denied.warnDeniedIn(st, esRegistryPolicyOp, acct, region, "registry(s) whose policy was not read were")
	}, errs...)
}

func readESRegistryPolicy(ctx context.Context, client eventSchemasAPI, acct *account, region, scanID, name string) esPolicyRead {
	out, err := client.GetResourcePolicy(ctx, &schemas.GetResourcePolicyInput{RegistryName: &name})
	switch {
	case err == nil:
	case isAPIErrorCode(err, "NotFoundException"):
		return esPolicyRead{}
	case isAccessDenied(err):
		return esPolicyRead{denied: err}
	default:
		return esPolicyRead{err: fmt.Errorf("%s %s: %w", esRegistryPolicyOp, name, err)}
	}
	if sv(out.Policy) == "" {
		return esPolicyRead{}
	}
	return esPolicyRead{row: &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEventSchemasRegistryPolicy, NativeID: arnFor(acct.partition(), "schemas", region, acct.ID, "registry/"+name+"/policy"),
		Name: &name, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}
}
