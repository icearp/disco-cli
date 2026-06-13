package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveGlobalAcceleratorListenerParent,
		EdgeDecl{TypeGlobalAcceleratorListener, TypeGlobalAcceleratorAccelerator, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlobalAcceleratorEndpointGroupRefs,
		EdgeDecl{TypeGlobalAcceleratorEndpointGroup, TypeGlobalAcceleratorListener, store.RelAttachedTo},
		EdgeDecl{TypeGlobalAcceleratorEndpointGroup, TypeELBv2LoadBalancer, store.RelRoutesTo},
		EdgeDecl{TypeGlobalAcceleratorEndpointGroup, TypeEC2EIP, store.RelRoutesTo},
		EdgeDecl{TypeGlobalAcceleratorEndpointGroup, TypeEC2Instance, store.RelRoutesTo},
	)
	registerResolver(
		resolveGlobalAcceleratorCrossAccountAttachmentRefs,
		EdgeDecl{TypeGlobalAcceleratorCrossAccountAttachment, TypeELBv2LoadBalancer, store.RelRoutesTo},
		EdgeDecl{TypeGlobalAcceleratorCrossAccountAttachment, TypeEC2EIP, store.RelRoutesTo},
		EdgeDecl{TypeGlobalAcceleratorCrossAccountAttachment, TypeEC2Instance, store.RelRoutesTo},
	)
}

// gaParentARN strips the trailing `/<segment>/{id}` from a Global Accelerator
// child ARN (listener under accelerator, endpoint-group under listener).
// `arn:aws:globalaccelerator::a:accelerator/A/listener/L/endpoint-group/E`
// → `arn:aws:globalaccelerator::a:accelerator/A/listener/L`.
func gaParentARN(arn, childSegment string) string {
	needle := "/" + childSegment + "/"
	i := strings.LastIndex(arn, needle)
	if i < 0 {
		return ""
	}
	return arn[:i]
}

func resolveGlobalAcceleratorListenerParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlobalAcceleratorListener},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	accSet, err := scannedIDSet(acct, st, TypeGlobalAcceleratorAccelerator)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := gaParentARN(r.NativeID, "listener")
		if parent == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeGlobalAcceleratorAccelerator, parent)
		if !accSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert globalaccelerator listener→accelerator: %w", err)
		}
	}
	return nil
}

// gaEndpointDispatch classifies an EndpointId/Resource.EndpointId against
// likely target types. Per Global Accelerator API, EndpointId carries:
//   - Full ARN for ALB/NLB (`arn:aws:elasticloadbalancing:r:a:loadbalancer/.../`).
//   - Bare allocation ID for EIP (`eipalloc-xxx`) — synthesise canonical
//     `arn:aws:ec2:r:a:elastic-ip/eipalloc-xxx`.
//   - Bare instance ID for EC2 (`i-xxx`).
//
// Returns ("", "") when shape unrecognised.
func gaEndpointDispatch(endpointID, region, acctID string) (typ, native string) {
	if endpointID == "" {
		return "", ""
	}
	switch {
	case strings.Contains(endpointID, ":loadbalancer/"):
		return TypeELBv2LoadBalancer, endpointID
	case strings.HasPrefix(endpointID, "eipalloc-"):
		return TypeEC2EIP, ec2ARN(region, acctID, "elastic-ip", endpointID)
	case strings.HasPrefix(endpointID, "i-"):
		return TypeEC2Instance, ec2ARN(region, acctID, "instance", endpointID)
	}
	return "", ""
}

func resolveGlobalAcceleratorEndpointGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlobalAcceleratorEndpointGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	listenerSet, err := scannedIDSet(acct, st, TypeGlobalAcceleratorListener)
	if err != nil {
		return err
	}
	sets := map[string]map[string]bool{}
	for _, t := range []string{TypeELBv2LoadBalancer, TypeEC2EIP, TypeEC2Instance} {
		s, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		sets[t] = s
	}
	for _, r := range rows {
		if parent := gaParentARN(r.NativeID, "endpoint-group"); parent != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeGlobalAcceleratorListener, parent)
			if listenerSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert globalaccelerator endpoint-group→listener: %w", err)
				}
			}
		}
		var attrs struct {
			EndpointGroupRegion  *string `json:"EndpointGroupRegion"`
			EndpointDescriptions []struct {
				EndpointID *string `json:"EndpointId"`
			} `json:"EndpointDescriptions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(attrs.EndpointGroupRegion)
		if region == "" {
			region = sv(r.Region)
		}
		for _, ep := range attrs.EndpointDescriptions {
			ttyp, native := gaEndpointDispatch(sv(ep.EndpointID), region, acct.ID)
			if ttyp == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, ttyp, native)
			if !sets[ttyp][tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert globalaccelerator endpoint-group→%s: %w", ttyp, err)
			}
		}
	}
	return nil
}

func resolveGlobalAcceleratorCrossAccountAttachmentRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlobalAcceleratorCrossAccountAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sets := map[string]map[string]bool{}
	for _, t := range []string{TypeELBv2LoadBalancer, TypeEC2EIP, TypeEC2Instance} {
		s, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		sets[t] = s
	}
	for _, r := range rows {
		var attrs struct {
			Resources []struct {
				EndpointID *string `json:"EndpointId"`
				Region     *string `json:"Region"`
			} `json:"Resources"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, res := range attrs.Resources {
			region := sv(res.Region)
			if region == "" {
				region = sv(r.Region)
			}
			ttyp, native := gaEndpointDispatch(sv(res.EndpointID), region, acct.ID)
			if ttyp == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, ttyp, native)
			if !sets[ttyp][tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert globalaccelerator cross-account-attachment→%s: %w", ttyp, err)
			}
		}
	}
	return nil
}
