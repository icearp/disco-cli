package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

// scanBackupLegalHolds stores the legal holds, account- and region-wide,
// with their tags.
func scanBackupLegalHolds(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "backup:ListLegalHolds",
		func(ctx context.Context, token *string) ([]backuptypes.LegalHold, *string, error) {
			out, err := client.ListLegalHolds(ctx, &backup.ListLegalHoldsInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.LegalHolds, out.NextToken, nil
		},
		func(h backuptypes.LegalHold) (*store.Resource, string, string) {
			arn := sv(h.LegalHoldArn)
			if arn == "" {
				return nil, "legal holds have no LegalHoldArn", cmp.Or(sv(h.Title), sv(h.LegalHoldId), bkNoName)
			}
			label := cmp.Or(sv(h.Title), arn)
			r := bkRow(acct, region, scanID, TypeBackupLegalHold, arn, &label, h)
			r.CreatedAt = tp(h.CreationDate)
			return r, "", ""
		}, tags, p)
}

// backupRecoveryPointTags is tags for recovery points: only a recovery point
// whose ARN is a Backup ARN is read. ListTags documents that an ARN of any
// other service (a recovery point of a resource without full AWS Backup
// management: an EBS snapshot, an RDS snapshot, an AMI) may be refused. Such
// a row is stored without tags and no call is made; the backup's tags, if
// any, are on that service's own resource, which this scanner does not read.
//
// A Backup ARN ListTags refuses with InvalidParameterValueException is
// tallied in refused and its row kept untagged, not failed: the ListTags doc
// says that error is returned for an ARN it does not support, and whether it
// supports the recovery points of every resource type in a logically
// air-gapped vault (all with Backup ARNs) is not verified live. The tally
// names the smallest ARN first (skippedLeast), so the warning does not depend
// on which concurrent read finished first.
func backupRecoveryPointTags(tags rowTagReader, refused *skipTally) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if !isARNOf(r.NativeID, "backup") {
			return nil, nil
		}
		t, err := read(ctx, r)
		if isAPIErrorCode(err, "InvalidParameterValueException") {
			refused.skippedLeast(r.NativeID, err)
			return nil, nil
		}
		return t, err
	}
	return tags
}

// scanBackupRecoveryPoints stores the recovery points of each vault the vault
// phase stored, with their tags (storeEachParentPagesTags). A vault
// ListRecoveryPointsByBackupVault no longer finds was deleted since it was
// listed and is skipped. A vault may hold tens of thousands, so each page is
// written once its tags are read rather than held for the whole phase.
func scanBackupRecoveryPoints(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, vaults []childParent, tags rowTagReader, p *phaseTotals) bool {
	var refused skipTally
	failed := storeEachParentPagesTags(ctx, st, acct, region, "backup:ListRecoveryPointsByBackupVault", backupNotFound, vaults,
		func(ctx context.Context, vault string, token *string) ([]backuptypes.RecoveryPointByBackupVault, *string, error) {
			out, err := client.ListRecoveryPointsByBackupVault(ctx, &backup.ListRecoveryPointsByBackupVaultInput{
				BackupVaultName: sdkaws.String(vault), NextToken: token, MaxResults: sdkaws.Int32(backupPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RecoveryPoints, out.NextToken, nil
		},
		func(vault string, rp backuptypes.RecoveryPointByBackupVault) (*store.Resource, string, string) {
			arn := sv(rp.RecoveryPointArn)
			if arn == "" {
				return nil, "recovery points have no RecoveryPointArn", cmp.Or(sv(rp.ResourceName), "in vault "+vault)
			}
			label := cmp.Or(sv(rp.ResourceName), arn)
			r := bkRow(acct, region, scanID, TypeBackupRecoveryPoint, arn, &label, rp)
			r.CreatedAt = tp(rp.CreationDate)
			return r, "", ""
		}, backupRecoveryPointTags(tags, &refused), p)
	if !failed && refused.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: tags.op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d recovery point(s) stored without tags: ListTags refused their ARN; first %s: %v", refused.count, refused.first, refused.err),
		})
	}
	return failed
}
