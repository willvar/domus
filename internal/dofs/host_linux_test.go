//go:build linux

package dofs

import (
	"strings"
	"testing"
)

func TestFuseConfigAllowsOther(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "enabled", input: "# comment\n user_allow_other # trusted service\n", want: true},
		{name: "commented", input: "# user_allow_other\n", want: false},
		{name: "different option", input: "mount_max = 1000\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := fuseConfigAllowsOther(strings.NewReader(test.input))
			if err != nil || got != test.want {
				t.Fatalf("fuseConfigAllowsOther() = %t, %v; want %t", got, err, test.want)
			}
		})
	}
}
