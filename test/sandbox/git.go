// Copyright 2021 Mineiros GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sandbox

import (
	"fmt"
	"testing"
	"time"

	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/git"
	"github.com/mineiros-io/terramate/test"
)

// GitConfig configures the sandbox's git repository.
type GitConfig struct {
	LocalBranchName         string
	DefaultRemoteName       string
	DefaultRemoteBranchName string

	repoDir string
}

// Git is a git wrapper that makes testing easy by handling
// errors automatically, failing the caller test.
type Git struct {
	t        testing.TB
	g        *git.Git
	cfg      GitConfig
	bareRepo string
}

// NewGit creates a new git wrapper using sandbox defaults.
func NewGit(t testing.TB, repodir string) *Git {
	t.Helper()

	cfg := defaultGitConfig()
	cfg.repoDir = repodir

	return &Git{
		t:   t,
		cfg: cfg,
		g:   test.NewGitWrapper(t, repodir, []string{}),
	}
}

// NewGitWithConfig creates a new git wrapper with the provided GitConfig.
func NewGitWithConfig(t testing.TB, cfg GitConfig) *Git {
	return &Git{
		t:   t,
		cfg: cfg,
		g:   test.NewGitWrapper(t, cfg.repoDir, []string{}),
	}
}

// Init will initialize the local git repo with a default remote.
// After calling Init(), the methods Push() and Pull() pushes and pulls changes
// from/to the configured default remote.
func (git *Git) Init() {
	t := git.t
	t.Helper()

	git.InitLocalRepo()

	// the main branch only exists after first commit.
	// The entropy is used to generate different root commits for different repos.
	// So we can test if disjoint branches are not reachable (ie. no merge-base).
	path := test.WriteFile(t, git.cfg.repoDir, "README.md",
		fmt.Sprintf("# generated by terramate (entropy %d)", time.Now().UnixNano()))
	git.Add(path)
	git.Commit("first commit")
	git.configureDefaultRemote()
}

// BareRepoAbsPath returns the path for the bare remote repository of this
// repository.
func (git *Git) BareRepoAbsPath() string {
	git.t.Helper()
	if git.bareRepo == "" {
		git.t.Fatal("baregit not initialized")
	}
	return git.bareRepo
}

func (git *Git) configureDefaultRemote() {
	cfg := git.cfg
	remoteRepo := git.initRemoteRepo(cfg.DefaultRemoteBranchName)
	git.RemoteAdd(cfg.DefaultRemoteName, remoteRepo)
	// Pushes current branch onto defRemote and defBranch
	git.PushOn(cfg.DefaultRemoteName, cfg.DefaultRemoteBranchName, cfg.LocalBranchName)

}

// SetupRemote creates a bare remote repository and setup the local repo with it
// using remoteName and remoteBranch.
func (git Git) SetupRemote(remoteName, remoteBranch, localBranch string) {
	remoteRepo := git.initRemoteRepo(remoteBranch)
	git.RemoteAdd(remoteName, remoteRepo)
	git.PushOn(remoteName, remoteBranch, localBranch)
}

func (git *Git) initRemoteRepo(branchName string) string {
	t := git.t
	t.Helper()

	git.bareRepo = t.TempDir()
	baregit := test.NewGitWrapper(t, git.bareRepo, []string{})

	err := baregit.Init(git.bareRepo, branchName, true)
	assert.NoError(t, err, "Git.Init(%v, %v, true)", git.bareRepo, branchName)

	return git.bareRepo
}

// InitLocalRepo will do the git initialization of a local repository,
// not providing a remote configuration.
func (git Git) InitLocalRepo() {
	t := git.t
	t.Helper()

	if err := git.g.Init(git.cfg.repoDir, git.cfg.LocalBranchName, false); err != nil {
		t.Fatalf("Git.Init(%v, %v, false) = %v", git.cfg.repoDir, git.cfg.LocalBranchName, err)
	}
}

// RevParse parses the reference name and returns the reference hash.
func (git Git) RevParse(ref string) string {
	git.t.Helper()

	val, err := git.g.RevParse(ref)
	if err != nil {
		git.t.Fatalf("Git.RevParse(%v) = %v", ref, err)
	}

	return val
}

// RemoteAdd adds a new remote on the repo
func (git Git) RemoteAdd(name, url string) {
	err := git.g.RemoteAdd(name, url)
	assert.NoError(git.t, err, "Git.RemoteAdd(%v, %v)", name, url)
}

