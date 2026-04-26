# CLAUDE.md — `internal/rules/`

YAML or built-in rules evaluated against store by `cmd/check.go`. Rules filter `resources`, emit `Finding`s with severity. Seed rules in `builtin.go`: public S3, unencrypted EBS, SGs open to `0.0.0.0/0:22`, stale IAM keys. Extend by adding to `builtin.go` or author YAML + pass `--rules path.yaml`.
