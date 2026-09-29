package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// nextStatusLine is the last line of a command that changed state.
const nextStatusLine = "next: noahsark status"

// arkMount is the mount point that every block of status uses.
const arkMount = "/mnt/ark"

// folderBurnPointer follows the packed block. It is not part of the
// block: it points to the second burn method.
const folderBurnPointer = `or burn the folder directly; see the guide, "Burn the folder directly"`

// nextRepo is what the next block needs to know about a repository.
type nextRepo struct {
	// repo is the absolute path of the repository.
	repo string
	// device is pack.device, and source is sources.root.
	device string
	source string
	// staged is the number of Staged items.
	staged int
	now    time.Time
	// discs are the discs that are not undone, in the order of their
	// numbers.
	discs []nextDisc
}

// nextDisc is one disc as the next block sees it.
type nextDisc struct {
	// arg names the disc in a command line: the disc number, or the full
	// uuid when the number matches more than one disc.
	arg   string
	label string
	info  stage.DiscInfo
	// image is the path of the image of the disc. treeExists and
	// imageExists tell whether the disc root and the image exist.
	image       string
	treeExists  bool
	imageExists bool
}

// lastCheckFailed reports whether the newest check of the disc failed.
func (d nextDisc) lastCheckFailed() bool {
	return d.info.LastCheck == stage.CheckResultFailed
}

// nextBlock returns the lines that status prints after the disc lines:
// the one next block of the repository, and the lines that go with it.
// The first match in this order gives the block: a missing disc, an on
// disc only disc whose last check failed, a packed or burned disc, data
// that gc can free now, staged data, a verified disc that waits, and
// nothing. Inside one step the disc with the lowest number wins.
func nextBlock(r nextRepo) []string {
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
	if d, ok := r.first(func(d nextDisc) bool {
		return d.info.State == stage.DiscPacked || d.info.State == stage.DiscBurned
	}); ok {
		return r.burnBlock(d)
	}
	if d, ok := r.first(func(d nextDisc) bool {
		return d.info.State == stage.DiscVerified && !r.now.Before(gcFreeTime(d))
	}); ok {
		return []string{secondCopyAdvice(d), "next: noahsark gc"}
	}
	if r.staged > 0 {
		return []string{
			"next: load a blank disc, then run:",
			"dvd+rw-mediainfo " + quoteShellWord(r.device) + " | grep -E 'Mounted Media|Free Blocks'",
			"then paste this line, type the capacity, and press Enter:",
			"noahsark pack --capacity=",
		}
	}
	if d, ok := r.first(func(d nextDisc) bool { return d.info.State == stage.DiscVerified }); ok {
		return []string{
			fmt.Sprintf("next: nothing to do; gc can free disc %s after %s", d.arg, statusDate(gcFreeTime(d))),
			secondCopyAdvice(d),
		}
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

// missingBlock gives a missing disc to recover, or names it lost.
func (r nextRepo) missingBlock(d nextDisc) []string {
	return []string{
		fmt.Sprintf("next: load disc %s %q, then run:", d.arg, d.label),
		r.mountLine() + " &&",
		"noahsark recover --source=" + quoteShellWord(r.source) + " --disc=" + arkMount + " &&",
		"sudo umount " + arkMount,
		fmt.Sprintf("or, when disc %s is gone for good, run:", d.arg),
		"noahsark disc lost " + d.arg,
	}
}

// burnBlock gives the block of a packed disc, or of a burned disc. A
// packed disc, and a burned disc whose last check failed, need a new
// disc from the kept disc root. A disc with no disc root can only be
// named lost.
func (r nextRepo) burnBlock(d nextDisc) []string {
	dev := quoteShellWord(r.device)
	verifyLines := []string{
		"eject " + dev + " && eject -t " + dev + " && sleep 5 &&",
		r.mountLine() + " &&",
		"noahsark verify " + arkMount + " &&",
		"sudo umount " + arkMount + " && eject " + dev,
	}
	if d.info.State == stage.DiscBurned && !d.lastCheckFailed() {
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
	lines = append(lines, verifyLines...)
	return append(lines, folderBurnPointer)
}

// mountLine mounts the disc in the drive read-only on arkMount.
func (r nextRepo) mountLine() string {
	return "sudo mkdir -p " + arkMount + " && sudo mount -o ro " + quoteShellWord(r.device) + " " + arkMount
}

// secondCopyAdvice is the advice line of a verified disc.
func secondCopyAdvice(d nextDisc) string {
	if d.imageExists {
		return fmt.Sprintf(`advice: burn a second copy of %s before gc; see the guide, "A second copy"`, d.image)
	}
	return fmt.Sprintf(`advice: copy disc %s before gc; see the guide, "A second copy"`, d.arg)
}

// gcFreeTime is the time from which gc can free a verified disc.
func gcFreeTime(d nextDisc) time.Time {
	return d.info.VerifiedTime.Add(retainAfterClean)
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
