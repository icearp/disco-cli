package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// scanSQL discovers Azure SQL servers and their databases.
func scanSQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	serversClient, err := armsql.NewServersClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armsql:NewServersClient: %w", err)
	}
	dbsClient, err := armsql.NewDatabasesClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armsql:NewDatabasesClient: %w", err)
	}

	pager := serversClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armsql:Servers.List", sub.ID, err)
			}
			return fmt.Errorf("armsql:Servers.List: %w", err)
		}
		var serverBatch []*store.Resource
		for _, srv := range page.Value {
			if srv.ID == nil {
				continue
			}
			name := sv(srv.Name)
			location := sv(srv.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:sql:server",
				NativeID:       sv(srv.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(srv),
				ScanID:         scanID,
			}
			if srv.Tags != nil {
				s := mustJSON(srv.Tags)
				r.TagsJSON = &s
			}
			serverBatch = append(serverBatch, r)
		}
		if len(serverBatch) > 0 {
			if err := st.UpsertResources(serverBatch); err != nil {
				return fmt.Errorf("upsert SQL servers: %w", err)
			}
		}

		// Fetch databases for each server.
		for _, srv := range page.Value {
			if srv.Name == nil || srv.ID == nil {
				continue
			}
			rgName := rgFromID(sv(srv.ID))
			srvResourceID := store.ResourceID("azure", sub.ID, "azure:sql:server", sv(srv.ID))

			dbPager := dbsClient.NewListByServerPager(rgName, sv(srv.Name), nil)
			for dbPager.More() {
				dbPage, err := dbPager.NextPage(ctx)
				if err != nil {
					if isAccessDenied(err) {
						break
					}
					return fmt.Errorf("armsql:Databases.ListByServer: %w", err)
				}
				var dbBatch []*store.Resource
				for _, db := range dbPage.Value {
					if db.ID == nil {
						continue
					}
					dbName := sv(db.Name)
					dbLocation := sv(db.Location)
					r := &store.Resource{
						Provider:       "azure",
						AccountID:      sub.ID,
						AccountName:    &sub.Name,
						Type:           "azure:sql:database",
						NativeID:       sv(db.ID),
						Name:           &dbName,
						Region:         &dbLocation,
						AttributesJSON: mustJSON(db),
						ScanID:         scanID,
						ParentID:       &srvResourceID,
					}
					if db.Tags != nil {
						s := mustJSON(db.Tags)
						r.TagsJSON = &s
					}
					dbBatch = append(dbBatch, r)
				}
				if len(dbBatch) > 0 {
					if err := st.UpsertResources(dbBatch); err != nil {
						return fmt.Errorf("upsert SQL databases: %w", err)
					}
					pairs := make([][2]string, len(dbBatch))
					for i, r := range dbBatch {
						pairs[i] = [2]string{store.ResourceID("azure", sub.ID, "azure:sql:database", r.NativeID), srvResourceID}
					}
					if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
						return fmt.Errorf("closure SQL databases: %w", err)
					}
				}
			}
		}
	}
	return nil
}
