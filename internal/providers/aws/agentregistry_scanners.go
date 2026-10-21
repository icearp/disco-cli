package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAgentRegistryRegistry, Service: "agent-registry"})
	registerType(restype.Descriptor{Type: TypeAgentRegistryRegistryRecord, Service: "agent-registry"})
	registerService(serviceEntry{name: "aws:agent-registry", fn: scanAgentRegistry})
}

const (
	agentRegistryListRegistriesOp = "agent-registry:ListRegistries"
	agentRegistryListRecordsOp    = "agent-registry:ListRegistryRecords"
	// agentRegistryPageCap is the model's maxResults ceiling for both list ops.
	agentRegistryPageCap = 100
)

// agentRegistryAPI is the narrow Agent Registry Control surface the scanners
// use; *agentregistrycontrol.Client satisfies it.
type agentRegistryAPI interface {
	ListRegistries(context.Context, *agentregistrycontrol.ListRegistriesInput, ...func(*agentregistrycontrol.Options)) (*agentregistrycontrol.ListRegistriesOutput, error)
	ListRegistryRecords(context.Context, *agentregistrycontrol.ListRegistryRecordsInput, ...func(*agentregistrycontrol.Options)) (*agentregistrycontrol.ListRegistryRecordsOutput, error)
}

func scanAgentRegistry(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := agentregistrycontrol.NewFromConfig(acct.cfg, func(o *agentregistrycontrol.Options) { o.Region = region })
	return scanAgentRegistryAll(ctx, client, acct, region, st, scanID)
}

// scanAgentRegistryAll lists the registries, then the records of every
// registry it stored. Both errors pass through demoteTransient before they
// are joined, so a throttle on one phase cannot make the dispatcher classify
// the whole service transient and report the other phase's hard error as a
// transient one. NXDOMAIN is left as is and reports the region unavailable.
func scanAgentRegistryAll(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	registries, total, inserted, rerr := scanAgentRegistryRegistries(ctx, client, acct, region, st, scanID)
	t, i, cerr := scanAgentRegistryRecords(ctx, client, acct, region, st, scanID, registries)
	return total + t, inserted + i, errors.Join(
		demoteTransient(st, agentRegistryListRegistriesOp, acct, region, rerr),
		demoteTransient(st, agentRegistryListRecordsOp, acct, region, cerr),
	)
}

// scanAgentRegistryRegistries lists the account's registries in the region.
// It returns the registries of every page it stored, so the records phase
// queries only registries that have a row, including after a later page
// fails.
func scanAgentRegistryRegistries(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string) (registries []childParent, total, inserted int, err error) {
	pager := agentregistrycontrol.NewListRegistriesPaginator(client, &agentregistrycontrol.ListRegistriesInput{},
		func(o *agentregistrycontrol.ListRegistriesPaginatorOptions) { o.Limit = agentRegistryPageCap })
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return registries, total, inserted, skipIfAccessDenied(st, agentRegistryListRegistriesOp, acct.ID, region, perr)
			}
			return registries, total, inserted, fmt.Errorf("%s: %w", agentRegistryListRegistriesOp, perr)
		}
		batch := make([]*store.Resource, 0, len(out.Registries))
		listed := make([]childParent, 0, len(out.Registries))
		for _, r := range out.Registries {
			id, arn := sv(r.RegistryId), sv(r.RegistryArn)
			if id == "" || arn == "" {
				continue
			}
			listed = append(listed, childParent{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAgentRegistryRegistry, NativeID: arn,
				Name: r.Name, Status: nonEmptyPtr(string(r.Status)), Region: &region, CreatedAt: tp(r.CreatedAt),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "agent-registry registries")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return registries, total, inserted, uerr
		}
		registries = append(registries, listed...)
	}
	return registries, total, inserted, nil
}

// scanAgentRegistryRecords lists each registry's records (MCP servers,
// agents, skills and custom entries published to it), under the registry.
//
// Per registry: one deleted after it was listed (ResourceNotFoundException)
// is skipped silently. ConflictException and ValidationException, both
// modeled for the op, skip that registry with one warning per code: a
// registry that is not READY (CREATING, CREATE_FAILED, DELETING) may refuse
// its record listing with either (not observed live), and failing the phase
// would fail every scan while it stays in that state. A transient error left
// after the SDK's retries also skips that registry with one warning. A
// skipped registry stores none of its records.
func scanAgentRegistryRecords(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string, registries []childParent) (int, int, error) {
	const why = "a registry that is not READY may refuse its record listing"
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	var conflict skipTally
	total, inserted, err := childFanOutSkipNotReady(ctx, st, acct, region, agentRegistryListRecordsOp, registries, isGone, byParentID,
		"registry(ies)", why,
		func(gctx context.Context, reg childParent) ([]*store.Resource, error) {
			rows, err := listAgentRegistryRecords(gctx, client, acct, region, scanID, reg)
			if conflict.dropCode("ConflictException", reg.id, err) {
				return nil, nil
			}
			return rows, err
		})
	conflict.warnCode(st, agentRegistryListRecordsOp, acct, region, "ConflictException", "registry(ies)", why)
	return total, inserted, err
}

// listAgentRegistryRecords returns the records of one registry.
func listAgentRegistryRecords(ctx context.Context, client agentRegistryAPI, acct *account, region, scanID string, reg childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := agentregistrycontrol.NewListRegistryRecordsPaginator(client,
		&agentregistrycontrol.ListRegistryRecordsInput{RegistryId: &reg.id},
		func(o *agentregistrycontrol.ListRegistryRecordsPaginatorOptions) { o.Limit = agentRegistryPageCap })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range out.RegistryRecords {
			arn := sv(rec.RecordArn)
			if arn == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAgentRegistryRegistryRecord, NativeID: arn,
				Name: rec.Name, Status: nonEmptyPtr(string(rec.Status)), Region: &region, CreatedAt: tp(rec.CreatedAt),
				AttributesJSON: mustJSON(rec), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
