// SPDX-License-Identifier: Apache-2.0

package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testRepo is a throwaway repository with helpers to write and commit.
type testRepo struct {
	t   *testing.T
	dir string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// The work tree root is reported with symlinks resolved (macOS keeps its
	// temp directory behind one).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &testRepo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A", "-f")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func TestOpenOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// GIT_CEILING_DIRECTORIES keeps git from finding a repository above the
	// temp directory (the checkout these tests run in, on some setups).
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := Open(dir); err == nil {
		t.Fatal("Open() of a plain directory succeeded")
	}
}

func TestOpenWithoutACommit(t *testing.T) {
	r := newTestRepo(t)
	if _, err := Open(r.dir); err == nil || !strings.Contains(err.Error(), "no commit yet") {
		t.Fatalf("Open() = %v, want the missing commit named", err)
	}
}

func TestRepoBasics(t *testing.T) {
	r := newTestRepo(t)
	r.write("data/definitions/sites.yaml", "a\n")
	first := r.commit("first")
	r.write("data/definitions/sites.yaml", "b\n")
	second := r.commit("second")

	repo, err := Open(filepath.Join(r.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != r.dir || repo.Head != second {
		t.Fatalf("Open() = %+v, want root %s at %s", repo, r.dir, second)
	}

	if rel, ok := repo.Rel(filepath.Join(r.dir, "data", "definitions")); !ok || rel != "data/definitions" {
		t.Errorf("Rel(inside) = %q, %v", rel, ok)
	}
	if _, ok := repo.Rel(filepath.Dir(r.dir)); ok {
		t.Error("Rel(parent of the work tree) reported inside")
	}

	if got, err := repo.Resolve("HEAD~1"); err != nil || got != first {
		t.Errorf("Resolve(HEAD~1) = %q, %v, want %s", got, err, first)
	}
	if _, err := repo.Resolve("0123456789abcdef0123456789abcdef01234567"); err == nil {
		t.Error("Resolve() of an unknown commit succeeded")
	}
	if _, err := repo.Resolve("--all"); err == nil {
		t.Error("Resolve() accepted an option as a ref")
	}

	if !repo.IsAncestor(first, second) || repo.IsAncestor(second, first) {
		t.Error("IsAncestor() has the direction wrong")
	}
}

func TestMergeBaseOfDivergedBranches(t *testing.T) {
	r := newTestRepo(t)
	r.write("f", "1\n")
	fork := r.commit("fork point")
	r.git("checkout", "-q", "-b", "feature")
	r.write("g", "feature\n")
	r.commit("on feature")
	r.git("checkout", "-q", "main")
	r.write("f", "2\n")
	main := r.commit("on main")
	r.git("checkout", "-q", "feature")

	repo, err := Open(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if repo.IsAncestor(main, repo.Head) {
		t.Fatal("main's tip is an ancestor of the feature branch")
	}
	if got, err := repo.MergeBase(main, repo.Head); err != nil || got != fork {
		t.Errorf("MergeBase() = %q, %v, want %s", got, err, fork)
	}
}

// TestDirtySeesIgnoredFiles: a data directory listed in .gitignore is still
// what the loader reads, so a new file in it means the disk is not the commit.
func TestDirtySeesIgnoredFiles(t *testing.T) {
	r := newTestRepo(t)
	r.write(".gitignore", "/definitions/\n")
	r.write("definitions/sites.yaml", "a\n")
	r.write("other/readme", "x\n")
	r.commit("first")

	repo, err := Open(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"definitions", "inventory"}
	if dirty, err := repo.Dirty(paths); err != nil || len(dirty) != 0 {
		t.Fatalf("Dirty() on a clean tree = %v, %v", dirty, err)
	}
	if dirty, err := repo.Dirty(nil); err != nil || dirty != nil {
		t.Fatalf("Dirty(nil) = %v, %v", dirty, err)
	}

	r.write("other/readme", "changed outside the data\n")
	if dirty, _ := repo.Dirty(paths); len(dirty) != 0 {
		t.Errorf("Dirty() = %v; a change outside the paths is not its business", dirty)
	}

	r.write("definitions/new.yaml", "ignored and untracked\n")
	r.write("definitions/sites.yaml", "modified\n")
	dirty, err := repo.Dirty(paths)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"definitions/sites.yaml", "definitions/new.yaml"}
	if !reflect.DeepEqual(dirty, want) {
		t.Errorf("Dirty() = %v, want %v", dirty, want)
	}
}

func TestChangedFilesIncludesUncommittedEdits(t *testing.T) {
	r := newTestRepo(t)
	r.write("definitions/a.yaml", "1\n")
	r.write("definitions/b.yaml", "1\n")
	r.write("cmd/main.go", "package main\n")
	first := r.commit("first")
	r.write("definitions/a.yaml", "2\n")
	r.commit("second")
	r.write("definitions/b.yaml", "uncommitted\n")

	repo, err := Open(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.ChangedFiles(first, []string{"definitions"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"definitions/a.yaml", "definitions/b.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedFiles() = %v, want %v", got, want)
	}
	if code, err := repo.ChangedFiles(first, []string{"*.go", "go.mod"}); err != nil || len(code) != 0 {
		t.Errorf("ChangedFiles(*.go) = %v, %v, want none", code, err)
	}
	if _, err := repo.ChangedFiles("0123456789abcdef0123456789abcdef01234567", []string{"definitions"}); err == nil {
		t.Error("ChangedFiles() against an unknown commit succeeded")
	}
}

func TestExtractWritesTheOldContent(t *testing.T) {
	r := newTestRepo(t)
	r.write("data/definitions/sites/sites.yaml", "old\n")
	r.write("data/inventory/hw/servers.yaml", "old inventory\n")
	r.write("unrelated.txt", "not asked for\n")
	first := r.commit("first")
	r.write("data/definitions/sites/sites.yaml", "new\n")
	r.write("data/library/type.yaml", "added later\n")
	r.commit("second")

	repo, err := Open(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	// data/library did not exist at the first commit: skipped, not an error.
	if err := repo.Extract(first, []string{"data/definitions", "data/inventory", "data/library"}, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "data", "definitions", "sites", "sites.yaml"))
	if err != nil || string(got) != "old\n" {
		t.Errorf("extracted sites.yaml = %q, %v, want the first commit's content", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "data", "inventory", "hw", "servers.yaml")); err != nil {
		t.Errorf("inventory was not extracted: %v", err)
	}
	for _, absent := range []string{"unrelated.txt", "data/library"} {
		if _, err := os.Stat(filepath.Join(dest, absent)); err == nil {
			t.Errorf("%s was extracted", absent)
		}
	}

	// Nothing present at all: nothing written, no error.
	empty := t.TempDir()
	if err := repo.Extract(first, []string{"nowhere"}, empty); err != nil {
		t.Errorf("Extract() of absent paths = %v", err)
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Errorf("Extract() of absent paths wrote %d entries", len(entries))
	}
}
