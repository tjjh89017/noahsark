package main

import (
	"encoding/hex"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
)

// File and directory names of a repository. OPERATIONS.md, "Repository,
// staging and catalog", gives the layout.
const (
	configFileName    = "config.yaml"
	gitignoreFileName = ".gitignore"
	stateDirName      = "state"
	stateLogFileName  = "state.db"
	discLogFileName   = "discstate.db"
	discsLedgerName   = "discs.bin"
	refsLedgerName    = "refslog.bin"
	refsFileName      = "refs.txt"
	catalogStateName  = "catalog-state.txt"
	chunksDirName     = "chunks"
	plansDirName      = "plans"
	planTreeName      = "tree"
	planImageName     = "tree.img"
)

// gitignoreLines are the lines that .gitignore of a repository holds:
// the lock file and the staging directory stay out of version control.
var gitignoreLines = []string{"/" + repolock.FileName, "/staging/"}

// repoLayout gives every path of one repository. repo is the
// repository directory. staging is the staging directory, absolute: the
// staging.dir key of the config, relative to repo when it is relative.
type repoLayout struct {
	repo    string
	staging string
}

// layoutOf returns the layout of the repository at repoDir with the
// config cfg.
func layoutOf(repoDir string, cfg repoConfig) repoLayout {
	return repoLayout{repo: repoDir, staging: cfg.StagingDir}
}

// configPath returns the path of the config file of the repository at
// repoDir. Repository discovery calls it before it reads a config.
func configPath(repoDir string) string {
	return filepath.Join(repoDir, configFileName)
}

func (l repoLayout) configFile() string    { return configPath(l.repo) }
func (l repoLayout) gitignoreFile() string { return filepath.Join(l.repo, gitignoreFileName) }
func (l repoLayout) lockFile() string      { return filepath.Join(l.repo, repolock.FileName) }

// stateDir holds the state files. staging.dir never moves it.
func (l repoLayout) stateDir() string         { return filepath.Join(l.repo, stateDirName) }
func (l repoLayout) stateLogFile() string     { return filepath.Join(l.stateDir(), stateLogFileName) }
func (l repoLayout) discLogFile() string      { return filepath.Join(l.stateDir(), discLogFileName) }
func (l repoLayout) discsLedgerFile() string  { return filepath.Join(l.stateDir(), discsLedgerName) }
func (l repoLayout) refsLedgerFile() string   { return filepath.Join(l.stateDir(), refsLedgerName) }
func (l repoLayout) refsFile() string         { return filepath.Join(l.stateDir(), refsFileName) }
func (l repoLayout) catalogStateFile() string { return filepath.Join(l.stateDir(), catalogStateName) }

// catalogDir is the catalog. The catalog package names the files in it.
func (l repoLayout) catalogDir() string { return catalog.Dir(l.repo) }

func (l repoLayout) stagingDir() string { return l.staging }
func (l repoLayout) chunksDir() string  { return filepath.Join(l.staging, chunksDirName) }
func (l repoLayout) plansDir() string   { return filepath.Join(l.staging, plansDirName) }

// chunkFile is the file of one chunk object, below a fan-out directory:
// the first two hex digits of the digest.
func (l repoLayout) chunkFile(id object.ID) string {
	return filepath.Join(l.chunksDir(), id.FanoutByte(), id.TextForm())
}

// planDir is the directory of the disc root and the image of one disc.
func (l repoLayout) planDir(discUUID [16]byte) string {
	return filepath.Join(l.plansDir(), uuidText(discUUID))
}

func (l repoLayout) planTree(discUUID [16]byte) string {
	return filepath.Join(l.planDir(discUUID), planTreeName)
}

func (l repoLayout) planImage(discUUID [16]byte) string {
	return filepath.Join(l.planDir(discUUID), planImageName)
}

// chunkPath gives the file of a chunk object, for the object writer.
func (l repoLayout) chunkPath(_ format.ObjectKind, id object.ID) string {
	return l.chunkFile(id)
}

// objectPath gives the file of every object kind: a chunk in staging,
// and a snapshot, tree or blob object in the catalog c.
func (l repoLayout) objectPath(c *catalog.Catalog) image.ObjectPathFunc {
	return func(kind format.ObjectKind, id object.ID) string {
		if kind == format.ObjectKindChunk {
			return l.chunkFile(id)
		}
		return c.MetaPath(kind, id)
	}
}

// uuidText is the one text form of a disc uuid in a path and in output:
// hyphenated, lower case.
func uuidText(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
