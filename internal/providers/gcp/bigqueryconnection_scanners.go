package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigqueryconnection/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBQConnection, Service: "bigqueryconnection", Upstream: "bigqueryconnection.googleapis.com/Connection"})
	registerService(serviceEntry{
		name: "gcp:bigqueryconnection",
		fn:   scanBQConnections,
	})
}

// scanBQConnections lists BigQuery Connections per-region via
// gcpRegionFanoutScan rather than the "locations/-" wildcard other
// wildcard-capable scanners in this package use: the bigqueryconnection/v1
// discovery doc's own `parent` path regex
// (`^projects/[^/]+/locations/[^/]+$`) documents no wildcard exception (Cloud
// Run/Functions v2/Artifact Registry all document theirs explicitly), and a
// GCP community thread confirms there is no all-locations list call for this
// API. A wrong wildcard guess wouldn't just fail this one service — GCP's
// scan dispatch (`scanProject`/`Scan()`) still fans out per-project-service
// via `errgroup.WithContext`, so a real per-service error here cancels every
// sibling service AND every sibling project's phase-1 scan, aborting
// relationship resolution scan-wide (see internal/providers/CLAUDE.md
// "Errors never abort scan" — GCP's dispatcher predates that fix and hasn't
// been migrated to `sync.WaitGroup` yet, unlike AWS). The bounded, known
// trade-off here: BigQuery connection locations can technically also be the
// multi-regions "US"/"EU" (not real Compute regions), which gcpRegions won't
// enumerate — a real but narrow gap (only relevant for BigQuery-source-only
// connection types; Cloud SQL/Spanner-backed connections are always regional
// and fully covered), versus the wildcard's unknown-but-plausibly-total
// failure mode. Revisit if bigqueryconnection later exposes Locations.List.
func scanBQConnections(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := bigqueryconnection.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("bigqueryconnection client: %w", err)
	}
	return scanBQConnectionsWithClient(ctx, svc, p, st, scanID)
}

// scanBQConnectionsWithClient is the test seam for scanBQConnections — takes
// the pre-built client directly so tests can point it at a fake server.
func scanBQConnectionsWithClient(ctx context.Context, svc *bigqueryconnection.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return scanBQConnectionsIn(ctx, svc, p, st, scanID, regions)
}

// scanBQConnectionsIn is the testable core of scanBQConnectionsWithClient:
// takes a pre-resolved region slice instead of calling gcpRegions, so tests
// can inject an arbitrary region list without faking the Compute regions API.
func scanBQConnectionsIn(ctx context.Context, svc *bigqueryconnection.Service, p *project, st *store.Store, scanID string, regions []string) (total, inserted int, err error) {
	return gcpRegionFanoutScanIn(
		ctx, p, st, fanoutMed, regions, "bigqueryconnection:connections.list",
		func(region string) pager[bigqueryconnection.ListConnectionsResponse] {
			parent := fmt.Sprintf("projects/%s/locations/%s", p.ID, region)
			return svc.Projects.Locations.Connections.List(parent)
		},
		func(page *bigqueryconnection.ListConnectionsResponse) []*bigqueryconnection.Connection {
			return page.Connections
		},
		func(c *bigqueryconnection.Connection, region string) *store.Resource {
			if c == nil || c.Name == "" {
				return nil
			}
			name := lastSegment(c.Name)
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeBQConnection,
				NativeID:       c.Name,
				Name:           &name,
				Region:         &region,
				CreatedAt:      msToRFC3339(c.CreationTime),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
		},
	)
}
