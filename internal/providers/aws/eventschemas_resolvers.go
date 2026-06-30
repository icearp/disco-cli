package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveEventSchemasSchemaRegistry,
		EdgeDecl{TypeEventSchemasSchema, TypeEventSchemasRegistry, store.RelAttachedTo},
	)
}

// resolveEventSchemasSchemaRegistry wires each EventBridge Schemas schema to
// its parent registry. The schema ARN embeds the registry name in its path
// (`arn:aws:schemas:r:a:schema/{registryName}/{schemaName}`); the registry
// ARN is `arn:aws:schemas:r:a:registry/{registryName}`. FK-safe via the
// scanned-registry id set.
func resolveEventSchemasSchemaRegistry(acct *account, st *store.Store) error {
	regSet, err := scannedIDSet(acct, st, TypeEventSchemasRegistry)
	if err != nil {
		return err
	}
	if len(regSet) == 0 {
		return nil
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventSchemasSchema},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		const marker = ":schema/"
		i := strings.Index(r.NativeID, marker)
		if i < 0 {
			continue
		}
		prefix := r.NativeID[:i] // arn:aws:schemas:{r}:{a}
		rest := r.NativeID[i+len(marker):]
		registryName, _, ok := strings.Cut(rest, "/")
		if !ok || registryName == "" {
			continue
		}
		regARN := prefix + ":registry/" + registryName
		tgt := store.ResourceID("aws", acct.ID, TypeEventSchemasRegistry, regARN)
		if !regSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert event-schemas schema→registry: %w", err)
		}
	}
	return nil
}
