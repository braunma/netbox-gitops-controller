// SPDX-License-Identifier: Apache-2.0

package discovery

import "testing"

func TestGPUSummary(t *testing.T) {
	cases := []struct {
		name string
		gpus []GPU
		want string
	}{
		{"none", nil, ""},
		{"memory not reported", []GPU{{Model: "Tesla T4"}, {Model: "Tesla T4"}, {Model: "Tesla T4"}}, "3× Tesla T4"},
		{"memory reported", []GPU{{Model: "NVIDIA A100", MemoryGB: 80}}, "1× NVIDIA A100 (80 GB)"},
		{
			"mixed, in the order reported",
			[]GPU{{Model: "NVIDIA H100"}, {Model: "NVIDIA A30"}, {Model: "NVIDIA H100"}, {Model: "NVIDIA A30"}},
			"2× NVIDIA H100, 2× NVIDIA A30",
		},
	}
	for _, c := range cases {
		if got := GPUSummary(c.gpus); got != c.want {
			t.Errorf("%s: GPUSummary = %q, want %q", c.name, got, c.want)
		}
	}
}
