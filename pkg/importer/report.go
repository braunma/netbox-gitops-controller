// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the honest account of an import: how many of each kind NetBox held
// versus how many were exported, every object skipped and why, and the standing
// gaps between NetBox's model and this schema. A gap that is only reported is
// acceptable; a gap that is silently dropped and later pruned is data loss, so
// everything the importer could not represent lands here.
type Report struct {
	counts []endpointCount
	skips  []skipEntry
	gaps   []string
}

type endpointCount struct {
	Endpoint string
	InNetBox int
	Exported int
}

type skipEntry struct {
	Endpoint string
	Object   string
	Reason   string
}

func newReport() *Report { return &Report{} }

// count records that an endpoint held inNetBox objects, of which exported were
// written. Called once per endpoint.
func (r *Report) count(endpoint string, inNetBox, exported int) {
	r.counts = append(r.counts, endpointCount{endpoint, inNetBox, exported})
}

// skip records one object the importer did not represent, with a reason.
func (r *Report) skip(endpoint, object, reason string) {
	r.skips = append(r.skips, skipEntry{endpoint, object, reason})
}

// note records a standing model gap (a NetBox concept this schema has no field
// for), shown once regardless of how many objects it affected.
func (r *Report) note(gap string) { r.gaps = append(r.gaps, gap) }

// HasSkips reports whether any object was skipped — the condition --fail-on-gaps
// turns into a non-zero exit.
func (r *Report) HasSkips() bool { return len(r.skips) > 0 }

// Summary is a one-line count for stderr.
func (r *Report) Summary() string {
	var in, out int
	for _, c := range r.counts {
		in += c.InNetBox
		out += c.Exported
	}
	return fmt.Sprintf("imported %d of %d object(s) across %d endpoint(s); %d skipped",
		out, in, len(r.counts), len(r.skips))
}

// Markdown renders the report as IMPORT-REPORT.md.
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Import report\n\n")
	b.WriteString("What this import read from NetBox, what it wrote, and what it could\n")
	b.WriteString("not represent. Anything listed under \"skipped\" or \"gaps\" is **not**\n")
	b.WriteString("in the generated YAML — pruning against a partial import would delete\n")
	b.WriteString("the managed objects it omitted, so read this before enabling `--prune`.\n\n")

	b.WriteString("## Per-endpoint counts\n\n")
	b.WriteString("| Endpoint | In NetBox | Exported |\n|---|---:|---:|\n")
	counts := append([]endpointCount(nil), r.counts...)
	sort.Slice(counts, func(i, j int) bool { return counts[i].Endpoint < counts[j].Endpoint })
	for _, c := range counts {
		b.WriteString(fmt.Sprintf("| %s | %d | %d |\n", c.Endpoint, c.InNetBox, c.Exported))
	}

	if len(r.skips) > 0 {
		b.WriteString("\n## Skipped objects\n\n")
		b.WriteString("| Endpoint | Object | Reason |\n|---|---|---|\n")
		skips := append([]skipEntry(nil), r.skips...)
		sort.Slice(skips, func(i, j int) bool {
			if skips[i].Endpoint != skips[j].Endpoint {
				return skips[i].Endpoint < skips[j].Endpoint
			}
			return skips[i].Object < skips[j].Object
		})
		for _, s := range skips {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", s.Endpoint, s.Object, s.Reason))
		}
	}

	if len(r.gaps) > 0 {
		b.WriteString("\n## Model gaps\n\n")
		b.WriteString("NetBox concepts this schema has no field for. Objects of these kinds,\n")
		b.WriteString("or these fields on objects that were imported, are not represented:\n\n")
		for _, g := range sortedUnique(r.gaps) {
			b.WriteString("- " + g + "\n")
		}
	}
	return b.String()
}
