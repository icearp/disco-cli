package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func init() {
	// SageMaker emits are declared per family file via registerExtraEmits —
	// the scanSageMaker dispatcher itself upserts no resources, only fans
	// out to family scanners (studio, training, inference, monitoring, …).
	registerService(serviceEntry{name: "aws:sagemaker", fn: scanSageMaker})
}

// scanSageMaker discovers all SageMaker resource types in one region by
// running family scanners concurrently. Each family scanner fans out to
// its own per-resource sub-phases via an internal errgroup.
func scanSageMaker(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sagemaker.NewFromConfig(acct.cfg, func(o *sagemaker.Options) { o.Region = region })
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
	)
}
