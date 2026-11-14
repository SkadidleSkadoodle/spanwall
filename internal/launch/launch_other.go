//go:build !windows

package launch

import "errors"

var errWindowsOnly = errors.New("the background app runs on Windows only")

// Start is Windows only.
func Start() error { return errWindowsOnly }

// Stop is Windows only.
func Stop() error { return errWindowsOnly }
