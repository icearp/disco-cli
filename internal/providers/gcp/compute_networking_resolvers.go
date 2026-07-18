package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R24 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): first resolvers for `compute_networking_scanners.go`'s
// NetworkEndpointGroup family (zonal/regional/global — one shared attrs shape
// per `go doc compute.NetworkEndpointGroup`) and PacketMirroring.
//
// NEG.Network/Subnetwork are full Compute self-link URLs (same-API field,
// exact match against Network/Subnet's own NativeID convention — mirrors
// `firewall_resolvers.go`'s Firewall.Network, NOT a bare-name field like the
// cross-API cases in Dataproc/Dataflow). NEG.CloudRun.Service and
// NEG.CloudFunction.Function are bare cross-API service/function names
// (verified via `go doc`), matched via the existing `bareNameIndex` — same
// precedent already used for CloudRunSvc in `serverless_resolvers.go`.
// NEG.AppEngine is deliberately NOT wired: no App Engine service scanner
// exists in this provider today.
//
// Per `go doc compute.NetworkEndpointGroup`'s own field docs, not every
// (scope, field) combination is reachable: Network "cannot be set for...
// global NEGs" (adversarial review caught this — dropped from the EdgeDecl
// list below; TypeComputeGlobalNetworkEndpointGroup is flagged `Leaf: true`
// in compute_networking_scanners.go since it now has zero real outbound
// fields), and CloudRun/CloudFunction only apply to SERVERLESS-type
// endpoints, which per the package doc's own scope note are created only via
// the regional API — so those two fields are unreachable on the zonal
// (TypeComputeNetworkEndpointGroup) and global variants and are declared
// only for the regional type. The shared parsing loop below still reads all
// three types generically (harmless no-op on the scope-impossible fields);
// only the coverage-facing EdgeDecl list is scoped to what can really fire.
//
// PacketMirroring.Network.URL / MirroredResources.Instances[].URL /
// MirroredResources.Subnetworks[].URL / CollectorIlb.URL are all full
// self-link URLs (same-API fields), exact-matched the same way.
func init() {
	registerResolver(resolveNetworkEndpointGroupRelationships,
		EdgeDecl{TypeComputeNetworkEndpointGroup, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeNetworkEndpointGroup, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionNetworkEndpointGroup, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionNetworkEndpointGroup, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionNetworkEndpointGroup, TypeCloudRunSvc, store.RelUses},
		EdgeDecl{TypeComputeRegionNetworkEndpointGroup, TypeCloudFunction, store.RelUses},
	)
	registerResolver(resolvePacketMirroringRelationships,
		EdgeDecl{TypeComputePacketMirroring, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputePacketMirroring, TypeComputeInstance, store.RelUses},
		EdgeDecl{TypeComputePacketMirroring, TypeComputeSubnet, store.RelUses},
		EdgeDecl{TypeComputePacketMirroring, TypeComputeForwardingRule, store.RelUses},
	)
}

type negAttrs struct {
	Network    string `json:"network"`
	Subnetwork string `json:"subnetwork"`
	CloudRun   *struct {
		Service string `json:"service"`
	} `json:"cloudRun"`
	CloudFunction *struct {
		Function string `json:"function"`
	} `json:"cloudFunction"`
}

func resolveNetworkEndpointGroupRelationships(p *project, st *store.Store) error {
	negTypes := []string{TypeComputeNetworkEndpointGroup, TypeComputeRegionNetworkEndpointGroup, TypeComputeGlobalNetworkEndpointGroup}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: negTypes,
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedNets, err := scannedIDSet(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	scannedSubnets, err := scannedIDSet(p, st, TypeComputeSubnet)
	if err != nil {
		return err
	}
	runByName, err := bareNameIndex(p, st, TypeCloudRunSvc)
	if err != nil {
		return err
	}
	funcByName, err := bareNameIndex(p, st, TypeCloudFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs negAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedNets, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return fmt.Errorf("upsert neg→network: %w", err)
		}
		if err := upsertIfScanned(st, scannedSubnets, r.ID, "gcp", p.ID, TypeComputeSubnet, attrs.Subnetwork, store.RelAttachedTo); err != nil {
			return fmt.Errorf("upsert neg→subnetwork: %w", err)
		}
		if attrs.CloudRun != nil && attrs.CloudRun.Service != "" {
			if toID, ok := runByName[attrs.CloudRun.Service]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert neg→cloudRun: %w", err)
				}
			}
		}
		if attrs.CloudFunction != nil && attrs.CloudFunction.Function != "" {
			if toID, ok := funcByName[attrs.CloudFunction.Function]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert neg→cloudFunction: %w", err)
				}
			}
		}
	}
	return nil
}

func resolvePacketMirroringRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputePacketMirroring},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedNets, err := scannedIDSet(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	scannedInstances, err := scannedIDSet(p, st, TypeComputeInstance)
	if err != nil {
		return err
	}
	scannedSubnets, err := scannedIDSet(p, st, TypeComputeSubnet)
	if err != nil {
		return err
	}
	scannedFwdRules, err := scannedIDSet(p, st, TypeComputeForwardingRule)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Network *struct {
				URL string `json:"url"`
			} `json:"network"`
			MirroredResources *struct {
				Instances []*struct {
					URL string `json:"url"`
				} `json:"instances"`
				Subnetworks []*struct {
					URL string `json:"url"`
				} `json:"subnetworks"`
			} `json:"mirroredResources"`
			CollectorIlb *struct {
				URL string `json:"url"`
			} `json:"collectorIlb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Network != nil {
			if err := upsertIfScanned(st, scannedNets, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network.URL, store.RelAttachedTo); err != nil {
				return fmt.Errorf("upsert packetMirroring→network: %w", err)
			}
		}
		if attrs.MirroredResources != nil {
			for _, inst := range attrs.MirroredResources.Instances {
				if inst == nil {
					continue
				}
				if err := upsertIfScanned(st, scannedInstances, r.ID, "gcp", p.ID, TypeComputeInstance, inst.URL, store.RelUses); err != nil {
					return fmt.Errorf("upsert packetMirroring→instance: %w", err)
				}
			}
			for _, sn := range attrs.MirroredResources.Subnetworks {
				if sn == nil {
					continue
				}
				if err := upsertIfScanned(st, scannedSubnets, r.ID, "gcp", p.ID, TypeComputeSubnet, sn.URL, store.RelUses); err != nil {
					return fmt.Errorf("upsert packetMirroring→subnetwork: %w", err)
				}
			}
		}
		if attrs.CollectorIlb != nil {
			if err := upsertIfScanned(st, scannedFwdRules, r.ID, "gcp", p.ID, TypeComputeForwardingRule, attrs.CollectorIlb.URL, store.RelUses); err != nil {
				return fmt.Errorf("upsert packetMirroring→forwardingRule: %w", err)
			}
		}
	}
	return nil
}