// Add will add files to the commit list
func (git Git) Add(files ...string) {
	git.t.Helper()

	if err := git.g.Add(files...); err != nil {
		git.t.Fatalf("Git.Add(%v) = %v", files, err)
	}
}

// AddSubmodule adds name as a submodule for the provided url.
func (git Git) AddSubmodule(name string, url string) {
	git.t.Helper()
	if _, err := git.g.AddSubmodule(name, url); err != nil {
		git.t.Fatalf("Git.AddSubmodule(%v) = %v", url, err)
	}
}

// CurrentBranch returns the short branch name that HEAD points to.
func (git *Git) CurrentBranch() string {
	git.t.Helper()

	branch, err := git.g.CurrentBranch()
	if err != nil {
		git.t.Fatalf("Git.CurrentBranch() = %v", err)
	}
	return branch
}

// DeleteBranch deletes the ref branch.
func (git Git) DeleteBranch(ref string) {
	git.t.Helper()

	if err := git.g.DeleteBranch(ref); err != nil {
		git.t.Fatalf("Git.DeleteBranch(%q) = %v", ref, err)
	}
}

// Commit will commit previously added files
func (git Git) Commit(msg string, args ...string) {
	git.t.Helper()

	if err := git.g.Commit(msg, args...); err != nil {
		git.t.Fatalf("Git.Commit(%q, %v) = %v", msg, args, err)
	}
}

// Clone will clone a repository into the given dir.
func (git Git) Clone(repoURL, dir string) {
	git.t.Helper()

	if err := git.g.Clone(repoURL, dir); err != nil {
		git.t.Fatalf("Git.Clone(%q, %q) = %v", repoURL, dir, err)
	}
}

// Push pushes changes from branch onto default remote and same remote branch name.
func (git Git) Push(branch string) {
	git.t.Helper()
	git.PushOn(git.cfg.DefaultRemoteName, branch, branch)
}

// PushOn pushes changes from localBranch onto the given remote and remoteBranch.
func (git Git) PushOn(remote, remoteBranch, localBranch string) {
	git.t.Helper()

	err := git.g.Push(remote, fmt.Sprintf("%s:%s", localBranch, remoteBranch))
	if err != nil {
		git.t.Fatalf("Git.Push(%v, %v) = %v", remote, localBranch, err)
	}
}

// Pull pulls changes from default remote into branch
func (git Git) Pull(branch string) {
	git.t.Helper()

	if err := git.g.Pull(git.cfg.DefaultRemoteName, branch); err != nil {
		git.t.Fatalf("Git.Pull(%v, %v) = %v", git.cfg.DefaultRemoteName, branch, err)
	}
}

// CommitAll will add all changed files and commit all of them.
// It requires files to be committed otherwise it fails.
func (git Git) CommitAll(msg string, ignoreErr ...bool) {
	git.t.Helper()

	ignore := len(ignoreErr) > 0 && ignoreErr[0]

	if err := git.g.Add("."); err != nil && !ignore {
		git.t.Fatalf("Git.Add(.) = %v", err)
	}
	if err := git.g.Commit(msg); err != nil && !ignore {
		git.t.Fatalf("Git.Commit(%q) = %v", msg, err)
	}
}

// Checkout will checkout a pre-existing revision
func (git Git) Checkout(rev string) {
	git.t.Helper()
	git.checkout(rev, false)
}

// CheckoutNew will checkout a new revision (creating it on the process)
func (git Git) CheckoutNew(rev string) {
	git.t.Helper()
	git.checkout(rev, true)
}

func (git Git) checkout(rev string, create bool) {
	git.t.Helper()

	if err := git.g.Checkout(rev, create); err != nil {
		git.t.Fatalf("Git.Checkout(%s, %v) = %v", rev, create, err)
	}
}

// Merge will merge the current branch with the given branch.
// Fails the caller test if an error is found.
func (git Git) Merge(branch string) {
	git.t.Helper()

	if err := git.g.Merge(branch); err != nil {
		git.t.Fatalf("Git.Merge(%s) = %v", branch, err)
	}
}

// SetRemoteURL sets the URL of the remote.
func (git Git) SetRemoteURL(remote, url string) {
	git.t.Helper()
	assert.NoError(git.t, git.g.SetRemoteURL(remote, url))
}

// BaseDir the repository base dir
func (git Git) BaseDir() string {
	return git.cfg.repoDir
}

func defaultGitConfig() GitConfig {
	return GitConfig{
		LocalBranchName:         "main",
		DefaultRemoteName:       "origin",
		DefaultRemoteBranchName: "main",
	}
}
