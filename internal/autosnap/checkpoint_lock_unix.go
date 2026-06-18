//go:build !windows

package autosnap

import (
	"errors"
	"os"
	"syscall"
)

func tryLockCheckpointFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errCheckpointLockBusy
	}
	return err
}

func unlockCheckpointFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
