package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckWithoutGitCheckout(t *testing.T) {
	svc := New(Options{Version: "dev", InstallDir: t.TempDir()})
	st := svc.Check(context.Background(), true)
	if st.Supported {
		t.Fatalf("expected unsupported status, got %+v", st)
	}
	if st.Available {
		t.Fatal("an unsupported installation must not report an update")
	}
	if st.Error == "" {
		t.Fatal("expected an explanatory error")
	}
	if st.Current != "dev" {
		t.Fatalf("current = %q, want dev", st.Current)
	}
}

func TestApplyUnsupportedWithoutInstaller(t *testing.T) {
	svc := New(Options{Version: "dev", InstallDir: t.TempDir()})
	if _, err := svc.Apply(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Apply error = %v, want ErrUnsupported", err)
	}
}

func TestCheckDetectsUpdateAndCaches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	install := filepath.Join(root, "install")

	gitRun(t, "", "init", "--bare", "--initial-branch=main", remote)
	gitRun(t, "", "init", "--initial-branch=main", work)
	writeFile(t, filepath.Join(work, "README.md"), "first\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "first")
	gitRun(t, work, "remote", "add", "origin", remote)
	gitRun(t, work, "push", "-u", "origin", "main")
	gitRun(t, "", "clone", remote, install)

	behind := 2
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"ahead_by": behind})
	}))
	defer api.Close()

	svc := New(Options{
		Repo:       "acme/gopanel",
		Version:    "old",
		InstallDir: install,
		APIBase:    api.URL,
		TTL:        time.Hour,
	})

	first := svc.Check(context.Background(), true)
	if !first.Supported {
		t.Fatalf("expected a supported status, got %+v", first)
	}
	if first.Available {
		t.Fatalf("fresh clone should be up to date, got %+v", first)
	}
	if first.Latest == "" {
		t.Fatal("expected the latest revision to be reported")
	}

	writeFile(t, filepath.Join(work, "README.md"), "second\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "second")
	gitRun(t, work, "push", "origin", "main")

	second := svc.Check(context.Background(), true)
	if !second.Available {
		t.Fatalf("expected an update, got %+v", second)
	}
	if second.Behind != 2 {
		t.Fatalf("behind = %d, want 2", second.Behind)
	}
	if second.URL == "" || second.Latest == "" {
		t.Fatalf("expected compare URL and latest revision, got %+v", second)
	}

	// Cached checks must not hit the API again.
	behind = 9
	cached := svc.Check(context.Background(), false)
	if cached.Behind != 2 {
		t.Fatalf("cached behind = %d, want 2", cached.Behind)
	}
	forced := svc.Check(context.Background(), true)
	if forced.Behind != 9 {
		t.Fatalf("forced behind = %d, want 9", forced.Behind)
	}
}

func TestParseRemoteSHA(t *testing.T) {
	out := "1111111111111111111111111111111111111111\trefs/heads/dev\n" +
		"2222222222222222222222222222222222222222\trefs/heads/main\n"
	if got := parseRemoteSHA(out); got != "2222222222222222222222222222222222222222" {
		t.Fatalf("parseRemoteSHA = %q", got)
	}
	if got := parseRemoteSHA("nonsense"); got != "" {
		t.Fatalf("parseRemoteSHA(nonsense) = %q", got)
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("0123456789abcdef"); got != "0123456" {
		t.Fatalf("shortSHA = %q", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Fatalf("shortSHA short = %q", got)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
