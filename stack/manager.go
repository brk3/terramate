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

package stack

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mineiros-io/terramate/config"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/git"
	"github.com/mineiros-io/terramate/project"
	"github.com/mineiros-io/terramate/run"
	"github.com/mineiros-io/terramate/run/dag"
	"github.com/mineiros-io/terramate/stack/trigger"
	"github.com/mineiros-io/terramate/tf"
	"github.com/rs/zerolog/log"
)

type (
	// Manager is the terramate stacks manager.
	Manager struct {
		root       *config.Root // whole config
		gitBaseRef string       // gitBaseRef is the git ref where we compare changes.
	}

	// Report is the report of project's stacks and the result of its default checks.
	Report struct {
		Stacks []Entry

		// Checks contains the result info of default checks.
		Checks RepoChecks
	}

	// RepoChecks contains the info of default checks.
	RepoChecks struct {
		UncommittedFiles []string
		UntrackedFiles   []string
	}

	// Entry is a stack entry result.
	Entry struct {
		Stack  *config.Stack
		Reason string // Reason why this entry was returned.
	}
)

const errList errors.Kind = "listing stacks error"
const errListChanged errors.Kind = "listing changed stacks error"

// NewManager creates a new stack manager.The root is the project root config
// and and gitBaseRef is the git reference to compare for changes.
func NewManager(root *config.Root, gitBaseRef string) *Manager {
	return &Manager{
		root:       root,
		gitBaseRef: gitBaseRef,
	}
}

// List walks the basedir directory looking for terraform stacks.
// It returns a lexicographic sorted list of stack directories.
func (m *Manager) List() (*Report, error) {
	logger := log.With().
		Str("action", "Manager.List()").
		Logger()

	logger.Debug().Msg("List stacks.")

	entries, err := List(m.root.Tree())
	if err != nil {
		return nil, err
	}

	report := &Report{
		Stacks: entries,
	}

	logger.Trace().Str("repo", m.root.HostDir()).
		Msg("Create git wrapper for repo.")

	g, err := git.WithConfig(git.Config{
		WorkingDir: m.root.HostDir(),
	})
	if err != nil {
		return nil, errors.E(errList, err)
	}

	logger.Trace().Msg("Check if path is git repo.")
	if !g.IsRepository() {
		return report, nil
	}

	report.Checks, err = checkRepoIsClean(g)
	if err != nil {
		return nil, errors.E(errList, err)
	}

	return report, nil
}

// ListChanged lists the stacks that have changed on the current branch,
// compared to the main branch. This method assumes a version control
// system in place and that you are working on a branch that is not main.
// It's an error to call this method in a directory that's not
// inside a repository or a repository with no commits in it.
func (m *Manager) ListChanged() (*Report, error) {
	logger := log.With().
		Str("action", "ListChanged()").
		Logger()

	logger.Trace().Msg("Create git wrapper on project root.")

	g, err := git.WithConfig(git.Config{
		WorkingDir: m.root.HostDir(),
	})

	if err != nil {
		return nil, errors.E(errListChanged, err)
	}

	logger.Trace().Msg("Check if path is git repo.")

	if !g.IsRepository() {
		return nil, errors.E(
			errListChanged,
			"the path \"%s\" is not a git repository",
			m.root.HostDir(),
		)
	}

	checks, err := checkRepoIsClean(g)
	if err != nil {
		return nil, errors.E(errListChanged, err)
	}

	logger.Debug().Msg("List changed files.")

	changedFiles, err := listChangedFiles(m.root.HostDir(), m.gitBaseRef)
	if err != nil {
		return nil, errors.E(errListChanged, err)
	}

	stackSet := map[project.Path]Entry{}

	for _, path := range changedFiles {
		abspath := filepath.Join(m.root.HostDir(), path)
		projpath := project.PrjAbsPath(m.root.HostDir(), abspath)
		triggeredStack, isTriggerFile := trigger.StackPath(projpath)

		logger = logger.With().
			Stringer("path", projpath).
			Logger()

		if strings.HasPrefix(path, ".") && !isTriggerFile {
			logger.Debug().Msg("ignoring changed file starting with .")
			continue
		}

		if isTriggerFile {
			logger = logger.With().
				Stringer("trigger", triggeredStack).
				Logger()

			logger.Debug().Msg("trigger file change detected")

			if _, err := os.Stat(abspath); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					logger.Debug().Msg("ignoring deleted trigger file")
					continue
				}
			}

			cfg, found := m.root.Lookup(triggeredStack)
			if !found || !cfg.IsStack() {
				logger.Debug().Msg("trigger path is not a stack, nothing to do")
				continue
			}

			s, err := config.NewStackFromHCL(m.root.HostDir(), cfg.Node)
			if err != nil {
				return nil, errors.E(errListChanged, err)
			}

			stackSet[s.Dir] = Entry{
				Stack:  s,
				Reason: "stack has been triggered by: " + projpath.String(),
			}
			continue
		}

		dirname := filepath.Dir(abspath)

		if _, ok := stackSet[project.PrjAbsPath(m.root.HostDir(), dirname)]; ok {
			continue
		}

		logger.Debug().
			Str("path", dirname).
			Msg("Try load changed.")

		cfgpath := project.PrjAbsPath(m.root.HostDir(), dirname)
		stackTree, found := m.root.Lookup(cfgpath)
		if !found || !stackTree.IsStack() {
			logger.Debug().
				Str("path", dirname).
				Msg("Lookup parent stack.")

			checkdir := cfgpath
			for checkdir.String() != "/" {
				checkdir = checkdir.Dir()
				stackTree, found = m.root.Lookup(checkdir)
				if found && stackTree.IsStack() {
					break
				}
			}
			if !found || !stackTree.IsStack() {
				continue
			}
		}

		s, err := config.NewStackFromHCL(m.root.HostDir(), stackTree.Node)
		if err != nil {
			return nil, errors.E(errListChanged, err)
		}

		stackSet[s.Dir] = Entry{
			Stack:  s,
			Reason: "stack has unmerged changes",
		}
	}

	logger.Debug().Msg("Get list of all stacks.")

	allstacks, err := List(m.root.Tree())
	if err != nil {
		return nil, errors.E(errListChanged, "searching for stacks", err)
	}

	logger.Trace().Msg("Range over all stacks.")

