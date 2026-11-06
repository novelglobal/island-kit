// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var errNoPrinter = errors.New("no USB printer seen: check it is on and its cable is in")

type Printer struct {
	mu  sync.Mutex
	dev string
	dry string
}

func newPrinter() *Printer {
	return &Printer{dev: os.Getenv("ISLAND_PRINTER"), dry: os.Getenv("ISLAND_DRY_DIR")}
}

func (p *Printer) find() string {
	if p.dev != "" {
		return p.dev
	}
	m, _ := filepath.Glob("/dev/usb/lp*")
	sort.Strings(m)
	if len(m) > 0 {
		return m[0]
	}
	return ""
}

func (p *Printer) Here() bool {
	if p.dry != "" {
		return true
	}
	d := p.find()
	if d == "" {
		return false
	}
	_, err := os.Stat(d)
	return err == nil
}

func (p *Printer) Print(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dry != "" {
		name := filepath.Join(p.dry, fmt.Sprintf("slip-%d.bin", time.Now().UnixNano()))
		return os.WriteFile(name, b, 0o644)
	}
	var err error
	for i := 0; i < 5; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		dev := p.find()
		if dev == "" {
			err = errNoPrinter
			continue
		}
		var f *os.File
		if f, err = os.OpenFile(dev, os.O_WRONLY, 0); err != nil {
			continue
		}
		_, err = f.Write(b)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			return nil
		}
	}
	return err
}

func (p *Printer) WaitFor(d time.Duration) bool {
	end := time.Now().Add(d)
	for !p.Here() {
		if time.Now().After(end) {
			return false
		}
		time.Sleep(time.Second)
	}
	return true
}
