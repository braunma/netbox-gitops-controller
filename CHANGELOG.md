# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Because this controller can **delete objects from a live NetBox** when run with
`--prune`, treat this file as an upgrade safety document rather than
bookkeeping: read the entries between the version you last ran and the one you
are about to run before applying a sync with pruning enabled.

## [Unreleased]

### Added

- **Community device type library support.** Device types published in the
  community library layout (`<library>/<Manufacturer>/<model>.yaml`, one
  document per file, hyphenated component keys) can now be consumed unchanged.
  Point `--devicetype-library` or `DEVICETYPE_LIBRARY` at a library checkout,
  or drop files into `definitions/device_type_library/`. Library entries are
  merged with natively defined device types; a local definition with the same
  slug wins and the override is logged. Library files go through the same
  model validation as native ones, multi-document files are read in full,
  empty files are skipped, and two library files claiming the same slug are
  rejected rather than resolved by directory walk order.
- **Device type component templates** for console ports, console server ports,
  power ports and power outlets, plus the `part_number`, `airflow`,
  `description`, `comments` and `weight`/`weight_unit` fields. Power ports are
  reconciled before power outlets so an outlet can resolve the port that feeds
  it.
- **Multi-position patch panel ports.** Rear ports accept `positions` and
  front ports accept `rear_port_position`, so a breakout panel is no longer
  flattened to a single position per port. Both default to 1, and both are
  applied correctly on NetBox 4.6+ (see the front port fix below). Verified
  against NetBox 4.6.7.
- **NetBox version check.** The controller queries `/api/status/` on startup,
  logs the detected release, and refuses to run against a NetBox older than
  3.6 — the release that renamed the device `device_role` field to `role`.
  A server whose version cannot be determined is reported as a warning and the
  run continues.
- **Ignored files.** Files matching an ignore pattern are skipped while
  loading, so inventory owned by another system can stay in the repository
  without being applied. Defaults to `_*.yaml` and `_*.yml`; override with
  `--ignore-file` or `IGNORED_FILES` (comma-separated globs matched against
  the filename), and load everything with `--include-ignored-files`. Every
  skipped file is logged, and a run that both skips files and uses `--prune`
  warns with the list before deleting anything.

  > **Upgrade note:** the `_*` default is new. If your repository already
  > contains YAML files whose names start with an underscore, they will no
  > longer be applied. Objects this controller previously created from such a
  > file still carry the managed tag and would be deleted as orphans by
  > `--prune`. Check with `--dry-run --prune` before the first pruning run
  > after upgrading, or set `IGNORED_FILES` to a pattern that does not match
  > them.
- **Shared test fixture factories** for the reconciler suite
  (`pkg/reconciler/fixtures_test.go`), so tests state only the fields they are
  actually about.
- This changelog.

### Fixed

- **Front ports are wired correctly on NetBox 4.6+.** That release replaced a
  front port's singular `rear_port`/`rear_port_position` fields with a
  `rear_ports` list of `{position, rear_port, rear_port_position}` mappings,
  and accepts the old fields silently instead of rejecting them — so front
  ports were created unwired and re-`PATCH`ed on every run. The shape is now
  chosen from the release reported by `/api/status/`, and the mappings are
  compared as an order-insensitive set so they stay idempotent.
- **A device in a bay is no longer given a rack.** NetBox derives a
  bay-mounted device's location from its parent, and installing it into the
  bay clears `rack`/`position`/`face` — so a blade that inherited `rack_slug`
  from its file's `defaults` had the rack set on every run and cleared again
  on every install.
- **`rack_slug` now resolves.** NetBox racks have no slug of their own — they
  are identified by name within a site — so the YAML slug only ever existed
  locally and nothing registered it. Every device was silently created with no
  rack, position or face, on every run. Racks are now registered site-scoped
  under their YAML slug, and a `rack_slug` that still cannot be resolved is
  reported instead of dropped in silence.
- **`--dry-run` works against an empty NetBox.** An object planned but never
  written is registered with id 0, and NetBox rejects `site_id=0` as an
  invalid choice rather than returning no results — so the first dry-run, the
  run the README tells you to start with, aborted. A lookup scoped by a
  reference that does not exist yet cannot match anything, so it is now
  treated as a create instead of being sent to the API.
- **VRF-scoped prefixes are no longer created twice.** The global cache is
  loaded once, before any phase runs, and `ReconcileVRFs` never registered the
  VRFs it created — so on a fresh NetBox every prefix referencing a VRF was
  created in the global table, and the next run created a second, correctly
  scoped copy beside it. The stale copy persisted until a `--prune` run reaped
  it. VRFs are now registered on creation, as sites, roles and device types
  already were. Found running against a live NetBox 4.6.7.
- **Component templates are no longer re-`PATCH`ed on every run.** The four new
  template endpoints (console port, console server port, power port, power
  outlet) were not registered as untaggable, so the managed tag was injected
  into payloads NetBox silently drops. The tag never came back, the field
  always looked changed, and the diff was invisible because `tags` is hidden
  from diff output. Found by running against a real NetBox 4.6.7.
- **A device type declaring `device_bays` without `subdevice_role: parent`** is
  now rejected by model validation. NetBox refuses the bay with a message that
  names the constraint but not the fix, after the device type itself has
  already been written. The bundled example chassis had this defect and could
  never have been applied to a real NetBox.
- **Decimal fields no longer produce a phantom update on every run.** NetBox
  renders decimal fields (`u_height`, `weight`) as JSON strings when its
  serializer coerces decimals, while the payload carries a number, so the two
  never compared equal and every affected device type was re-`PATCH`ed on
  every sync. Values are now compared numerically when one side is a number
  and the other is a string that cleanly parses as one.
- **Parent devices are reconciled before their children.** A device with
  `parent_device` is installed into a bay on that parent, which is resolved
  with a live NetBox lookup. Devices were previously reconciled in the order
  the loader read the files (lexical by filename, then by position within each
  file), so a child that happened to be listed first aborted the run with
  `parent device X not found`. Devices are now topologically sorted, so a
  parent always precedes its children at any nesting depth, and file layout no
  longer matters. A `parent_device` cycle is reported before any change is
  applied.

### Changed

- `DeviceType.UHeight` is now a decimal rather than an integer, so half-height
  (0.5U) device types are represented correctly. Existing whole-number
  definitions are unaffected.
- The phase order and the two intra-phase orderings (cables applied in a
  second pass, parents before children) are documented in the README, and the
  forward phase order is now pinned by a test alongside the existing prune
  order test.
