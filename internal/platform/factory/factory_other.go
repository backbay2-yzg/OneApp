//go:build !windows

package factory

import (
	"oneapp/internal/platform"
	stubbridge "oneapp/internal/platform/stub"
)

// NewOSBridge constructs the fallback non-Windows OSBridge implementation.
func NewOSBridge() platform.OSBridge {
	return stubbridge.New()
}
