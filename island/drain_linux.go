// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// The usblp driver hands each write to USB and returns at once on a
// non-blocking file, then drops whatever is still on its way when the file
// closes. An empty write answers EAGAIN until the printer has taken it all.
func drain(f *os.File, wait time.Duration) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	end := time.Now().Add(wait)
	for {
		var werr error
		if err := rc.Control(func(fd uintptr) { _, werr = syscall.Write(int(fd), nil) }); err != nil {
			return err
		}
		if werr != syscall.EAGAIN && werr != syscall.EINTR {
			return werr
		}
		if time.Now().After(end) {
			return errors.New("the printer stopped taking the slip: check its paper and lid")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
