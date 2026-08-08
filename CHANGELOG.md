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
- **Multi-position patch panel rear ports.** Rear port templates accept
  `positions`, so a breakout panel's trunk is no longer flattened to a single
  position. Verified against NetBox 4.6.7.

  > **Known limitation:** the matching front-port side (`rear_port_position`)
  > is **not** applied on NetBox 4.6+. That release replaced the front port's
  > singular `rear_port`/`rear_port_position` fields with a `rear_ports` list,
  > and NetBox accepts the old fields silently instead of rejecting them — so
  > front ports are created but left unwired, and re-`PATCH`ed on every run.
  > This affects all front port handling (device types and device instances),
  > not only the new field. See "Front ports on NetBox 4.6+" in the README.
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
