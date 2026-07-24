package aws

import (
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveRefactorSpacesHierarchy,
		EdgeDecl{TypeRefactorSpacesApplication, TypeRefactorSpacesEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeRefactorSpacesService, TypeRefactorSpacesApplication, store.RelAttachedTo},
		EdgeDecl{TypeRefactorSpacesRoute, TypeRefactorSpacesApplication, store.RelAttachedTo},
	)
}

// rsParentARN slices a NativeID up to (not including) the `/<segment>/` marker,
// or returns "" if the marker is absent.
func rsParentARN(arn, segment string) string {
	i := strings.Index(arn, "/"+segment+"/")
	if i < 0 {
		return ""
	}
	return arn[:i]
}

// rsServiceARN: route ARN `…/application/{appId}/route/{routeId}` shares the
// `…/application/{appId}` prefix with service ARN
// `…/application/{appId}/service/{serviceId}`, but never encodes the service
// id — so this resolver wires routes only to their application; service
// edges need a ServiceId attribute lookup.
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
		tgtID := store.ResourceID("aws", acct.ID, tgtARN)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRefactorSpacesApplication}, Limit: util.AllResources,
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRefactorSpacesService}, Limit: util.AllResources,
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRefactorSpacesRoute}, Limit: util.AllResources,
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
