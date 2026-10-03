---
paths:
  - "**/*_test.go"
description: "What makes a test worth keeping — the quality bar for every test in this repo. Mechanics live in the §Testing notes in internal/providers/CLAUDE.md, store/CLAUDE.md, cmd/CLAUDE.md; this is the judgment layer."
---

# Testing quality

A test is a *claim about behavior that fails loudly when the behavior breaks*. If it does not
do that, it is a liability — it slows changes, trains people to ignore red, and gives false
confidence. Use the standard Go idioms — table-driven cases, `t.Run` subtests, `t.Helper`,
`t.Cleanup`, stdlib `testing` only (no assertion library) — and name tests `TestX_Scenario`.
The disco-specific test patterns —
`newTestStore`/`upsertTestResource`, hierarchy seeding, fake servers, registration tests —
are in [internal/providers/CLAUDE.md](../../internal/providers/CLAUDE.md) §Testing,
[store/CLAUDE.md](../../store/CLAUDE.md), and [cmd/CLAUDE.md](../../cmd/CLAUDE.md). This file
is the bar those mechanics serve.

## A good test

- **Tests a contract, not an implementation.** Assert observable outcomes — return values,
  sentinel errors, stored resource rows, graph edges (`RelationshipsFrom`), CLI JSON output —
  never internal call sequences or private fields. It should survive a refactor that
  preserves behavior.
- **Has one reason to fail, named.** One behavior per test/subtest; the name says *what* under
  *what condition* (`TestResolveACMCertificateRelationships_PrivateCA`). A red run should
  localize the break without reading the body.
