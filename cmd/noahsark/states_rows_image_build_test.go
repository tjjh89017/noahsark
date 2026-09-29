package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// imageDriver is a test command. It prepares the repository and then
// runs "image build" with a chosen user id:
//
//	image-build-as [--root] [--image-exists] [--no-tree] [--force] UUID DISC
//
// UUID names the disc whose plan directory the options change. DISC is
// the argument of image build. With no --root, the user id is 1000.
const imageDriver = "image-build-as"

// fakeImageSectorBytes is the length of the image that pack writes into
// DISC.bin for the --capacity=64MiB of repoWithDisc.
const fakeImageSectorBytes = "(67108864 bytes)"

// fakeMakeImage stands in for mkudffs and the loop mount: it makes a
// sparse file of the image length.
func fakeMakeImage(treeDir, imagePath string, sectors uint64, _ *progress.Reporter) error {
	if _, err := os.Stat(treeDir); err != nil {
		return err
	}
	f, err := os.Create(imagePath)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(sectors * 2048)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// fakeMkudffsVersion is a good udftools version.
func fakeMkudffsVersion() (string, error) { return "udftools 2.3", nil }

func imageDriverFlags(fs *flag.FlagSet) runFunc {
	root := fs.Bool("root", false, "run as root")
	imageExists := fs.Bool("image-exists", false, "write an old image file first")
	noTree := fs.Bool("no-tree", false, "remove the disc root first")
	force := fs.Bool("force", false, "pass --force to image build")
	return func(e *env, args []string) int {
		if len(args) != 2 {
			_, _ = fmt.Fprintln(e.stderr, "usage: noahsark "+imageDriver+" [options] UUID DISC")
			return 2
		}
		repoDir, err := e.findRepo()
		if err != nil {
			_, _ = fmt.Fprintln(e.stderr, err)
			return 2
		}
		cfg, err := readConfig(configPath(repoDir))
		if err != nil {
			_, _ = fmt.Fprintln(e.stderr, err)
			return 2
		}
		discUUID, err := decodeUUID(strings.ReplaceAll(args[0], "-", ""))
		if err != nil {
			_, _ = fmt.Fprintln(e.stderr, err)
			return 2
		}
		layout := layoutOf(repoDir, cfg)
		if *imageExists {
			if err := os.WriteFile(layout.planImage(discUUID), []byte("old image"), 0o644); err != nil {
				_, _ = fmt.Fprintln(e.stderr, err)
				return 2
			}
		}
		if *noTree {
			if err := os.RemoveAll(layout.planTree(discUUID)); err != nil {
				_, _ = fmt.Fprintln(e.stderr, err)
				return 2
			}
		}
		uid := 1000
		if *root {
			uid = 0
		}
		asUser := *e
		asUser.euid = func() int { return uid }
		o := &imageBuildOptions{force: *force}
		return o.run(&asUser, args[1:])
	}
}

// imageBuilt is the output of a good image build of the disc.
var imageBuilt = []string{"built image ", "{UUID}/tree.img " + fakeImageSectorBytes + "\n"}

// imageNeedsRoot is the refusal of an image build that is not root.
var imageNeedsRoot = []string{"image build needs root for the loop mount; run: sudo noahsark --repo=", " image build {SEQ}\n"}

// imageNoDiscRoot is the refusal of a disc with no disc root.
var imageNoDiscRoot = []string{"no disc root at ", "{UUID}/tree\n"}

// imageExistsRefusal is the refusal of an image that exists.
var imageExistsRefusal = []string{"{UUID}/tree.img exists; add --force to build it again\n"}

func init() {
	imageHost.mkudffsVersion = fakeMkudffsVersion
	imageHost.makeImage = fakeMakeImage
	register(&command{
		name:    imageDriver,
		usage:   imageDriver + " [--root] [--image-exists] [--no-tree] [--force] UUID DISC",
		summary: "Test command: prepare the repository, then run image build.",
		flags:   imageDriverFlags,
	})

	registerStateCases(
		stateCase{
			row: "15", name: "image build of a packed disc",
			start: stage.DiscPacked, args: []string{imageDriver, "--root", "{UUID}", "{SEQ}"},
			stdout: imageBuilt,
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "15", name: "image build of a packed disc, named by uuid",
			start: stage.DiscPacked, args: []string{imageDriver, "--root", "{UUID}", "{UUID}"},
			stdout: imageBuilt,
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
	)
	for _, start := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(
			stateCase{
				row: "16", name: "image build not as root, " + start.String(),
				start: start, args: []string{imageDriver, "{UUID}", "{SEQ}"},
				exit: 1, stderr: imageNeedsRoot,
				absent: []string{"built image", "--force"},
				end:    start,
			},
			stateCase{
				row: "17", name: "image build of an image that exists, " + start.String(),
				start: start, args: []string{imageDriver, "--root", "--image-exists", "{UUID}", "{SEQ}"},
				exit: 1, stderr: imageExistsRefusal,
				absent: []string{"built image"},
				end:    start,
			},
			stateCase{
				row: "17a", name: "image build --force of an image that exists, " + start.String(),
				start: start, args: []string{imageDriver, "--root", "--image-exists", "--force", "{UUID}", "{SEQ}"},
				stdout: imageBuilt,
				end:    start,
			},
			stateCase{
				row: "19", name: "image build with no disc root, as root, " + start.String(),
				start: start, args: []string{imageDriver, "--root", "--no-tree", "{UUID}", "{SEQ}"},
				exit: 1, stderr: imageNoDiscRoot,
				absent: []string{"built image"},
				end:    start,
			},
		)
	}
	for _, start := range []stage.DiscState{stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(stateCase{
			row: "18", name: "image build, disc " + start.String(),
			start: start, args: []string{imageDriver, "--root", "{UUID}", "{SEQ}"},
			stdout: imageBuilt,
			end:    start,
		})
	}
	// The state check comes first: these refusals need neither root nor
	// mkudffs.
	for _, start := range []stage.DiscState{stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing} {
		registerStateCases(stateCase{
			row: "19", name: "image build, disc " + start.String(),
			start: start, args: []string{imageDriver, "{UUID}", "{SEQ}"},
			exit: 1, stderr: imageNoDiscRoot,
			absent: []string{"built image", "needs root"},
			end:    start,
		})
	}
}
