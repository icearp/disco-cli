# AWS resolver gaps

Audit output: candidate `(source_type → target_type)` pairs where the source's
`AttributesJSON` carries an ARN/ID reference to a scanned target type but no
edge exists between them in `relationships`. Ranked by sample frequency.

## Regenerate

```bash
# 1. Populate disco.db with a representative scan (broad region coverage).
./disco scan aws --regions us-east-1,us-west-2,eu-west-1

# 2. Run the audit tool — sample size, top-N optional.
go run ./cmd/aws-resolver-audit --db ~/.local/share/disco/disco.db --top 100
```

Tool source: `cmd/aws-resolver-audit/main.go`. Output is TSV; pipe to `column
-t -s$'\t'` for human reading.

## Validation workflow

Each candidate pair below needs three checks before it counts as a real gap:

1. **Resolver search.** `grep -nE "Type<Source>.*Type<Target>|Type<Target>.*Type<Source>" internal/providers/aws/*resolvers*.go` — if a resolver names both types, the gap is likely a sample artifact (sampled rows happened to have nil refs, or target unscanned in this account).
2. **Skip-doc check.** `grep -i "<source>.*<target>" docs/aws-skip.md` — pair may be intentionally deferred.
3. **Synthetic-stub check.** Target type with `Synthetic: true` may already be covered via the cross-account / foreign-resource resolver pattern documented in `internal/providers/CLAUDE.md`.

Pairs surviving all three are implementable resolver work.

## Implementation precedents

When implementing a confirmed gap, start from the closest existing resolver:

| Target service | Helper / pattern                                            | Precedent file                              |
|----------------|--------------------------------------------------------------|---------------------------------------------|
| KMS            | `loadKMSResolveIndex` + `idx.resolveKMSKeyID`               | `internal/providers/aws/kms_helpers.go`     |
| IAM (any)      | `scannedIDSet` + `scannedIDsByRegion`                       | `internal/providers/aws/securityhub_resolvers.go` |
| EC2 IDs        | `loadSecurityGroupIndex`-style id-set lookup                | `internal/providers/aws/ec2_*_resolvers.go` |
| ECR via image  | `apprunnerImageToRepoARN`                                   | `internal/providers/aws/apprunner_resolvers.go` |
| Cross-acct     | Synthetic stub upsert pre-edge                              | `internal/providers/aws/iam_resolvers.go::resolveIAMRoleCrossAccountTrust` |

## Current candidates

Last refresh: 2026-05-02 against 4-region scan (us-east-1, us-west-2, eu-west-1,
ap-northeast-1; 3245 resources). **Zero open candidates.** Re-run after the
next broad scan to surface new gaps as scanner coverage grows.

### Closed (this session)

| Source | Target | Resolution |
|--------|--------|------------|
| `aws:iam:service-linked-role` → `aws:iam:role` | audit hardening: skip refs equal to source NativeID (Phase A self-ARN suppression) |
| `aws:iam:role` → `aws:iam:policy` | audit hardening: direction-blind diff — `policy → role` already emitted by `resolveManagedPolicyAttachments` |
| `aws:kms:grant` → `aws:kms:key` | audit hardening: direction-blind diff — `key → grant` hierarchy `contains` exists |
| `aws:iam:instance-profile` → `aws:iam:role` | covered by `resolveInstanceProfileRoles`; surfaced as sample artifact, cleared on re-scan |
| `aws:ec2:route-table` → IGW/NAT/TGW/peering/VPCE/instance | `resolveRouteTableRoutes` (`ec2_networking_resolvers.go`) walks `Routes[]` and dispatches per target field |
| `aws:ec2:security-group` → `aws:ec2:vpc` | `resolveSecurityGroupVPC` (`ec2_networking_resolvers.go`) reads `VpcId` |
| `aws:route53resolver:resolver-config` → `aws:ec2:vpc` | `resolveR53RResolverConfigVPC` (`route53resolver_resolvers.go`) — new file |
| `aws:route53resolver:resolver-rule-association` → `aws:ec2:vpc` + `:resolver-rule` | `resolveR53RResolverRuleAssoc` (`route53resolver_resolvers.go`) |
| `aws:ec2:network-acl` → `aws:ec2:subnet` | `resolveNetworkACLRelationships` extended to walk `Associations[].SubnetId` |

### Bonus shipped alongside r53r work

| Source | Target | Resolver |
|--------|--------|----------|
| `aws:route53resolver:firewall-rule-group-association` → `aws:ec2:vpc` | `resolveR53RFirewallRuleGroupAssoc` |

## Limitations

- **Sample frequency depends on scan breadth.** A pair surfacing once may be common in production accounts; broaden scan coverage (more regions, more services enabled) before dismissing low-freq rows.
- **ARN classification is heuristic.** `cmd/aws-resolver-audit/main.go::arnKindSuffix` ships a generic mapping; service-specific kind segments (e.g. SFN `stateMachine`, CloudWatch `alarm`, etc.) need entries when their pairs surface as `""` target types. Extend rather than rewrite.
- **Bare-ID detection is ARN-prefix-based.** Resources keyed by name only (DynamoDB tables, IAM roles when referenced by name) will not surface as gaps via Signal B — those pairs need ARN form in the source attrs to be detected.
- **Self-edges suppressed.** `(typeX → typeX)` pairs are dropped — peering, parent-child within same type, etc. Existing resolvers handle these.
