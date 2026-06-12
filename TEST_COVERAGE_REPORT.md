# Test Coverage & Validation Report

**Generated:** 2026-06-12
**Branch:** `claude/test-coverage-analysis-we2ije`
**Status:** ✅ All Tests Passing (race detector enabled)

---

## Executive Summary

Total statement coverage across `./pkg/...` is **73.4%** (up from 19.8%).
All 96 test functions pass with `-race`. CI enforces a minimum coverage of
**65%** via the `COVERAGE_THRESHOLD` variable in `.gitlab-ci.yml` — raise it
as coverage improves.

> This report is a manual snapshot. For live numbers run:
> `go test ./pkg/... -coverprofile=coverage.out && go tool cover -func=coverage.out`

---

## Coverage by Package

| Package | Coverage | Test Functions |
|---------|----------|----------------|
| `pkg/models` | **100.0%** | 20 |
| `pkg/reconciler` | **80.1%** | 42 |
| `pkg/loader` | **76.2%** | 4 |
| `pkg/utils` | **68.1%** | 8 |
| `pkg/client` | **51.7%** | 22 |
| `cmd/*` | 0% | 0 |

`pkg/client`'s number understates real coverage: the reconciler suite drives
the client (including the cache and tag managers) end-to-end, but cross-package
execution does not count toward `pkg/client`'s own percentage.

## What Is Covered

### pkg/models
- Every `Validate()` implementation (required fields, VID bounds,
  `min_vid`/`max_vid`, `parent_device`⇄`device_bay` pairing, nested
  interface/port/module checks, link validation, error aggregation).
- Slug generation and model structure tests.

### pkg/client
- `Apply()` create/update/no-op paths, managed-tag injection, partial PATCH
  of only changed fields, nested-reference ID comparison, missing-ID and
  filter-failure error paths.
- **Dry-run safety:** `--dry-run` sends zero mutating requests (asserted at
  the HTTP layer) while reads still work; `SetDryRun` toggling.
- Pagination following, page-size override, retry/backoff rules
  (no retry on 4xx or POST 5xx), diff calculation, tag comparison.

### pkg/reconciler
All flow tests run against an in-memory fake NetBox API
(`fakenetbox_test.go`) with the real client, cache, and tag bootstrap:
- **Foundation:** site create → no-op → partial update; rack site resolution
  (slug, name fallback, skip-on-missing); role/tag color normalization.
- **Network:** VRF/VLAN-group/VLAN/prefix reconciliation with global and
  site-scoped cache lookups; second-run idempotency.
- **Device types:** manufacturer auto-creation; rear-before-front template
  ordering; front ports resolving rear-port template IDs; full-tree
  idempotency.
- **Devices:** end-to-end create with rack placement, interfaces,
  site-scoped VLANs, IP assignment and primary-IP; child-into-device-bay
  installation (detach → install); device-bay self-healing from templates;
  module installation incl. skip rules; role-based cable endpoint selection
  (interface vs front port vs rear port backbone); missing-peer skip.
- **Cables:** creation with normalized colors; idempotency in both
  directions; config updates; forced deletion of wrong/blocking cables on
  local and peer ports; dry-run issues zero mutations even in conflict
  scenarios.

### pkg/loader / pkg/utils
- YAML loading of all definition and inventory types, validation hook,
  slugify, ID extraction, tag helpers, color helpers.

## Known Gaps

- `cmd/netbox-gitops` and `cmd/yamlcheck` (CLI wiring) are untested.
- `pkg/client` cache/tag managers are only exercised indirectly via the
  reconciler suite.
- Loader error paths (malformed YAML, unreadable directories) are partially
  covered.
- `setPrimaryIP` PATCHes the device on every run even when the primary IP
  is already correct (documented by `TestReconcileDevicesFullFlow`).

## Notable Fix Found by Tests

`reconcileDevice` previously reconciled front ports **before** rear ports,
so a front port referencing a YAML-defined rear port on the same device was
skipped on the first run. The order now matches the device-type template
ordering (rear ports first).
