// Package discovery finds git repositories and worktrees in a directory.
package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Repo represents a discovered git repository.
type Repo struct {
	Name   string
	Path   string
	Branch string
	Dirty  bool
}

// Worktree represents a discovered git worktree.
type Worktree struct {
	RepoName string
	Branch   string
}

// FindRepos discovers immediate-child git repos in dir, sorted alphabetically.
// Repos with .worktrees in their name are excluded.
// Branch and dirty state are captured concurrently for speed.
func FindRepos(dir string) ([]Repo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// First pass: collect candidate paths.
	type candidate struct {
		name string
		path string
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.Contains(name, ".worktrees") {
			continue
		}
		path := filepath.Join(dir, name)
		if !hasGitDir(path) {
			continue
		}
		candidates = append(candidates, candidate{name: name, path: path})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Second pass: fetch branch + dirty state concurrently.
	repos := make([]Repo, len(candidates))
	var wg sync.WaitGroup
	for idx, cand := range candidates {
		wg.Add(1)
		go func(idx int, cand candidate) {
			defer wg.Done()
			repos[idx] = Repo{
				Name:   cand.name,
				Path:   cand.path,
				Branch: gitBranch(cand.path),
				Dirty:  isGitDirty(cand.path),
			}
		}(idx, cand)
	}
	wg.Wait()

	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Name < repos[j].Name
	})
	return repos, nil
}

// FindWorktrees discovers active worktrees under <repo>.worktrees/ directories.
// Supports both 3-level (<repo>.worktrees/<branch>/.git) and 4-level
// (<repo>.worktrees/<prefix>/<branch>/.git) layouts.
func FindWorktrees(dir string) ([]Worktree, error) {
	seen := make(map[string]bool)
	var worktrees []Worktree
	var mu sync.Mutex
	var wg sync.WaitGroup

	addMatches := func(pattern string, levelsAboveGit int) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return
		}
		for _, match := range matches {
			wtDir := filepath.Dir(match)
			mu.Lock()
			if seen[wtDir] {
				mu.Unlock()
				continue
			}
			seen[wtDir] = true
			mu.Unlock()

			wg.Add(1)
			go func(wtDir string, levelsAboveGit int, match string) {
				defer wg.Done()

				branch := gitBranch(wtDir)

				// Walk up to find the <repo>.worktrees dir.
				repoWorktreesDir := match
				for i := 0; i <= levelsAboveGit; i++ {
					repoWorktreesDir = filepath.Dir(repoWorktreesDir)
				}
				repoName := strings.TrimSuffix(filepath.Base(repoWorktreesDir), ".worktrees")

				mu.Lock()
				worktrees = append(worktrees, Worktree{
					RepoName: repoName,
					Branch:   branch,
				})
				mu.Unlock()
			}(wtDir, levelsAboveGit, match)
		}
	}

	// 3-level: <repo>.worktrees/<branch>/.git
	addMatches(filepath.Join(dir, "*.worktrees", "*", ".git"), 1)
	// 4-level: <repo>.worktrees/<prefix>/<branch>/.git
	addMatches(filepath.Join(dir, "*.worktrees", "*", "*", ".git"), 2)

	wg.Wait()

	sort.Slice(worktrees, func(i, j int) bool {
		if worktrees[i].RepoName != worktrees[j].RepoName {
			return worktrees[i].RepoName < worktrees[j].RepoName
		}
		return worktrees[i].Branch < worktrees[j].Branch
	})
	return worktrees, nil
}

func hasGitDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func gitBranch(path string) string {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
}

func isGitDirty(path string) bool {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}
