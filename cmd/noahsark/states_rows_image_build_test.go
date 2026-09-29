package main

import (
	"os"
	"testing"

	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

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

// imageBuilt is the output of a good image build of the disc.
var imageBuilt = []string{"built image ", "{UUID}/tree.img " + fakeImageSectorBytes + "\n"}

// imageNeedsRoot is the refusal of an image build that is not root.
var imageNeedsRoot = []string{"image build needs root for the loop mount; run: sudo noahsark --repo=", " image build {SEQ}\n"}

// imageNoDiscRoot is the refusal of a disc with no disc root.
var imageNoDiscRoot = []string{"no disc root at ", "{UUID}/tree\n"}

// imageExistsRefusal is the refusal of an image that exists.
var imageExistsRefusal = []string{"{UUID}/tree.img exists; add --force to build it again\n"}

// imageExistsSetup writes an old image file for the disc of fx.
func imageExistsSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	if err := os.WriteFile(testLayout(t, fx.repo).planImage(fx.uuidBytes(t)), []byte("old image"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noTreeSetup removes the disc root of the disc of fx from its plan
// directory.
func noTreeSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	if err := os.RemoveAll(testLayout(t, fx.repo).planTree(fx.uuidBytes(t))); err != nil {
		t.Fatal(err)
	}
}

func init() {
	imageHost.mkudffsVersion = fakeMkudffsVersion
	imageHost.makeImage = fakeMakeImage

	registerStateCases(
		stateCase{
			row: "15", name: "image build of a packed disc",
			start: stage.DiscPacked, root: true, args: []string{"image", "build", "{SEQ}"},
			stdout: imageBuilt,
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "15", name: "image build of a packed disc, named by uuid",
			start: stage.DiscPacked, root: true, args: []string{"image", "build", "{UUID}"},
			stdout: imageBuilt,
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
	)
	for _, start := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(
			stateCase{
				row: "16", name: "image build not as root, " + start.String(),
				start: start, args: []string{"image", "build", "{SEQ}"},
				exit: 1, stderr: imageNeedsRoot,
				absent: []string{"built image", "--force"},
				end:    start,
			},
			stateCase{
				row: "17", name: "image build of an image that exists, " + start.String(),
				start: start, root: true, setup: imageExistsSetup,
				args: []string{"image", "build", "{SEQ}"},
				exit: 1, stderr: imageExistsRefusal,
				absent: []string{"built image"},
				end:    start,
			},
			stateCase{
				row: "17a", name: "image build --force of an image that exists, " + start.String(),
				start: start, root: true, setup: imageExistsSetup,
				args:   []string{"image", "build", "--force", "{SEQ}"},
				stdout: imageBuilt,
				end:    start,
			},
			stateCase{
				row: "19", name: "image build with no disc root, as root, " + start.String(),
				start: start, root: true, setup: noTreeSetup,
				args: []string{"image", "build", "{SEQ}"},
				exit: 1, stderr: imageNoDiscRoot,
				absent: []string{"built image"},
				end:    start,
			},
		)
	}
	for _, start := range []stage.DiscState{stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(stateCase{
			row: "18", name: "image build, disc " + start.String(),
			start: start, root: true, args: []string{"image", "build", "{SEQ}"},
			stdout: imageBuilt,
			end:    start,
		})
	}
	// The state check comes first: these refusals need neither root nor
	// mkudffs.
	for _, start := range []stage.DiscState{stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing} {
		registerStateCases(stateCase{
			row: "19", name: "image build, disc " + start.String(),
			start: start, args: []string{"image", "build", "{SEQ}"},
			exit: 1, stderr: imageNoDiscRoot,
			absent: []string{"built image", "needs root"},
			end:    start,
		})
	}
}
