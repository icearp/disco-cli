package aws

import (
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
// scanned-registry id set. A schema NativeID that names no registry is
// reported after the other schemas are linked.
func resolveEventSchemasSchemaRegistry(acct *account, st *store.Store) error {
	regSet, err := scannedIDSet(acct, st, TypeEventSchemasRegistry)
	if err != nil {
		return err
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventSchemasSchema},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		regARN, ok := eventSchemasRegistryARN(r.NativeID)
		if !ok {
			errs = append(errs, fmt.Errorf("event-schemas schema %q: NativeID names no registry", r.NativeID))
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, regARN)
		if !regSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return errors.Join(append(errs, fmt.Errorf("upsert event-schemas schema→registry: %w", err))...)
		}
	}
	return errors.Join(errs...)
}

// eventSchemasRegistryARN returns the ARN of the registry a schema ARN names,
// and false when the ARN has no `:schema/{registryName}/` path.
func eventSchemasRegistryARN(schemaARN string) (string, bool) {
	prefix, rest, ok := strings.Cut(schemaARN, ":schema/")
	if !ok {
		return "", false
	}
	registryName, _, ok := strings.Cut(rest, "/")
	if !ok || registryName == "" {
		return "", false
	}
	return prefix + ":registry/" + registryName, true
}
