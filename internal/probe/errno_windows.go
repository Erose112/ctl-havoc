//go:build windows

package probe

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func isConnRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}

func isConnReset(err error) bool {
	return errors.Is(err, windows.WSAECONNRESET) || errors.Is(err, syscall.ECONNRESET)
}
