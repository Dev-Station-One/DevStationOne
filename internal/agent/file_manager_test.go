package agent

import (
	"context"
	"os/user"
	"strconv"
	"testing"

	"devstationone/internal/protocol"
)

func TestExecuteFile_Chmod(t *testing.T) {
	root := t.TempDir()
	cmd := protocol.FileCommandPayload{Action: "create", Root: root, Path: "a.txt"}
	if _, err := executeFile(context.Background(), cmd); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := executeFile(context.Background(), protocol.FileCommandPayload{
		Action: "chmod", Root: root, Path: "a.txt", Mode: "640",
	}); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	result, err := executeFile(context.Background(), protocol.FileCommandPayload{Action: "stat", Root: root, Path: "a.txt"})
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if result.Info == nil || result.Info.Mode != "-rw-r-----" {
		t.Errorf("mode = %+v, want -rw-r-----", result.Info)
	}
}

func TestExecuteFile_ChmodInvalidMode(t *testing.T) {
	root := t.TempDir()
	executeFile(context.Background(), protocol.FileCommandPayload{Action: "create", Root: root, Path: "a.txt"})

	_, err := executeFile(context.Background(), protocol.FileCommandPayload{
		Action: "chmod", Root: root, Path: "a.txt", Mode: "not-octal",
	})
	if err == nil {
		t.Fatal("chmod with invalid mode should fail")
	}
}

func TestExecuteFile_ChownToSelf(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skip("no current user available")
	}
	if _, err := strconv.Atoi(u.Uid); err != nil {
		t.Skip("non-numeric uid")
	}

	root := t.TempDir()
	executeFile(context.Background(), protocol.FileCommandPayload{Action: "create", Root: root, Path: "a.txt"})

	if _, err := executeFile(context.Background(), protocol.FileCommandPayload{
		Action: "chown", Root: root, Path: "a.txt", Owner: u.Username,
	}); err != nil {
		t.Fatalf("chown to self: %v", err)
	}
}

func TestExecuteFile_ChownUnknownUser(t *testing.T) {
	root := t.TempDir()
	executeFile(context.Background(), protocol.FileCommandPayload{Action: "create", Root: root, Path: "a.txt"})

	_, err := executeFile(context.Background(), protocol.FileCommandPayload{
		Action: "chown", Root: root, Path: "a.txt", Owner: "devstationone-nonexistent-user-xyz",
	})
	if err == nil {
		t.Fatal("chown to an unknown user should fail")
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    uint32
		wantErr bool
	}{
		{"755", 0o755, false},
		{"0644", 0o644, false},
		{"", 0, true},
		{"888", 0, true},
		{"rwx", 0, true},
	}
	for _, c := range cases {
		got, err := parseMode(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseMode(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && uint32(got) != c.want {
			t.Errorf("parseMode(%q) = %o, want %o", c.in, got, c.want)
		}
	}
}

func TestResolveOwnerGroup(t *testing.T) {
	uid, gid, err := resolveOwnerGroup("", "")
	if err != nil || uid != -1 || gid != -1 {
		t.Errorf("resolveOwnerGroup(\"\",\"\") = %d,%d,%v, want -1,-1,nil", uid, gid, err)
	}

	if _, _, err := resolveOwnerGroup("devstationone-nonexistent-user-xyz", ""); err == nil {
		t.Error("resolveOwnerGroup with an unknown user should fail")
	}
	if _, _, err := resolveOwnerGroup("", "devstationone-nonexistent-group-xyz"); err == nil {
		t.Error("resolveOwnerGroup with an unknown group should fail")
	}
}
