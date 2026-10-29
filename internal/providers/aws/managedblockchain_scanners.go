package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/managedblockchain"
	mbtypes "github.com/aws/aws-sdk-go-v2/service/managedblockchain/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeManagedBlockchainAccessor, Service: "managed-blockchain"})
	registerType(restype.Descriptor{Type: TypeManagedBlockchainMember, Service: "managed-blockchain"})
	registerType(restype.Descriptor{Type: TypeManagedBlockchainNetwork, Service: "managed-blockchain"})
	registerType(restype.Descriptor{Type: TypeManagedBlockchainNode, Service: "managed-blockchain"})
	registerType(restype.Descriptor{Type: TypeManagedBlockchainProposal, Service: "managed-blockchain"})
	registerService(serviceEntry{
		name: "aws:managed-blockchain",
		fn:   scanManagedBlockchain,
	})
}

type managedBlockchainAPI interface {
	ListAccessors(context.Context, *managedblockchain.ListAccessorsInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListAccessorsOutput, error)
	ListNetworks(context.Context, *managedblockchain.ListNetworksInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNetworksOutput, error)
	ListMembers(context.Context, *managedblockchain.ListMembersInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListMembersOutput, error)
	ListNodes(context.Context, *managedblockchain.ListNodesInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListNodesOutput, error)
	ListProposals(context.Context, *managedblockchain.ListProposalsInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListProposalsOutput, error)
	ListTagsForResource(context.Context, *managedblockchain.ListTagsForResourceInput, ...func(*managedblockchain.Options)) (*managedblockchain.ListTagsForResourceOutput, error)
}

const (
	mbTagsOp = "managedblockchain:ListTagsForResource"
	// mbNotReady is the code ListTagsForResource models for a resource that
	// exists but is not in a status that can complete the call.
	mbNotReady = "ResourceNotReadyException"
	// The MaxResults maximum each listing's model sets.
	mbAccessorsPageCap = 50
	mbNetworksPageCap  = 10
	mbMembersPageCap   = 20
	mbNodesPageCap     = 20
	mbProposalsPageCap = 100
	// mbStatusDeleted is the DELETED value of the accessor, network, member
	// and node status enums.
	mbStatusDeleted = "DELETED"
)

// mbGone is the not-found code ListTagsForResource models, and the one
// ListProposals models for a network deleted since it was listed. ListMembers
// and ListNodes model none, so a network deleted between the listings fails
// them.
var mbGone = []string{"ResourceNotFoundException"}

// errMBProposalNoARN fails the tag read of a proposal listed without the Arn
// ListTagsForResource needs; the row is stored without tags.
var errMBProposalNoARN = errors.New("the proposal was listed without an ARN, so its tags could not be read; it is stored without tags")

// mbNetwork is a network the per-network phases read: its id, which their
// listings are sent, its ARN ("" when it was listed without one), and whether
// it runs on Ethereum.
type mbNetwork struct {
	id, arn  string
	ethereum bool
}

// mbOwnedMembers is keyed by each network a member this account owns was
// listed on, and holds the ids of those members whose nodes can be listed:
// none when every one of them was listed without an Id.
type mbOwnedMembers map[string][]string

