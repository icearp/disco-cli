package gcp

import (
	"context"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/widgets/v1"
	beta "google.golang.org/api/widgets/v1beta1"
)

func scanWidgets(ctx context.Context, parent string, st *store.Store) error {
	svc, err := widgets.NewService(ctx)
	if err != nil {
		return err
	}
	err = svc.Projects.Locations.Widgets.List(parent).Pages(ctx, func(page *widgets.ListWidgetsResponse) error {
		for _, w := range page.Widgets {
			st.Put(&store.Resource{Type: TypeWidget, NativeID: w.Name})
		}
		return nil
	})
	if err != nil {
		return skipIfDenied(st, "widgets:widgets.list", err)
	}
	return scanParts(ctx, svc, parent, st)
}

func scanParts(ctx context.Context, svc *widgets.Service, parent string, st *store.Store) error {
	resp, err := svc.Projects.Locations.Widgets.Parts.List(parent).Do()
	if err != nil {
		return skipIfDenied(st, "widgets:projects.locations.widgets.parts.list", err)
	}
	for _, p := range resp.Parts {
		st.Put(&store.Resource{Type: TypePart, NativeID: p.Name})
	}
	return nil
}

// describeWidget reads one widget: a non-lister op.
func describeWidget(ctx context.Context, svc *widgets.Service, name string, st *store.Store) error {
	w, err := svc.Projects.Locations.Widgets.Get(name).Do()
	if err != nil {
		return err
	}
	st.Put(&store.Resource{Type: TypeWidgetDetail, NativeID: w.Name})
	return nil
}

type gizmoScan struct {
	svc *widgets.Service
	st  *store.Store
}

func (s *gizmoScan) scanGizmos(ctx context.Context, project string) error {
	err := s.svc.Gizmos.AggregatedList(project).Pages(ctx, func(page *widgets.GizmoAggregatedList) error {
		for _, g := range page.Items {
			s.st.Put(&store.Resource{Type: TypeGizmo, NativeID: g.Name})
		}
		return nil
	})
	if err != nil {
		return skipIfDenied(s.st, "widgets:gizmos.aggregatedList", err)
	}
	return nil
}

// run hands scanZone to a generic driver as a method value.
func (s *gizmoScan) run(ctx context.Context, projects []string) error {
	if err := forEachItem(ctx, projects, s.scanZone); err != nil {
		return skipIfDenied(s.st, "widgets:zones.list", err)
	}
	return nil
}

func (s *gizmoScan) scanZone(ctx context.Context, project string) error {
	resp, err := s.svc.Zones.List(project).Do()
	if err != nil {
		return err
	}
	for _, z := range resp.Items {
		s.st.Put(&store.Resource{Type: TypeZone, NativeID: z.Name})
	}
	return nil
}

func scanBuckets(ctx context.Context, svc *widgets.Service, project string, st *store.Store) error {
	resp, err := svc.Buckets.List(project).Do()
	if err != nil {
		return err
	}
	for _, b := range resp.Items {
		st.Put(&store.Resource{Type: TypeBucket, NativeID: b.Name})
	}
	return nil
}

func scanGadgets(ctx context.Context, parent string, st *store.Store) error {
	svc, err := beta.NewService(ctx)
	if err != nil {
		return err
	}
	return svc.Projects.Locations.Gadgets.List(parent).Pages(ctx, func(page *beta.ListGadgetsResponse) error {
		for _, g := range page.Gadgets {
			st.Put(&store.Resource{Type: TypeGadget, NativeID: g.Name})
		}
		return nil
	})
}

func staleLabel(st *store.Store) error {
	return skipIfDenied(st, "widgets:notifications.list", nil)
}

func forEachItem(ctx context.Context, items []string, f func(context.Context, string) error) error {
	for _, it := range items {
		if err := f(ctx, it); err != nil {
			return err
		}
	}
	return nil
}

func skipIfDenied(st *store.Store, op string, err error) error { return err }
