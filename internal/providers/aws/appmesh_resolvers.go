package aws

import (
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveAppMeshChildren,
		EdgeDecl{TypeAppMeshVirtualGateway, TypeAppMeshMesh, store.RelAttachedTo},
		EdgeDecl{TypeAppMeshVirtualNode, TypeAppMeshMesh, store.RelAttachedTo},
		EdgeDecl{TypeAppMeshVirtualRouter, TypeAppMeshMesh, store.RelAttachedTo},
		EdgeDecl{TypeAppMeshVirtualService, TypeAppMeshMesh, store.RelAttachedTo},
		EdgeDecl{TypeAppMeshRoute, TypeAppMeshMesh, store.RelAttachedTo},
		EdgeDecl{TypeAppMeshGatewayRoute, TypeAppMeshMesh, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppMeshRouteParent,
		EdgeDecl{TypeAppMeshRoute, TypeAppMeshVirtualRouter, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppMeshGatewayRouteParent,
		EdgeDecl{TypeAppMeshGatewayRoute, TypeAppMeshVirtualGateway, store.RelAttachedTo},
	)
}

// appMeshMeshARNFromChild extracts `arn:aws:appmesh:r:a:mesh/{name}` from any
// child ARN of shape `…:mesh/{name}/<kind>/<id>[/…]`. Returns "" when the ARN
// does not carry a `mesh/{name}/` segment.
func appMeshMeshARNFromChild(arn string) string {
	const prefix = "mesh/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// appMeshGrandparentARN strips the trailing `/<kind>/<id>` from a route or
// gateway-route ARN to recover its virtual-router or virtual-gateway parent.
// Input: `…:mesh/{m}/virtualRouter/{vr}/route/{rt}` →
// output: `…:mesh/{m}/virtualRouter/{vr}`. Returns "" on shape mismatch.
func appMeshGrandparentARN(arn string) string {
	last := strings.LastIndexByte(arn, '/')
	if last < 0 {
		return ""
	}
	mid := strings.LastIndexByte(arn[:last], '/')
	if mid < 0 {
		return ""
	}
	return arn[:mid]
}

// resolveAppMeshChildren wires every per-mesh child (virtual gateway, node,
// router, service, route, gateway-route) to its parent mesh by parsing the
// NativeID's `mesh/{name}/...` segment.
func resolveAppMeshChildren(acct *account, st *store.Store) error {
	childTypes := []string{
		TypeAppMeshVirtualGateway,
		TypeAppMeshVirtualNode,
		TypeAppMeshVirtualRouter,
		TypeAppMeshVirtualService,
		TypeAppMeshRoute,
		TypeAppMeshGatewayRoute,
	}
	meshSet, err := scannedIDSet(acct, st, TypeAppMeshMesh)
	if err != nil {
		return err
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := appMeshMeshARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if !meshSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appmesh %s→mesh: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveAppMeshRouteParent wires each route to its parent virtual-router via
// NativeID grandparent extraction.
func resolveAppMeshRouteParent(acct *account, st *store.Store) error {
	return resolveAppMeshGrandparent(acct, st, TypeAppMeshRoute, TypeAppMeshVirtualRouter)
}

// resolveAppMeshGatewayRouteParent wires each gateway-route to its parent
// virtual-gateway via NativeID grandparent extraction.
func resolveAppMeshGatewayRouteParent(acct *account, st *store.Store) error {
	return resolveAppMeshGrandparent(acct, st, TypeAppMeshGatewayRoute, TypeAppMeshVirtualGateway)
}

func resolveAppMeshGrandparent(acct *account, st *store.Store, childType, parentType string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := appMeshGrandparentARN(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert appmesh %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}
