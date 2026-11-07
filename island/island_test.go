// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestWrap(t *testing.T) {
	got := wrap("one two three four", 9)
	if strings.Join(got, "|") != "one two|three|four" {
		t.Fatalf("wrap: %q", got)
	}
	if got := wrap("abcdefghijk", 4); strings.Join(got, "|") != "abcd|efgh|ijk" {
		t.Fatalf("a long word: %q", got)
	}
}

func TestASCII(t *testing.T) {
	if got := ascii("A·B — “C” é✓"); got != `A-B - "C" e?` {
		t.Fatalf("ascii: %q", got)
	}
}

func TestClean(t *testing.T) {
	if got := clean("  \"abc def\"\r\n", 40); got != "abc def" {
		t.Fatalf("clean: %q", got)
	}
	if got := clean("ÉÉÉÉ", 5); got != "ÉÉ" {
		t.Fatalf("cut within bytes, whole characters: %q", got)
	}
}

func TestQR(t *testing.T) {
	b := NewSlip().QR("HELLO", 6).Bytes()
	if bytes.Contains(b, []byte("\x1d(k")) || !bytes.Contains(b, []byte("\x1dv0\x00\x40\x00")) {
		t.Fatalf("qr as a picture: % x", b[:24])
	}
}

func TestRaster(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 16, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 16; x++ {
			img.SetGray(x, y, color.Gray{255})
		}
	}
	img.SetGray(0, 0, color.Gray{0})
	img.SetGray(9, 0, color.Gray{0})
	b := NewSlip().Image(img).Bytes()[5:]
	if n := bytes.Count(b, []byte("\x1dv0\x00")); n != 3 {
		t.Fatalf("bands: %d", n)
	}
	if !bytes.HasPrefix(b, []byte{0x1d, 'v', '0', 0, 2, 0, 128, 0, 0x80, 0x40}) {
		t.Fatalf("first band: % x", b[:12])
	}
	if want := 3*8 + 300*2; len(b) != want {
		t.Fatalf("length %d, want %d", len(b), want)
	}
}

func TestWifiQR(t *testing.T) {
	if got := wifiQR(`A;B`, `p:w`); got != `WIFI:T:WPA;S:A\;B;P:p\:w;;` {
		t.Fatalf("wifi qr: %q", got)
	}
	if got := wifiQR("OPEN", ""); got != "WIFI:T:nopass;S:OPEN;;" {
		t.Fatalf("open wifi qr: %q", got)
	}
}

func TestFieldsAndScan(t *testing.T) {
	if f := fields(`My\:Net:72:WPA2`); len(f) != 3 || f[0] != "My:Net" {
		t.Fatalf("fields: %q", f)
	}
	nets := parseScan("Cafe:40:WPA2\nCafe:80:WPA2\nLibrary:55:\n:90:WPA2\nISLAND 1A2B SETUP:99:WPA2\n")
	if len(nets) != 2 || nets[0].Name != "Cafe" || nets[0].Signal != 80 || nets[1].Locked {
		t.Fatalf("scan: %+v", nets)
	}
}

func TestConfigKept(t *testing.T) {
	p := filepath.Join(t.TempDir(), "island.json")
	c, err := loadConfig(p)
	if err != nil || len(c.SetupPass) != 10 || c.Server != defaultServer {
		t.Fatalf("fresh: %+v %v", c, err)
	}
	c.Mode, c.Token = "online", "secret"
	if err := c.save(p); err != nil {
		t.Fatal(err)
	}
	c2, _ := loadConfig(p)
	if c2.SetupPass != c.SetupPass || c2.Token != "secret" {
		t.Fatalf("kept: %+v", c2)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("readable by others: %v", fi.Mode())
	}
}

