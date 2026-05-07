package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	// IoT emits are declared per family file via registerExtraEmits — the
	// scanIoT dispatcher upserts no resources, only fans out to family
	// scanners (things, certs/auth, defender, jobs, software, logging).
	registerService(serviceEntry{name: "aws:iot", fn: scanIoT})
}

// scanIoT discovers all IoT resource types in one region by running family
// scanners concurrently.
func scanIoT(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iot.NewFromConfig(acct.cfg, func(o *iot.Options) { o.Region = region })
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanIoTThings(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTCerts(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTDefender(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTJobs(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTSoftware(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTTopic(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIoTLogging(ctx, client, acct, region, st, scanID)
		},
	)
}
