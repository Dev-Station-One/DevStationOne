package commands

import (
	"encoding/json"
	"regexp"
	"strings"

	"devstationone/internal/protocol"
)

// ImportCandidate is one discovered command the import UI offers as a
// checkbox row; nothing is saved until the user accepts it.
type ImportCandidate struct {
	Source  string `json:"source"` // makefile | npm | scripts
	Name    string `json:"name"`
	Command string `json:"command"`
}

// maxImportCandidates bounds each parser's output so a Makefile with
// hundreds of targets cannot flood the import modal.
// ponytail: fixed cap, no config knob unless a real project needs one.
const maxImportCandidates = 100

// makefileTarget matches a target line (`build:` / `build deps:`). The char
// right after the colon must not be `=`, which skips variable assignments
// like `VAR := x`; dot-prefixed special targets (.PHONY and friends) are
// excluded by requiring an alphanumeric start.
var makefileTarget = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9_.%-]*)\s*:(?:[^=]|\s*$)`)

// ParseMakefile extracts a Makefile's targets as `make <target>` candidates.
// It is a line heuristic, not a real Makefile parser — a Makefile whose
// variable declarations or snippets happen to match the target pattern can
// produce a stray candidate, which the user just leaves unchecked.
func ParseMakefile(data []byte) []ImportCandidate {
	var out []ImportCandidate
	seen := make(map[string]bool)
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if line == "" || line[0] == '\t' || line[0] == '#' {
			continue
		}
		m := makefileTarget.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, ImportCandidate{Source: "makefile", Name: m[1], Command: "make " + m[1]})
		if len(out) >= maxImportCandidates {
			break
		}
	}
	return out
}

// ParsePackageJSON extracts a package.json's scripts as `npm run <key>`
// candidates.
func ParsePackageJSON(data []byte) []ImportCandidate {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	var out []ImportCandidate
	for name := range pkg.Scripts {
		out = append(out, ImportCandidate{Source: "npm", Name: name, Command: "npm run " + name})
		if len(out) >= maxImportCandidates {
			break
		}
	}
	return out
}

// ParseScriptsDir turns a file.command "list" result for a project's
// scripts/ directory into `./scripts/<name>` candidates, one per executable
// regular file (the POSIX mode string carries an x bit). Non-executable
// files and subdirectories are skipped.
func ParseScriptsDir(entries []protocol.FileInfo) []ImportCandidate {
	var out []ImportCandidate
	for _, e := range entries {
		if e.Type != "file" || len(e.Mode) < 4 || !strings.Contains(e.Mode[1:], "x") {
			continue
		}
		name := strings.TrimSuffix(e.Name, ".sh")
		out = append(out, ImportCandidate{Source: "scripts", Name: name, Command: "./scripts/" + e.Name})
		if len(out) >= maxImportCandidates {
			break
		}
	}
	return out
}
