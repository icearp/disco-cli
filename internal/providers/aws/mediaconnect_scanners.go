package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/mediaconnect"
)

func init() {
	registerService(serviceEntry{
		name: "aws:mediaconnect",
		fn:   scanMediaConnect,
		emits: []coverage.TypeDecl{
			{Service: "mediaconnect", DiscoType: TypeMediaConnectBridge},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectBridgeOutput},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectBridgeSource},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectFlow, Leaf: true},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectFlowEntitlement},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectFlowOutput},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectFlowSource},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectFlowVpcInterface},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectGateway, Leaf: true},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectRouterInput, Leaf: true},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectRouterNetworkInterface, Leaf: true},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectRouterOutput, Leaf: true},
			{Service: "mediaconnect", DiscoType: TypeMediaConnectReservation, Leaf: true},
		},
	})
}

type mediaConnectAPI interface {
	ListBridges(context.Context, *mediaconnect.ListBridgesInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListBridgesOutput, error)
	DescribeBridge(context.Context, *mediaconnect.DescribeBridgeInput, ...func(*mediaconnect.Options)) (*mediaconnect.DescribeBridgeOutput, error)
	ListFlows(context.Context, *mediaconnect.ListFlowsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListFlowsOutput, error)
	DescribeFlow(context.Context, *mediaconnect.DescribeFlowInput, ...func(*mediaconnect.Options)) (*mediaconnect.DescribeFlowOutput, error)
	ListGateways(context.Context, *mediaconnect.ListGatewaysInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListGatewaysOutput, error)
	ListRouterInputs(context.Context, *mediaconnect.ListRouterInputsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterInputsOutput, error)
	ListRouterNetworkInterfaces(context.Context, *mediaconnect.ListRouterNetworkInterfacesInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterNetworkInterfacesOutput, error)
	ListRouterOutputs(context.Context, *mediaconnect.ListRouterOutputsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListRouterOutputsOutput, error)
	ListReservations(context.Context, *mediaconnect.ListReservationsInput, ...func(*mediaconnect.Options)) (*mediaconnect.ListReservationsOutput, error)
}

func scanMediaConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediaconnect.NewFromConfig(acct.cfg, func(o *mediaconnect.Options) { o.Region = region })

	bridgeArns, t, i, ferr := scanMCBridges(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	flowArns, t, i, ferr := scanMCFlows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanMCBridgeChildren(ctx, client, acct, region, st, scanID, bridgeArns)
		},
		func() (int, int, error) { return scanMCFlowChildren(ctx, client, acct, region, st, scanID, flowArns) },
		func() (int, int, error) { return scanMCGateways(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCRouterInputs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCRouterNetworkInterfaces(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCRouterOutputs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCReservations(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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

func scanMCFlows(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediaconnect.NewListFlowsPaginator(client, &mediaconnect.ListFlowsInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListFlows", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("mediaconnect:ListFlows: %w", perr)
		}
		for _, f := range out.Flows {
			arn := sv(f.FlowArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(f.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediaconnect flows")
	return arns, t, i, err
}

// scanMCFlowChildren — DescribeFlow per flow, embedded Entitlements,
// Outputs, Sources, VpcInterfaces each emit own row.
func scanMCFlowChildren(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string, flowArns []string) (int, int, error) {
	if len(flowArns) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, fa := range flowArns {
		arn := fa
		out, err := client.DescribeFlow(ctx, &mediaconnect.DescribeFlowInput{FlowArn: &arn})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("mediaconnect:DescribeFlow %s: %w", fa, err)
		}
		if out.Flow == nil {
			continue
		}
		for _, e := range out.Flow.Entitlements {
			ea := sv(e.EntitlementArn)
			if ea == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = ea
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlowEntitlement, NativeID: ea,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		for _, o := range out.Flow.Outputs {
			oa := sv(o.OutputArn)
			if oa == "" {
				continue
			}
			label := sv(o.Name)
			if label == "" {
				label = oa
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlowOutput, NativeID: oa,
				Name: &label, Region: &region, AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
		for _, s := range out.Flow.Sources {
			sa := sv(s.SourceArn)
			if sa == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sa
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlowSource, NativeID: sa,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		for _, v := range out.Flow.VpcInterfaces {
			vname := sv(v.Name)
			if vname == "" {
				continue
			}
			vArn := arn + "/vpc-interface/" + vname
			label := vname
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectFlowVpcInterface, NativeID: vArn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconnect flow-children")
}

func scanMCGateways(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediaconnect.NewListGatewaysPaginator(client, &mediaconnect.ListGatewaysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "mediaconnect:ListGateways", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListGateways: %w", perr)
		}
		for _, g := range out.Gateways {
			arn := sv(g.GatewayArn)
			if arn == "" {
				continue
			}
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
	return upsertBatch(st, batch, "mediaconnect gateways")
}

func scanMCRouterInputs(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListRouterInputs(ctx, &mediaconnect.ListRouterInputsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconnect:ListRouterInputs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListRouterInputs: %w", err)
		}
		for _, r := range out.RouterInputs {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterInput, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "mediaconnect router-inputs")
}

func scanMCRouterNetworkInterfaces(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListRouterNetworkInterfaces(ctx, &mediaconnect.ListRouterNetworkInterfacesInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconnect:ListRouterNetworkInterfaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListRouterNetworkInterfaces: %w", err)
		}
		for _, r := range out.RouterNetworkInterfaces {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = sv(r.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterNetworkInterface, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "mediaconnect router-network-interfaces")
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

func scanMCRouterOutputs(ctx context.Context, client mediaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListRouterOutputs(ctx, &mediaconnect.ListRouterOutputsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconnect:ListRouterOutputs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconnect:ListRouterOutputs: %w", err)
		}
		for _, r := range out.RouterOutputs {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConnectRouterOutput, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "mediaconnect router-outputs")
}
