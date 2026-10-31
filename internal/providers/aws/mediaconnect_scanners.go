package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediaconnect"
	mctypes "github.com/aws/aws-sdk-go-v2/service/mediaconnect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaConnectBridge, Service: "mediaconnect", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMediaConnectBridgeOutput, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectBridgeSource, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectFlow, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectFlowEntitlement, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectFlowOutput, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectFlowSource, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectFlowVpcInterface, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectGateway, Service: "mediaconnect", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMediaConnectGatewayInstance, Service: "mediaconnect", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMediaConnectRouterInput, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectRouterNetworkInterface, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectRouterOutput, Service: "mediaconnect"})
	registerType(restype.Descriptor{Type: TypeMediaConnectReservation, Service: "mediaconnect", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:mediaconnect",
		fn:   scanMediaConnect,
	})
}

type mediaConnectAPI interface {
	ListBridges(context.Context, *mediaconnect.ListBridgesInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListBridgesOutput, error)
	DescribeBridge(context.Context, *mediaconnect.DescribeBridgeInput, ...func(*mediaconnect.Options)) (*mediaconnect.DescribeBridgeOutput, error)
	ListFlows(context.Context, *mediaconnect.ListFlowsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListFlowsOutput, error)
	DescribeFlow(context.Context, *mediaconnect.DescribeFlowInput, ...func(*mediaconnect.Options)) (*mediaconnect.DescribeFlowOutput, error)
	ListGateways(context.Context, *mediaconnect.ListGatewaysInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListGatewaysOutput, error)
	ListGatewayInstances(context.Context, *mediaconnect.ListGatewayInstancesInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListGatewayInstancesOutput, error)
	ListRouterInputs(context.Context, *mediaconnect.ListRouterInputsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterInputsOutput, error)
	ListRouterNetworkInterfaces(context.Context, *mediaconnect.ListRouterNetworkInterfacesInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterNetworkInterfacesOutput, error)
	ListRouterOutputs(context.Context, *mediaconnect.ListRouterOutputsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterOutputsOutput, error)
	ListReservations(context.Context, *mediaconnect.ListReservationsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListReservationsOutput, error)
	ListTagsForResource(context.Context, *mediaconnect.ListTagsForResourceInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListTagsForResourceOutput, error)
	ListTagsForGlobalResource(context.Context, *mediaconnect.ListTagsForGlobalResourceInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListTagsForGlobalResourceOutput, error)
}

const (
	mcListBridgesOp                 = "mediaconnect:ListBridges"
	mcDescribeBridgeOp              = "mediaconnect:DescribeBridge"
	mcListFlowsOp                   = "mediaconnect:ListFlows"
	mcDescribeFlowOp                = "mediaconnect:DescribeFlow"
	mcListGatewaysOp                = "mediaconnect:ListGateways"
	mcListRouterInputsOp            = "mediaconnect:ListRouterInputs"
	mcListRouterNetworkInterfacesOp = "mediaconnect:ListRouterNetworkInterfaces"
	mcListRouterOutputsOp           = "mediaconnect:ListRouterOutputs"
	mcListReservationsOp            = "mediaconnect:ListReservations"
	mcListGatewayInstancesOp        = "mediaconnect:ListGatewayInstances"
	mcTagsOp                        = "mediaconnect:ListTagsForResource"
	mcGlobalTagsOp                  = "mediaconnect:ListTagsForGlobalResource"
	// mcFlowPageCap is ListFlows' MaxResults cap (1-1000).
	mcFlowPageCap = 1000
	// mcRouterPageCap is the MaxResults cap (1-100) of the three router
	// listings.
	mcRouterPageCap = 100
)

// mcNotFound is the not-found code DescribeFlow and both tag ops model: a
// resource deleted after it was listed.
var mcNotFound = []string{"NotFoundException"}

// errMCNoFlowBody fails the read of a flow whose DescribeFlow answer carried
// no flow.
var errMCNoFlowBody = errors.New("DescribeFlow answered without a flow, so its entitlements, outputs, sources and VPC interfaces were not read; rescan, and report it if it recurs")

// errMCVpcInterfaceNotFound is the tag read of a VPC interface that
// ListTagsForResource reports not found. The interface was in the flow's
// DescribeFlow answer moments before, and the ARN sent for it is built (the
// API returns none), so the answer may mean that ARN is not one MediaConnect
// accepts rather than that the interface is gone. The row is kept untagged
// and the read is returned as an error instead of dropping the row.
var errMCVpcInterfaceNotFound = errors.New("MediaConnect did not find the VPC interface by the ARN its tags were read with, so it was stored without tags; it may have been removed during the scan: rescan, and report it if it recurs")

func scanMediaConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := mediaconnect.NewFromConfig(acct.cfg, func(o *mediaconnect.Options) { o.Region = region })
	return scanMediaConnectAll(ctx, client, acct, region, st, scanID)
}

// scanMediaConnectAll runs every listing as its own phase, one at a time and
// in this order: bridges, flows, each bridge's outputs and sources, each
// flow's entitlements, outputs, sources and VPC interfaces, gateways, router
// inputs, router network interfaces, router outputs, reservations, gateway
// instances. A phase's error does not stop the later ones; a failed write and
// ctx's end do, and so does a bridge phase that stores nothing and fails only
// with NXDOMAIN, since the region does not serve the service (runHostPhases).
// Flows and their children have their tags read with ListTagsForResource,
// the router resources with ListTagsForGlobalResource; each op's denials are
// one warning for the whole scan. Bridges and their outputs and sources,
// gateways, gateway instances and reservations have no tags read: the Service
// Reference lists no way to tag them.
func scanMediaConnectAll(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := mcTags(client).sharingDenials()
	globalTags := mcGlobalTags(client).sharingDenials()
	var (
		bridgeArns, gatewayArns []string
		flows                   []childParent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "mediaconnect", 0, []hostPhase{
		{op: mcListBridgesOp, run: func(p *phaseTotals) bool {
			var t, i int
			var err error
			bridgeArns, t, i, err = scanMCBridges(ctx, client, acct, region, st, scanID)
			return p.addPhase(mcListBridgesOp, t, i, err)
		}},
		{op: mcListFlowsOp, run: func(p *phaseTotals) bool {
			var failed bool
			flows, failed = scanMCFlows(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, mcDescribeBridgeOp, func() (int, int, error) {
			return scanMCBridgeChildren(ctx, client, acct, region, st, scanID, bridgeArns)
		}),
		{op: mcDescribeFlowOp, run: func(p *phaseTotals) bool {
			return scanMCFlowChildren(ctx, client, acct, region, st, scanID, flows, tags, p)
		}},
		{op: mcListGatewaysOp, run: func(p *phaseTotals) bool {
			var t, i int
			var err error
			gatewayArns, t, i, err = scanMCGateways(ctx, client, acct, region, st, scanID)
			return p.addPhase(mcListGatewaysOp, t, i, err)
		}},
		{op: mcListRouterInputsOp, run: func(p *phaseTotals) bool {
			return scanMCRouterInputs(ctx, client, acct, region, st, scanID, globalTags, p)
		}},
		{op: mcListRouterNetworkInterfacesOp, run: func(p *phaseTotals) bool {
			return scanMCRouterNetworkInterfaces(ctx, client, acct, region, st, scanID, globalTags, p)
		}},
		{op: mcListRouterOutputsOp, run: func(p *phaseTotals) bool {
			return scanMCRouterOutputs(ctx, client, acct, region, st, scanID, globalTags, p)
		}},
		countedPhase(0, mcListReservationsOp, func() (int, int, error) {
			return scanMCReservations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, mcListGatewayInstancesOp, func() (int, int, error) {
			return scanMCGatewayInstances(ctx, client, acct, region, st, scanID, gatewayArns)
		}),
	})
	tags.warnDenials(st, acct, region)
	globalTags.warnDenials(st, acct, region)
	return total, inserted, err
}

// mcTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing or its flow's DescribeFlow returned. The
// Service Reference's TagResource covers flows, entitlements, outputs,
// sources and VPC interfaces with tag condition keys (its
// ListTagsForResource entry names no resource at all), and the inputs that
// create each take tags. The op returns every tag at once. NotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
func mcTags(client mediaConnectAPI) rowTagReader {
	return rowTagReader{
		op:       mcTagsOp,
		notFound: mcNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			return mcListTags(ctx, client, r.NativeID)
		},
	}
}

func mcListTags(ctx context.Context, client mediaConnectAPI, arn string) (*string, error) {
	out, err := client.ListTagsForResource(ctx, &mediaconnect.ListTagsForResourceInput{ResourceArn: &arn})
	if err != nil {
		return nil, err
	}
	return mapTagsJSON(out.Tags), nil
}

// mcGlobalTags reads a router input's, router network interface's or router
// output's tags with ListTagsForGlobalResource, sent the row's NativeID, the
// ARN its listing returned: the SDK documents ListTagsForGlobalResource for
// these three, which it calls global resources. The Service Reference covers
// them under TagResource with tag condition keys, and their Create inputs
// take tags. NotFoundException, its modeled not-found, drops a resource
// deleted after it was listed.
//
// It reads one row per call although BatchGetRouterInput, -NetworkInterface
// and -Output return Tags for up to 20 ARNs: those ops model no
// NotFoundException, and they report each ARN they could not read in Errors
// under a Code the model leaves an unlisted string. A router resource deleted
// after it was listed could then not be told from any other failure, so it
// would be kept without tags and fail the scan instead of being dropped.
func mcGlobalTags(client mediaConnectAPI) rowTagReader {
	return rowTagReader{
		op:       mcGlobalTagsOp,
		notFound: mcNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForGlobalResource(ctx, &mediaconnect.ListTagsForGlobalResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

func scanMCBridges(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediaconnect.NewListBridgesPaginator(client, &mediaconnect.ListBridgesInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListBridges", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("mediaconnect:ListBridges: %w", perr)
		}
		for _, b := range out.Bridges {
			arn := sv(b.BridgeArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(b.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectBridge, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediaconnect bridges")
	return arns, t, i, err
}

// scanMCBridgeChildren — DescribeBridge per bridge, embedded Outputs +
// Sources arrays each emit own row. Output/Source variants are
// tagged-union (Flow / Network); resolve name from non-nil branch.
func scanMCBridgeChildren(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, bridgeArns []string) (int, int, error) {
	if len(bridgeArns) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, ba := range bridgeArns {
		arn := ba
		out, err := client.DescribeBridge(ctx, &mediaconnect.DescribeBridgeInput{BridgeArn: &arn})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("mediaconnect:DescribeBridge %s: %w", ba, err)
		}
		if out.Bridge == nil {
			continue
		}
		for _, o := range out.Bridge.Outputs {
			name := ""
			switch {
			case o.FlowOutput != nil:
				name = sv(o.FlowOutput.Name)
			case o.NetworkOutput != nil:
				name = sv(o.NetworkOutput.Name)
			}
			if name == "" {
				continue
			}
			outArn := arn + "/output/" + name
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectBridgeOutput, NativeID: outArn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
		for _, s := range out.Bridge.Sources {
			name := ""
			switch {
			case s.FlowSource != nil:
				name = sv(s.FlowSource.Name)
			case s.NetworkSource != nil:
				name = sv(s.NetworkSource.Name)
			}
			if name == "" {
				continue
			}
			srcArn := arn + "/source/" + name
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectBridgeSource, NativeID: srcArn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconnect bridge-children")
}

// scanMCFlows stores the flows, each page written as soon as its rows' tags
// are read, and returns those stored, keyed by the ARN DescribeFlow takes. It
// reports whether a write failed, after which the scan must stop.
func scanMCFlows(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, mcListFlowsOp, "flows", "FlowArn",
		func(ctx context.Context, token *string) ([]mctypes.ListedFlow, *string, error) {
			out, err := client.ListFlows(ctx, &mediaconnect.ListFlowsInput{MaxResults: sdkaws.Int32(mcFlowPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Flows, out.NextToken, nil
		},
		func(f mctypes.ListedFlow) (*store.Resource, string, string) {
			label := cmp.Or(sv(f.Name), sv(f.FlowArn))
			return arnRow("flows", f.FlowArn, f.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlow, NativeID: sv(f.FlowArn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		},
		func(f mctypes.ListedFlow) string { return sv(f.FlowArn) },
		tags, p)
}

// mcFlowChild is one entitlement, output, source or VPC interface of a
// described flow: exactly one field is set.
type mcFlowChild struct {
	entitlement  *mctypes.Entitlement
	output       *mctypes.Output
	source       *mctypes.Source
	vpcInterface *mctypes.VpcInterface
}

// mcFlowChildren is every child of flow. The flow's Source is added unless
// Sources already holds its SourceArn: Sources documents the sources assigned
// to the flow without saying it holds the one Source names, so a flow without
// failover may list its only source in Source alone.
func mcFlowChildren(flow *mctypes.Flow) []mcFlowChild {
	var children []mcFlowChild
	for i := range flow.Entitlements {
		children = append(children, mcFlowChild{entitlement: &flow.Entitlements[i]})
	}
	for i := range flow.Outputs {
		children = append(children, mcFlowChild{output: &flow.Outputs[i]})
	}
	inSources := map[string]bool{}
	for i := range flow.Sources {
		inSources[sv(flow.Sources[i].SourceArn)] = true
		children = append(children, mcFlowChild{source: &flow.Sources[i]})
	}
	if flow.Source != nil && !inSources[sv(flow.Source.SourceArn)] {
		children = append(children, mcFlowChild{source: flow.Source})
	}
	for i := range flow.VpcInterfaces {
		children = append(children, mcFlowChild{vpcInterface: &flow.VpcInterfaces[i]})
	}
	return children
}

// mcFlowVpcInterfaceARN is the Service Reference's ARN of a flow's VPC
// interface (arn:${Partition}:mediaconnect:${Region}:${Account}:flow:${FlowId}:${FlowName}/vpcInterface/${VpcInterfaceName}),
// built on the flow ARN DescribeFlow was sent. It is what the tags are read
// with; the row keeps its NativeID {flowARN}/vpc-interface/{name}.
func mcFlowVpcInterfaceARN(flowARN, name string) string {
	return flowARN + "/vpcInterface/" + name
}

// scanMCFlowChildren describes each stored flow (DescribeFlow, the flows read
// concurrently) and stores every entitlement, output, source and VPC
// interface the answer holds, each with its tags read. A flow DescribeFlow no
// longer finds (NotFoundException) was deleted since it was listed and is
// skipped silently; a denied flow warns once; a flow answered without a body
// and any other error are returned after the other flows' children are
// stored. A child without an ARN (a VPC interface: without a name) is warned
// about, not stored. It reports whether the write failed, after which the
// scan must stop.
func scanMCFlowChildren(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, flows []childParent, tags rowTagReader, p *phaseTotals) bool {
	// Written by the row builder, which runs in turn before the tag reads,
	// and only read by those.
	vpcTagARNs := map[*store.Resource]string{}
	childTags := tags
	childTags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if r.Type != TypeMediaConnectFlowVpcInterface {
			return tags.read(ctx, r)
		}
		arn := vpcTagARNs[r]
		got, err := mcListTags(ctx, client, arn)
		if isAPIErrorCode(err, mcNotFound...) {
			// %v: the not-found code must not reach readRowTags, which would
			// drop the row.
			return nil, fmt.Errorf("%s: %w: %v", arn, errMCVpcInterfaceNotFound, err)
		}
		return got, err
	}
	row := func(flowARN string, c mcFlowChild) (*store.Resource, string, string) {
		build := func(typ, nativeID, name string, attrs any) *store.Resource {
			label := cmp.Or(name, nativeID)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: typ, NativeID: nativeID,
				Name: &label, Region: &region, AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			}
		}
		switch {
		case c.entitlement != nil:
			e := c.entitlement
			return arnRow("flow entitlements", e.EntitlementArn, e.Name, build(TypeMediaConnectFlowEntitlement, sv(e.EntitlementArn), sv(e.Name), e))
		case c.output != nil:
			o := c.output
			return arnRow("flow outputs", o.OutputArn, o.Name, build(TypeMediaConnectFlowOutput, sv(o.OutputArn), sv(o.Name), o))
		case c.source != nil:
			s := c.source
			return arnRow("flow sources", s.SourceArn, s.Name, build(TypeMediaConnectFlowSource, sv(s.SourceArn), sv(s.Name), s))
		}
		v := c.vpcInterface
		name := sv(v.Name)
		if name == "" {
			return nil, "flow VPC interfaces have no Name", "a VPC interface of " + flowARN
		}
		r := build(TypeMediaConnectFlowVpcInterface, flowARN+"/vpc-interface/"+name, name, v)
		vpcTagARNs[r] = mcFlowVpcInterfaceARN(flowARN, name)
		return r, "", ""
	}
	_, failed := storeEachParentTags(ctx, st, acct, region, mcDescribeFlowOp, mcNotFound, flows,
		func(ctx context.Context, flowARN string, _ *string) ([]mcFlowChild, *string, error) {
			out, err := client.DescribeFlow(ctx, &mediaconnect.DescribeFlowInput{FlowArn: &flowARN})
			if err != nil {
				return nil, nil, err
			}
			if out.Flow == nil {
				return nil, nil, errMCNoFlowBody
			}
			return mcFlowChildren(out.Flow), nil, nil
		},
		row, childTags, p)
	return failed
}

func scanMCGateways(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediaconnect.NewListGatewaysPaginator(client, &mediaconnect.ListGatewaysInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListGateways", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("mediaconnect:ListGateways: %w", perr)
		}
		for _, g := range out.Gateways {
			arn := sv(g.GatewayArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(g.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectGateway, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediaconnect gateways")
	return arns, t, i, err
}

// scanMCGatewayInstances lists every gateway instance in the region with one
// unfiltered call (FilterArn is optional) instead of one call per gateway. The
// contains edge is recorded only for gateways stored by this scan, so a denied
// ListGateways does not raise a missing-parent warning per instance.
func scanMCGatewayInstances(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, gatewayArns []string) (int, int, error) {
	listed := make(map[string]bool, len(gatewayArns))
	for _, a := range gatewayArns {
		listed[a] = true
	}
	pager := mediaconnect.NewListGatewayInstancesPaginator(client, &mediaconnect.ListGatewayInstancesInput{})
	var batch []*store.Resource
	var pairs [][2]string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListGatewayInstances", acct.ID, region, perr)
				break
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListGatewayInstances: %w", perr)
		}
		for _, gi := range out.Instances {
			arn := sv(gi.GatewayInstanceArn)
			if arn == "" {
				continue
			}
			label := sv(gi.InstanceId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectGatewayInstance, NativeID: arn,
				Name: &label, Region: &region, Status: nonEmptyPtr(string(gi.InstanceState)),
				AttributesJSON: mustJSON(gi), DiscoveredBy: scanID,
			})
			if gw := sv(gi.GatewayArn); listed[gw] {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, arn), store.ResourceID("aws", acct.ID, gw)})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "mediaconnect gateway-instances")
	if err != nil {
		return 0, 0, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("record gateway→gateway-instance hierarchy: %w", err)
	}
	return t, i, nil
}

// scanMCRouterInputs stores the router inputs, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanMCRouterInputs(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcListRouterInputsOp,
		func(ctx context.Context, token *string) ([]mctypes.ListedRouterInput, *string, error) {
			out, err := client.ListRouterInputs(ctx, &mediaconnect.ListRouterInputsInput{MaxResults: sdkaws.Int32(mcRouterPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RouterInputs, out.NextToken, nil
		},
		func(r mctypes.ListedRouterInput) (*store.Resource, string, string) {
			label := sv(r.Id)
			return arnRow("router inputs", r.Arn, r.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterInput, NativeID: sv(r.Arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanMCRouterNetworkInterfaces stores the router network interfaces, each
// page written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanMCRouterNetworkInterfaces(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcListRouterNetworkInterfacesOp,
		func(ctx context.Context, token *string) ([]mctypes.ListedRouterNetworkInterface, *string, error) {
			out, err := client.ListRouterNetworkInterfaces(ctx, &mediaconnect.ListRouterNetworkInterfacesInput{MaxResults: sdkaws.Int32(mcRouterPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RouterNetworkInterfaces, out.NextToken, nil
		},
		func(r mctypes.ListedRouterNetworkInterface) (*store.Resource, string, string) {
			label := cmp.Or(sv(r.Name), sv(r.Id))
			return arnRow("router network interfaces", r.Arn, r.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterNetworkInterface, NativeID: sv(r.Arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

func scanMCReservations(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediaconnect.NewListReservationsPaginator(client, &mediaconnect.ListReservationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListReservations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListReservations: %w", perr)
		}
		for _, r := range out.Reservations {
			arn := sv(r.ReservationArn)
			if arn == "" {
				continue
			}
			label := sv(r.ReservationName)
			if label == "" {
				label = arn
			}
			status := string(r.ReservationState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectReservation, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconnect reservations")
}

// scanMCRouterOutputs stores the router outputs, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanMCRouterOutputs(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcListRouterOutputsOp,
		func(ctx context.Context, token *string) ([]mctypes.ListedRouterOutput, *string, error) {
			out, err := client.ListRouterOutputs(ctx, &mediaconnect.ListRouterOutputsInput{MaxResults: sdkaws.Int32(mcRouterPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RouterOutputs, out.NextToken, nil
		},
		func(r mctypes.ListedRouterOutput) (*store.Resource, string, string) {
			label := sv(r.Id)
			return arnRow("router outputs", r.Arn, r.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterOutput, NativeID: sv(r.Arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		tags, p)
}
