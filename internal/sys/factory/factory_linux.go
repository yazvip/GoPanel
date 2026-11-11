//go:build linux

package factory

import (
	"gopanel/internal/config"
	"gopanel/internal/sys"
	"gopanel/internal/sys/linux"
)

func New(cfg *config.Config) sys.System {
	return linux.New(cfg)
}
