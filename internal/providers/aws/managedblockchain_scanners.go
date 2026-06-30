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
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainNetwork, Leaf: true},
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainNode, Leaf: true},
			// Proposal rows resolve to their parent network (resolver), not Leaf.
			{Service: "managed-blockchain", DiscoType: TypeManagedBlockchainProposal},
		},
	})
}

type managedBlockchainAPI interface {
	ListAccessors(context.Context, *managedblockchain.ListAccessorsInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListAccessorsOutput, error)
	ListNetworks(context.Context, *managedblockchain.ListNetworksInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNetworksOutput, error)
	ListMembers(context.Context, *managedblockchain.ListMembersInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListMembersOutput, error)
	ListNodes(context.Context, *managedblockchain.ListNodesInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNodesOutput, error)
	ListProposals(context.Context, *managedblockchain.ListProposalsInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListProposalsOutput, error)
}

// mbNetworkRef pairs a network's id (needed as the required input for member /
// node / proposal list ops) with its ARN (the network row's NativeID, used to
// synthesize child proposal NativeIDs so a resolver can recover the parent).
type mbNetworkRef struct {
	id, arn string
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

	nets, t, i, ferr := scanMBNetworks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, n := range nets {
		t, i, ferr = scanMBMembers(ctx, client, acct, region, st, scanID, n.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanMBNodes(ctx, client, acct, region, st, scanID, n.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanMBProposals(ctx, client, acct, region, st, scanID, n)
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

// scanMBNetworks lists the blockchain networks the account belongs to,
// upserts a row per network, and returns id+ARN refs for the per-network
// member / node / proposal fan-out.
func scanMBNetworks(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string) ([]mbNetworkRef, int, int, error) {
	pager := managedblockchain.NewListNetworksPaginator(client, &managedblockchain.ListNetworksInput{})
	var refs []mbNetworkRef
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "managedblockchain:ListNetworks", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("managedblockchain:ListNetworks: %w", err)
		}
		for _, n := range out.Networks {
			id := sv(n.Id)
			if id == "" {
				continue
			}
			arn := sv(n.Arn)
			refs = append(refs, mbNetworkRef{id: id, arn: arn})
			if arn == "" {
				continue
			}
			status := string(n.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeManagedBlockchainNetwork, NativeID: arn,
				Name: n.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "managedblockchain networks")
	return refs, t, i, err
}

// scanMBProposals lists governance proposals on a network. ProposalSummary
// carries its own ARN, but the NativeID is synthesized as
// {networkARN}/proposal/{ProposalId} so the resolver can recover the parent
// network. Networks without an ARN (skipped above) can't carry proposals.
func scanMBProposals(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, net mbNetworkRef) (int, int, error) {
	if net.arn == "" {
		return 0, 0, nil
	}
	pager := managedblockchain.NewListProposalsPaginator(client, &managedblockchain.ListProposalsInput{NetworkId: &net.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "managedblockchain:ListProposals", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("managedblockchain:ListProposals: %w", err)
		}
		for _, p := range out.Proposals {
			pid := sv(p.ProposalId)
			if pid == "" {
				continue
			}
			nativeID := net.arn + "/proposal/" + pid
			label := pid
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeManagedBlockchainProposal, NativeID: nativeID,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "managedblockchain proposals")
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
