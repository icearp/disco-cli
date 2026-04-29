# CLAUDE.md — `internal/rules/`

YAML or built-in rules evaluated against store by `cmd/check.go`. Rules filter `resources`, emit `Finding`s with severity. Seed rules in `builtin.go`: public S3, unencrypted EBS, SGs open to `0.0.0.0/0:22`, stale IAM keys. Extend by adding to `builtin.go` or author YAML + pass `--rules path.yaml`.

## Graph traversal in rules

A rule's `Match` may carry a `Related` block to require an outbound or inbound edge to a target matching a nested `Match`:

```yaml
match:
  type: aws:ec2:instance
  related:
    direction: out          # 'out' (default) walks RelationshipsFrom; 'in' walks RelationshipsTo
    kinds: [uses]           # optional edge-kind filter — empty means any kind
    target:
      type: aws:ec2:security-group
      where:
        - path: GroupName
          op: eq
          value: open
```

Single-hop only at each level — multi-hop is achieved by nesting `Related` inside `Target`. Recursion is bounded only by file-load (no runtime guard), so deeply nested DSL graphs blow the stack — keep depth ≤4. Eval logic: `evalRelated` in `eval.go`. Direction validation lives in `validateMatch`.

## Rule metadata fields (tags / category / remediation / ref_url)

Rules carry optional `tags: map[string][]string`, `category`, `remediation`, `ref_url` for compliance-pack mapping and human reporting. All four propagate onto `Finding`. `disco check --tag k=v` (or bare `--tag k`) filters findings post-eval — multiple `--tag` flags are OR'd. Use `tags` namespaces consistently across packs (`cis-aws`, `nist-800-53`, `pci`, `owasp`) so cross-pack queries work.
