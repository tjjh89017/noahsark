package image

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The loop device requests of linux/loop.h.
const (
	loopSetFD        = 0x4C00
	loopClrFD        = 0x4C01
	loopSetStatus64  = 0x4C04
	loopCtlGetFree   = 0x4C82
	loFlagsAutoclear = 4
	loNameSize       = 64
)

// loopInfo64 is struct loop_info64 of linux/loop.h, the argument of
// LOOP_SET_STATUS64. It is a kernel interface, not an on-disc structure.
// The blank fields are the fields that attachLoop leaves zero.
type loopInfo64 struct {
	_        [5]uint64 // lo_device, lo_inode, lo_rdevice, lo_offset, lo_sizelimit
	_        [3]uint32 // lo_number, lo_encrypt_type, lo_encrypt_key_size
	flags    uint32
	fileName [loNameSize]byte
	_        [loNameSize + 32]byte // lo_crypt_name, lo_encrypt_key
	_        [2]uint64             // lo_init
}

// loopInfo64Size is the size of struct loop_info64. The two arrays
// below do not compile when loopInfo64 has another size.
const loopInfo64Size = 232

var (
	_ [unsafe.Sizeof(loopInfo64{}) - loopInfo64Size]byte
	_ [loopInfo64Size - unsafe.Sizeof(loopInfo64{})]byte
)

// loopAttachTries is the number of free loop devices that attachLoop
// tries: another program can take a free device first.
const loopAttachTries = 8

// attachLoop attaches the open image file img to a free loop device and
// returns the path of the device and a function that detaches it. The
// device holds the descriptor, never the path, so a change of the path
// after the open does not change the file that the device reads. The
// device has the autoclear flag: the kernel detaches it when the last
// user closes it, also after a crash of this process. name is the text
// that losetup shows for the backing file.
func attachLoop(img *os.File, name string) (string, func() error, error) {
	ctl, err := os.OpenFile("/dev/loop-control", os.O_RDWR, 0)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = ctl.Close() }()

	var lastErr error
	for range loopAttachTries {
		n, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ctl.Fd(), loopCtlGetFree, 0)
		if errno != 0 {
			return "", nil, fmt.Errorf("/dev/loop-control: find a free loop device: %w", errno)
		}
		dev := fmt.Sprintf("/dev/loop%d", n)
		loop, err := os.OpenFile(dev, os.O_RDWR, 0)
		if err != nil {
			return "", nil, err
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, loop.Fd(), loopSetFD, img.Fd()); errno != 0 {
			_ = loop.Close()
			lastErr = fmt.Errorf("%s: attach: %w", dev, errno)
			if errors.Is(errno, syscall.EBUSY) {
				continue
			}
			return "", nil, lastErr
		}
		info := loopInfo64{flags: loFlagsAutoclear}
		copy(info.fileName[:loNameSize-1], name)
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, loop.Fd(), loopSetStatus64, uintptr(unsafe.Pointer(&info))); errno != 0 {
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, loop.Fd(), loopClrFD, 0)
			_ = loop.Close()
			return "", nil, fmt.Errorf("%s: set autoclear: %w", dev, errno)
		}
		detach := func() error {
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, loop.Fd(), loopClrFD, 0)
			closeErr := loop.Close()
			if errno != 0 && !errors.Is(errno, syscall.ENXIO) {
				return fmt.Errorf("%s: detach: %w", dev, errno)
			}
			return closeErr
		}
		return dev, detach, nil
	}
	return "", nil, lastErr
}
