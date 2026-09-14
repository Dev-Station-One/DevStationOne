package agent

import (
	"reflect"
	"testing"
)

func TestParseShells(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"normal", "/bin/sh\n/bin/bash\n", []string{"/bin/sh", "/bin/bash"}},
		{"comments and blanks", "# /etc/shells\n\n/bin/sh\n\n# end\n/bin/zsh\n", []string{"/bin/sh", "/bin/zsh"}},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseShells([]byte(c.in))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseShells(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestReadShells_MissingFile(t *testing.T) {
	if got := readShells("/nonexistent/devstationone-test/shells"); got != nil {
		t.Fatalf("readShells missing file = %v, want nil", got)
	}
}
