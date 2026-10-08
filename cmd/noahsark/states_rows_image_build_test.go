package main

import (
	"os"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// fakeImageBytes is the length of the image that pack writes into
// DISC.bin for the --capacity=64MiB of repoWithDisc.
const fakeImageBytes = "67108864"

// fakeMakeImage stands in for mkudffs and the loop mount: it makes a
// sparse file of the image length through the descriptors of the plan.
func fakeMakeImage(plan *image.Plan, sectors uint64, _ string, _ *progress.Reporter) error {
	img, err := plan.CreateImage()
	if err != nil {
		return err
	}
	defer func() { _ = img.Close() }()
	if err := img.Truncate(int64(sectors * 2048)); err != nil {
		plan.DiscardImage(img)
		return err
	}
	return plan.FinishImage(img)
}

// fakeMkudffsVersion is a good udftools version.
func fakeMkudffsVersion() (string, error) { return "udftools 2.3", nil }

// imageCells gives the cells the image file, the disc root and the
// length of the image of the disc of fx.
func imageCells(t *testing.T, fx *discFixture) {
	t.Helper()
	layout := testLayout(t, fx.repo)
	fx.cell("FILE", layout.planImage(fx.uuidBytes(t)))
	fx.cell("DIR", layout.planTree(fx.uuidBytes(t)))
	fx.cell("N", fakeImageBytes)
}

// imageExistsSetup writes an old image file for the disc of fx.
func imageExistsSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	imageCells(t, fx)
	if err := os.WriteFile(testLayout(t, fx.repo).planImage(fx.uuidBytes(t)), []byte("old image"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noTreeSetup removes the disc root of the disc of fx from its plan
// directory.
func noTreeSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	imageCells(t, fx)
	if err := os.RemoveAll(testLayout(t, fx.repo).planTree(fx.uuidBytes(t))); err != nil {
		t.Fatal(err)
	}
}

func init() {
	registerStateCases(
		stateCase{
			row: "15", name: "image build of a packed disc",
			start: stage.DiscPacked, root: true, args: []string{"image", "build", "{SEQ}"},
			setup: imageCells, exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "15", name: "image build of a packed disc, named by uuid",
			start: stage.DiscPacked, root: true, args: []string{"image", "build", "{UUID}"},
			setup: imageCells, exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
	)
	for _, start := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(
			stateCase{
				row: "16", name: "image build not as root, " + start.String(),
				start: start, args: []string{"image", "build", "{SEQ}"},
				exactStderr: true,
				absent:      []string{"built image", "--force"},
				end:         start,
			},
			stateCase{
				row: "17", name: "image build of an image that exists, " + start.String(),
				start: start, root: true, setup: imageExistsSetup,
				args:        []string{"image", "build", "{SEQ}"},
				exactStderr: true,
				absent:      []string{"built image"},
				end:         start,
			},
			stateCase{
				row: "17a", name: "image build --force of an image that exists, " + start.String(),
				start: start, root: true, setup: imageExistsSetup,
				args:  []string{"image", "build", "--force", "{SEQ}"},
				exact: true, exactStderr: true,
				end: start,
			},
			stateCase{
				row: "19", name: "image build with no disc root, as root, " + start.String(),
				start: start, root: true, setup: noTreeSetup,
				args:        []string{"image", "build", "{SEQ}"},
				exactStderr: true,
				absent:      []string{"built image"},
				end:         start,
			},
		)
	}
	for _, start := range []stage.DiscState{stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(stateCase{
			row: "18", name: "image build, disc " + start.String(),
			start: start, root: true, args: []string{"image", "build", "{SEQ}"},
			setup: imageCells, exact: true, exactStderr: true,
			end: start,
		})
	}
	// The state check comes first: these refusals need neither root nor
	// mkudffs.
	for _, start := range []stage.DiscState{stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing} {
		registerStateCases(stateCase{
			row: "19", name: "image build, disc " + start.String(),
			start: start, setup: imageCells, args: []string{"image", "build", "{SEQ}"},
			exactStderr: true,
			absent:      []string{"built image", "needs root"},
			end:         start,
		})
	}
}