rangeStacks:
	for _, stackEntry := range allstacks {
		stack := stackEntry.Stack
		if _, ok := stackSet[stack.Dir]; ok {
			continue
		}

		logger.Debug().
			Stringer("stack", stack).
			Msg("Check for changed watch files.")

		if changed, ok := hasChangedWatchedFiles(stack, changedFiles); ok {
			logger.Debug().
				Stringer("stack", stack).
				Stringer("watchfile", changed).
				Msg("changed.")

			stack.IsChanged = true
			stackSet[stack.Dir] = Entry{
				Stack: stack,
				Reason: fmt.Sprintf(
					"stack changed because watched file %q changed",
					changed,
				),
			}
			continue rangeStacks
		}

		logger.Debug().
			Stringer("stack", stack).
			Msg("Apply function to stack.")

		err := m.filesApply(stack.HostDir(m.root), func(file fs.DirEntry) error {
			if path.Ext(file.Name()) != ".tf" {
				return nil
			}

			logger.Debug().
				Stringer("stack", stack).
				Msg("Get tf file path.")

			tfpath := filepath.Join(stack.HostDir(m.root), file.Name())

			logger.Trace().
				Stringer("stack", stack).
				Str("configFile", tfpath).
				Msg("Parse modules.")

			modules, err := tf.ParseModules(tfpath)
			if err != nil {
				return errors.E(errListChanged, "parsing modules", err)
			}

			logger.Trace().
				Stringer("stack", stack).
				Str("configFile", tfpath).
				Msg("Range over modules.")

			for _, mod := range modules {
				logger.Trace().
					Stringer("stack", stack).
					Str("configFile", tfpath).
					Msg("Check if module changed.")

				changed, why, err := m.moduleChanged(mod, stack.HostDir(m.root), make(map[string]bool))
				if err != nil {
					return errors.E(errListChanged, err, "checking module %q", mod.Source)
				}

				if changed {
					logger.Debug().
						Stringer("stack", stack).
						Str("configFile", tfpath).
						Msg("Module changed.")

					stack.IsChanged = true
					stackSet[stack.Dir] = Entry{
						Stack: stack,
						Reason: fmt.Sprintf(
							"stack changed because %q changed because %s",
							mod.Source, why,
						),
					}
					return nil
				}
			}
			return nil
		})

		if err != nil {
			return nil, errors.E(errListChanged, "checking module changes", err)
		}
	}

	logger.Trace().Msg("Make set of changed stacks.")

	changedStacks := make([]Entry, 0, len(stackSet))
	for _, stack := range stackSet {
		changedStacks = append(changedStacks, stack)
	}

	logger.Trace().Msg("Sort changed stacks.")

	sort.Sort(EntrySlice(changedStacks))

	return &Report{
		Checks: checks,
		Stacks: changedStacks,
	}, nil
}

