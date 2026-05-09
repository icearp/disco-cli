package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/managedblockchain"
)

func init() {
	registerService(serviceEntry{
		name: "aws:managed-blockchain",
		fn:   scanManagedBlockchain,
		emits: []coverage.TypeDecl{
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainAccessor, Leaf: true},
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainMember, Leaf: true},
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainNode, Leaf: true},
		},
	})
}

type managedBlockchainAPI interface {
	ListAccessors(context.Context, *managedblockchain.ListAccessorsInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListAccessorsOutput, error)
	ListNetworks(context.Context, *managedblockchain.ListNetworksInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNetworksOutput, error)
	ListMembers(context.Context, *managedblockchain.ListMembersInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListMembersOutput, error)
	ListNodes(context.Context, *managedblockchain.ListNodesInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNodesOutput, error)
}

// scanManagedBlockchain discovers ManagedBlockchain accessors plus per-network
// members and nodes. Members and nodes fan out per network via ListNetworks.
func scanManagedBlockchain(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := managedblockchain.NewFromConfig(acct.cfg, func(o *managedblockchain.Options) { o.Region = region })

	t, i, ferr := scanMBAccessors(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	netIDs, ferr := scanMBNetworkIDs(ctx, client, acct, region, st)
	if ferr != nil {
		return total, inserted, ferr
	}
	for _, nid := range netIDs {
		t, i, ferr = scanMBMembers(ctx, client, acct, region, st, scanID, nid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanMBNodes(ctx, client, acct, region, st, scanID, nid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanMBAccessors(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := managedblockchain.NewListAccessorsPaginator(client, &managedblockchain.ListAccessorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "managedblockchain:ListAccessors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("managedblockchain:ListAccessors: %w", err)
		}
		for _, a := range out.Accessors {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Id)
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeManagedBlockchainAccessor, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "managedblockchain accessors")
}

func scanMBNetworkIDs(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store) ([]string, error) {
	pager := managedblockchain.NewListNetworksPaginator(client, &managedblockchain.ListNetworksInput{})
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "managedblockchain:ListNetworks", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("managedblockchain:ListNetworks: %w", err)
		}
		for _, n := range out.Networks {
			if id := sv(n.Id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func scanMBMembers(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, networkID string) (int, int, error) {
	nid := networkID
	pager := managedblockchain.NewListMembersPaginator(client, &managedblockchain.ListMembersInput{NetworkId: &nid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "managedblockchain:ListMembers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("managedblockchain:ListMembers: %w", err)
		}
		for _, m := range out.Members {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeManagedBlockchainMember, NativeID: arn,
				Name: m.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "managedblockchain members")
}

func scanMBNodes(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, networkID string) (int, int, error) {
	nid := networkID
	pager := managedblockchain.NewListNodesPaginator(client, &managedblockchain.ListNodesInput{NetworkId: &nid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "managedblockchain:ListNodes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("managedblockchain:ListNodes: %w", err)
		}
		for _, n := range out.Nodes {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			label := sv(n.Id)
			status := string(n.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeManagedBlockchainNode, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "managedblockchain nodes")
}
