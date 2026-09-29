package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// errNoRepo is the error of a command that needs a repository and finds
// none. Its exit code is 2.
var errNoRepo = errors.New("no repository; run noahsark init, or give --repo")

// repoEnvVar names the environment variable that gives the repository.
const repoEnvVar = "NOAHSARK_REPO"

// isRepoDir reports whether dir holds a readable config file.
func isRepoDir(dir string) bool {
	_, err := os.Stat(configPath(dir))
	return err == nil
}

// abs makes path absolute against the working directory of e.
func (e *env) abs(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	wd, err := e.getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, path), nil
}

// findRepo returns the repository directory. The first hit wins: the
// global option --repo, then NOAHSARK_REPO, then the working directory
// and each ancestor of it. A --repo or NOAHSARK_REPO value that is not a
// repository is an error. No repository at all gives errNoRepo.
func (e *env) findRepo() (string, error) {
	given, source := e.givenRepo()
	if given != "" {
		dir, err := e.abs(given)
		if err != nil {
			return "", err
		}
		if !isRepoDir(dir) {
			return "", fmt.Errorf("%s%s is not a noahsark repository", source, given)
		}
		return dir, nil
	}
	dir, err := e.getwd()
	if err != nil {
		return "", err
	}
	for {
		if isRepoDir(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errNoRepo
		}
		dir = parent
	}
}

// givenRepo returns the repository that --repo or NOAHSARK_REPO gives,
// and a prefix that names the source for an error message. It returns
// an empty path when neither gives one.
func (e *env) givenRepo() (path, source string) {
	if e.global.repo != "" {
		return e.global.repo, ""
	}
	if v := e.getenv(repoEnvVar); v != "" {
		return v, repoEnvVar + "="
	}
	return "", ""
}

// recoverRepoDir returns the directory that recover writes into, also
// when it is not a repository yet: --repo, then NOAHSARK_REPO, then the
// repository that discovery finds.
func (e *env) recoverRepoDir() (string, error) {
	if given, _ := e.givenRepo(); given != "" {
		return e.abs(given)
	}
	dir, err := e.findRepo()
	if err == nil {
		return dir, nil
	}
	return "", fmt.Errorf("no repository directory given: give --repo, or set %s, to say where to rebuild one", repoEnvVar)
}
