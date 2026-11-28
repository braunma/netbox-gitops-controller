// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestDebugIsPrintedOnlyWhenVerbose(t *testing.T) {
	var out bytes.Buffer
	oldOut, oldVerbose := DefaultOutput(), Verbose()
	SetDefaultOutput(&out)
	defer func() { SetDefaultOutput(oldOut); SetVerbose(oldVerbose) }()
	logger := NewLogger(false)

	SetVerbose(false)
	logger.Debug("looked at %s", "a")
	logger.Info("changed %s", "b")
	logger.Warning("careful with %s", "c")
	if got := out.String(); strings.Contains(got, "looked at a") || !strings.Contains(got, "changed b") || !strings.Contains(got, "careful with c") {
		t.Errorf("quiet output = %q, want the change and the warning without the detail", got)
	}

	out.Reset()
	SetVerbose(true)
	logger.Debug("looked at %s", "a")
	if !strings.Contains(out.String(), "looked at a") {
		t.Errorf("verbose output = %q, want the detail", out.String())
	}
}
