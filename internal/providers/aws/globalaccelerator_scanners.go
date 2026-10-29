package aws

import (
	"cmp"
	"context"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *globalaccelerator.ListTagsForResourceInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListTagsForResourceOutput, error)
}

const (
	gaAcceleratorsOp                = "globalaccelerator:ListAccelerators"
	gaCrossAccountAttachmentsOp     = "globalaccelerator:ListCrossAccountAttachments"
	gaListenersOp                   = "globalaccelerator:ListListeners"
	gaEndpointGroupsOp              = "globalaccelerator:ListEndpointGroups"
	gaCustomRoutingAcceleratorsOp   = "globalaccelerator:ListCustomRoutingAccelerators"
	gaCustomRoutingListenersOp      = "globalaccelerator:ListCustomRoutingListeners"
	gaCustomRoutingEndpointGroupsOp = "globalaccelerator:ListCustomRoutingEndpointGroups"
	gaByoipCidrsOp                  = "globalaccelerator:ListByoipCidrs"
	gaTagsOp                        = "globalaccelerator:ListTagsForResource"
	// gaPageLimit is the model's MaxResults maximum for every listing read here.
	gaPageLimit = 100
)

var (
	// gaAcceleratorGone is the code ListTagsForResource, ListListeners and
	// ListCustomRoutingListeners model for an accelerator that does not exist.
	gaAcceleratorGone = []string{"AcceleratorNotFoundException"}
	// gaListenerGone is the code ListEndpointGroups and
	// ListCustomRoutingEndpointGroups model for a listener that does not exist.
	gaListenerGone = []string{"ListenerNotFoundException"}
)