- **Asserts the negative space.** Not only "the edge I wanted is present" but "the edge that
  must NOT exist is absent" and "siblings were untouched." A **public** ACM cert must produce
  *no* `uses` edge to a private CA; a resolver must never leak a reversed `contains` edge
  (`store.ReversedContainsEdges`, wired into `newTestStore`'s `t.Cleanup`). A happy-path-only
  resolver test is half a test.
- **Is deterministic and hermetic.** No `time.Sleep` races, real network, unseeded randomness,
  or dependence on test order / map-iteration order / ambient DB state. Self-seeds its
  fixtures (unique temp DB via `newTestStore`/`openTestStore`/`seedTestDB`), tears down via
  `t.Cleanup`. Two runs in any order give the same result.
- **Exercises the real store path.** Prefer the real SQLite store
  (`newTestStore`/`openTestStore`/`seedTestDB`) over mocks for data-layer behavior — the
  behavior under test usually *is* the SQL + edge writes. For cloud SDK calls, prefer a
  realistic fake (`httptest` fake server, GCP `fakeGCPServer`; AWS Smithy middleware stubs
  `stubResponses`) over a calls-asserting interface mock, so error classification, pagination,
  and retry still run. A store method with hand-written SQL needs `withDialects`, not
  `openTestStore`: SQLite is the permissive dialect, so check the `/postgres` subtest PASSed
  rather than skipped (`store/CLAUDE.md`).
- **Fails first.** A test you have never seen fail proves nothing. Before relying on it, break
  the assertion or the code once and confirm red → green.
- **Reports got-vs-want with the input.** `t.Errorf("X(%q) = %v; want %v", in, got, want)`.
- **Is proportional to risk.** Pure logic (ARN/armid parse, `classify*`, coverage) → fast
  table unit test, no DB. Resolver edge wiring or a store invariant → integration test against
  the real SQLite store. Match the test's cost to the cost of the bug it prevents.

## Not a good test

- **A change-detector.** Snapshotting implementation: exact internal call order, private
  state, or byte-equality of CLI output / generated SQL. Assert the semantic outcome — the
  resource rows, the graph edges, the decoded JSON envelope (`json.Unmarshal`, not byte
  compare) — not the bytes.
- **A tautology / coverage theater.** Re-deriving the SUT's logic in the test, asserting
  `x==x`, or calling a function only to move the coverage number while asserting nothing but
  "no error" when the edge or row is the point. If the test would pass against a stub
  returning its input, it tests nothing.
- **Happy-path-only where a negative case is the contract.** Missing the empty-/no-attrs case,
  the public-resource-produces-no-edge case, or the bad-input case.
- **Flaky.** A sometimes-red test is worse than none. Fix or quarantine; never merge
  known-flaky.
- **Over-mocked.** Mocking the store / SQLite you actually need to exercise. Mock only true
  externalities (the cloud SDK endpoints) — and prefer a fake server (`fakeGCPServer`,
  `httptest.NewServer`) or the Smithy middleware stub (`stubResponses`) over a calls-asserting
  interface mock.
- **Coupled to incidental detail.** Hard-coded generated IDs, the order of an unordered query,
  or substring matches on error *prose* (copy is not a contract). Match error identity via
  `errors.Is` / sentinels, not error-string substring match.

## What to assert, by layer

- **Pure function** (`aws` ARN helpers, `azure` armid parsers, `classify*`, coverage):
  inputs→outputs table incl. boundary cases, zero values, unknown/empty input → safe default.
- **Store / data layer** (`store/*_test.go`): upsert round-trip survives write→read
  (`TestUpsertResources_RoundTrip`), `RelationshipsFrom` returns the expected edges,
  hierarchy closure pairs, time-filter, read-only mode; assert negative space (no
  flipped/reversed edges); `ResourceID` algorithm stability (`TestResourceID_Algorithm`).
- **Resolver** (`internal/providers/*/*_resolvers_test.go`): the positive edge is present
  **and** the empty-/no-attrs case is covered (guards nil-pointer panics on missing JSON
  fields). Pass `region` to `upsertTestResource` when the resolver builds ARNs — omitting it
  points computed IDs at phantom resources. Seed `RecordHierarchyBatch` pairs the production
  scanner emits when the resolver depends on the closure.
- **Scanner / registration / coverage**: `httptest` fake or middleware stub for SDK calls;
  `<p>_redact_test.go` asserts sensitive fields come back `[REDACTED]`; `<p>_scanner_test.go`
  `expected{AWS,Azure,GCP}Services` list updated for a new service; `emits []coverage.TypeDecl`
  declared via `registerType` (guarded by `TestEveryTypeConstantIsUsed`, the pairing tests and
  `TestEveryEmittedTypeHasScannerService`).
- **cmd** (`cmd/*_test.go`): seed via `seedTestDB`; capture output via `captureStdout`; reset
  pflag state (`resetCheckFlags`/`resetCoverageFlags`) before each `Execute()` (pflag
  accumulates across calls); assert JSON via `json.Unmarshal`, not byte equality.

## Regression ratchets (structural guard tests, not a coverage mandate)

disco has no per-package coverage floor. Its safety net is **structural guard tests** that
fail the moment an invariant drifts — keep them green, and extend them when you add the
structure they guard:

- `TestEveryTypeConstantIsUsed` (`<provider>_types_test.go`) — AST-walks for `Type*`
  constants declared but referenced nowhere; catches retired-service cruft.
- `TestEveryEmittedTypePaired` (`<provider>_pairing_test.go`) — every emitted type pairs with
  an SDK call or an explained reason; `make check-coverage` ratchets the percentage.
- `<p>_scanner_test.go` `expected{AWS,Azure,GCP}Services` (plus `expectedAzureTenantServices`,
  `expectedGCPOrgServices`) — service registered but unlisted, or listed but unregistered, fails.
- `<p>_redact_test.go` — sensitive SDK fields come back `[REDACTED]`; an SDK field rename breaks
  it on `go mod tidy`, a cheap drift catch.
- the reversed-`contains` cleanup guard in `newTestStore` (`store.ReversedContainsEdges`) —
  every provider test guards against flipped graphs for free.
- `TestResourceID_Algorithm` — pins the ResourceID hash so the algorithm can't silently change.
- `make check-migrations` — SQLite↔Postgres single-tenant schema parity, drops included (CI `test` job).

100% line coverage is deliberately not the target: it is a Goodhart metric (certifies lines
executed, not invariants asserted), and the last mile is dominated by fault-injection-only
branches whose tests would violate the rules above. Cover *behavior*, not lines. If a
meaningful behavior is uncovered, that is a real gap — cover it. If the residual is a
defensive IO-error return reachable only via fault injection, it is a documented exclusion,
covered by inspection.

## Repo-specific hard rules

1. A new `<service>_resolvers.go` ships with a matching `<service>_resolvers_test.go`; a new
   service updates `<p>_scanner_test.go`'s expected list **and** declares `emits`.
2. Always pair the happy path with the **no-attrs / empty** case.
3. Build attrs via `json.Marshal` of the real SDK struct (or `marshalAttrs`, or the AWS
   `<svc><Resource>Attrs` builders in `aws_testhelpers_test.go`) — never hand-rolled JSON
   literals that silently drift from the SDK shape (Azure `arm*` types hide their JSON shape behind custom `MarshalJSON`).
4. Resolver tests pass `region` and seed the scanner's hierarchy pairs (see "What to assert,
   by layer" → Resolver).
5. Error identity via `errors.Is` / sentinels — never error-string substring match.
6. Real SQLite store for data-layer behavior; `httptest` / middleware fakes for cloud SDK
   calls; mocks only for true externalities. All tests build `CGO_ENABLED=0`, use unique temp
   DBs, tear down via `t.Cleanup`, and hold no cross-test state.
7. Prove red→green before claiming a test guards something.
