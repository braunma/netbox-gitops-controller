# Roadmap

What this controller does not do yet, roughly in the order it is worth doing.
Everything it *does* do is documented in the [README](../README.md); what
changed and when is in the [changelog](../CHANGELOG.md).

## Extended live validation

`netbox-gitops validate` asks the target instance which values it accepts —
choices and string lengths from each endpoint's OPTIONS response — and looks up
the references this repository does not declare itself. What it does not yet
check: custom fields an instance marks required, uniqueness constraints that
depend on existing objects (a serial or asset tag another device already
holds), and whether a rack position is free. Each needs a query per object
rather than per endpoint, so it wants care over the request count. Effort: M.

## A config file

Settings come from flags, the environment, and the `KEY=value` file `--config`
names — see [CONFIGURATION.md](CONFIGURATION.md). What is still hard-coded: the
managed tag slug, API page size, retry counts and the HTTP timeout.

**Wanted:** an optional YAML config for those. Effort: S.

## Extended IPAM coverage

VRFs, VLAN groups, VLANs and prefixes are managed. Aggregates, RIRs, IP ranges,
route targets, ASNs and services are not — each is a straightforward "ensure"
loop, and cheaper still once the repeated build-payload→lookup→apply loop in
the simple reconcilers is factored into one helper. Effort: S–M.

## Release and packaging

There is a `Makefile`, a multi-stage `Dockerfile`, an Apache-2.0 `LICENSE`,
`--version` stamped from ldflags, and CI on both GitLab and GitHub Actions.

**Wanted:** goreleaser with semver tags and published container images.
Effort: S.

## Observability

Console logging aimed at a human reading a pipeline. **Wanted:**
`--log-format json`, `--quiet`/`--verbose` levels, and end-of-run metrics
(objects changed, API calls, duration). Effort: M.

## Daemon / watch mode and webhooks

Run-once CLI only. **Wanted (long-term):** a `--watch` interval mode and an
HTTP endpoint for NetBox webhooks, so drift is corrected without a pipeline
trigger. Worth building only now that pruning and plan output exist — a daemon
that cannot delete cannot converge state. Effort: L.
