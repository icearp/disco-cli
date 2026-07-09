package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/binaryauthorization/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBinAuthPolicy, Service: "binaryauthorization", Upstream: "binaryauthorization.googleapis.com/Policy"})
	registerType(restype.Descriptor{Type: TypeBinAuthAttestor, Service: "binaryauthorization", Upstream: "binaryauthorization.googleapis.com/Attestor"})
	registerService(serviceEntry{
		name: "gcp:binaryauthorization",
		fn:   scanBinaryAuthorization,
	})
}

// scanBinaryAuthorization discovers the project's BinAuth policy (singleton
// per project — `projects/{p}/policy`) and any user-defined attestors. The
// policy is fetched via Get rather than List because there is no list
// surface — exactly one policy exists per project.
func scanBinaryAuthorization(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := binaryauthorization.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("binaryauthorization client: %w", err)
	}

	// Policy (singleton).
	policy, err := svc.Projects.GetPolicy(fmt.Sprintf("projects/%s/policy", p.ID)).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "binaryauthorization:projects.getPolicy", p.ID, err)
		}
		return 0, 0, err
	}
	pname := lastSegment(policy.Name)
	t, n, e := upsertWithProjClosure(p, st, []*store.Resource{{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           TypeBinAuthPolicy,
		NativeID:       policy.Name,
		Name:           &pname,
		AttributesJSON: mustJSON(policy),
		DiscoveredBy:   scanID,
	}})
	total += t
	inserted += n
	if e != nil {
		return total, inserted, e
	}

	// Attestors.
	at, an, aerr := runPaginated(ctx, st, p, "binaryauthorization:attestors.list",
		svc.Projects.Attestors.List(fmt.Sprintf("projects/%s", p.ID)),
		func(page *binaryauthorization.ListAttestorsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Attestors))
			for _, a := range page.Attestors {
				name := lastSegment(a.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBinAuthAttestor,
					NativeID:       a.Name,
					Name:           &name,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += at
	inserted += an
	return total, inserted, aerr
}
