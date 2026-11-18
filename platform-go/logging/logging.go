// Package logging installs Zap as the structured backend for existing slog contracts.
package logging

import (
	"errors"
	"log/slog"
	"os"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
)

type Options struct {
	Service     string
	Level       string
	Development bool
}

func Install(opts Options) (func() error, error) {
	cfg := zap.NewProductionConfig()
	if opts.Development {
		cfg = zap.NewDevelopmentConfig()
	}
	if opts.Level != "" {
		if err := cfg.Level.UnmarshalText([]byte(opts.Level)); err != nil {
			return nil, err
		}
	}
	logger, err := cfg.Build(zap.Fields(zap.String("service", opts.Service)))
	if err != nil {
		return nil, err
	}
	previous := slog.Default()
	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core())))
	return func() error {
		slog.SetDefault(previous)
		err := logger.Sync()
		// Standard streams are not fsync-able on all supported platforms.
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, os.ErrInvalid) {
			return nil
		}
		return err
	}, nil
}
