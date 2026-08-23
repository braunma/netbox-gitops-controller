// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines are shown around a change. Three is
// what `git diff` and `diff -u` use, so the output reads like every other diff
// in a pipeline log.
const diffContext = 3

// maxDiffLines caps the quadratic line matching below. An inventory file is
// tens to hundreds of lines; a file far past this is not one a human is going
// to review line by line anyway, so it gets a whole-file diff instead.
const maxDiffLines = 4000

// unifiedDiff renders a change the way `diff -u` would, for --dry-run.
//
// Printing the diff rather than a summary is the point of --dry-run here: this
// tool's whole review model is that a fact contradicting a declared value shows
// up as a change to that line, so the thing to look at before writing anything
// is the line.
func unifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}

	beforeLines := splitLines(before)
	afterLines := splitLines(after)

	var b strings.Builder
	if before == "" {
		fmt.Fprintf(&b, "--- /dev/null\n+++ %s\n", path)
	} else {
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", path, path)
	}

	for _, h := range hunks(beforeLines, afterLines) {
		b.WriteString(h)
	}
	return b.String()
}

// splitLines splits content into lines, dropping the empty element a trailing
// newline produces so the diff does not show a phantom last line.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// op is one line of the diff.
type op struct {
	kind byte // ' ', '-', '+'
	text string
}

// hunks renders the changed regions with context around them.
func hunks(before, after []string) []string {
	ops := diffOps(before, after)

	// Which ops to show: every change, plus diffContext lines either side.
	show := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		for j := max(0, i-diffContext); j <= min(len(ops)-1, i+diffContext); j++ {
			show[j] = true
		}
	}

	var out []string
	beforeLine, afterLine := 1, 1
	for i := 0; i < len(ops); {
		if !show[i] {
			switch ops[i].kind {
			case ' ':
				beforeLine++
				afterLine++
			case '-':
				beforeLine++
			case '+':
				afterLine++
			}
			i++
			continue
		}

		startBefore, startAfter := beforeLine, afterLine
		var body strings.Builder
		var countBefore, countAfter int
		for ; i < len(ops) && show[i]; i++ {
			body.WriteString(string(ops[i].kind) + ops[i].text + "\n")
			switch ops[i].kind {
			case ' ':
				countBefore++
				countAfter++
				beforeLine++
				afterLine++
			case '-':
				countBefore++
				beforeLine++
			case '+':
				countAfter++
				afterLine++
			}
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s",
			startBefore, countBefore, startAfter, countAfter, body.String()))
	}
	return out
}

// diffOps returns the line-by-line edit script.
func diffOps(before, after []string) []op {
	if len(before) > maxDiffLines || len(after) > maxDiffLines {
		ops := make([]op, 0, len(before)+len(after))
		for _, line := range before {
			ops = append(ops, op{'-', line})
		}
		for _, line := range after {
			ops = append(ops, op{'+', line})
		}
		return ops
	}

	// Longest common subsequence of lines. The files here are small, so the
	// straightforward table is both fast enough and obviously correct.
	lcs := make([][]int, len(before)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		switch {
		case before[i] == after[j]:
			ops = append(ops, op{' ', before[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', before[i]})
			i++
		default:
			ops = append(ops, op{'+', after[j]})
			j++
		}
	}
	for ; i < len(before); i++ {
		ops = append(ops, op{'-', before[i]})
	}
	for ; j < len(after); j++ {
		ops = append(ops, op{'+', after[j]})
	}
	return ops
}
