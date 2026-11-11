//go:build !linux

package factory

import (
	"gopanel/internal/config"
	"gopanel/internal/sys"
	"gopanel/internal/sys/sysfake"
)

func New(cfg *config.Config) sys.System {
	return sysfake.New()
}
