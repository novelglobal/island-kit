// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func (p *Printer) Model() string {
	d := p.find()
	if d == "" || p.dry != "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/usbmisc", filepath.Base(d), "device/ieee1284_id"))
	if err != nil {
		return ""
	}
	return modelOf(string(b))
}

func modelOf(id string) string {
	for _, f := range strings.Split(id, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(f), ":")
		if ok && (k == "MDL" || k == "MODEL") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func paperOf(model string) string {
	m := strings.ToUpper(model)
	for _, k := range []string{"58", "TM-P20", "TM-P60", "MTP-2", "PT-210", "GOOJPRT"} {
		if strings.Contains(m, k) {
			return "58"
		}
	}
	return "80"
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
	// NULs first: ignored by a printer at rest, they finish any picture a cut-off slip left it waiting on
	b = append(make([]byte, 8192), b...)
	var err error
	for i := 0; i < 5 && len(b) > 0; i++ {
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
		for len(b) > 0 {
			n := min(len(b), 2048)
			var w int
			w, err = f.Write(b[:n])
			b = b[w:]
			if err != nil {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err == nil {
			err = drain(f, 30*time.Second)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
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
