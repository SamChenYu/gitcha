package gitcha

import (
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// SearchResult combines the absolute path of a file with a FileInfo struct.
type SearchResult struct {
	Path string
	Info os.FileInfo
}

type GitIgnoreEntry struct {
	Dir string
	Gi  *ignore.GitIgnore
}

// GitRepoForPath returns the directory of the git repository path is a member
// of, or an error.
func GitRepoForPath(path string) (string, error) {
	dir, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	for {
		st, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil && st.IsDir() {
			return dir, nil
		}

		// reached root?
		if dir == filepath.Dir(dir) {
			return "", nil
		}

		// check parent dir
		dir = filepath.Dir(dir)
	}
}

// IsPathInGit returns true when a path is part of a git repository.
func IsPathInGit(path string) bool {
	p, err := GitRepoForPath(path)
	if err != nil {
		return false
	}

	return len(p) > 0
}

// FindAllFiles finds all files from list in path. It does not respect any
// gitignore files.
func FindAllFiles(path string, list []string) (chan SearchResult, error) {
	return findFiles(path, list, nil, false)
}

// FindAllFilesExcept finds all files from list in path. It does not respect any
// gitignore files.
func FindAllFilesExcept(path string, list, ignorePatterns []string) (chan SearchResult, error) {
	return findFiles(path, list, ignorePatterns, false)
}

// FindFiles finds files from list in path. It respects all .gitignores it finds
// while traversing paths.
func FindFiles(path string, list []string) (chan SearchResult, error) {
	return findFiles(path, list, nil, true)
}

// FindFilesExcept finds files from a list in a path, excluding any matches in
// a given set of ignore patterns. It also respects all .gitignores it finds
// while traversing paths.
func FindFilesExcept(path string, list, ignorePatterns []string) (chan SearchResult, error) {
	return findFiles(path, list, ignorePatterns, true)
}

// FindFirstFile looks for files from a list in a path, returning the first
// match it finds. It respects all .gitignores it finds along the way.
func FindFirstFile(path string, list []string) (SearchResult, error) {
	ch, err := FindFilesExcept(path, list, nil)
	if err != nil {
		return SearchResult{}, err
	}

	for v := range ch {
		return v, nil
	}

	return SearchResult{}, nil
}

func findFiles(path string, list, ignorePatterns []string, respectGitIgnore bool) (chan SearchResult, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, err
	}

	ch := make(chan SearchResult)
	go func() {
		defer close(ch)

		var ignoreStack []GitIgnoreEntry

		if respectGitIgnore {
			// Find all intermediary .gitignores from repoRoot -> CWD, load them all into ignoreStack
			repoRoot, err := GitRepoForPath(path)
			if err == nil && repoRoot != "" && repoRoot != path {
				curr := repoRoot
				rel, _ := filepath.Rel(repoRoot, path)
				parts := strings.Split(rel, string(filepath.Separator))

				for _, part := range parts {
					gitIgnorePath := filepath.Join(curr, ".gitignore")
					if _, err := os.Stat(gitIgnorePath); err == nil {
						if gi, err := ignore.CompileIgnoreFile(gitIgnorePath); err == nil {
							ignoreStack = append(ignoreStack, GitIgnoreEntry{curr, gi})
						}
					}
					curr = filepath.Join(curr, part)
				}
			}
		}

		_ = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return err
			}

			if respectGitIgnore {

				if info.IsDir() && info.Name() == ".git" {
					return filepath.SkipDir
				}

				// Check and pop the ignore stack for any irrelevant gitignores
				for len(ignoreStack) > 0 {
					rel, err := filepath.Rel(ignoreStack[len(ignoreStack)-1].Dir, path)
					if err != nil || strings.HasPrefix(rel, "..") { // checking for '..' means that to get the relative path you have to go outside of the path anyways
						ignoreStack = ignoreStack[:len(ignoreStack)-1]
					} else {
						break
					}
				}

				// Add any current gitignores from the current directory
				if info.IsDir() {
					currGitIgnorePath := filepath.Join(path, ".gitignore")
					_, err := os.Stat(currGitIgnorePath)
					if err == nil {
						currCompiledGitIgnore, err := ignore.CompileIgnoreFile(currGitIgnorePath)
						if err == nil {
							ignoreStack = append(ignoreStack, GitIgnoreEntry{path, currCompiledGitIgnore})
						}
					}
				}
				
				for _, entry := range ignoreStack {
					rel, err := filepath.Rel(entry.Dir, path)
					if err != nil || strings.HasPrefix(rel, "..") {
						continue
					}

					matches := entry.Gi.MatchesPath(filepath.ToSlash(rel))
					if matches {
						if info.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
				}
			}

			for _, pattern := range ignorePatterns {
				// If there's no path separator in the pattern try to match
				// against the directory we're currently walking.
				if !strings.Contains(pattern, string(os.PathSeparator)) {
					dir := filepath.Dir(path)
					if dir == "." {
						continue // path is empty
					}
					pattern = filepath.Join(dir, pattern)
				}

				matched, err := filepath.Match(pattern, path)
				if err != nil {
					continue
				}
				if matched && info.IsDir() {
					return filepath.SkipDir
				}
				if matched {
					return nil
				}
			}

			for _, v := range list {
				matched := strings.EqualFold(filepath.Base(path), v)
				if !matched {
					matched, _ = filepath.Match(strings.ToLower(v), strings.ToLower(filepath.Base(path)))
				}

				if matched {
					res, err := filepath.Abs(path)
					if err == nil {
						ch <- SearchResult{
							Path: res,
							Info: info,
						}
					}

					// only match each path once
					return nil
				}
			}
			return nil
		})
	}()

	return ch, nil
}
