package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// WithPluginSettingsLock serializes control-plane writes across processes.
// The installed packages and runtime config remain outside this document.
func WithPluginSettingsLock(ctx context.Context, path string, apply func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return apply()
}
func WritePluginSettings(path string, raw []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".plugins-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
