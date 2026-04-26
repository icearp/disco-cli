package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
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
			mu         sync.Mutex
			batch      []*store.Resource
			grantBatch []*store.Resource
			grantPairs [][2]string
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
				} else if !isAPIErrorCode(rerr, "UnsupportedOperationException") && !isAccessDenied(rerr) {
					return fmt.Errorf("kms:GetKeyRotationStatus %s: %w", sv(md.KeyId), rerr)
				}

				enabled := "Disabled"
				if md.Enabled {
					enabled = "Enabled"
				}
				keyARN := sv(md.Arn)
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeKMSKey,
					NativeID:       keyARN,
					Name:           md.KeyId,
					Region:         &region,
					CreatedAt:      tp(md.CreationDate),
					Status:         &enabled,
					AttributesJSON: mustJSON(attrs),
					DiscoveredBy:   scanID,
				}
				keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, keyARN)

				// ListGrants per key — grants authorize principals outside the key
				// policy. AccessDenied per-key tolerated so one locked-down key
				// doesn't fail the scan.
				gpager := kms.NewListGrantsPaginator(client, &kms.ListGrantsInput{KeyId: md.KeyId})
				var grants []*store.Resource
				var pairs [][2]string
				for gpager.HasMorePages() {
					gpage, gerr := gpager.NextPage(gctx)
					if gerr != nil {
						if isAccessDenied(gerr) {
							break
						}
						return fmt.Errorf("kms:ListGrants %s: %w", sv(md.KeyId), gerr)
					}
					for _, ge := range gpage.Grants {
						// KMS grants have no AWS-issued ARN. Synthesize a stable
						// NativeID under the key's ARN so ResourceID + hierarchy
						// are deterministic across rescans.
						gid := sv(ge.GrantId)
						if gid == "" {
							continue
						}
						arn := keyARN + "/grant/" + gid
						name := ge.Name
						if name == nil || *name == "" {
							name = ge.GrantId
						}
						gr := &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeKMSGrant,
							NativeID:       arn,
							Name:           name,
							Region:         &region,
							CreatedAt:      tp(ge.CreationDate),
							AttributesJSON: mustJSON(ge),
							DiscoveredBy:   scanID,
						}
						grantID := store.ResourceID("aws", acct.ID, TypeKMSGrant, arn)
						grants = append(grants, gr)
						pairs = append(pairs, [2]string{grantID, keyID})
					}
				}

				mu.Lock()
				batch = append(batch, r)
				grantBatch = append(grantBatch, grants...)
				grantPairs = append(grantPairs, pairs...)
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
		if len(grantBatch) > 0 {
			n, err := st.UpsertResources(grantBatch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert KMS grants: %w", err)
			}
			total += len(grantBatch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(grantPairs); err != nil {
				return 0, 0, fmt.Errorf("closure KMS grants: %w", err)
			}
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