// AddWantedOf returns all wanted stacks from the given stacks.
func (m *Manager) AddWantedOf(scopeStacks config.List[*config.SortableStack]) (config.List[*config.SortableStack], error) {
	logger := log.With().
		Str("action", "manager.AddWantedOf").
		Logger()

	wantsDag := dag.New()
	allstacks, err := config.LoadAllStacks(m.root.Tree())
	if err != nil {
		return nil, errors.E(err, "loading all stacks")
	}

	visited := dag.Visited{}
	sort.Sort(allstacks)
	for _, elem := range allstacks {
		logger.Trace().
			Stringer("stack", elem.Dir()).
			Msg("Building dag")

		err := run.BuildDAG(
			wantsDag,
			m.root,
			elem.Stack,
			"wanted_by",
			func(s config.Stack) []string { return s.WantedBy },
			"wants",
			func(s config.Stack) []string { return s.Wants },
			visited,
		)

		if err != nil {
			return nil, errors.E(err, "building wants DAG")
		}
	}

	logger.Trace().Msg("Validating DAG.")

	reason, err := wantsDag.Validate()
	if err != nil {
		if errors.IsKind(err, dag.ErrCycleDetected) {
			logger.Warn().
				Str("reason", reason).
				Err(err).
				Msg("The stack selection clauses (wants/wanted_by) have cycles (ignored).")
		} else {
			logger.Warn().
				Err(err).
				Msg("The stack selection clauses (wants/wanted_by) have errors (ignored)")
		}
	}

	var selectedStacks config.List[*config.SortableStack]
	visited = dag.Visited{}
	addStack := func(s *config.Stack) {
		if _, ok := visited[dag.ID(s.Dir.String())]; ok {
			return
		}

		visited[dag.ID(s.Dir.String())] = struct{}{}
		selectedStacks = append(selectedStacks, s.Sortable())
	}

	var pending []dag.ID
	for _, s := range scopeStacks {
		pending = append(pending, dag.ID(s.Dir().String()))
	}

	for len(pending) > 0 {
		id := pending[0]
		node, _ := wantsDag.Node(id)
		s := node.(*config.Stack)

		addStack(s)
		pending = pending[1:]

		ancestors := wantsDag.AncestorsOf(id)
		for _, id := range ancestors {
			if _, ok := visited[id]; !ok {
				pending = append(pending, id)
			}
		}
	}
	return selectedStacks, nil
}

func (m *Manager) filesApply(dir string, apply func(file fs.DirEntry) error) error {
	logger := log.With().
		Str("action", "filesApply()").
		Str("path", dir).
		Logger()

	logger.Debug().
		Msg("Read dir.")
	files, err := os.ReadDir(dir)
	if err != nil {
		return errors.E(err, "listing files of directory %q", dir)
	}

	logger.Trace().
		Msg("Range files in dir.")
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		logger.Debug().
			Msg("Apply function to file.")
		err := apply(file)
		if err != nil {
			return errors.E(err, "applying operation to file %q", file.Name())
		}
	}

	return nil
}

