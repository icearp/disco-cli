package azure

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppServiceServerFarm, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceSite, Service: "microsoft.web", Redact: []redact.Rule{{Path: "properties.siteConfig.appSettings[*].value", Mode: redact.RedactScalar}, {Path: "properties.siteConfig.connectionStrings[*].connectionString", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAppServiceSiteSlot, Service: "microsoft.web", Redact: []redact.Rule{{Path: "properties.siteConfig.appSettings[*].value", Mode: redact.RedactScalar}, {Path: "properties.siteConfig.connectionStrings[*].connectionString", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeAppServiceEnvironment, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceEnvironmentWorkerPool, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceEnvironmentMultiRolePool, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceKubeEnvironment, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceStaticSite, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceStaticSiteBuild, Service: "microsoft.web"})
	registerType(restype.Descriptor{Type: TypeAppServiceCertificate, Service: "microsoft.web"})
	registerService(serviceEntry{
		name: "azure:microsoft.web",
		fn:   scanAppService,
	})
}

// siteEntry holds the identifying fields of a web app for slot fanout.
type siteEntry struct {
	rg, name, nativeID, discoID string
	isFunctionApp               bool
}

// aseEntry holds the identifying fields of an App Service Environment for pool fanout.
type aseEntry struct {
	rg, name, nativeID, discoID string
}

// staticSiteEntry holds the identifying fields of a static site for build fanout.
type staticSiteEntry struct {
	rg, name, nativeID, discoID string
}

// scanAppService discovers all Microsoft.Web resources: App Service plans,
// web apps and deployment slots, App Service Environments and their pools,
// Kube Environments, Static Web Apps and their builds, and TLS certificates.
// Top-level scanners run in parallel; each fanout chain runs sequentially.
func scanAppService(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		mu      sync.Mutex
		planErr = make(chan error, 1)
		webErr  = make(chan error, 1)
		aseErr  = make(chan error, 1)
		kubeErr = make(chan error, 1)
		ssErr   = make(chan error, 1)
		certErr = make(chan error, 1)
	)
	addTotals := func(t, n int) {
		mu.Lock()
		total += t
		inserted += n
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(6)

	go func() {
		defer wg.Done()
		t, n, e := scanAppServicePlans(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			planErr <- e
		}
	}()
	go func() {
		defer wg.Done()
		t, n, e := scanWebAppsChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			webErr <- e
		}
	}()
	go func() {
		defer wg.Done()
		t, n, e := scanEnvironmentsChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			aseErr <- e
		}
	}()
	go func() {
		defer wg.Done()
		t, n, e := scanKubeEnvironments(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			kubeErr <- e
		}
	}()
	go func() {
		defer wg.Done()
		t, n, e := scanStaticSitesChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			ssErr <- e
		}
	}()
	go func() {
		defer wg.Done()
		t, n, e := scanCertificates(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			certErr <- e
		}
	}()

	wg.Wait()

	for _, ch := range []chan error{planErr, webErr, aseErr, kubeErr, ssErr, certErr} {
		select {
		case e := <-ch:
			return 0, 0, e
		default:
		}
	}
	return total, inserted, nil
}

// scanAppServicePlans discovers App Service Plans (Microsoft.Web/serverFarms).
func scanAppServicePlans(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewPlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewPlansClient: %w", err)
	}
	return azPageScan(ctx, "armappservice:Plans.List", sub, st,
		client.NewListPager(nil),
		func(page armappservice.PlansClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, p := range page.Value {
				if p.ID == nil {
					continue
				}
				name, loc, nativeID := sv(p.Name), sv(p.Location), sv(p.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAppServiceServerFarm, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(p.Tags), AttributesJSON: mustJSON(p),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceServerFarm, nativeID))
			}
			return batch, pairs
		})
}

// scanWebAppsChain discovers web apps (Microsoft.Web/sites) then fans out to
// scan deployment slots (Microsoft.Web/sites/slots) per app.
func scanWebAppsChain(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewWebAppsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewWebAppsClient: %w", err)
	}

	var (
		batch   []*store.Resource
		pairs   [][2]string
		entries []siteEntry
	)

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:WebApps.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:WebApps.List: %w", err)
		}
		for _, s := range page.Value {
			if s.ID == nil {
				continue
			}
			name := sv(s.Name)
			location := sv(s.Location)
			nativeID := sv(s.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceSite,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(s.Tags)
			discoID := store.ResourceID("azure", sub.ID, nativeID)
			kind := sv(s.Kind)
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceSite, nativeID))
			entries = append(entries, siteEntry{
				rg:            rgNameFromID(nativeID),
				name:          name,
				nativeID:      nativeID,
				discoID:       discoID,
				isFunctionApp: strings.Contains(strings.ToLower(kind), "functionapp"),
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure web apps: %w", err)
		}
		total += len(batch)
		inserted += n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure web apps: %w", err)
		}
	}
	if len(entries) == 0 {
		return total, inserted, nil
	}

	// Fan out slot scans per web app, plus app-settings fetch for function
	// apps; both share the maxConcurrentFanout budget.
	var (
		mu                sync.Mutex
		sTotal, sInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, e := range entries {
		entry := e
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, n, e := scanWebAppSlots(gCtx, sub, client, st, scanID, entry)
			if e != nil {
				return e
			}
			mu.Lock()
			sTotal += t
			sInserted += n
			mu.Unlock()
			return nil
		})
		if entry.isFunctionApp {
			g.Go(func() error {
				if err := sem.Acquire(gCtx, 1); err != nil {
					return err
				}
				defer sem.Release(1)
				return fetchFunctionAppSettings(gCtx, sub, client, entry)
			})
		}
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + sTotal, inserted + sInserted, nil
}

// fetchFunctionAppSettings calls WebAppsClient.ListApplicationSettings for one
// function-app site, storing the result in the per-subscription sidecar
// (consumed by resolveFunctionAppRelationships). AccessDenied tolerated —
// partial-cred scans skip the resolver edges silently.
func fetchFunctionAppSettings(ctx context.Context, sub *subscription, client *armappservice.WebAppsClient, site siteEntry) error {
	resp, err := client.ListApplicationSettings(ctx, site.rg, site.name, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return nil
		}
		// App settings fetch is best-effort enrichment, not scan-fatal;
		// resolver tolerates missing entries.
		return nil
	}
	if len(resp.Properties) == 0 {
		return nil
	}
	settings := make(map[string]string, len(resp.Properties))
	for k, v := range resp.Properties {
		if v != nil {
			settings[k] = *v
		}
	}
	recordFunctionAppSettings(sub.ID, site.discoID, settings)
	return nil
}

// scanWebAppSlots lists deployment slots for a single web app.
func scanWebAppSlots(ctx context.Context, sub *subscription, client *armappservice.WebAppsClient, st *store.Store, scanID string, site siteEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string

	pager := client.NewListSlotsPager(site.rg, site.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:WebApps.ListSlots", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:WebApps.ListSlots %s/%s: %w", site.rg, site.name, err)
		}
		for _, s := range page.Value {
			if s.ID == nil {
				continue
			}
			name := sv(s.Name)
			location := sv(s.Location)
			nativeID := sv(s.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceSiteSlot,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(s.Tags)
			slotID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			// slot → parent site hierarchy
			pairs = append(pairs, [2]string{slotID, site.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure web app slots %s: %w", site.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure web app slots %s: %w", site.name, err)
		}
	}
	return total, inserted, nil
}

// scanEnvironmentsChain discovers App Service Environments (Microsoft.Web/hostingEnvironments)
// then fans out to scan worker pools and the multi-role pool per ASE.
func scanEnvironmentsChain(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewEnvironmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewEnvironmentsClient: %w", err)
	}

	var (
		batch   []*store.Resource
		pairs   [][2]string
		entries []aseEntry
	)

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:Environments.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:Environments.List: %w", err)
		}
		for _, e := range page.Value {
			if e.ID == nil {
				continue
			}
			name := sv(e.Name)
			location := sv(e.Location)
			nativeID := sv(e.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceEnvironment,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(e.Tags)
			discoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceEnvironment, nativeID))
			entries = append(entries, aseEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure App Service Environments: %w", err)
		}
		total += len(batch)
		inserted += n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure App Service Environments: %w", err)
		}
	}
	if len(entries) == 0 {
		return total, inserted, nil
	}

	// Fan out pool scans per ASE.
	var (
		mu                sync.Mutex
		pTotal, pInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, ase := range entries {
		entry := ase
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, n, e := scanASEPools(gCtx, sub, client, st, scanID, entry)
			if e != nil {
				return e
			}
			mu.Lock()
			pTotal += t
			pInserted += n
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + pTotal, inserted + pInserted, nil
}

// scanASEPools scans worker pools (Microsoft.Web/hostingEnvironments/workerPools)
// and multi-role pools (Microsoft.Web/hostingEnvironments/multiRolePools) for one ASE.
func scanASEPools(ctx context.Context, sub *subscription, client *armappservice.EnvironmentsClient, st *store.Store, scanID string, ase aseEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string

	// Worker pools
	wpPager := client.NewListWorkerPoolsPager(ase.rg, ase.name, nil)
	for wpPager.More() {
		page, err := wpPager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:Environments.ListWorkerPools", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:Environments.ListWorkerPools %s/%s: %w", ase.rg, ase.name, err)
		}
		for _, wp := range page.Value {
			if wp.ID == nil {
				continue
			}
			name := sv(wp.Name)
			nativeID := sv(wp.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceEnvironmentWorkerPool,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(wp),
				DiscoveredBy:   scanID,
			}
			wpID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{wpID, ase.discoID})
		}
	}

	// Multi-role pool (one per ASE)
	mrpPager := client.NewListMultiRolePoolsPager(ase.rg, ase.name, nil)
	for mrpPager.More() {
		page, err := mrpPager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:Environments.ListMultiRolePools", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:Environments.ListMultiRolePools %s/%s: %w", ase.rg, ase.name, err)
		}
		for _, mrp := range page.Value {
			if mrp.ID == nil {
				continue
			}
			name := sv(mrp.Name)
			nativeID := sv(mrp.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceEnvironmentMultiRolePool,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(mrp),
				DiscoveredBy:   scanID,
			}
			mrpID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{mrpID, ase.discoID})
		}
	}

	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure ASE pools %s: %w", ase.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure ASE pools %s: %w", ase.name, err)
		}
	}
	return total, inserted, nil
}

// scanKubeEnvironments discovers Kubernetes Environments (Microsoft.Web/kubeEnvironments).
func scanKubeEnvironments(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewKubeEnvironmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewKubeEnvironmentsClient: %w", err)
	}
	return azPageScan(ctx, "armappservice:KubeEnvironments.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armappservice.KubeEnvironmentsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, ke := range page.Value {
				if ke.ID == nil {
					continue
				}
				name, loc, nativeID := sv(ke.Name), sv(ke.Location), sv(ke.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAppServiceKubeEnvironment, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(ke.Tags), AttributesJSON: mustJSON(ke),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceKubeEnvironment, nativeID))
			}
			return batch, pairs
		})
}

// scanStaticSitesChain discovers Static Web Apps (Microsoft.Web/staticSites) then
// fans out to scan their builds (Microsoft.Web/staticSites/builds).
func scanStaticSitesChain(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewStaticSitesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewStaticSitesClient: %w", err)
	}

	var (
		batch   []*store.Resource
		pairs   [][2]string
		entries []staticSiteEntry
	)

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:StaticSites.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:StaticSites.List: %w", err)
		}
		for _, ss := range page.Value {
			if ss.ID == nil {
				continue
			}
			name := sv(ss.Name)
			location := sv(ss.Location)
			nativeID := sv(ss.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceStaticSite,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(ss),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(ss.Tags)
			discoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceStaticSite, nativeID))
			entries = append(entries, staticSiteEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure Static Sites: %w", err)
		}
		total += len(batch)
		inserted += n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure Static Sites: %w", err)
		}
	}
	if len(entries) == 0 {
		return total, inserted, nil
	}

	// Fan out build scans per static site.
	var (
		mu                sync.Mutex
		bTotal, bInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, ss := range entries {
		entry := ss
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, n, e := scanStaticSiteBuilds(gCtx, sub, client, st, scanID, entry)
			if e != nil {
				return e
			}
			mu.Lock()
			bTotal += t
			bInserted += n
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + bTotal, inserted + bInserted, nil
}

// scanStaticSiteBuilds lists builds (Microsoft.Web/staticSites/builds) for one static site.
func scanStaticSiteBuilds(ctx context.Context, sub *subscription, client *armappservice.StaticSitesClient, st *store.Store, scanID string, ss staticSiteEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string

	pager := client.NewGetStaticSiteBuildsPager(ss.rg, ss.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armappservice:StaticSites.GetStaticSiteBuilds", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armappservice:StaticSites.GetStaticSiteBuilds %s/%s: %w", ss.rg, ss.name, err)
		}
		for _, b := range page.Value {
			if b.ID == nil {
				continue
			}
			name := sv(b.Name)
			nativeID := sv(b.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeAppServiceStaticSiteBuild,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
			}
			buildID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			// build → parent static site hierarchy
			pairs = append(pairs, [2]string{buildID, ss.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure Static Site builds %s: %w", ss.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure Static Site builds %s: %w", ss.name, err)
		}
	}
	return total, inserted, nil
}

// scanCertificates discovers App Service TLS certificates (Microsoft.Web/certificates).
func scanCertificates(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappservice.NewCertificatesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappservice:NewCertificatesClient: %w", err)
	}
	return azPageScan(ctx, "armappservice:Certificates.List", sub, st,
		client.NewListPager(nil),
		func(page armappservice.CertificatesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, c := range page.Value {
				if c.ID == nil {
					continue
				}
				name, loc, nativeID := sv(c.Name), sv(c.Location), sv(c.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAppServiceCertificate, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(c.Tags), AttributesJSON: mustJSON(c),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeAppServiceCertificate, nativeID))
			}
			return batch, pairs
		})
}
