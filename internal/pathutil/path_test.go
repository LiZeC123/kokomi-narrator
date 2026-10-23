package pathutil

import (
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := ExpandHome("~")
	if err != nil {
		t.Fatalf("ExpandHome(~) error = %v", err)
	}
	if got != home {
		t.Fatalf("ExpandHome(~) = %q, want %q", got, home)
	}

	got, err = ExpandHome("~/Music/bgm")
	if err != nil {
		t.Fatalf("ExpandHome(~/Music/bgm) error = %v", err)
	}
	if want := filepath.Join(home, "Music", "bgm"); got != want {
		t.Fatalf("ExpandHome(~/Music/bgm) = %q, want %q", got, want)
	}
}

func TestExpandHomeLeavesOtherPaths(t *testing.T) {
	absolute := filepath.Join(string(filepath.Separator), "tmp", "music")
	for _, path := range []string{absolute, "./data/bgm", "../music", "~other"} {
		got, err := ExpandHome(path)
		if err != nil {
			t.Fatalf("ExpandHome(%q) error = %v", path, err)
		}
		if got != path {
			t.Fatalf("ExpandHome(%q) = %q, want unchanged", path, got)
		}
	}
}
