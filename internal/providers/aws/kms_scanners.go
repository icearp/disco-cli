package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:kms", fn: scanKMS}) }

// kmsKeyAttrs is the stored attribute shape for a KMS key — DescribeKey metadata
// plus the key policy and rotation status, which each require their own API call.
// Rule predicates like "rotation disabled on customer key" read these fields.
type kmsKeyAttrs struct {
	Metadata           types.KeyMetadata
	Policy             *string
	KeyRotationEnabled *bool
}

// scanKMS discovers customer-managed KMS keys and aliases in one region. ListKeys
// returns KeyId only; DescribeKey + GetKeyPolicy + GetKeyRotationStatus fan out
// concurrently per page. AWS-managed keys are skipped — their policies are
// boilerplate and they can't be configured by the user.
func scanKMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kms.NewFromConfig(acct.cfg, func(o *kms.Options) { o.Region = region })

	pager := kms.NewListKeysPaginator(client, &kms.ListKeysInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kms:ListKeys", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kms:ListKeys: %w", err)
		}

		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, k := range page.Keys {
			g.Go(func() error {
				desc, err := client.DescribeKey(gctx, &kms.DescribeKeyInput{KeyId: k.KeyId})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("kms:DescribeKey %s: %w", sv(k.KeyId), err)
				}
				md := desc.KeyMetadata
				if md == nil {
					return nil
				}
				// Skip AWS-managed keys — not user-configurable and noise for rules.
				if md.KeyManager == types.KeyManagerTypeAws {
					return nil
				}

				policy, _ := client.GetKeyPolicy(gctx, &kms.GetKeyPolicyInput{
					KeyId:      md.KeyId,
					PolicyName: sp("default"),
				})
				attrs := kmsKeyAttrs{Metadata: *md}
				if policy != nil {
					attrs.Policy = policy.Policy
				}

				rot, rerr := client.GetKeyRotationStatus(gctx, &kms.GetKeyRotationStatusInput{KeyId: md.KeyId})
				if rerr == nil {
					b := rot.KeyRotationEnabled
					attrs.KeyRotationEnabled = &b
				} else if !isUnsupportedOp(rerr) && !isAccessDenied(rerr) {
					return fmt.Errorf("kms:GetKeyRotationStatus %s: %w", sv(md.KeyId), rerr)
				}

				enabled := "Disabled"
				if md.Enabled {
					enabled = "Enabled"
				}
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeKMSKey,
					NativeID:       sv(md.Arn),
					Name:           md.KeyId,
					Region:         &region,
					CreatedAt:      tp(md.CreationDate),
					Status:         &enabled,
					AttributesJSON: mustJSON(attrs),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if werr := g.Wait(); werr != nil {
			return 0, 0, werr
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert KMS keys: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	// Aliases: one region-wide ListAliases gets all of them.
	aliasPager := kms.NewListAliasesPaginator(client, &kms.ListAliasesInput{})
	for aliasPager.HasMorePages() {
		page, err := aliasPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "kms:ListAliases", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("kms:ListAliases: %w", err)
		}
		var batch []*store.Resource
		for _, a := range page.Aliases {
			if a.TargetKeyId == nil {
				continue
			}
			// Skip AWS-managed aliases (alias/aws/*) — they target AWS-managed
			// keys that the key scanner also skips, so the resolver would fail
			// the FK check against a non-existent target.
			if strings.HasPrefix(sv(a.AliasName), "alias/aws/") {
				continue
			}
			arn := sv(a.AliasArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeKMSAlias,
				NativeID:       arn,
				Name:           a.AliasName,
				Region:         &region,
				CreatedAt:      tp(a.CreationDate),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert KMS aliases: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// isUnsupportedOp reports whether err is KMS's UnsupportedOperationException,
// raised by GetKeyRotationStatus on asymmetric/imported keys.
func isUnsupportedOp(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() == "UnsupportedOperationException"
	}
	return false
}
