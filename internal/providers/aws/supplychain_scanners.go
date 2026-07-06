package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/supplychain"
)

func init() {
	registerService(serviceEntry{
		name: "aws:scn",
		fn:   scanSupplyChain,
		emits: []coverage.TypeDecl{
			{Service: "scn", DiscoType: TypeSupplyChainInstance, Leaf: true},
			// flow/dataset/namespace each wire an outbound attached-to edge to
			// their instance (see supplychain_resolvers.go).
			{Service: "scn", DiscoType: TypeSupplyChainDataIntegrationFlow},
			{Service: "scn", DiscoType: TypeSupplyChainDataset},
			{Service: "scn", DiscoType: TypeSupplyChainNamespace},
		},
	})
}

// supplyChainAPI is the narrow surface the scanner uses. ListInstances is
// account-wide; the rest require an InstanceId (and datasets a Namespace),
// fanned out over the scanned instances. All are paginator-native.
type supplyChainAPI interface {
	ListInstances(context.Context, *supplychain.ListInstancesInput, ...func(*supplychain.Options)) (*supplychain.ListInstancesOutput, error)
	ListDataIntegrationFlows(context.Context, *supplychain.ListDataIntegrationFlowsInput, ...func(*supplychain.Options)) (*supplychain.ListDataIntegrationFlowsOutput, error)
	ListDataLakeNamespaces(context.Context, *supplychain.ListDataLakeNamespacesInput, ...func(*supplychain.Options)) (*supplychain.ListDataLakeNamespacesOutput, error)
	ListDataLakeDatasets(context.Context, *supplychain.ListDataLakeDatasetsInput, ...func(*supplychain.Options)) (*supplychain.ListDataLakeDatasetsOutput, error)
}

// scnInstanceARN synthesizes the Supply Chain instance ARN — the Instance SDK
// shape exposes only InstanceId, no ARN field.
func scnInstanceARN(region, acct, instanceID string) string {
	return fmt.Sprintf("arn:aws:scn:%s:%s:instance/%s", region, acct, instanceID)
}

func scanSupplyChain(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := supplychain.NewFromConfig(acct.cfg, func(o *supplychain.Options) { o.Region = region })
	return scanSupplyChainWith(ctx, client, acct, region, st, scanID)
}

func scanSupplyChainWith(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	instanceIDs, t, i, ferr := scanSCNInstances(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, id := range instanceIDs {
		ft, fi, ferr := scanSCNFlows(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += ft
		inserted += fi

		nsNames, nt, ni, ferr := scanSCNNamespaces(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += nt
		inserted += ni

		for _, ns := range nsNames {
			dt, di, ferr := scanSCNDatasets(ctx, client, acct, region, st, scanID, id, ns)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += dt
			inserted += di
		}
	}
	return total, inserted, nil
}

func scanSCNInstances(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := supplychain.NewListInstancesPaginator(client, &supplychain.ListInstancesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "supplychain:ListInstances", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("supplychain:ListInstances: %w", perr)
		}
		for _, in := range out.Instances {
			id := sv(in.InstanceId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := scnInstanceARN(region, acct.ID, id)
			name := sv(in.InstanceName)
			status := string(in.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainInstance, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "scn instances")
	return ids, t, i, err
}

func scanSCNFlows(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID, instanceID string) (int, int, error) {
	id := instanceID
	pager := supplychain.NewListDataIntegrationFlowsPaginator(client, &supplychain.ListDataIntegrationFlowsInput{InstanceId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "supplychain:ListDataIntegrationFlows", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("supplychain:ListDataIntegrationFlows: %w", perr)
		}
		for _, f := range out.Flows {
			name := sv(f.Name)
			if name == "" {
				continue
			}
			// DataIntegrationFlow has no ARN field; synthesize one off the
			// instance ARN + flow name.
			arn := fmt.Sprintf("%s/data-integration-flow/%s", scnInstanceARN(region, acct.ID, id), name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainDataIntegrationFlow, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "scn data-integration-flows")
}

func scanSCNNamespaces(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID, instanceID string) ([]string, int, int, error) {
	id := instanceID
	pager := supplychain.NewListDataLakeNamespacesPaginator(client, &supplychain.ListDataLakeNamespacesInput{InstanceId: &id})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "supplychain:ListDataLakeNamespaces", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("supplychain:ListDataLakeNamespaces: %w", perr)
		}
		for _, n := range out.Namespaces {
			arn := sv(n.Arn)
			name := sv(n.Name)
			if name != "" {
				names = append(names, name)
			}
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainNamespace, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "scn namespaces")
	return names, t, i, err
}

func scanSCNDatasets(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID, instanceID, namespace string) (int, int, error) {
	id := instanceID
	ns := namespace
	pager := supplychain.NewListDataLakeDatasetsPaginator(client, &supplychain.ListDataLakeDatasetsInput{InstanceId: &id, Namespace: &ns})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "supplychain:ListDataLakeDatasets", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("supplychain:ListDataLakeDatasets: %w", perr)
		}
		for _, d := range out.Datasets {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainDataset, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "scn datasets")
}
