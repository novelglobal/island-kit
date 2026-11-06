// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package main

import (
	"os"
	"time"
)

func drain(f *os.File, wait time.Duration) error { return nil }
