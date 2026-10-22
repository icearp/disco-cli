package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorAccelerator, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorCrossAccountAttachment, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorListener, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorEndpointGroup, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorCustomRoutingAccelerator, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorCustomRoutingListener, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorCustomRoutingEndpointGroup, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorByoipCidr, Service: "global-accelerator"})
	registerService(serviceEntry{
		name:   "aws:global-accelerator",
		global: true,
		fn:     scanGlobalAccelerator,
	})
}

type globalAcceleratorAPI interface {
	ListAccelerators(context.Context, *globalaccelerator.ListAcceleratorsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListAcceleratorsOutput, error)
	ListCrossAccountAttachments(context.Context, *globalaccelerator.ListCrossAccountAttachmentsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListCrossAccountAttachmentsOutput, error)
	ListListeners(context.Context, *globalaccelerator.ListListenersInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListListenersOutput, error)
	ListEndpointGroups(context.Context, *globalaccelerator.ListEndpointGroupsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListEndpointGroupsOutput, error)
	ListCustomRoutingAccelerators(context.Context, *globalaccelerator.ListCustomRoutingAcceleratorsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListCustomRoutingAcceleratorsOutput, error)
	ListCustomRoutingListeners(context.Context, *globalaccelerator.ListCustomRoutingListenersInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListCustomRoutingListenersOutput, error)
	ListCustomRoutingEndpointGroups(context.Context, *globalaccelerator.ListCustomRoutingEndpointGroupsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListCustomRoutingEndpointGroupsOutput, error)
	ListByoipCidrs(context.Context, *globalaccelerator.ListByoipCidrsInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListByoipCidrsOutput, error)
}

const (
	gaCustomRoutingAcceleratorsOp   = "globalaccelerator:ListCustomRoutingAccelerators"
	gaCustomRoutingListenersOp      = "globalaccelerator:ListCustomRoutingListeners"
	gaCustomRoutingEndpointGroupsOp = "globalaccelerator:ListCustomRoutingEndpointGroups"
	gaByoipCidrsOp                  = "globalaccelerator:ListByoipCidrs"
	// gaPageLimit is the model's MaxResults maximum for each custom-routing and BYOIP list op.
	gaPageLimit = 100
)

