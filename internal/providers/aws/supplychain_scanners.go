package aws

import (
	"cmp"
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/supplychain"
	scntypes "github.com/aws/aws-sdk-go-v2/service/supplychain/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSupplyChainInstance, Service: "scn"})
	registerType(restype.Descriptor{Type: TypeSupplyChainDataIntegrationFlow, Service: "scn"})
	registerType(restype.Descriptor{Type: TypeSupplyChainDataset, Service: "scn"})
	registerType(restype.Descriptor{Type: TypeSupplyChainNamespace, Service: "scn"})
	registerService(serviceEntry{
		name: "aws:scn",
		fn:   scanSupplyChain,
	})
}

// supplyChainAPI is the narrow surface the scanner uses. ListInstances is
// account-wide; the other listings require an InstanceId (and datasets a
// Namespace), fanned out over the scanned instances.
type supplyChainAPI interface {
	ListInstances(context.Context, *supplychain.ListInstancesInput, ...func(*supplychain.Options)) (*supplychain.ListInstancesOutput, error)
	ListDataIntegrationFlows(context.Context, *supplychain.ListDataIntegrationFlowsInput, ...func(*supplychain.Options)) (*supplychain.ListDataIntegrationFlowsOutput, error)
	ListDataLakeNamespaces(context.Context, *supplychain.ListDataLakeNamespacesInput, ...func(*supplychain.Options)) (*supplychain.ListDataLakeNamespacesOutput, error)
	ListDataLakeDatasets(context.Context, *supplychain.ListDataLakeDatasetsInput, ...func(*supplychain.Options)) (*supplychain.ListDataLakeDatasetsOutput, error)
	ListTagsForResource(context.Context, *supplychain.ListTagsForResourceInput, ...func(*supplychain.Options)) (*supplychain.ListTagsForResourceOutput, error)
}

const (
	scnListInstancesOp  = "supplychain:ListInstances"
	scnListFlowsOp      = "supplychain:ListDataIntegrationFlows"
	scnListNamespacesOp = "supplychain:ListDataLakeNamespaces"
	scnListDatasetsOp   = "supplychain:ListDataLakeDatasets"
	scnTagsOp           = "supplychain:ListTagsForResource"
	// scnPageCap is the maxResults maximum the model documents for every
	// listing read here.
	scnPageCap = 20
)

// scnGone is the not-found code every op read here models, as one of the
// service's own errors. A parent deleted after it was listed answers it, and
// its listing is skipped; so does a row's tag read, and the row is dropped.
var scnGone = []string{"ResourceNotFoundException"}

// scnPredefinedNamespaces are the data lake namespaces the model documents
// every instance as coming with. ListDataLakeNamespaces returns only custom
// namespaces, so the datasets of these are listed by name.
var scnPredefinedNamespaces = []string{"asc", "default"}

// scnInstanceARN builds the Supply Chain instance ARN in the Service
// Reference's format: the Instance SDK shape has only InstanceId, no ARN
// field.
func scnInstanceARN(partition, region, acct, instanceID string) string {
	return arnFor(partition, "scn", region, acct, "instance/"+instanceID)
}

// scnFlowARN builds a data integration flow's ARN in the Service Reference's
// format: the DataIntegrationFlow SDK shape has no ARN field. A flow name
// (^[A-Za-z0-9-]+$) holds no "/".
func scnFlowARN(partition, region, acct, instanceID, name string) string {
	return scnInstanceARN(partition, region, acct, instanceID) + "/data-integration-flows/" + name
}

// scnNamespaceParent is the key a namespace's dataset listing runs under:
// its instance id and its name, neither of which holds a "/" (a UUID, and
// ^[a-z0-9_]+$).
func scnNamespaceParent(instanceID, namespace string) string {
	return instanceID + "/" + namespace
}

func scanSupplyChain(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := supplychain.NewFromConfig(acct.cfg, func(o *supplychain.Options) { o.Region = region })
	return scanSupplyChainWith(ctx, client, acct, region, st, scanID)
}

