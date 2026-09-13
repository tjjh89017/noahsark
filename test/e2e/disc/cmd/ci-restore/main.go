// Command ci-restore is CI-only tooling, not a NoahsArk command surface.
// It resolves the LATEST snapshot from a disc tree's REFS table and
// restores it into an output directory, so CI can run the same restore
// check against a real UDF image.
//
// Usage: ci-restore DISC-ROOT OUT-DIR
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/restore"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: ci-restore DISC-ROOT OUT-DIR")
		os.Exit(2)
	}
	root, outDir := os.Args[1], os.Args[2]

	snapID, err := firstRefsSnapshot(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-restore:", err)
		os.Exit(1)
	}

	if err := restore.Restore(root, snapID, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "ci-restore:", err)
		os.Exit(1)
	}
	fmt.Println("ci-restore: restored snapshot", snapID.TextForm(), "into", outDir)
}

// firstRefsSnapshot reads the newest run's REFS table directly, not
// through image.Read, since image.Read also verifies parity, which a
// caller corrupting the image on purpose runs only after healing, not
// before restoring.
func firstRefsSnapshot(root string) (object.ID, error) {
	base, err := image.FindNoahsark(root)
	if err != nil {
		return object.ID{}, err
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		return object.ID{}, err
	}
	data, err := os.ReadFile(filepath.Join(runDir, "catalog", "REFS.bin"))
	if err != nil {
		return object.ID{}, err
	}
	var refs format.RefsTable
	if _, err := refs.Decode(data); err != nil {
		return object.ID{}, err
	}
	if len(refs.Records) == 0 {
		return object.ID{}, fmt.Errorf("REFS has no records")
	}
	return object.ID(refs.Records[0].SnapshotID), nil
}
