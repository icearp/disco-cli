package azure

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "azure:sql", fn: scanSQL}) }

// sqlServer holds the fields we need after listing servers.
type sqlServer struct {
	resourceID string // disco resource ID
	name       string
	rgName     string
}

// scanSQL discovers Azure SQL servers and their databases.
// Servers are listed first, then all per-server database lists run concurrently.
func scanSQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	serversClient, err := armsql.NewServersClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armsql:NewServersClient: %w", err)
	}
	dbsClient, err := armsql.NewDatabasesClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armsql:NewDatabasesClient: %w", err)
	}

	// Phase 1: list all servers and upsert them.
	var servers []sqlServer
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
			if srv.ID == nil || srv.Name == nil {
				continue
			}
			name := sv(srv.Name)
			location := sv(srv.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServer,
				NativeID:       sv(srv.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(srv),
				DiscoveredBy:   scanID,
			}
			if srv.Tags != nil {
				s := mustJSON(srv.Tags)
				r.TagsJSON = &s
			}
			serverBatch = append(serverBatch, r)
			servers = append(servers, sqlServer{
				resourceID: store.ResourceID("azure", sub.ID, TypeSQLServer, sv(srv.ID)),
				name:       sv(srv.Name),
				rgName:     rgFromID(sv(srv.ID)),
			})
		}
		if len(serverBatch) > 0 {
			if err := st.UpsertResources(serverBatch); err != nil {
				return fmt.Errorf("upsert SQL servers: %w", err)
			}
		}
	}

	// Phase 2: fetch databases for all servers concurrently.
	var (
		mu      sync.Mutex
		allDBs  []*store.Resource
		allPairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, srv := range servers {
		g.Go(func() error {
			dbPager := dbsClient.NewListByServerPager(srv.rgName, srv.name, nil)
			for dbPager.More() {
				dbPage, err := dbPager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						break
					}
					return fmt.Errorf("armsql:Databases.ListByServer(%s): %w", srv.name, err)
				}
				var dbBatch []*store.Resource
				var pairs [][2]string
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
						Type:           TypeSQLDatabase,
						NativeID:       sv(db.ID),
						Name:           &dbName,
						Region:         &dbLocation,
						AttributesJSON: mustJSON(db),
						DiscoveredBy:   scanID,
					}
					if db.Tags != nil {
						s := mustJSON(db.Tags)
						r.TagsJSON = &s
					}
					dbBatch = append(dbBatch, r)
					pairs = append(pairs, [2]string{
						store.ResourceID("azure", sub.ID, TypeSQLDatabase, sv(db.ID)),
						srv.resourceID,
					})
				}
				if len(dbBatch) > 0 {
					mu.Lock()
					allDBs = append(allDBs, dbBatch...)
					allPairs = append(allPairs, pairs...)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if len(allDBs) > 0 {
		if err := st.UpsertResources(allDBs); err != nil {
			return fmt.Errorf("upsert SQL databases: %w", err)
		}
		if err := st.BatchAddToHierarchyClosure(allPairs); err != nil {
			return fmt.Errorf("closure SQL databases: %w", err)
		}
	}
	return nil
}
