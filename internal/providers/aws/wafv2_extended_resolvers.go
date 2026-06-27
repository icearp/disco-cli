package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveWAFv2LoggingConfigToWebACL,
		EdgeDecl{TypeWAFv2LoggingConfiguration, TypeWAFv2WebACL, store.RelAttachedTo},
	)
	registerResolver(
		resolveWAFv2WebACLAssociationRefs,
		EdgeDecl{TypeWAFv2WebACLAssociation, TypeWAFv2WebACL, store.RelAttachedTo},
		EdgeDecl{TypeWAFv2WebACLAssociation, TypeELBv2LoadBalancer, store.RelAttachedTo},
		EdgeDecl{TypeWAFv2WebACLAssociation, TypeAPIGatewayStage, store.RelAttachedTo},
		EdgeDecl{TypeWAFv2WebACLAssociation, TypeAppSyncGraphQLApi, store.RelAttachedTo},
		EdgeDecl{TypeWAFv2WebACLAssociation, TypeCognitoUserPool, store.RelAttachedTo},
	)
}

// resolveWAFv2LoggingConfigToWebACL wires each logging-configuration to its
// target web-acl. Scanner stores the WebACL ARN as the LoggingConfiguration
// NativeID directly (the API uses ResourceArn = WebACL ARN), so the lookup
// is a same-ARN identity match into the web-acl set.
func resolveWAFv2LoggingConfigToWebACL(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWAFv2LoggingConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	waSet, err := scannedIDSet(acct, st, TypeWAFv2WebACL)
	if err != nil {
		return err
	}
	for _, r := range rows {
		tgtID := store.ResourceID("aws", acct.ID, TypeWAFv2WebACL, r.NativeID)
		if !waSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wafv2 logging-config→web-acl: %w", err)
		}
	}
	return nil
}

// resolveWAFv2WebACLAssociationRefs wires the synthetic web-acl-association
// row to its web-acl (NativeID prefix before `/association/`) and to the
// protected resource (ARN tail after `/association/`). The protected resource
// dispatches by service segment of the ARN.
func resolveWAFv2WebACLAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWAFv2WebACLAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	waSet, err := scannedIDSet(acct, st, TypeWAFv2WebACL)
	if err != nil {
		return err
	}
	type setEntry struct {
		typ string
		set map[string]bool
	}
	dispatch := []struct {
		match string
		setEntry
	}{}
	loadSet := func(t string) (map[string]bool, error) { return scannedIDSet(acct, st, t) }
	for _, e := range []struct{ match, typ string }{
		{":loadbalancer/app/", TypeELBv2LoadBalancer},
		{":/restapis/", TypeAPIGatewayStage},
		{":apis/", TypeAppSyncGraphQLApi},
		{":userpool/", TypeCognitoUserPool},
	} {
		s, err := loadSet(e.typ)
		if err != nil {
			return err
		}
		dispatch = append(dispatch, struct {
			match string
			setEntry
		}{e.match, setEntry{e.typ, s}})
	}

	const seg = "/association/"
	for _, r := range rows {
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		webACLArn := r.NativeID[:i]
		resourceArn := r.NativeID[i+len(seg):]

		// → web-acl
		tgtID := store.ResourceID("aws", acct.ID, TypeWAFv2WebACL, webACLArn)
		if waSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert wafv2 assoc→web-acl: %w", err)
			}
		}

		// → protected resource (dispatch by ARN substring)
		for _, d := range dispatch {
			if !strings.Contains(resourceArn, d.match) {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, d.typ, resourceArn)
			if !d.set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert wafv2 assoc→%s: %w", d.typ, err)
			}
			break
		}
	}
	return nil
}
