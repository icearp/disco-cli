package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func init() {
	// SageMaker emits are declared per family file via registerExtraEmits —
	// the scanSageMaker dispatcher itself upserts no resources, only fans
	// out to family scanners (studio, training, inference, monitoring, …).
	registerService(serviceEntry{name: "aws:sagemaker", fn: scanSageMaker})
}

// sagemakerInUseProbe checks whether any of the three highest-signal SageMaker
// surfaces are non-empty: Studio domains (modern entry), notebook instances
// (legacy entry), and inference endpoints (deployed models). Every full
// SageMaker scan triggers ~42 ListXxx calls across 8 families; under SageMaker's
// low per-account TPS quota the SDK adaptive retry token bucket throttles for
// minutes. Probing first short-circuits dormant accounts to ~3 RPCs instead of
// dozens. False negatives possible (e.g. training-jobs-only accounts) but
// acceptable trade-off for the wall-time win.
func sagemakerInUseProbe(ctx context.Context, client *sagemaker.Client) (bool, error) {
	one := sdkaws.Int32(1)
	if d, err := client.ListDomains(ctx, &sagemaker.ListDomainsInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(d.Domains) > 0 {
		return true, nil
	}
	if n, err := client.ListNotebookInstances(ctx, &sagemaker.ListNotebookInstancesInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(n.NotebookInstances) > 0 {
		return true, nil
	}
	if e, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{MaxResults: one}); err != nil {
		return false, err
	} else if len(e.Endpoints) > 0 {
		return true, nil
	}
	return false, nil
}

// scanSageMaker discovers all SageMaker resource types in one region by
// running family scanners concurrently. Each family scanner fans out to
// its own per-resource sub-phases via an internal errgroup.
func scanSageMaker(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sagemaker.NewFromConfig(acct.cfg, func(o *sagemaker.Options) { o.Region = region })

	// Phase 0: cheap probe. Skip the full 42-phase fan-out on dormant accounts.
	// Probe errors propagate to the dispatch-level transient/access-denied
	// helpers — no special handling here.
	inUse, perr := sagemakerInUseProbe(ctx, client)
	if perr != nil {
		if isAccessDenied(perr) {
			return 0, 0, skipIfAccessDenied(st, "sagemaker:probe", acct.ID, region, perr)
		}
		// Non-access-denied probe errors fall through to full scan; the family
		// scanners have their own per-call tolerance and may still succeed.
	} else if !inUse {
		return 0, 0, nil
	}
	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerStudio(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerTraining(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerInference(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerRegistry(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerMonitoring(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerPipelines(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerEdge(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSageMakerMisc(ctx, client, acct, region, st, scanID)
		},
	)
}