type site struct {
	mu       sync.Mutex
	approved bool
	own      bool
	open     bool
	asked    int
	token    string
	number   int
	jobs     []map[string]any
	reports  []map[string]string
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("content-type", "application/json")
	switch {
	case r.URL.Path == "/api/pair":
		s.asked++
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"code": "ABCD2345", "device": "dev-secret", "every": 2})
	case r.URL.Path == "/api/pair/poll" && body["device"] == "dev-secret":
		if s.open {
			s.token = "tok-from-site"
			json.NewEncoder(w).Encode(map[string]any{"printer": "laundromate", "token": s.token, "number": 7, "key": "k3y-k3y-k3y-k3y-k3y-k3y-k3y-k3y0"})
			return
		}
		if !s.approved {
			w.WriteHeader(202)
			w.Write([]byte(`{"waiting":true}`))
			return
		}
		s.token = "tok-from-site"
		json.NewEncoder(w).Encode(map[string]any{"printer": "laundromate", "token": s.token, "number": s.number})
	case r.URL.Path == "/api/printers/laundromate/next":
		if r.Header.Get("authorization") != "Bearer "+s.token || s.token == "" {
			w.WriteHeader(401)
			return
		}
		if strings.Contains(r.URL.RawQuery, "can=off+wifi") == s.own {
			w.WriteHeader(400)
			return
		}
		if s.number > 0 {
			w.Header().Set("x-island", strconv.Itoa(s.number))
		}
		if len(s.jobs) == 0 {
			w.Header().Set("x-wait", "20")
			w.WriteHeader(204)
			return
		}
		j := s.jobs[0]
		s.jobs = s.jobs[1:]
		json.NewEncoder(w).Encode(j)
	case strings.HasPrefix(r.URL.Path, "/api/printers/laundromate/jobs/"):
		body["job"] = strings.TrimPrefix(r.URL.Path, "/api/printers/laundromate/jobs/")
		s.reports = append(s.reports, body)
		w.Write([]byte(`{"ok":true}`))
	default:
		w.WriteHeader(404)
	}
}

func testApp(t *testing.T, server string) (*App, string) {
	dir := t.TempDir()
	t.Setenv("ISLAND_DRY_DIR", dir)
	cfg, err := loadConfig(filepath.Join(dir, "island.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Name, cfg.Mode, cfg.Server = "LAUNDROMATE", "online", server
	return &App{cfg: cfg, path: filepath.Join(dir, "island.json"), pr: newPrinter(), asks: make(chan string, 4)}, dir
}

func slips(t *testing.T, dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "slip-*.bin"))
	var out []string
	for _, f := range m {
		b, _ := os.ReadFile(f)
		out = append(out, string(b))
	}
	return out
}

func TestPairThenPrint(t *testing.T) {
	s := &site{}
	srv := httptest.NewServer(s)
	defer srv.Close()
	a, dir := testApp(t, srv.URL)

	if w := a.pairStep(0); w.Seconds() != 2 {
		t.Fatalf("wait %v", w)
	}
	sl := slips(t, dir)
	if len(sl) != 1 || !strings.Contains(sl[0], "ABCD-2345") {
		t.Fatalf("pair slip: %q", sl)
	}
	if a.waiting(0) != "ABCD2345" {
		t.Fatalf("waiting: %q", a.waiting(0))
	}
	s.approved, s.number = true, 3
	a.pairStep(0)
	c := a.conf()
	if c.Printer != "laundromate" || c.Token != "tok-from-site" || s.asked != 1 || a.waiting(0) != "" || c.Name != "ISLAND 3" {
		t.Fatalf("paired: %+v asked %d", c, s.asked)
	}
	if sl := slips(t, dir); len(sl) != 2 || !strings.Contains(sl[0]+sl[1], "ISLAND 3") {
		t.Fatalf("paired slip missing: %d", len(sl))
	}
	s.number = 4
	a.pullStep(0)
	if a.conf().Name != "ISLAND 4" {
		t.Fatalf("renumbered: %q", a.conf().Name)
	}

	want := []byte("\x1b@HELLO\n\x1dVB\x00")
	s.jobs = append(s.jobs, map[string]any{"job": "j1", "code": "DA-7K2Q", "escpos": base64.StdEncoding.EncodeToString(want)})
	if _, next := a.pullStep(0); next != "" {
		t.Fatalf("next %q", next)
	}
	found := false
	for _, x := range slips(t, dir) {
		found = found || x == string(want)
	}
	if !found || len(s.reports) != 1 || s.reports[0]["status"] != "printed" || s.reports[0]["job"] != "j1" || a.conf().Printed != 1 {
		t.Fatalf("print: found %v reports %v", found, s.reports)
	}
	if w, _ := a.pullStep(0); w.Seconds() != 20 {
		t.Fatalf("x-wait: %v", w)
	}
	s.jobs = append(s.jobs, map[string]any{"off": true}, map[string]any{"wifi": true})
	if _, next := a.pullStep(0); next != "off" {
		t.Fatalf("off: %q", next)
	}
	if _, next := a.pullStep(0); next != "setup" {
		t.Fatalf("wifi: %q", next)
	}
	s.token = ""
	for i := 0; i < 3; i++ {
		a.pullStep(0)
	}
	if c := a.conf(); c.Token != "" || c.Printer != "" {
		t.Fatalf("still paired: %+v", c)
	}
}

