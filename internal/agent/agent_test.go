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

func TestParseColonNames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"passwd shape", "root:x:0:0:root:/root:/bin/bash\ndev:x:1000:1000::/home/dev:/bin/zsh\n", []string{"root", "dev"}},
		{"group shape", "root:x:0:\nsudo:x:27:dev\n", []string{"root", "sudo"}},
		{"comments and blanks", "# comment\n\nroot:x:0:0:root:/root:/bin/bash\n\n", []string{"root"}},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseColonNames([]byte(c.in))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseColonNames(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestReadColonNames_MissingFile(t *testing.T) {
	if got := readColonNames("/nonexistent/devstationone-test/passwd"); got != nil {
		t.Fatalf("readColonNames missing file = %v, want nil", got)
	}
}
