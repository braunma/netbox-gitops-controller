# Bug-Fix Record

Historical record of the correctness fixes applied to the Go implementation.
**All five are implemented and verified against the code (June 2026)**, as is
the one follow-up that was outstanding (see below). Open feature work lives in
`docs/MISSING_FEATURES.md`, the broader audit in `docs/AUDIT_AND_ROADMAP.md`.

Guiding principle at the time: none of these changed reconciliation decision
logic (especially cable reconciliation in `pkg/reconciler/cables.go`). They were
confined to the HTTP client, the loader and lookup plumbing, so behavior only
changed where it was wrong.

| # | Fix | Status | Where |
|---|-----|--------|-------|
| 1 | **API pagination** in `List()` — follow the `next` link (NetBox paginates at 50/default); previously only the first page was read, silently truncating the cache for any type with >50 objects. Now defaults to `limit=250`. | ✅ done | `pkg/client/client.go`, regression test `pkg/client/pagination_test.go` |
| 2 | **Retry/backoff** for transient failures — network errors, 429 and 5xx retried with exponential backoff (1s/2s/4s, ≤3 retries); 4xx (except 429) never retried, and POST never retried on a server response (no duplicate creates). | ✅ done | `pkg/client/client.go` (`doWithRetry`, `isRetryableStatus`) |
| 3 | **Post-unmarshal model validation** — every model has `Validate()`; the loader calls it after unmarshal and reports all errors before any API call (e.g. `device_bay` ↔ `parent_device` pairing, required slugs). | ✅ done (one gap) | `pkg/loader/loader.go`, `pkg/models/validate.go`, `validate_test.go` |
| 4 | **Cache API migration** — all reconcilers use explicit `GetGlobalID()`/`GetSiteID()`; the legacy collision-prone `CacheManager.GetID()` was removed. (`TagManager.GetID(slug)` is an unrelated tag-by-slug lookup.) | ✅ done | reconcilers, `pkg/client/cache.go` |
| 5 | **Pruning** — the README's "safe pruning" claim is now backed by an actual implementation (opt-in `--prune`, gitops-tagged orphans only). | ✅ done | `pkg/client/prune.go`, see `MISSING_FEATURES.md` |

**Closed (fix #3):** `cmd/yamlcheck` runs the same `Validate()` checks — it
loads every folder through the typed loader, which validates after unmarshal —
and since then also the cross-object checks in `pkg/lint`. See the README
section *Checking the YAML before it reaches NetBox*.

Also fixed along the way: the loader now accepts single-mapping YAML files (the
`example/definitions/device_types/*.yaml` files were previously unloadable), and
test fixtures were corrected (`u_height: 0` is valid for child device types;
NetBox stores cable colors as hex, not names).