// moduleChanged recursively check if the module mod or any of the modules it
// uses has changed. All .tf files of the module are parsed and this function is
// called recursively. The visited keep track of the modules already parsed to
// avoid infinite loops.
func (m *Manager) moduleChanged(
	mod tf.Module, basedir string, visited map[string]bool,
) (changed bool, why string, err error) {
	logger := log.With().
		Str("action", "moduleChanged()").
		Logger()

	if _, ok := visited[mod.Source]; ok {
		return false, "", nil
	}

	logger.Trace().
		Str("path", basedir).
		Msg("Check if module source is local directory.")
	if !mod.IsLocal() {
		// if the source is a remote path (URL, VCS path, S3 bucket, etc) then
		// we assume it's not changed.
		return false, "", nil
	}

	logger.Trace().
		Str("path", basedir).
		Msg("Get module path.")
	modPath := filepath.Join(basedir, mod.Source)

	logger.Trace().
		Str("path", modPath).
		Msg("Get module path info.")
	st, err := os.Stat(modPath)

	// TODO(i4k): resolve symlinks

	if err != nil || !st.IsDir() {
		return false, "", errors.E("\"source\" path %q is not a directory", modPath)
	}

	logger.Debug().
		Str("path", modPath).
		Msg("Get list of changed files.")
	changedFiles, err := listChangedFiles(modPath, m.gitBaseRef)
	if err != nil {
		return false, "", errors.E(err,
			"listing changes in the module %q",
			mod.Source)
	}

	if len(changedFiles) > 0 {
		return true, fmt.Sprintf("module %q has unmerged changes", mod.Source), nil
	}

	visited[mod.Source] = true

	logger.Debug().
		Str("path", modPath).
		Msg("Apply function to files in path.")
	err = m.filesApply(modPath, func(file fs.DirEntry) error {
		if changed {
			return nil
		}
		if path.Ext(file.Name()) != ".tf" {
			return nil
		}

		logger.Trace().
			Str("path", modPath).
			Msg("Parse modules.")
		modules, err := tf.ParseModules(filepath.Join(modPath, file.Name()))
		if err != nil {
			return errors.E(err, "parsing module %q", mod.Source)
		}

		logger.Trace().
			Str("path", modPath).
			Msg("Range over modules.")
		for _, mod2 := range modules {
			var reason string

			logger.Trace().
				Str("path", modPath).
				Msg("Get if module is changed.")
			changed, reason, err = m.moduleChanged(mod2, modPath, visited)
			if err != nil {
				return err
			}

			if changed {
				logger.Trace().
					Str("path", modPath).
					Msg("Module was changed.")
				why = fmt.Sprintf("%s%s changed because %s ", why, mod.Source, reason)
				return nil
			}
		}

		return nil
	})

	if err != nil {
		return false, "", err
	}

	return changed, fmt.Sprintf("module %q changed because %s", mod.Source, why), nil
}

// listChangedFiles lists all changed files in the dir directory.
func listChangedFiles(dir string, gitBaseRef string) ([]string, error) {
	logger := log.With().
		Str("action", "listChangedFiles()").
		Str("path", dir).
		Logger()

	logger.Trace().Msg("Get dir info.")

	st, err := os.Stat(dir)
	if err != nil {
		return nil, errors.E(err, "stat failed on %q", dir)
	}

	logger.Trace().Msg("Check if path is dir.")

	if !st.IsDir() {
		return nil, errors.E("is not a directory")
	}

	logger.Trace().Msg("Create git wrapper with dir.")

	g, err := git.WithConfig(git.Config{
		WorkingDir: dir,
	})
	if err != nil {
		return nil, err
	}

	logger.Trace().Msg("Get commit id of git base ref.")

	baseRef, err := g.RevParse(gitBaseRef)
	if err != nil {
		return nil, errors.E(err, "getting revision %q", gitBaseRef)
	}

	logger.Trace().Msg("Get commit id of HEAD.")

	headRef, err := g.RevParse("HEAD")
	if err != nil {
		return nil, errors.E(err, "getting HEAD revision")
	}

	if baseRef == headRef {
		return []string{}, nil
	}

	return g.DiffNames(baseRef, headRef)
}

func hasChangedWatchedFiles(stack *config.Stack, changedFiles []string) (project.Path, bool) {
	for _, watchFile := range stack.Watch {
		for _, file := range changedFiles {
			if file == watchFile.String()[1:] { // project paths
				return watchFile, true
			}
		}
	}
	return project.Path{}, false
}

func checkRepoIsClean(g *git.Git) (RepoChecks, error) {
	logger := log.With().
		Str("action", "checkRepoIsClean()").
		Logger()

	logger.Debug().Msg("Get list of untracked files.")

	untracked, err := g.ListUntracked()
	if err != nil {
		return RepoChecks{}, errors.E(err, "listing untracked files")
	}

	logger.Debug().Msg("Get list of uncommitted files in dir.")

	uncommitted, err := g.ListUncommitted()
	if err != nil {
		return RepoChecks{}, errors.E(err, "listing uncommitted files")
	}

	return RepoChecks{
		UntrackedFiles:   untracked,
		UncommittedFiles: uncommitted,
	}, nil
}

// EntrySlice implements the Sort interface.
type EntrySlice []Entry

func (x EntrySlice) Len() int           { return len(x) }
func (x EntrySlice) Less(i, j int) bool { return x[i].Stack.Dir.String() < x[j].Stack.Dir.String() }
func (x EntrySlice) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }
