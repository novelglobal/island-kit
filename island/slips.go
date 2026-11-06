// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

func head(s *Slip, name string) *Slip {
	return s.Center().Bold(true).Text(strings.ToUpper(name)).Bold(false).Feed(1)
}

func welcomeSlip(c *Config) []byte {
	ssid := setupSSID(c)
	s := head(NewSlip(), c.Name)
	s.Big(true).Line("SCAN TO BEGIN", cols/2).Big(false).Feed(1)
	s.Text("WI-FI  " + ssid).Text("PASSWORD  " + c.SetupPass).Text("NO PAGE? OPEN " + apAddr).Feed(1)
	s.QR(wifiQR(ssid, c.SetupPass), 7)
	return s.Cut().Bytes()
}

func pairSlip(c *Config, code string) []byte {
	s := head(NewSlip(), c.Name)
	s.Big(true).Line(showCode(code), cols/2).Big(false).Feed(1)
	s.Text("SCAN TO APPROVE ON " + strings.ToUpper(hostOf(c.Server))).Feed(1)
	s.QR(c.Server+"/admin#pair="+code, 6)
	return s.Cut().Bytes()
}

func pairedSlip(c *Config) []byte {
	return textSlip(c.Name, "PAIRED", strings.ToUpper(c.Printer)+" ON "+strings.ToUpper(hostOf(c.Server)), "READY TO PRINT")
}

func readySlip(c *Config, wifi string) []byte {
	lines := []string{"READY", strings.ToUpper(c.Printer)}
	if wifi != "" {
		lines = append(lines, "WI-FI "+wifi)
	}
	return textSlip(c.Name, lines...)
}

func islandSlip(c *Config) []byte {
	s := head(NewSlip(), c.Name)
	s.Big(true).Line("ISLAND OPEN", cols/2).Big(false).Feed(1)
	s.Text("WI-FI  " + c.Name)
	if c.IslandPass != "" {
		s.Text("PASSWORD  " + c.IslandPass)
	}
	s.Text("NO PAGE? OPEN " + apAddr).Feed(1)
	s.QR(wifiQR(c.Name, c.IslandPass), 7)
	return s.Cut().Bytes()
}

func textSlip(name string, lines ...string) []byte {
	s := head(NewSlip(), name)
	for _, l := range lines {
		s.Text(l)
	}
	return s.Cut().Bytes()
}

func wishSlip(name string, lines []string) []byte {
	s := head(NewSlip(), name).Left()
	for _, l := range lines {
		s.Text(l)
	}
	return s.Cut().Bytes()
}

func statusSlip(a *App) []byte {
	c := a.conf()
	s := head(NewSlip(), c.Name).Left()
	mode := map[string]string{"": "NOT SET UP", "online": "ONLINE", "offline": "OFFLINE ISLAND"}[c.Mode]
	s.Text("MODE  " + mode)
	if n, sig := current(); n != "" {
		s.Text(fmt.Sprintf("WI-FI  %s (%d%%)", n, sig))
	}
	if ip := localIP(); ip != "" {
		s.Text("ADDRESS  " + ip)
	}
	if c.Mode == "online" {
		s.Text("SITE  " + hostOf(c.Server))
		if c.Printer != "" {
			s.Text("PAIRED AS  " + strings.ToUpper(c.Printer))
		} else if code := a.waiting(); code != "" {
			s.Feed(1).Center().Big(true).Line(showCode(code), cols/2).Big(false).QR(c.Server+"/admin#pair="+code, 6).Text("SCAN TO APPROVE").Left()
		} else {
			s.Text("NOT PAIRED: WAITING FOR THE SITE")
		}
	}
	s.Text("PAPER  " + paper + " MM")
	if m := a.pr.Model(); m != "" {
		s.Text("PRINTER  " + m)
	}
	s.Text(fmt.Sprintf("PRINTED  %d", c.Printed))
	s.Text("SETUP WI-FI  " + setupSSID(c)).Text("PASSWORD  " + c.SetupPass)
	s.Text("VERSION  " + version)
	return s.Cut().Bytes()
}

func testStrip(name string) []byte {
	w, h := dots, 160
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			black := false
			switch {
			case y < 40:
				black = x < w/4 || (x < w/2 && x%2 == 0) || (x < 3*w/4 && x%4 < 2) || (x >= 3*w/4 && x%8 < 4)
			case y < 44:
			case y < 84:
				band := x * 8 / w
				black = (x+y)%(8-band) == 0
			case y < 88:
			default:
				black = bayer(x, y) < x*255/w
			}
			if !black {
				img.SetGray(x, y, color.Gray{255})
			}
		}
	}
	s := head(NewSlip(), name).Text("TEST STRIP").Image(img).Feed(1).Left()
	s.Text("A  abcdefghijklmnopqrstuvwxyz 0123456789")
	s.b.WriteString("\x1bM\x01")
	s.Line("B  abcdefghijklmnopqrstuvwxyz 0123456789 ABCDEFGHIJKLM", cols*4/3)
	s.b.WriteString("\x1bM\x00")
	s.Bold(true).Text("BOLD  ABCDEFGHIJ").Bold(false)
	s.Big(true).Line("BIG  ABC", cols/2).Big(false)
	s.Center().QR("https://novel.global", 6).Text("A CRISP CODE SCANS FROM ARM'S LENGTH")
	return s.Cut().Bytes()
}

func bayer(x, y int) int {
	m := [4][4]int{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}
	return m[y%4][x%4]*16 + 8
}

func setupSSID(c *Config) string { return trimSSID(c.Name, 26) + " SETUP" }

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	return u
}

func trimSSID(s string, n int) string {
	for len(s) > n {
		r := []rune(s)
		s = string(r[:len(r)-1])
	}
	return s
}
