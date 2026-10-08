package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// nextStatusLine ends the output of a command that changed state when
// the command cannot read the repository for the next block.
const nextStatusLine = "next: noahsark status"

// arkMount is the mount point that every block of status uses.
const arkMount = "/mnt/ark"

// folderBurnHead starts the second part of the packed block: the
// second burn method, a direct burn of the folder.
const folderBurnHead = `or burn the folder directly; see the guide, "Burn the folder directly". Load a blank disc, then run:`

// nextRepo is what the next block needs to know about a repository.
type nextRepo struct {
	// repo is the absolute path of the repository.
	repo string
	// staging is the absolute path of the staging directory, and
	// stagingMissing tells that it does not exist while a Staged or a
	// Packed item needs it.
	staging        string
	stagingMissing bool
	// newestSnapshot is the time of the newest snapshot of the catalog.
	newestSnapshot time.Time
	// noSnapshot tells that the catalog holds no snapshot.
	noSnapshot bool
	// device is pack.device, and source is sources.root.
	device string
	source string
	// staged is the number of Staged items.
	staged int
	// discs are the discs that are not undone, in the order of their
	// numbers.
	discs []nextDisc
	// repairs are the discs whose item records do not follow their
	// state, in the order of their numbers.
	repairs []nextRepair
}

// nextRepair is one disc whose item records do not follow its state.
type nextRepair struct {
	// arg names the disc as nextDisc.arg does.
	arg string
	// command is the command that stopped after its disc event.
	command string
}

// nextDisc is one disc as the next block sees it.
type nextDisc struct {
	// arg names the disc in a command line: the disc number, or the full
	// uuid when the number matches more than one disc.
	arg   string
	seq   uint64
	label string
	info  stage.DiscInfo
	// tree is the disc root of the disc: the target of the symlink for a
	// pack --out disc. image is the path of the image of the disc.
	// treeExists and imageExists tell whether the disc root and the
	// image exist.
	tree        string
	image       string
	treeExists  bool
	imageExists bool
}

// foundOnDiscOnly reports whether the disc is on disc only, and disc lost
// --undo gave it back after its last check: it needs a check.
func (d nextDisc) foundOnDiscOnly() bool {
	return d.info.State == stage.DiscOnDiscOnly && d.info.LastEvent == stage.EventLostUndone
}

// lastCheckFailed reports whether the newest check of the disc failed.
func (d nextDisc) lastCheckFailed() bool {
	return d.info.LastCheck == stage.CheckResultFailed
}

// waitsForCommit reports whether the data of a lost disc can wait for a
// commit: the disc was on disc only or missing when it was marked lost,
// thus no staged copy of its data is left.
func waitsForCommit(info stage.DiscInfo) bool {
	return info.State == stage.DiscLost && (info.BeforeLost == stage.DiscOnDiscOnly || info.BeforeLost == stage.DiscMissing)
}

// adviceLines returns one advice line for each verified disc, in the
// order of the disc numbers. gc frees the data of each of them at one
// time, thus status prints them before any block.
func adviceLines(r nextRepo) []string {
	var lines []string
	for _, d := range r.discs {
		if d.info.State == stage.DiscVerified {
			lines = append(lines, secondCopyAdvice(d))
		}
	}
	return lines
}

// nextBlock returns the one next block of the repository. The first
// match in this order gives the block: a staging directory that does
// not exist while an item needs it, a disc whose item records do not
// follow its state, a missing disc, an on disc only disc whose last
// check failed, a lost disc whose data waits for a commit, a disc to
// burn or to verify, a verified disc whose data gc can free, staged
// data, no snapshot, and nothing. Inside one step the disc with the
// lowest number wins.
func nextBlock(r nextRepo) []string {
	if r.stagingMissing {
		return []string{
			fmt.Sprintf("next: staging directory %s does not exist. Mount its volume, or correct staging.dir in config.yaml. When the staging store is gone for good, run:", r.staging),
			"mkdir -p " + quoteShellWord(r.staging),
		}
	}
	if len(r.repairs) > 0 {
		d := r.repairs[0]
		return []string{
			fmt.Sprintf("next: disc %s: an earlier %s stopped before it wrote the records of its items; gc writes them, and also frees the data of each verified disc; run:", d.arg, d.command),
			"noahsark gc",
		}
	}
	if d, ok := r.first(func(d nextDisc) bool { return d.info.State == stage.DiscMissing }); ok {
		return r.missingBlock(d)
	}
	if d, ok := r.first(func(d nextDisc) bool {
		return d.info.State == stage.DiscOnDiscOnly && d.lastCheckFailed()
	}); ok {
		return []string{
			fmt.Sprintf(`next: disc %s failed its last check. Copy it now, or use your second copy; see the guide, "A second copy". When no copy can be read, run:`, d.arg),
			"noahsark disc lost " + d.arg + " && noahsark commit",
		}
	}
	// A commit in the second of the Lost event counts as a later commit:
	// the event keeps whole seconds only.
	if d, ok := r.first(func(d nextDisc) bool {
		return waitsForCommit(d.info) && r.newestSnapshot.Unix() < d.info.LastEventTime.Unix()
	}); ok {
		return []string{
			fmt.Sprintf("next: disc %s is lost; a new commit stages what the source still holds; run:", d.arg),
			"noahsark commit",
		}
	}
	if d, ok := r.first(func(d nextDisc) bool {
		return d.info.State == stage.DiscPacked || d.info.State == stage.DiscBurned || d.foundOnDiscOnly()
	}); ok {
		return r.burnBlock(d)
	}
	if _, ok := r.first(func(d nextDisc) bool { return d.info.State == stage.DiscVerified }); ok {
		return []string{"next: noahsark gc"}
	}
	if r.staged > 0 {
		return []string{
			"next: load a blank disc, then run:",
			"dvd+rw-mediainfo " + quoteShellWord(r.device) + " | grep -E 'Mounted Media|Free Blocks'",
			"then paste this line, type the capacity, and press Enter:",
			"noahsark pack --capacity=",
		}
	}
	if r.noSnapshot {
		return []string{"next: noahsark commit"}
	}
	return []string{"next: nothing to do"}
}

