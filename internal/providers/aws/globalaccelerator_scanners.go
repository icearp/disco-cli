package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorAccelerator, Service: "global-accelerator", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorCrossAccountAttachment, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorListener, Service: "global-accelerator"})
	registerType(restype.Descriptor{Type: TypeGlobalAcceleratorEndpointGroup, Service: "global-accelerator"})
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
}

// scanGlobalAccelerator discovers Global Accelerator resources. The API is
// hosted only in us-west-2 (global=true, dispatched once per account,
// dispatcher passes region=""); substitute the canonical home to keep
// resource Region columns and per-op error scopes accurate.
func scanGlobalAccelerator(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-west-2"
	client := globalaccelerator.NewFromConfig(acct.cfg, func(o *globalaccelerator.Options) { o.Region = region })

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
