package util

import (
	"errors"
	"time"
)

var fileLockTimeout = 10 * time.Second

func FileLockTimeout() time.Duration { return fileLockTimeout }

const fileLockRetryInterval = 10 * time.Millisecond

var errFileLockTimeout = errors.New("file lock busy: holder did not release within timeout")

func acquireWithRetry(try func() error) error {
	deadline := time.Now().Add(fileLockTimeout)
	for {
		err := try()
		if err == nil {
			return nil
		}
		if !isLockBusy(err) {
			return err
		}
		if time.Now().After(deadline) {
			return errFileLockTimeout
		}
		time.Sleep(fileLockRetryInterval)
	}
}
