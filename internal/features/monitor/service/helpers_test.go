package service

import (
	"errors"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

func asIPC(err error, target **ipc.Error) bool { return errors.As(err, target) }
