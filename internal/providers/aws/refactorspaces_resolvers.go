package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRefactorSpacesHierarchy,
		EdgeDecl{TypeRefactorSpacesApplication, TypeRefactorSpacesEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeRefactorSpacesService, TypeRefactorSpacesApplication, store.RelAttachedTo},
		EdgeDecl{TypeRefactorSpacesRoute, TypeRefactorSpacesApplication, store.RelAttachedTo},
	)
}

// rsParentARN slices a NativeID up to (but not including) the segment marker
// `/<segment>/`. Returns the prefix when the segment is present.
func rsParentARN(arn, segment string) string {
	i := strings.Index(arn, "/"+segment+"/")
	if i < 0 {
		return ""
	}
	return arn[:i]
}

// rsServiceARN reconstructs the service ARN from a route NativeID. Route:
// `…/application/{appId}/route/{routeId}` — service NativeID has the same
// `…/application/{appId}` prefix plus `/service/{serviceId}`. We can't recover
// the service id from a route ARN alone (route doesn't reference it textually),
// so this resolver wires routes only to their application — service edges
// require a ServiceId attribute lookup.
func resolveRefactorSpacesHierarchy(acct *account, st *store.Store) error {
	envSet, err := scannedIDSet(acct, st, TypeRefactorSpacesEnvironment)
	if err != nil {
		return err
	}
	appSet, err := scannedIDSet(acct, st, TypeRefactorSpacesApplication)
	if err != nil {
		return err
	}

	emit := func(srcID, tgtType, tgtARN string, set map[string]bool) error {
		if tgtARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, tgtARN)
		if !set[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert refactor-spaces →%s: %w", tgtType, err)
		}
		return nil
	}

	// Application → Environment: strip `/application/{id}` tail.
	appRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRefactorSpacesApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range appRows {
		if err := emit(r.ID, TypeRefactorSpacesEnvironment, rsParentARN(r.NativeID, "application"), envSet); err != nil {
			return err
		}
	}
	// Service → Application: strip `/service/{id}` tail.
	svcRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRefactorSpacesService}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range svcRows {
		if err := emit(r.ID, TypeRefactorSpacesApplication, rsParentARN(r.NativeID, "service"), appSet); err != nil {
			return err
		}
	}
	// Route → Application: strip `/route/{id}` tail.
	routeRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRefactorSpacesRoute}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range routeRows {
		if err := emit(r.ID, TypeRefactorSpacesApplication, rsParentARN(r.NativeID, "route"), appSet); err != nil {
			return err
		}
	}
	return nil
}
