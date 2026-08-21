# Roadmap

What this controller does not do yet, roughly in the order it is worth doing.
Everything it *does* do is documented in the [README](../README.md); what
changed and when is in the [changelog](../CHANGELOG.md).

## Live-state validation

`yamlcheck` checks the YAML against the typed models and against the rest of
the repository — references that resolve to nothing declared, a name or IP used
twice, two devices in one rack unit, a port claimed by two cables, an interface
the device type does not have.

What it cannot see is the *server*. A status value, interface type or rack
position the models accept can still be rejected by a particular NetBox, and an
object that exists only in NetBox is invisible to it.

**Wanted:** a `validate` command that resolves choices and references against
the live API without writing, so a merge request fails before it reaches apply.
Effort: M.

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