// scanGlobalAccelerator discovers Global Accelerator resources. For aws
// accounts the API is called in us-west-2 (global=true, dispatched once per account,
// dispatcher passes region=""); substitute the canonical home to keep
// resource Region columns and per-op error scopes accurate.
//
// Every listing runs as its own phase, one at a time, in the order below: a
// phase's error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The child phases read the parents stored this scan, those
// parents concurrently. The accelerators of both kinds and the cross-account
// attachments have their tags read per row (gaTags), and the tag denials of
// the whole scan are one warning.
func scanGlobalAccelerator(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("globalaccelerator")
	if err != nil {
		return 0, 0, err
	}
	client := globalaccelerator.NewFromConfig(acct.cfg, func(o *globalaccelerator.Options) { o.Region = region })
	tags := gaTags(client).sharingDenials()
	var accelerators, crAccelerators, crListeners []childParent
	var listeners []string
	total, inserted, err := runHostPhases(ctx, st, acct, region, "globalaccelerator", 0, []hostPhase{
		{op: gaAcceleratorsOp, run: func(p *phaseTotals) (failed bool) {
			accelerators, failed = scanGAAccelerators(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: gaCrossAccountAttachmentsOp, run: func(p *phaseTotals) bool {
			return scanGACrossAccountAttachments(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: gaListenersOp, run: func(p *phaseTotals) (failed bool) {
			listeners, failed = scanGAListeners(ctx, client, acct, region, st, scanID, accelerators, p)
			return failed
		}},
		countedPhase(0, gaEndpointGroupsOp, func() (int, int, error) {
			return scanGAEndpointGroups(ctx, client, acct, region, st, scanID, listeners)
		}),
		{op: gaCustomRoutingAcceleratorsOp, run: func(p *phaseTotals) (failed bool) {
			crAccelerators, failed = scanGACustomRoutingAccelerators(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: gaCustomRoutingListenersOp, run: func(p *phaseTotals) (failed bool) {
			crListeners, failed = scanGACustomRoutingListeners(ctx, client, acct, region, st, scanID, crAccelerators, p)
			return failed
		}},
		countedPhase(0, gaCustomRoutingEndpointGroupsOp, func() (int, int, error) {
			return scanGACustomRoutingEndpointGroups(ctx, client, acct, region, st, scanID, crListeners)
		}),
		countedPhase(0, gaByoipCidrsOp, func() (int, int, error) {
			return scanGAByoipCidrs(ctx, client, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// gaTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The op returns every tag in one
// response (it takes no page token). Each phase sets notFound to the code the
// op models for its type.
//
// The Service Reference lists accelerator and attachment under
// ListTagsForResource. A custom routing accelerator has the accelerator ARN
// format and CreateCustomRoutingAccelerator takes Tags, so it is read too
// (unverified live). Listeners and endpoint groups of either kind are not
// read: the Service Reference lists them under neither ListTagsForResource
// nor TagResource, and their Create inputs take no Tags. A BYOIP range has
// no ARN.
func gaTags(client globalAcceleratorAPI) rowTagReader {
	return rowTagReader{
		op: gaTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &globalaccelerator.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t gatypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanGAAccelerators stores the standard accelerators with their tags and
// returns the stored ones, the parents of the listener phase. Each page is
// written as soon as its tags are read.
func scanGAAccelerators(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	tags.notFound = gaAcceleratorGone
	return storeParentPagesTags(ctx, st, acct, region, gaAcceleratorsOp, "accelerators", "AcceleratorArn",
		func(ctx context.Context, token *string) ([]gatypes.Accelerator, *string, error) {
			out, err := client.ListAccelerators(ctx, &globalaccelerator.ListAcceleratorsInput{MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Accelerators, out.NextToken, nil
		},
		func(a gatypes.Accelerator) (*store.Resource, string, string) {
			return arnRow("accelerators", a.AcceleratorArn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorAccelerator, NativeID: sv(a.AcceleratorArn),
				Name: a.Name, Region: regionGlobal, Status: nonEmptyPtr(string(a.Status)),
				CreatedAt: tp(a.CreatedTime), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		func(a gatypes.Accelerator) string { return sv(a.AcceleratorArn) },
		tags, p)
}

// scanGACrossAccountAttachments stores the cross-account attachments this
// account created, with their tags. Each page is written as soon as its tags
// are read.
func scanGACrossAccountAttachments(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"AttachmentNotFoundException"}
	return storeTokenPagesTags(ctx, st, acct, region, gaCrossAccountAttachmentsOp,
		func(ctx context.Context, token *string) ([]gatypes.Attachment, *string, error) {
			out, err := client.ListCrossAccountAttachments(ctx, &globalaccelerator.ListCrossAccountAttachmentsInput{MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CrossAccountAttachments, out.NextToken, nil
		},
		func(a gatypes.Attachment) (*store.Resource, string, string) {
			return arnRow("cross-account attachments", a.AttachmentArn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCrossAccountAttachment, NativeID: sv(a.AttachmentArn),
				Name: a.Name, Region: regionGlobal,
				CreatedAt: tp(a.CreatedTime), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanGAListeners stores each accelerator's listeners and returns the ARNs
// of those built, the parents of the endpoint-group phase. One accelerator's
// failure costs the others none of theirs (storeTokenEachParent). It reports
// whether the write failed, after which the scan must stop.
func scanGAListeners(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, accelerators []childParent, p *phaseTotals) ([]string, bool) {
	var listeners []string
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, gaListenersOp, gaAcceleratorGone, childParentIDs(accelerators),
		func(ctx context.Context, accelerator string, token *string) ([]gatypes.Listener, *string, error) {
			out, err := client.ListListeners(ctx, &globalaccelerator.ListListenersInput{AcceleratorArn: &accelerator, MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Listeners, out.NextToken, nil
		},
		func(accelerator string, l gatypes.Listener) (*store.Resource, string, string) {
			arn := sv(l.ListenerArn)
			r, why, key := arnRow("listeners", l.ListenerArn, aws.String("a listener of "+accelerator), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorListener, NativeID: arn,
				Name: &arn, Region: regionGlobal,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
			if r != nil {
				listeners = append(listeners, arn)
			}
			return r, why, key
		})
	return listeners, p.addPhase(gaListenersOp, total, inserted, err)
}

// scanGAEndpointGroups stores each listener's endpoint groups. One listener's
// failure costs the others none of theirs (storeTokenEachParent).
func scanGAEndpointGroups(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, listeners []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, gaEndpointGroupsOp, gaListenerGone, listeners,
		func(ctx context.Context, listener string, token *string) ([]gatypes.EndpointGroup, *string, error) {
			out, err := client.ListEndpointGroups(ctx, &globalaccelerator.ListEndpointGroupsInput{ListenerArn: &listener, MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EndpointGroups, out.NextToken, nil
		},
		func(listener string, g gatypes.EndpointGroup) (*store.Resource, string, string) {
			label := sv(g.EndpointGroupRegion)
			return arnRow("endpoint groups", g.EndpointGroupArn, aws.String(cmp.Or(label, noIdentifier)+" of "+listener), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorEndpointGroup, NativeID: sv(g.EndpointGroupArn),
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		})
}

// scanGACustomRoutingAccelerators stores the custom routing accelerators with
// their tags and returns the stored ones, the parents of the listener phase.
// Each page is written as soon as its tags are read.
func scanGACustomRoutingAccelerators(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	tags.notFound = gaAcceleratorGone
	return storeParentPagesTags(ctx, st, acct, region, gaCustomRoutingAcceleratorsOp, "custom routing accelerators", "AcceleratorArn",
		func(ctx context.Context, token *string) ([]gatypes.CustomRoutingAccelerator, *string, error) {
			out, err := client.ListCustomRoutingAccelerators(ctx, &globalaccelerator.ListCustomRoutingAcceleratorsInput{MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Accelerators, out.NextToken, nil
		},
		func(a gatypes.CustomRoutingAccelerator) (*store.Resource, string, string) {
			// Standard and custom-routing accelerators share this ARN shape. The
			// two are separate API families and the standard ops reject a
			// custom-routing ARN, so the listings are assumed disjoint; were they
			// not, UpsertResources' duplicate-NativeID detection would warn.
			return arnRow("custom routing accelerators", a.AcceleratorArn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingAccelerator, NativeID: sv(a.AcceleratorArn),
				Name: a.Name, Region: regionGlobal, Status: nonEmptyPtr(string(a.Status)),
				CreatedAt: tp(a.CreatedTime), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		func(a gatypes.CustomRoutingAccelerator) string { return sv(a.AcceleratorArn) },
		tags, p)
}

// scanGACustomRoutingListeners stores each custom routing accelerator's
// listeners, each contained by its accelerator, and returns those built, the
// parents of the endpoint-group phase. One accelerator's failure costs the
// others none of theirs (storeChildrenEachParent). It reports whether the
// write failed, after which the scan must stop.
func scanGACustomRoutingListeners(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, accelerators []childParent, p *phaseTotals) ([]childParent, bool) {
	var listeners []childParent
	total, inserted, err := storeChildrenEachParent(ctx, st, acct, region, gaCustomRoutingListenersOp, gaAcceleratorGone, accelerators, nil, nil,
		func(ctx context.Context, accelerator childParent, token *string) ([]gatypes.CustomRoutingListener, *string, error) {
			out, err := client.ListCustomRoutingListeners(ctx, &globalaccelerator.ListCustomRoutingListenersInput{AcceleratorArn: &accelerator.arn, MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Listeners, out.NextToken, nil
		},
		func(accelerator childParent, l gatypes.CustomRoutingListener) (*store.Resource, string, string) {
			arn := sv(l.ListenerArn)
			r, why, key := arnRow("custom routing listeners", l.ListenerArn, aws.String("a listener of "+accelerator.arn), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingListener, NativeID: arn,
				Name: &arn, Region: regionGlobal,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
			if r != nil {
				listeners = append(listeners, childParent{id: arn, arn: arn})
			}
			return r, why, key
		})
	return listeners, p.addPhase(gaCustomRoutingListenersOp, total, inserted, err)
}

// scanGACustomRoutingEndpointGroups stores each custom routing listener's
// endpoint groups, each contained by its listener. One listener's failure
// costs the others none of theirs (storeChildrenEachParent).
func scanGACustomRoutingEndpointGroups(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string, listeners []childParent) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, gaCustomRoutingEndpointGroupsOp, gaListenerGone, listeners, nil, nil,
		func(ctx context.Context, listener childParent, token *string) ([]gatypes.CustomRoutingEndpointGroup, *string, error) {
			out, err := client.ListCustomRoutingEndpointGroups(ctx, &globalaccelerator.ListCustomRoutingEndpointGroupsInput{ListenerArn: &listener.arn, MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EndpointGroups, out.NextToken, nil
		},
		func(listener childParent, g gatypes.CustomRoutingEndpointGroup) (*store.Resource, string, string) {
			label := sv(g.EndpointGroupRegion)
			return arnRow("custom routing endpoint groups", g.EndpointGroupArn, aws.String(cmp.Or(label, noIdentifier)+" of "+listener.arn), &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorCustomRoutingEndpointGroup, NativeID: sv(g.EndpointGroupArn),
				Name: nonEmptyPtr(label), Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		})
}

// gaByoipCidrNativeID builds the NativeID of an address range provisioned to
// Global Accelerator. The API issues no ARN or id for it, and a bare CIDR is
// not a namespaced identity (EC2 BYOIP and IPAM ranges use the same strings),
// so the id is ARN-shaped in the globalaccelerator namespace.
func gaByoipCidrNativeID(partition, accountID, cidr string) string {
	return arnFor(partition, "globalaccelerator", "", accountID, "byoip-cidr/"+cidr)
}

// scanGAByoipCidrs stores the address ranges provisioned to Global
// Accelerator. The listing keeps a DEPROVISIONED range, which no longer
// exists in Global Accelerator, so it is not stored. A failed page keeps the
// pages read before it (storeTokenList).
func scanGAByoipCidrs(ctx context.Context, client globalAcceleratorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, gaByoipCidrsOp,
		func(ctx context.Context, token *string) ([]gatypes.ByoipCidr, *string, error) {
			out, err := client.ListByoipCidrs(ctx, &globalaccelerator.ListByoipCidrsInput{MaxResults: aws.Int32(gaPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(out.ByoipCidrs, func(c gatypes.ByoipCidr) bool {
				return c.State == gatypes.ByoipCidrStateDeprovisioned
			}), out.NextToken, nil
		},
		func(c gatypes.ByoipCidr) (*store.Resource, string, string) {
			cidr := sv(c.Cidr)
			if cidr == "" {
				return nil, "BYOIP ranges have no Cidr", noIdentifier
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlobalAcceleratorByoipCidr, NativeID: gaByoipCidrNativeID(acct.partition(), acct.ID, cidr),
				Name: &cidr, Region: regionGlobal, Status: nonEmptyPtr(string(c.State)),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
}