// first returns the first disc for which match reports true.
func (r nextRepo) first(match func(nextDisc) bool) (nextDisc, bool) {
	for _, d := range r.discs {
		if match(d) {
			return d, true
		}
	}
	return nextDisc{}, false
}

// missingBlock gives a missing disc to recover, or names it lost. The
// unmount follows a ";": recover exits 1 while another disc is missing.
func (r nextRepo) missingBlock(d nextDisc) []string {
	return []string{
		fmt.Sprintf("next: load disc %s %q, then run:", d.arg, d.label),
		r.driveMountLine() + " &&",
		"noahsark recover --source=" + quoteShellWord(r.source) + " --disc=" + arkMount + ";",
		r.unmountLine(),
		fmt.Sprintf("or, when disc %s is gone for good, run:", d.arg),
		"noahsark disc lost " + d.arg,
	}
}

// burnBlock gives the block of a packed disc, of a burned disc, or of a
// found on disc only disc. A packed disc, and a burned disc whose last
// check failed, need a new disc from the kept disc root. A disc with no
// disc root can only be named lost. The unmount follows a ";", so that
// a failed verify leaves no disc mounted. A burned disc is in the drive
// already, thus its block does not eject it.
func (r nextRepo) burnBlock(d nextDisc) []string {
	verifyLines := []string{
		r.driveMountLine() + " &&",
		"noahsark verify " + arkMount + ";",
		r.unmountLine(),
	}
	if d.foundOnDiscOnly() || d.info.State == stage.DiscBurned && !d.lastCheckFailed() {
		return append([]string{fmt.Sprintf("next: load disc %s, then run:", d.arg)}, verifyLines...)
	}
	if !d.treeExists {
		return []string{
			fmt.Sprintf("next: disc %s has no disc root; no new disc can be burned from it. Discard the disc, then run:", d.arg),
			"noahsark disc lost " + d.arg,
		}
	}
	lines := []string{"next: load a blank disc, then run:"}
	if !d.imageExists {
		lines = append(lines, "sudo noahsark --repo="+quoteShellWord(r.repo)+" image build "+d.arg+" &&")
	}
	burn := "growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z "
	if d.info.Close {
		burn = "growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty -Z "
	}
	lines = append(lines, burn+quoteShellWord(r.device+"="+d.image)+" &&")
	lines = append(lines, r.afterBurnLines()...)
	return append(lines, r.folderBurnLines(d)...)
}

// folderBurnLines is the second part of the packed block: a check of the
// disc root, a direct burn of it as ISO 9660 with Rock Ridge, and the
// lines after a burn. A disc packed with --close is sealed.
func (r nextRepo) folderBurnLines(d nextDisc) []string {
	tree := quoteShellWord(d.tree)
	burn := "growisofs -Z "
	if d.info.Close {
		burn = "growisofs -dvd-compat -Z "
	}
	lines := []string{
		folderBurnHead,
		"noahsark verify " + tree + " &&",
		burn + quoteShellWord(r.device) + " -R -iso-level 4 -V " + image.VolumeLabel(d.seq) + " " + tree + " &&",
	}
	return append(lines, r.afterBurnLines()...)
}

// afterBurnLines load the burned disc again, so that the read comes from
// the disc, then mount it and verify it. sudo -v renews the sudo ticket:
// a burn can take longer than the sudo timeout.
func (r nextRepo) afterBurnLines() []string {
	dev := quoteShellWord(r.device)
	return []string{
		"eject " + dev + " && eject -t " + dev + " &&",
		"sudo -v && " + r.driveMountLine() + " &&",
		"noahsark verify " + arkMount + ";",
		r.unmountLine(),
	}
}

// driveMountLine mounts the disc in the drive read-only on arkMount. A
// drive needs some seconds to read a disc that it just loaded, thus the
// line tries the mount up to 30 times, 2 seconds apart. The loop ends
// with exit code 0, thus mountpoint fails when no try mounted the disc.
func (r nextRepo) driveMountLine() string {
	return "sudo mkdir -p " + arkMount + " && for i in $(seq 30); do sudo mount -o ro " + quoteShellWord(r.device) + " " + arkMount +
		" 2>/dev/null && break; sleep 2; done && mountpoint " + arkMount
}

// unmountLine unmounts arkMount and ejects the disc. The eject runs also
// when the umount fails, as when the disc did not mount.
func (r nextRepo) unmountLine() string {
	return "sudo umount " + arkMount + "; eject " + quoteShellWord(r.device)
}

// secondCopyAdvice is the advice line of a verified disc.
func secondCopyAdvice(d nextDisc) string {
	if d.imageExists {
		return fmt.Sprintf(`advice: burn a second copy of %s before gc; see the guide, "A second copy"`, d.image)
	}
	return fmt.Sprintf(`advice: copy disc %s before gc; see the guide, "A second copy"`, d.arg)
}

// statusDate is the DATE of status and gc: the local date, YYYY-MM-DD.
func statusDate(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// plainShellWordRe matches a word that the shell reads as it is.
var plainShellWordRe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// quoteShellWord returns s as one shell word. It puts s in single quotes
// when s holds a character that the shell would change.
func quoteShellWord(s string) string {
	if plainShellWordRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
