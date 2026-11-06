// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/binary"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const keyProg1 = 148

func buttonDevice() string {
	b, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return ""
	}
	for _, block := range strings.Split(string(b), "\n\n") {
		if !strings.Contains(strings.ToLower(block), "gpio") && !strings.Contains(strings.ToLower(block), "button") {
			continue
		}
		for _, l := range strings.Split(block, "\n") {
			if !strings.HasPrefix(l, "H: Handlers=") {
				continue
			}
			for _, h := range strings.Fields(strings.TrimPrefix(l, "H: Handlers=")) {
				if strings.HasPrefix(h, "event") {
					return "/dev/input/" + h
				}
			}
		}
	}
	return ""
}

func (a *App) button() {
	for {
		dev := buttonDevice()
		if dev == "" {
			time.Sleep(time.Minute)
			continue
		}
		if err := a.readButton(dev); err != nil {
			log.Printf("the button: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func (a *App) readButton(dev string) error {
	f, err := os.Open(dev)
	if err != nil {
		return err
	}
	defer f.Close()
	tv := strconv.IntSize / 8 * 2
	ev := make([]byte, tv+8)
	var down time.Time
	for {
		if _, err := io.ReadFull(f, ev); err != nil {
			return err
		}
		typ, code := binary.LittleEndian.Uint16(ev[tv:]), binary.LittleEndian.Uint16(ev[tv+2:])
		val := int32(binary.LittleEndian.Uint32(ev[tv+4:]))
		if typ != 1 || code != keyProg1 {
			continue
		}
		switch val {
		case 1:
			down = time.Now()
		case 0:
			if down.IsZero() {
				continue
			}
			held := time.Since(down)
			down = time.Time{}
			if held >= 3*time.Second {
				log.Printf("a long press: the status slip")
				a.print(statusSlip(a))
			} else {
				a.ask("off")
			}
		}
	}
}