// scanManagedBlockchain discovers the Managed Blockchain resources of one
// region: the accessors, the networks this account takes part in, and each
// network's members, nodes and proposals. Every listing runs as its own
// phase, one at a time: a phase's error does not stop the later ones; a
// failed write and ctx's end do (runHostPhases). Every type has its tags
// read per row (mbTagReads); the denials of the whole scan are one warning,
// and its refusals as not ready another.
//
// The model documents ListMembers and ListProposals as applying only to
// Hyperledger Fabric, and ListNodes as requiring a MemberId on Fabric, so an
// Ethereum network gets one network-wide node listing and no member or
// proposal listing, and every other network gets its member and proposal
// listings and one node listing per member this account owns. A network
// with a framework other than ETHEREUM is listed as Fabric.
//
// A Fabric network is shared by the accounts of its members: its member
// listing returns other accounts' members too, which are theirs, so their
// tags are not read (mbScanMembers). Networks and proposals are network-wide:
// every account taking part can tag them, and its tags are seen by it alone
// (the Managed Blockchain tagging guides), so every network's and proposal's
// tags are read.
func scanManagedBlockchain(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := managedblockchain.NewFromConfig(acct.cfg, func(o *managedblockchain.Options) { o.Region = region })
	reads := &mbTagReads{client: client}
	tags := reads.mbReader().sharingDenials()
	var (
		networks []mbNetwork
		owned    mbOwnedMembers
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "managedblockchain", 0, []hostPhase{
		{op: "managedblockchain:ListAccessors", run: func(p *phaseTotals) bool {
			return mbScanAccessors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "managedblockchain:ListNetworks", run: func(p *phaseTotals) (failed bool) {
			networks, failed = mbScanNetworks(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "managedblockchain:ListMembers", run: func(p *phaseTotals) (failed bool) {
			owned, failed = mbScanMembers(ctx, client, acct, region, st, scanID, networks, tags, p)
			return failed
		}},
		{op: "managedblockchain:ListNodes", run: func(p *phaseTotals) bool {
			return mbScanNodes(ctx, client, acct, region, st, scanID, networks, owned, tags, p)
		}},
		{op: "managedblockchain:ListProposals", run: func(p *phaseTotals) bool {
			return mbScanProposals(ctx, reads, acct, region, st, scanID, networks, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	// A refused row may be one a failed write lost, so nothing is warned
	// about then.
	if !errors.Is(err, store.ErrStoreWrite) {
		reads.warnMBNotReady(st, acct, region)
	}
	return total, inserted, err
}

// mbTagReads reads one scan's rows' tags with ListTagsForResource, which
// returns every tag in one response (it takes no page token).
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed. mbNotReady keeps the row untagged and is tallied in
// notReady, warned about once after the scan instead of failing it: failing
// would fail every scan for as long as the resource stays in a status that
// refuses the call. Which statuses get it is unverified. The reads run
// concurrently, so the tally keeps the smallest NativeID.
type mbTagReads struct {
	client   managedBlockchainAPI
	notReady skipTally
}

// mbReader reads a row's tags at its NativeID, the ARN its listing returned.
// The Service Reference lists every type read here under
// ListTagsForResource (accessor, member, network, node, proposal).
func (m *mbTagReads) mbReader() rowTagReader {
	return rowTagReader{
		op:       mbTagsOp,
		notFound: mbGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			return m.mbReadAt(ctx, r, r.NativeID)
		},
	}
}

// mbReadAt reads r's tags at arn. A row whose status is DELETED is not read
// and keeps the TagsJSON its builder set: the listings keep returning deleted
// members and nodes, and if the op answered such a row with its not-found
// code (unverified live), the row would be dropped although the listing
// returned it.
func (m *mbTagReads) mbReadAt(ctx context.Context, r *store.Resource, arn string) (*string, error) {
	if sv(r.Status) == mbStatusDeleted {
		return r.TagsJSON, nil
	}
	out, err := m.client.ListTagsForResource(ctx, &managedblockchain.ListTagsForResourceInput{ResourceArn: &arn})
	if isAPIErrorCode(err, mbNotReady) {
		m.notReady.skippedLeast(r.NativeID, err)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return mapTagsJSON(out.Tags), nil
}

// warnMBNotReady reports the rows whose tag read mbNotReady refused, once.
// Call it only once the rows are written.
func (m *mbTagReads) warnMBNotReady(st *store.Store, acct *account, region string) {
	m.notReady.warnCode(st, mbTagsOp, acct, region, mbNotReady, "row(s) stored without tags",
		"the resource's status refused the tag read")
}

// mbRow is the row of a listed element stored under nativeID.
func mbRow(acct *account, region, scanID, typ, nativeID string, name *string, status string, element any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: name, Region: &region, Status: &status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

// mbScanAccessors stores the accessors with their tags, each page written as
// soon as its tags are read.
func mbScanAccessors(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "managedblockchain:ListAccessors",
		func(ctx context.Context, token *string) ([]mbtypes.AccessorSummary, *string, error) {
			out, err := client.ListAccessors(ctx, &managedblockchain.ListAccessorsInput{MaxResults: sdkaws.Int32(mbAccessorsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Accessors, out.NextToken, nil
		},
		func(a mbtypes.AccessorSummary) (*store.Resource, string, string) {
			label := sv(a.Id)
			return arnRow("accessors", a.Arn, a.Id, mbRow(acct, region, scanID, TypeManagedBlockchainAccessor, sv(a.Arn), &label, string(a.Status), a))
		},
		tags, p)
}

// mbScanNetworks stores the networks with their tags and returns the ones
// that have an id, the parents of the per-network phases: those stored, and
// those listed without an Arn, which are not stored (one empty NativeID would
// merge them) but whose members and nodes are still read, as their rows do
// not need the network's ARN. Networks are few (AWS quotas bound how many an
// account takes part in), so their listing is held whole. It reports whether
// the write failed, after which the scan must stop.
//
// An Ethereum network is a public network AWS runs, not one an account
// deletes, so a not-found answer to its tag read cannot mean it was deleted
// since it was listed: the row is kept untagged instead of being dropped
// with the nodes it parents. The Ethereum guide says an account can tag such
// a network only once it has a node on it; how the op answers an account
// without one is unverified live.
func mbScanNetworks(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]mbNetwork, bool) {
	// Written while the rows are built, before the concurrent tag reads,
	// which only read them.
	ethereum := map[*store.Resource]bool{}
	byARN := map[string]mbNetwork{}
	var noARN []mbNetwork
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if ethereum[r] && isAPIErrorCode(err, mbGone...) {
			return nil, nil
		}
		return got, err
	}
	stored, failed := storeParentListTags(ctx, st, acct, region, "managedblockchain:ListNetworks", "networks", "Id",
		func(ctx context.Context, token *string) ([]mbtypes.NetworkSummary, *string, error) {
			out, err := client.ListNetworks(ctx, &managedblockchain.ListNetworksInput{MaxResults: sdkaws.Int32(mbNetworksPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Networks, out.NextToken, nil
		},
		func(n mbtypes.NetworkSummary) (*store.Resource, string, string) {
			r, why, key := arnRow("networks", n.Arn, n.Id, mbRow(acct, region, scanID, TypeManagedBlockchainNetwork, sv(n.Arn), n.Name, string(n.Status), n))
			eth := n.Framework == mbtypes.FrameworkEthereum
			switch {
			case r != nil:
				ethereum[r] = eth
				byARN[r.NativeID] = mbNetwork{id: sv(n.Id), arn: r.NativeID, ethereum: eth}
			case sv(n.Id) != "":
				noARN = append(noARN, mbNetwork{id: sv(n.Id), ethereum: eth})
			}
			return r, why, key
		},
		func(n mbtypes.NetworkSummary) string { return sv(n.Id) },
		tags, p)
	if failed {
		return nil, true
	}
	networks := make([]mbNetwork, len(stored))
	for i, s := range stored {
		networks[i] = byARN[s.arn]
	}
	return append(networks, noARN...), false
}

// mbFabric is the networks that are not Ethereum, as the parents their
// member and proposal listings key on, and their ARNs by id.
func mbFabric(networks []mbNetwork) ([]childParent, map[string]string) {
	var parents []childParent
	arns := map[string]string{}
	for _, n := range networks {
		if !n.ethereum {
			parents = append(parents, childParent{id: n.id, arn: n.arn})
			arns[n.id] = n.arn
		}
	}
	return parents, arns
}

// mbScanMembers stores each Fabric network's members with their tags and
// returns the members this account owns, the parents of the Fabric node
// listings: those stored, and those listed without an Arn, which are not
// stored but whose nodes are still read, as their rows do not need the
// member's ARN. A member another account owns (IsOwned false) is that
// account's, so its tags are not read; a member without IsOwned counts as
// this account's, so a failed read of it is reported rather than silenced.
// A network's members are few (AWS quotas bound them), so they are held
// until one write; one network's failure costs the others none of theirs.
func mbScanMembers(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, networks []mbNetwork, tags rowTagReader, p *phaseTotals) (mbOwnedMembers, bool) {
	type member struct{ network, id string }
	// Written while the rows are built, before the concurrent tag reads,
	// which only read it.
	ownedRows := map[*store.Resource]member{}
	var noARN []member
	listedOwned := map[string]bool{}
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if _, own := ownedRows[r]; !own {
			return r.TagsJSON, nil
		}
		return read(ctx, r)
	}
	parents, _ := mbFabric(networks)
	stored, failed := storeEachParentTags(ctx, st, acct, region, "managedblockchain:ListMembers", nil, parents,
		func(ctx context.Context, networkID string, token *string) ([]mbtypes.MemberSummary, *string, error) {
			out, err := client.ListMembers(ctx, &managedblockchain.ListMembersInput{NetworkId: &networkID, MaxResults: sdkaws.Int32(mbMembersPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Members, out.NextToken, nil
		},
		func(networkID string, m mbtypes.MemberSummary) (*store.Resource, string, string) {
			r, why, key := arnRow("members", m.Arn, m.Id, mbRow(acct, region, scanID, TypeManagedBlockchainMember, sv(m.Arn), m.Name, string(m.Status), m))
			if m.IsOwned != nil && !*m.IsOwned {
				return r, why, key
			}
			listedOwned[networkID] = true
			switch {
			case r != nil:
				ownedRows[r] = member{network: networkID, id: sv(m.Id)}
			case sv(m.Id) != "":
				noARN = append(noARN, member{network: networkID, id: sv(m.Id)})
			}
			return r, why, key
		},
		tags, p)
	if failed {
		return nil, true
	}
	// A network whose owned members all lack an Id is keyed too, so the node
	// phase does not warn that it had none listed; ListMembers warns of them.
	owned := mbOwnedMembers{}
	for network := range listedOwned {
		owned[network] = nil
	}
	var noID skipTally
	for _, r := range stored {
		m, own := ownedRows[r]
		switch {
		case !own:
		case m.id == "":
			noID.skipped(r.NativeID, nil)
		default:
			owned[m.network] = append(owned[m.network], m.id)
		}
	}
	for _, m := range noARN {
		owned[m.network] = append(owned[m.network], m.id)
	}
	warnNoChildID(st, "managedblockchain:ListMembers", acct, region, &noID, "members", "Id")
	return owned, false
}

// mbScanNodes stores the nodes with their tags: each Ethereum network's
// nodes, listed network-wide, and on every other network the nodes of each
// member this account owns. Every node's tags are read: a Fabric listing is
// sent a member this account owns, and an Ethereum network's nodes are
// those of the account listing them (unverified live). A listing's nodes are
// few (AWS quotas bound them), so they are held until one write; one
// listing's failure costs the others none of theirs.
//
// ListNetworks returns only the networks this account takes part in, so a
// Fabric network without a member of this account is one whose member
// listing failed or was cut short: its nodes cannot be listed, which is
// warned about once after the write.
func mbScanNodes(ctx context.Context, client managedBlockchainAPI, acct *account, region string, st *store.Store, scanID string, networks []mbNetwork, owned mbOwnedMembers, tags rowTagReader, p *phaseTotals) bool {
	type nodeListing struct{ network, member string }
	var (
		parents  []childParent
		noMember skipTally
	)
	listings := map[string]nodeListing{}
	for _, n := range networks {
		if n.ethereum {
			parents = append(parents, childParent{id: n.id, arn: n.arn})
			listings[n.id] = nodeListing{network: n.id}
			continue
		}
		if _, listed := owned[n.id]; !listed {
			noMember.skipped(n.id, nil)
		}
		for _, m := range owned[n.id] {
			key := n.id + "/" + m
			parents = append(parents, childParent{id: key, arn: n.arn})
			listings[key] = nodeListing{network: n.id, member: m}
		}
	}
	_, failed := storeEachParentTags(ctx, st, acct, region, "managedblockchain:ListNodes", nil, parents,
		func(ctx context.Context, key string, token *string) ([]mbtypes.NodeSummary, *string, error) {
			l := listings[key]
			out, err := client.ListNodes(ctx, &managedblockchain.ListNodesInput{
				NetworkId: &l.network, MemberId: nonEmptyPtr(l.member), MaxResults: sdkaws.Int32(mbNodesPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Nodes, out.NextToken, nil
		},
		func(_ string, n mbtypes.NodeSummary) (*store.Resource, string, string) {
			label := sv(n.Id)
			return arnRow("nodes", n.Arn, n.Id, mbRow(acct, region, scanID, TypeManagedBlockchainNode, sv(n.Arn), &label, string(n.Status), n))
		},
		tags, p)
	if !failed && noMember.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "managedblockchain:ListNodes", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d network(s) had no member of this account listed, so this account's nodes on them could not be listed; first %s", noMember.count, noMember.first),
		})
	}
	return failed
}

// mbScanProposals stores each Fabric network's proposals with their tags,
// whoever's member made them: an account can tag any proposal of its
// network, its tags seen by it alone. ProposalSummary carries its own ARN,
// but the NativeID is {networkARN}/proposal/{ProposalId} so the resolver can
// recover the parent network; the tag read is sent the Arn. A network listed
// without an Arn therefore has no proposal listing. A network's proposals
// are held until one write; one network's failure costs the others none of
// theirs.
func mbScanProposals(ctx context.Context, reads *mbTagReads, acct *account, region string, st *store.Store, scanID string, networks []mbNetwork, tags rowTagReader, p *phaseTotals) bool {
	// Written while the rows are built, before the concurrent tag reads,
	// which only read it.
	arnOf := map[*store.Resource]string{}
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		arn := arnOf[r]
		if arn == "" {
			return nil, errMBProposalNoARN
		}
		return reads.mbReadAt(ctx, r, arn)
	}
	parents, arns := mbFabric(networks)
	parents = slices.DeleteFunc(parents, func(n childParent) bool { return n.arn == "" })
	_, failed := storeEachParentTags(ctx, st, acct, region, "managedblockchain:ListProposals", mbGone, parents,
		func(ctx context.Context, networkID string, token *string) ([]mbtypes.ProposalSummary, *string, error) {
			out, err := reads.client.ListProposals(ctx, &managedblockchain.ListProposalsInput{NetworkId: &networkID, MaxResults: sdkaws.Int32(mbProposalsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Proposals, out.NextToken, nil
		},
		func(networkID string, pr mbtypes.ProposalSummary) (*store.Resource, string, string) {
			pid := sv(pr.ProposalId)
			if pid == "" {
				return nil, "proposals have no ProposalId", cmp.Or(sv(pr.Arn), noIdentifier)
			}
			label := pid
			r := mbRow(acct, region, scanID, TypeManagedBlockchainProposal, arns[networkID]+"/proposal/"+pid, &label, string(pr.Status), pr)
			arnOf[r] = sv(pr.Arn)
			return r, "", ""
		},
		tags, p)
	return failed
}
