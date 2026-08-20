## What this changes

<!-- One or two sentences. For an inventory change: which devices, and why. -->

## Checks

- [ ] `make check` passes (`gofmt`, `go vet`, licence headers, tests, and
      `yamlcheck` — syntax, models and the cross-object inventory checks)
- [ ] For a change to `definitions/` or `inventory/`: the dry-run plan below is
      what I expect, and nothing appears under "to delete" that I did not mean
      to remove

## Plan

<!--
For a data change, paste the plan. It is the review: a reviewer cannot tell
from YAML alone whether an edit creates one interface or replaces a device.

    ./netbox-gitops --dry-run

    Plan: 3 to create, 1 to update, 0 to delete, 41 unchanged
-->

```text

```