// scanGlobalAccelerator discovers Global Accelerator resources. The API is
// hosted only in us-west-2 (global=true, dispatched once per account,
// dispatcher passes region=""); substitute the canonical home to keep
// resource Region columns and per-op error scopes accurate.
//
// The older phases (standard accelerators through endpoint groups) stop at
// their first hard error. The custom-routing and BYOIP phases run whatever any
// other phase returned, so their failure costs no other phase its rows and an
// older phase's failure does not skip them. Each result has its transient error
// demoted to a warning only beside a hard error: joined, it would be buried in
// the hard error instead of reported under its op; the rest are joined and the
// counts summed.
func scanGlobalAccelerator(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region := "us-west-2"
	client := globalaccelerator.NewFromConfig(acct.cfg, func(o *globalaccelerator.Options) { o.Region = region })
	total, inserted, err := scanGAOlderPhases(ctx, client, acct, region, st, scanID)
	var errs phaseErrs
	errs.add("globalaccelerator", err)
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{gaCustomRoutingAcceleratorsOp, func() (int, int, error) { return scanGACustomRouting(ctx, client, acct, region, st, scanID) }},
		{gaByoipCidrsOp, func() (int, int, error) { return scanGAByoipCidrs(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs.add(phase.op, perr)
	}
	return total, inserted, errs.join(st, acct, region)
}

// scanGAOlderPhases runs the phases that predate custom routing and BYOIP,
// stopping at the first hard error.
func scanGAOlderPhases(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	accARNs, t, i, ferr := scanGAAccelerators(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanGACrossAccountAttachments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, aa := range accARNs {
		listenerARNs, tt, ii, lerr := scanGAListeners(ctx, client, acct, region, st, scanID, aa)
		if lerr != nil {
			return total, inserted, lerr
		}
		total += tt
		inserted += ii
		for _, la := range listenerARNs {
			t, i, ferr = scanGAEndpointGroups(ctx, client, acct, region, st, scanID, la)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanGAAccelerators(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := globalaccelerator.NewListAcceleratorsPaginator(client, &globalaccelerator.ListAcceleratorsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "globalaccelerator:ListAccelerators", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("globalaccelerator:ListAccelerators: %w", err)
		}
		for _, a := range out.Accelerators {
			arn := sv(a.AcceleratorArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorAccelerator, NativeID: arn,
				Name: a.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "globalaccelerator accelerators")
	return arns, t, i, err
}

func scanGACrossAccountAttachments(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := globalaccelerator.NewListCrossAccountAttachmentsPaginator(client, &globalaccelerator.ListCrossAccountAttachmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "globalaccelerator:ListCrossAccountAttachments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("globalaccelerator:ListCrossAccountAttachments: %w", err)
		}
		for _, a := range out.CrossAccountAttachments {
			arn := sv(a.AttachmentArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCrossAccountAttachment, NativeID: arn,
				Name: a.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "globalaccelerator cross-account-attachments")
}

func scanGAListeners(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, accARN string) ([]string, int, int, error) {
	aa := accARN
	pager := globalaccelerator.NewListListenersPaginator(client, &globalaccelerator.ListListenersInput{AcceleratorArn: &aa})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "globalaccelerator:ListListeners", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("globalaccelerator:ListListeners: %w", err)
		}
		for _, l := range out.Listeners {
			arn := sv(l.ListenerArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorListener, NativeID: arn,
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "globalaccelerator listeners")
	return arns, t, i, err
}

func scanGAEndpointGroups(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, listenerARN string) (int, int, error) {
	la := listenerARN
	pager := globalaccelerator.NewListEndpointGroupsPaginator(client, &globalaccelerator.ListEndpointGroupsInput{ListenerArn: &la})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "globalaccelerator:ListEndpointGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("globalaccelerator:ListEndpointGroups: %w", err)
		}
		for _, g := range out.EndpointGroups {
			arn := sv(g.EndpointGroupArn)
			if arn == "" {
				continue
			}
			label := sv(g.EndpointGroupRegion)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorEndpointGroup, NativeID: arn,
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "globalaccelerator endpoint-groups")
}

// scanGACustomRouting stores the custom routing accelerators, each one's
// listeners and each listener's endpoint groups, each child contained by its
// parent. A level that fails stops the levels below it, which have no parents.
func scanGACustomRouting(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	accelerators, total, inserted, err := scanGACustomRoutingAccelerators(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	listeners, t, i, err := scanGACustomRoutingListeners(ctx, client, acct, region, st, scanID, accelerators)
	total += t
	inserted += i
	if err != nil {
		return total, inserted, err
	}
	t, i, err = scanGACustomRoutingEndpointGroups(ctx, client, acct, region, st, scanID, listeners)
	return total + t, inserted + i, err
}

// scanGACustomRoutingAccelerators stores the custom routing accelerators and
// returns them as parents for the listener phase. A denial on any page warns
// and keeps the accelerators already listed.
func scanGACustomRoutingAccelerators(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := globalaccelerator.NewListCustomRoutingAcceleratorsPaginator(client, &globalaccelerator.ListCustomRoutingAcceleratorsInput{},
		func(o *globalaccelerator.ListCustomRoutingAcceleratorsPaginatorOptions) { o.Limit = gaPageLimit })
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, gaCustomRoutingAcceleratorsOp, acct.ID, region, err)
			break
		}
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%s: %w", gaCustomRoutingAcceleratorsOp, err)
		}
		for _, a := range out.Accelerators {
			arn := sv(a.AcceleratorArn)
			if arn == "" {
				continue
			}
			// Standard and custom-routing accelerators share this ARN shape. The
			// two are separate API families and the standard ops reject a
			// custom-routing ARN, so the listings are assumed disjoint; were they
			// not, UpsertResources' duplicate-NativeID detection would warn.
			parents = append(parents, childParent{id: arn, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingAccelerator, NativeID: arn,
				Name: a.Name, Region: regionGlobal, Status: nonEmptyPtr(string(a.Status)),
				CreatedAt: tp(a.CreatedTime), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "globalaccelerator custom-routing-accelerators")
	return parents, t, i, err
}

// scanGACustomRoutingListeners stores each accelerator's listeners and returns
// them as parents for the endpoint-group phase. An accelerator deleted since
// it was listed is skipped; one whose call still fails transiently after the
// SDK's retries is skipped, keeping its siblings' rows, with one summary
// warning.
func scanGACustomRoutingListeners(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, accelerators []childParent) ([]childParent, int, int, error) {
	var (
		transient transientTally
		mu        sync.Mutex
		listeners []childParent
	)
	isGone := func(err error) bool { return isAPIErrorCode(err, "AcceleratorNotFoundException") }
	total, inserted, err := childFanOut(ctx, st, acct, region, gaCustomRoutingListenersOp, accelerators, isGone,
		func(ctx context.Context, accelerator childParent) ([]*store.Resource, error) {
			rows, err := listGACustomRoutingListenerRows(ctx, client, acct, scanID, accelerator)
			if isTransientNetworkError(err) {
				transient.skipped(accelerator.arn, err)
				return nil, nil
			}
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				listeners = append(listeners, childParent{id: r.NativeID, arn: r.NativeID})
			}
			return rows, err
		})
	transient.warnSkipped(st, gaCustomRoutingListenersOp, acct, region)
	return listeners, total, inserted, err
}

func listGACustomRoutingListenerRows(ctx context.Context, client globalAcceleratorAPI, acct *account, scanID string, accelerator childParent) ([]*store.Resource, error) {
	pager := globalaccelerator.NewListCustomRoutingListenersPaginator(client,
		&globalaccelerator.ListCustomRoutingListenersInput{AcceleratorArn: &accelerator.arn},
		func(o *globalaccelerator.ListCustomRoutingListenersPaginatorOptions) { o.Limit = gaPageLimit })
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, l := range out.Listeners {
			arn := sv(l.ListenerArn)
			if arn == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingListener, NativeID: arn,
				Name: &arn, Region: regionGlobal,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// scanGACustomRoutingEndpointGroups stores each listener's endpoint groups. A
// listener deleted since it was listed is skipped; one whose call still fails
// transiently after the SDK's retries is skipped, keeping its siblings' rows,
// with one summary warning.
func scanGACustomRoutingEndpointGroups(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, listeners []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ListenerNotFoundException") }
	return childFanOutSkipTransient(ctx, st, acct, region, gaCustomRoutingEndpointGroupsOp, listeners, isGone, byParentARN,
		func(ctx context.Context, listener childParent) ([]*store.Resource, error) {
			return listGACustomRoutingEndpointGroupRows(ctx, client, acct, scanID, listener)
		})
}

func listGACustomRoutingEndpointGroupRows(ctx context.Context, client globalAcceleratorAPI, acct *account, scanID string, listener childParent) ([]*store.Resource, error) {
	pager := globalaccelerator.NewListCustomRoutingEndpointGroupsPaginator(client,
		&globalaccelerator.ListCustomRoutingEndpointGroupsInput{ListenerArn: &listener.arn},
		func(o *globalaccelerator.ListCustomRoutingEndpointGroupsPaginatorOptions) { o.Limit = gaPageLimit })
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range out.EndpointGroups {
			arn := sv(g.EndpointGroupArn)
			if arn == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingEndpointGroup, NativeID: arn,
				Name: nonEmptyPtr(sv(g.EndpointGroupRegion)), Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// gaByoipCidrNativeID builds the NativeID of an address range provisioned to
// Global Accelerator. The API issues no ARN or id for it, and a bare CIDR is
// not a namespaced identity (EC2 BYOIP and IPAM ranges use the same strings),
// so the id is ARN-shaped in the globalaccelerator namespace.
func gaByoipCidrNativeID(accountID, cidr string) string {
	return "arn:aws:globalaccelerator::" + accountID + ":byoip-cidr/" + cidr
}

// scanGAByoipCidrs stores the address ranges provisioned to Global Accelerator.
// The listing keeps a DEPROVISIONED range, which no longer exists in Global
// Accelerator, so it is not stored. A denial on any page warns and keeps the
// ranges already listed.
func scanGAByoipCidrs(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := globalaccelerator.NewListByoipCidrsPaginator(client, &globalaccelerator.ListByoipCidrsInput{},
		func(o *globalaccelerator.ListByoipCidrsPaginatorOptions) { o.Limit = gaPageLimit })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, gaByoipCidrsOp, acct.ID, region, err)
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", gaByoipCidrsOp, err)
		}
		for _, c := range out.ByoipCidrs {
			cidr := sv(c.Cidr)
			if cidr == "" || c.State == gatypes.ByoipCidrStateDeprovisioned {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorByoipCidr, NativeID: gaByoipCidrNativeID(acct.ID, cidr),
				Name: &cidr, Region: regionGlobal, Status: nonEmptyPtr(string(c.State)),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "globalaccelerator byoip-cidrs")
}
