//go:build openbsd || darwin || freebsd || netbsd

package console

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenTTY öffnet das Terminalgerät einer VM im Rohmodus mit 115200 Baud –
// dieselben Einstellungen, die cu(1) bei "vmctl console" verwendet.
func OpenTTY(path string) (io.ReadWriteCloser, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	rc, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	var terr error
	rc.Control(func(fd uintptr) {
		t, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
		if err != nil {
			terr = err
			return
		}
		// cfmakeraw(3)
		t.Iflag &^= unix.IMAXBEL | unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
		t.Oflag &^= unix.OPOST
		t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
		t.Cflag &^= unix.CSIZE | unix.PARENB
		t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
		t.Cc[unix.VMIN] = 1
		t.Cc[unix.VTIME] = 0
		t.Ispeed = unix.B115200
		t.Ospeed = unix.B115200
		terr = unix.IoctlSetTermios(int(fd), unix.TIOCSETA, t)
	})
	if terr != nil {
		f.Close()
		return nil, terr
	}
	return f, nil
}
