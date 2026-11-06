// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed page.html
var pageHTML string

var pages = template.Must(template.New("page").Parse(pageHTML))

type Choice struct {
	Name, Mode, Net, Pass, Server, Paper string
	Hidden                               bool
}

type portal struct {
	a       *App
	setup   bool
	nets    []Net
	known   []Known
	note    string
	chosen  chan Choice
	mu      sync.Mutex
	last    map[string]time.Time
	today   int
	dayFrom time.Time
}

func listenAddr() string {
	if a := os.Getenv("ISLAND_LISTEN"); a != "" {
		return a
	}
	return apAddr + ":80"
}

func (p *portal) serve(ctx context.Context) {
	var ln net.Listener
	var err error
	for i := 0; i < 20; i++ {
		if ln, err = net.Listen("tcp", listenAddr()); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Printf("the page could not open: %v", err)
		return
	}
	srv := &http.Server{Handler: p, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	srv.Serve(ln)
}

type view struct {
	Name, SSID, Note, Server, Done, Paper string
	Setup, Island, Offline                bool
	Nets                                  []Net
	Known                                 []Known
}

func (p *portal) render(w http.ResponseWriter, v view) {
	c := p.a.conf()
	v.Name, v.SSID, v.Server = c.Name, setupSSID(c), c.Own.Server
	v.Offline = c.Mode == "offline"
	v.Paper = c.Paper
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	if err := pages.Execute(w, v); err != nil {
		log.Printf("page: %v", err)
	}
}

func (p *portal) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ISLAND_LISTEN") == "" && strings.Split(r.Host, ":")[0] != apAddr {
		http.Redirect(w, r, "http://"+apAddr+"/", http.StatusFound)
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == "GET" && p.setup:
		p.render(w, view{Setup: true, Nets: p.nets, Known: p.known, Note: p.takeNote()})
	case r.URL.Path == "/" && r.Method == "GET":
		p.render(w, view{Island: true, Note: p.takeNote()})
	case r.URL.Path == "/setup" && r.Method == "POST" && p.setup:
		p.choose(w, r)
	case r.URL.Path == "/setup" && r.Method == "POST":
		p.reopen(w, r)
	case r.URL.Path == "/setup" && r.Method == "GET" && !p.setup:
		p.render(w, view{Done: "ask"})
	case r.URL.Path == "/print" && r.Method == "POST" && !p.setup:
		p.printWish(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *portal) takeNote() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.note
	p.note = ""
	return n
}

func (p *portal) say(w http.ResponseWriter, r *http.Request, note string) {
	p.mu.Lock()
	p.note = note
	p.mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (p *portal) choose(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.ParseForm() != nil {
		p.say(w, r, "TRY AGAIN.")
		return
	}
	c := Choice{Name: strings.ToUpper(clean(r.FormValue("name"), 24)), Mode: r.FormValue("mode"), Server: strings.TrimRight(clean(r.FormValue("server"), 100), "/")}
	if p := r.FormValue("paper"); p == "58" || p == "80" {
		c.Paper = p
	}
	other := clean(r.FormValue("other"), 32)
	c.Net, c.Pass = clean(r.FormValue("net"), 64), r.FormValue("pass")
	if other != "" {
		c.Net, c.Hidden = other, true
		for _, n := range p.nets {
			if n.Name == other {
				c.Hidden = false
			}
		}
	}
	if c.Name == "" {
		c.Name = p.a.conf().Name
	}
	if c.Server != "" && !strings.HasPrefix(c.Server, "https://") && !strings.HasPrefix(c.Server, "http://") {
		c.Server = "https://" + c.Server
	}
	switch {
	case c.Mode != "online" && c.Mode != "offline":
		p.say(w, r, "CHOOSE ONLINE OR OFFLINE.")
		return
	case c.Mode == "online" && c.Net == "":
		p.say(w, r, "CHOOSE A WI-FI.")
		return
	case c.Mode == "online" && c.Pass != "" && strings.EqualFold(strings.TrimSpace(c.Pass), p.a.conf().SetupPass):
		p.say(w, r, "THAT IS THE SLIP KEY. TYPE YOUR OWN WI-FI PASSWORD.")
		return
	case c.Mode == "online" && !strings.HasPrefix(c.Net, "keep:") && c.Pass != "" && (len(c.Pass) < 8 || len(c.Pass) > 63):
		p.say(w, r, "A WI-FI PASSWORD HAS 8 TO 63 CHARACTERS.")
		return
	}
	done := "online"
	if c.Mode == "offline" {
		done = "offline"
	}
	p.render(w, view{Done: done})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	select {
	case p.chosen <- c:
	default:
	}
}

func (p *portal) reopen(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	r.ParseForm()
	if !strings.EqualFold(strings.TrimSpace(clean(r.FormValue("pass"), 64)), p.a.conf().SetupPass) {
		time.Sleep(2 * time.Second)
		p.render(w, view{Done: "ask", Note: "TYPE THE SLIP KEY, PRINTED ON THE SLIP. YOUR WI-FI PASSWORD GOES ON THE NEXT PAGE."})
		return
	}
	p.render(w, view{Done: "reopen"})
	go func() { time.Sleep(2 * time.Second); p.a.ask("setup") }()
}

func (p *portal) printWish(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	p.mu.Lock()
	if p.last == nil || time.Since(p.dayFrom) > 24*time.Hour {
		p.last, p.today, p.dayFrom = map[string]time.Time{}, 0, time.Now()
	}
	busy := time.Since(p.last[ip]) < 20*time.Second
	full := p.today >= 300
	if !busy && !full {
		p.last[ip] = time.Now()
		p.today++
	}
	p.mu.Unlock()
	if busy {
		p.answer(w, r, "ONE AT A TIME: WAIT A MOMENT.")
		return
	}
	if full {
		p.answer(w, r, "THE ISLAND HAS PRINTED ENOUGH FOR TODAY.")
		return
	}
	var slip []byte
	name := p.a.conf().Name
	if strings.HasPrefix(r.Header.Get("content-type"), "image/png") {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
		if err != nil {
			p.answer(w, r, "TOO LARGE.")
			return
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil || cfg.Width > 1200 || cfg.Height > 4000 {
			p.answer(w, r, "NOT A PICTURE THE PRINTER CAN TAKE.")
			return
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			p.answer(w, r, "NOT A PICTURE THE PRINTER CAN TAKE.")
			return
		}
		slip = NewSlip().Center().Image(img).Cut().Bytes()
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		r.ParseForm()
		var lines []string
		for _, k := range []string{"w", "i", "s", "h"} {
			if l := clean(r.FormValue(k), 120); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			p.answer(w, r, "WRITE A LINE FIRST.")
			return
		}
		slip = wishSlip(name, lines)
	}
	if err := p.a.pr.Print(slip); err != nil {
		log.Printf("a W.I.S.H. not printed: %v", err)
		p.answer(w, r, "NOT PRINTED: THE PRINTER DID NOT ANSWER.")
		return
	}
	p.a.update(func(c *Config) { c.Printed++ })
	p.answer(w, r, "PRINTED. TAKE IT FROM THE PRINTER.")
}

func (p *portal) answer(w http.ResponseWriter, r *http.Request, note string) {
	if r.Header.Get("x-island") != "" {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		io.WriteString(w, note)
		return
	}
	p.say(w, r, note)
}
