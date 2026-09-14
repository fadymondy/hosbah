//go:build !linux

package agent

import "errors"

func reexec() error { return errors.New("re-exec after an update is only supported on Linux") }
