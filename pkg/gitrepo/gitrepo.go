// SPDX-License-Identifier: Apache-2.0

// Package gitrepo answers the few questions a change-based sync asks of the
// git repository the data directory lives in: which commit is checked out,
// whether the data on disk is exactly that commit's, and what the data looked
// like at an earlier commit.
//
// It shells out to git and only ever reads. Every failure is returned to the
// caller, which falls back to a full sync: not being able to ask git is never
// a reason to guess.
package gitrepo

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is the git work tree a directory belongs to.
type Repo struct {
	// Root is the absolute path of the work tree.
	Root string
	// Head is the full hash of the checked-out commit.
	Head string
}

// Open finds the work tree dir belongs to. It fails when dir is not inside a
// git work tree, when git is not installed, or when the repository has no
// commit yet.
func Open(dir string) (*Repo, error) {
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	head, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("the repository has no commit yet: %w", err)
	}
	return &Repo{Root: root, Head: head}, nil
}

// Rel returns path relative to the work tree, slash-separated, and whether it
// lies inside the work tree at all.
func (r *Repo) Rel(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	// The work tree root is reported with symlinks resolved; resolve the
	// path the same way or a symlinked checkout looks like it is outside.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	root := r.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// Resolve returns the full hash of the commit ref names, or an error when
// this clone does not hold it (a shallow clone, a rewritten history, a typo).
func (r *Repo) Resolve(ref string) (string, error) {
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("%q is not a commit", ref)
	}
	return run(r.Root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// IsAncestor reports whether commit a is an ancestor of (or equal to) b.
func (r *Repo) IsAncestor(a, b string) bool {
	_, err := run(r.Root, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// MergeBase returns the best common ancestor of two commits.
func (r *Repo) MergeBase(a, b string) (string, error) {
	return run(r.Root, "merge-base", a, b)
}

// Dirty lists the files under paths (relative to the work tree) whose content
// on disk is not what HEAD holds: modified, deleted, staged, untracked, and
// untracked-but-ignored. Ignored files count because a data directory may be
// listed in .gitignore and still be what the loader reads.
func (r *Repo) Dirty(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"status", "--porcelain", "--ignored", "--untracked-files=all", "--"}, paths...)
	out, err := run(r.Root, args...)
	if err != nil {
		return nil, err
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 3 {
			dirty = append(dirty, line[3:])
		}
	}
	return dirty, nil
}

// ChangedFiles lists the files under paths (git pathspecs, relative to the
// work tree) that differ between the commit from and the work tree as it is
// on disk, uncommitted edits included.
func (r *Repo) ChangedFiles(from string, paths []string) ([]string, error) {
	args := append([]string{"diff", "--name-only", "--no-renames", from, "--"}, paths...)
	out, err := run(r.Root, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Extract writes the given paths (relative to the work tree) as they were at
// commit into dest, keeping their relative layout. A path the commit does not
// hold is skipped: a folder that did not exist yet simply had nothing in it.
func (r *Repo) Extract(commit string, paths []string, dest string) error {
	var present []string
	for _, p := range paths {
		if _, err := run(r.Root, "cat-file", "-e", commit+":"+p); err == nil {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		return nil
	}

	args := append([]string{"-C", r.Root, "archive", "--format=tar", commit, "--"}, present...)
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	untarErr := untar(stdout, dest)
	// Drain what is left so git can exit even when untar stopped early.
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %w: %s", commit, err, strings.TrimSpace(stderr.String()))
	}
	return untarErr
}

// untar unpacks regular files and directories below dest. Anything else in
// the archive (symlinks, devices) is skipped, and no entry may leave dest.
func untar(src io.Reader, dest string) error {
	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q leaves the target directory", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

// run executes git in dir and returns its trimmed standard output.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
