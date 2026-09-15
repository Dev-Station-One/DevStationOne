package commands

import (
	"testing"

	"devstationone/internal/protocol"
)

func TestParseMakefile(t *testing.T) {
	mk := []byte(".PHONY: build test\nVAR := nope\nbuild: deps\ntest:\n\t@echo \"a recipe line\"\nreal-target: ; @echo inline\ndup:\ndup:\n")
	got := ParseMakefile(mk)
	if len(got) != 4 {
		t.Fatalf("got %d candidates %+v, want 4", len(got), got)
	}
	want := [][2]string{
		{"build", "make build"},
		{"test", "make test"},
		{"real-target", "make real-target"},
		{"dup", "make dup"},
	}
	for i, w := range want {
		if got[i].Name != w[0] || got[i].Command != w[1] || got[i].Source != "makefile" {
			t.Fatalf("candidate[%d] = %+v, want %v", i, got[i], w)
		}
	}
}

func TestParseMakefile_VariableAssignmentsSkipped(t *testing.T) {
	mk := []byte("VAR := x\nVAR2 ?= y\nCFLAGS = -Wall\nbuild:\n")
	got := ParseMakefile(mk)
	if len(got) != 1 || got[0].Name != "build" {
		t.Fatalf("got %+v, want only build", got)
	}
}

func TestParseMakefile_EmptyIsNil(t *testing.T) {
	if got := ParseMakefile(nil); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestParsePackageJSON(t *testing.T) {
	pkg := []byte("{\"scripts\": {\"dev\": \"vite\", \"build\": \"vite build\", \"test:unit\": \"vitest\"}}")
	got := ParsePackageJSON(pkg)
	if len(got) != 3 {
		t.Fatalf("got %d candidates %+v", len(got), got)
	}
	byName := make(map[string]ImportCandidate)
	for _, c := range got {
		byName[c.Name] = c
	}
	c := byName["dev"]
	if c.Command != "npm run dev" || c.Source != "npm" {
		t.Fatalf("dev candidate = %+v", c)
	}
	if _, ok := byName["test:unit"]; !ok {
		t.Fatal("test:unit script missing")
	}
}

func TestParsePackageJSON_MalformedOrNoScripts(t *testing.T) {
	got := ParsePackageJSON([]byte("not json"))
	if got != nil {
		t.Fatalf("malformed json = %+v, want nil", got)
	}
	got = ParsePackageJSON([]byte("{\"name\": \"x\"}"))
	if got != nil {
		t.Fatalf("no scripts = %+v, want nil", got)
	}
}

func TestParseScriptsDir_ExecutableFilesOnly(t *testing.T) {
	entries := []protocol.FileInfo{
		{Name: "deploy.sh", Type: "file", Mode: "-rwxr-xr-x"},
		{Name: "notes.txt", Type: "file", Mode: "-rw-r--r--"},
		{Name: "subdir", Type: "dir", Mode: "drwxr-xr-x"},
		{Name: "readonly.sh", Type: "file", Mode: "-rw-r--r--"},
	}
	got := ParseScriptsDir(entries)
	if len(got) != 1 {
		t.Fatalf("got %+v, want only deploy", got)
	}
	if got[0].Name != "deploy" || got[0].Command != "./scripts/deploy.sh" || got[0].Source != "scripts" {
		t.Fatalf("got %+v", got[0])
	}
}
