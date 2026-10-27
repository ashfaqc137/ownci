package cli

import (
	"io"
	"os"
	"syscall"
	"unsafe"

	"github.com/ashfaqc137/ownci/internal/progress"
)

// isTerminal asks the tty driver whether f is a terminal. (A character-device
// check would also accept /dev/null.)
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

// newProgress reports progress on out, redrawing a live line only when out
// is the process's stdout and a terminal that can do it.
func newProgress(out io.Writer) *progress.Printer {
	live := out == io.Writer(os.Stdout) && isTerminal(os.Stdout) && os.Getenv("TERM") != "dumb"
	return progress.New(out, live)
}