func TestStatusSlip(t *testing.T) {
	a, _ := testApp(t, "https://example.org")
	a.setWaiting(0, "WXYZ6789")
	b := string(statusSlip(a))
	for _, want := range []string{"LAUNDROMATE", "MODE  ONLINE", "WXYZ-6789", a.conf().SetupPass} {
		if !strings.Contains(b, want) {
			t.Fatalf("status slip lacks %q", want)
		}
	}
}

func TestWelcomeSlip(t *testing.T) {
	c := &Config{Name: "ISLAND 1A2B", SetupPass: "abcdefghjk"}
	b := string(welcomeSlip(c))
	for _, want := range []string{"novel.global", "ISLAND 1A2B", "abcdefghjk\n", "IP: " + apAddr + "\n", "\x1dVB\x00"} {
		if !strings.Contains(b, want) {
			t.Fatalf("welcome slip lacks %q", want)
		}
	}
	for _, gone := range []string{"SETUP", "SCAN", "KEY", "WI-FI", "RASPBERRY"} {
		if strings.Contains(b, gone) {
			t.Fatalf("welcome slip still says %q", gone)
		}
	}
}

func TestPaper(t *testing.T) {
	if m := modelOf("MFG:EPSON;CMD:ESC/POS;MDL:TM-T88V;CLS:PRINTER;"); m != "TM-T88V" || paperOf(m) != "80" {
		t.Fatalf("epson: %q", m)
	}
	if paperOf(modelOf("MANUFACTURER:Printer;MODEL:POS58 Printer USB;")) != "58" || paperOf("") != "80" {
		t.Fatal("58 mm")
	}
	setPaper("58")
	defer setPaper("80")
	if b := NewSlip().QR("HELLO", 6).Bytes(); !bytes.Contains(b, []byte("\x1dv0\x00\x30\x00")) {
		t.Fatalf("58 mm qr: % x", b[:24])
	}
}

func TestOwnSiteBeside(t *testing.T) {
	home, own := &site{approved: true}, &site{approved: true, own: true}
	hs, os2 := httptest.NewServer(home), httptest.NewServer(own)
	defer hs.Close()
	defer os2.Close()
	a, _ := testApp(t, hs.URL)
	a.update(func(c *Config) { c.Own = Link{Server: os2.URL} })
	for i := 0; i < 2; i++ {
		a.pairStep(i)
		a.pairStep(i)
	}
	c := a.conf()
	if c.Token != "tok-from-site" || c.Own.Token != "tok-from-site" || c.Own.Server != os2.URL {
		t.Fatalf("both paired: %+v", c)
	}
	own.jobs = append(own.jobs, map[string]any{"off": true})
	if _, next := a.pullStep(1); next != "" {
		t.Fatalf("an own site turned the island off: %q", next)
	}
	if _, next := a.pullStep(0); next != "" {
		t.Fatalf("home: %q", next)
	}
}

func TestOwnSiteMoved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "island.json")
	os.WriteFile(p, []byte(`{"name":"X","server":"https://example.org","printer":"x","token":"t"}`), 0o600)
	c, err := loadConfig(p)
	if err != nil || c.Server != defaultServer || c.Token != "" || c.Own != (Link{"https://example.org", "x", "t"}) {
		t.Fatalf("moved: %+v %v", c, err)
	}
}

func TestOpenJoining(t *testing.T) {
	s := &site{open: true}
	srv := httptest.NewServer(s)
	defer srv.Close()
	a, dir := testApp(t, srv.URL)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	a.pairStep(0)
	c := a.conf()
	if c.Token != "tok-from-site" || c.Key != "k3y-k3y-k3y-k3y-k3y-k3y-k3y-k3y0" || c.Name != "ISLAND 7" {
		t.Fatalf("joined: %+v", c)
	}
	sl := slips(t, dir)
	if len(sl) != 1 || strings.Contains(sl[0], "SCAN TO APPROVE") || !strings.Contains(sl[0], "YOUR ISLAND") {
		t.Fatalf("one slip, the island's: %q", sl)
	}
	if strings.Contains(logs.String(), "k3y") {
		t.Fatal("the key was logged")
	}
}
