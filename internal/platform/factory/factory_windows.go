//go:build windows

package factory

import (
	"oneapp/internal/platform"
	winbridge "oneapp/internal/platform/windows"
)

// NewOSBridge constructs the Windows native OSBridge implementation.
func NewOSBridge() platform.OSBridge {
	return winbridge.New()
}
