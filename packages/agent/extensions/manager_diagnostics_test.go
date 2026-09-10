package extensions

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadErrorsIdentifyExtensionDirectory(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		mode := "discover"
		if explicit {
			mode = "explicit"
		}
		for _, tc := range []struct {
			name     string
			manifest string
			want     string
		}{
			{"invalid-json", `{`, "parse manifest"},
			{"missing-name", `{"exec":"missing"}`, "name is required"},
			{"missing-exec", `{"name":"broken"}`, "exec, theme, or skills is required"},
			{"open-log", `{"name":"broken","exec":"missing"}`, "open log"},
			{"missing-runtime", `{"name":"broken","exec":"zot-test-missing-runtime"}`, "failed to start"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				home := t.TempDir()
				dir := filepath.Join(home, "extensions", tc.name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "extension.json"), []byte(tc.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
				if tc.name == "open-log" {
					if err := os.WriteFile(filepath.Join(home, "logs"), nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "missing-runtime" {
					t.Setenv("PATH", t.TempDir())
				}
				mgr := New(home, "", "test", "", "", nil)
				var errs []error
				if explicit {
					errs = mgr.LoadExplicit(context.Background(), []string{dir})
				} else {
					errs = mgr.Discover(context.Background())
				}
				if len(errs) != 1 {
					t.Fatalf("errors = %v, want one", errs)
				}
				if got := errs[0].Error(); !strings.Contains(got, dir) || !strings.Contains(got, tc.want) {
					t.Fatalf("error = %q, want directory %q and %q", got, dir, tc.want)
				}
				if tc.name == "missing-runtime" {
					if !errors.Is(errs[0], exec.ErrNotFound) {
						t.Fatalf("error does not wrap exec.ErrNotFound: %v", errs[0])
					}
					if strings.Count(errs[0].Error(), dir) != 1 {
						t.Fatalf("directory should appear once: %v", errs[0])
					}
				}
			})
		}
	}
}