// scanSupplyChainWith stores the region's instances, then each instance's
// data integration flows and data lake namespaces, then the datasets of each
// instance's predefined namespaces and of the custom namespaces stored. Every
// listing runs as its own phase, one at a time: a phase's error does not
// stop the later ones; a failed write and ctx's end do (runHostPhases).
// Every type has its tags read per row (scnTags), whose denials are one
// warning for the whole scan.
func scanSupplyChainWith(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := scnTags(client).sharingDenials()
	var instances, namespaces []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "supplychain", 0, []hostPhase{
		{op: scnListInstancesOp, run: func(p *phaseTotals) (failed bool) {
			instances, failed = scanSCNInstances(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: scnListFlowsOp, run: func(p *phaseTotals) bool {
			return scanSCNFlows(ctx, client, acct, region, st, scanID, instances, tags, p)
		}},
		{op: scnListNamespacesOp, run: func(p *phaseTotals) (failed bool) {
			namespaces, failed = scanSCNNamespaces(ctx, client, acct, region, st, scanID, instances, tags, p)
			return failed
		}},
		{op: scnListDatasetsOp, run: func(p *phaseTotals) bool {
			return scanSCNDatasets(ctx, client, acct, region, st, scanID, instances, namespaces, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// scnTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN the listing returned for a namespace or dataset, the ARN
// built in the Service Reference's format for an instance or flow. The
// Service Reference lists only the instance under ListTagsForResource and
// TagResource, but it gives every type stored here the aws:ResourceTag
// condition key, every type's Create input takes Tags, and the op's model
// ARN pattern accepts any path under an instance, with a flow ARN as its
// example. ResourceNotFoundException, its modeled not-found, drops a
// resource deleted after it was listed.
//
// An instance whose State is Deleted is not read and stays untagged: if the
// op answered it with its not-found code (unverified live), the row would be
// dropped although the listing returned it.
func scnTags(client supplyChainAPI) rowTagReader {
	return rowTagReader{
		op:       scnTagsOp,
		notFound: scnGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if sv(r.Status) == string(scntypes.InstanceStateDeleted) {
				return nil, nil
			}
			out, err := client.ListTagsForResource(ctx, &supplychain.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanSCNInstances stores the instances with their tags, a page at a time,
// and returns those stored as the parents of the per-instance phases, each
// keyed by its InstanceId. A Deleted instance is a parent too, as it was
// before tags were read: a child listing that answers it with the modeled
// not-found is skipped (scnGone), and any rows it lists are stored. It
// reports whether a write failed, after which the scan must stop.
func scanSCNInstances(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, scnListInstancesOp, "instances", "InstanceId",
		func(ctx context.Context, token *string) ([]scntypes.Instance, *string, error) {
			out, err := client.ListInstances(ctx, &supplychain.ListInstancesInput{MaxResults: sdkaws.Int32(scnPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Instances, out.NextToken, nil
		},
		func(in scntypes.Instance) (*store.Resource, string, string) {
			id := sv(in.InstanceId)
			if id == "" {
				return nil, "instances have no InstanceId", cmp.Or(sv(in.InstanceName), noIdentifier)
			}
			name := sv(in.InstanceName)
			status := string(in.State)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainInstance, NativeID: scnInstanceARN(acct.partition(), region, acct.ID, id),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			}, "", ""
		},
		func(in scntypes.Instance) string { return sv(in.InstanceId) },
		tags, p)
}

// scanSCNFlows stores each instance's data integration flows with their
// tags, each page written as soon as its tags are read
// (storeEachParentPagesTags); one instance's failure costs the others none
// of theirs, and an instance deleted since it was listed is skipped. A flow
// is keyed by the ARN scnFlowARN builds from its instance and name.
func scanSCNFlows(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, scnListFlowsOp, scnGone, instances,
		func(ctx context.Context, instanceID string, token *string) ([]scntypes.DataIntegrationFlow, *string, error) {
			out, err := client.ListDataIntegrationFlows(ctx, &supplychain.ListDataIntegrationFlowsInput{
				InstanceId: &instanceID, MaxResults: sdkaws.Int32(scnPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Flows, out.NextToken, nil
		},
		func(instanceID string, f scntypes.DataIntegrationFlow) (*store.Resource, string, string) {
			name := sv(f.Name)
			if name == "" {
				return nil, "data integration flows have no name", instanceID
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainDataIntegrationFlow, NativeID: scnFlowARN(acct.partition(), region, acct.ID, instanceID, name),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scnListedNamespace is a listed namespace with the instance it was listed
// under, which keys its datasets as it did before tags were read; the
// element's own InstanceId is not read.
type scnListedNamespace struct {
	instanceID string
	ns         scntypes.DataLakeNamespace
}

// scanSCNNamespaces stores each instance's custom data lake namespaces with
// their tags, each page written as soon as its tags are read, and returns
// those stored as parents of the dataset phase, each keyed by
// scnNamespaceParent of the instance it was listed under and its Name. An
// instance deleted since it was listed is skipped. A namespace without an
// Arn (model-required) is warned about and not stored, so its datasets are
// not listed: the dataset phase reads the namespaces this scan stored, never
// those it could not. It reports whether a write failed, after which the
// scan must stop.
func scanSCNNamespaces(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string, instances []childParent, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, scnListNamespacesOp, "namespaces", "Name", scnGone, instances,
		func(ctx context.Context, instanceID string, token *string) ([]scnListedNamespace, *string, error) {
			out, err := client.ListDataLakeNamespaces(ctx, &supplychain.ListDataLakeNamespacesInput{
				InstanceId: &instanceID, MaxResults: sdkaws.Int32(scnPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			listed := make([]scnListedNamespace, len(out.Namespaces))
			for i, n := range out.Namespaces {
				listed[i] = scnListedNamespace{instanceID: instanceID, ns: n}
			}
			return listed, out.NextToken, nil
		},
		func(_ string, it scnListedNamespace) (*store.Resource, string, string) {
			name := sv(it.ns.Name)
			return arnRow("namespaces", it.ns.Arn, it.ns.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainNamespace, NativeID: sv(it.ns.Arn),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(it.ns), DiscoveredBy: scanID,
			})
		},
		func(it scnListedNamespace) string {
			if sv(it.ns.Name) == "" {
				return ""
			}
			return scnNamespaceParent(it.instanceID, sv(it.ns.Name))
		},
		tags, p)
}

// scanSCNDatasets stores the datasets of each instance's predefined
// namespaces and of each custom namespace stored, with their tags, each page
// written as soon as its tags are read (storeEachParentPagesTags); one
// namespace's failure costs the others none of theirs. A namespace or
// instance deleted since it was listed answers its modeled not-found and is
// skipped.
func scanSCNDatasets(ctx context.Context, client supplyChainAPI, acct *account, region string, st *store.Store, scanID string, instances, namespaces []childParent, tags rowTagReader, p *phaseTotals) bool {
	parents := make([]childParent, 0, len(instances)*len(scnPredefinedNamespaces)+len(namespaces))
	for _, inst := range instances {
		for _, ns := range scnPredefinedNamespaces {
			parents = append(parents, childParent{id: scnNamespaceParent(inst.id, ns)})
		}
	}
	parents = append(parents, namespaces...)
	return storeEachParentPagesTags(ctx, st, acct, region, scnListDatasetsOp, scnGone, parents,
		func(ctx context.Context, parent string, token *string) ([]scntypes.DataLakeDataset, *string, error) {
			instanceID, namespace, _ := strings.Cut(parent, "/")
			out, err := client.ListDataLakeDatasets(ctx, &supplychain.ListDataLakeDatasetsInput{
				InstanceId: &instanceID, Namespace: &namespace, MaxResults: sdkaws.Int32(scnPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Datasets, out.NextToken, nil
		},
		func(_ string, d scntypes.DataLakeDataset) (*store.Resource, string, string) {
			name := sv(d.Name)
			return arnRow("datasets", d.Arn, d.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupplyChainDataset, NativeID: sv(d.Arn),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		},
		tags, p)
}
