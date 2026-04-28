package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/logging/v2"
)

func init() {
	registerOrgService(orgServiceEntry{name: "gcp:logging-org", fn: scanLoggingSinksOrg})
}

// scanLoggingSinksOrg discovers Cloud Logging sinks defined at organization
// and folder scopes. Sibling to the per-project scanLoggingSinks; runs ONCE
// per scan via the org-service lane. NativeIDs follow the GCP-canonical
// `{scope}/sinks/{name}` shape so they don't collide with project-scope sinks.
func scanLoggingSinksOrg(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := logging.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("logging client: %w", err)
	}

	for _, sc := range scopes {
		t, n, perr := scanLoggingSinksForScope(ctx, svc, sc, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}

// scanLoggingSinksForScope handles either Organizations.Sinks.List or
// Folders.Sinks.List. The two APIs are structurally identical but live on
// distinct sub-services; dispatch picks the right one via the scope kind.
func scanLoggingSinksForScope(ctx context.Context, svc *logging.Service, sc orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	pages := func(handler func(*logging.ListSinksResponse) error) error {
		switch sc.Kind {
		case "organization":
			return svc.Organizations.Sinks.List(sc.Name).Context(ctx).Pages(ctx, handler)
		case "folder":
			return svc.Folders.Sinks.List(sc.Name).Context(ctx).Pages(ctx, handler)
		default:
			return fmt.Errorf("unknown scope kind %q", sc.Kind)
		}
	}
	err = pages(func(page *logging.ListSinksResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, s := range page.Sinks {
			if s == nil || s.Name == "" {
				continue
			}
			name := s.Name
			nativeID := sc.Name + "/sinks/" + s.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      sc.Name,
				Type:           TypeLoggingSink,
				NativeID:       nativeID,
				Name:           &name,
				CreatedAt:      strp(s.CreateTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
			sinkID := store.ResourceID("gcp", sc.Name, TypeLoggingSink, nativeID)
			pairs = append(pairs, [2]string{sinkID, sc.Resource})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert org logging sinks: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.BatchAddToHierarchyClosure(pairs); cErr != nil {
			return fmt.Errorf("closure org logging sinks: %w", cErr)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "logging:"+sc.Kind+"-sinks.list", sc.Name, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}
