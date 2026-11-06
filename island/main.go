// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

const lostFor = 2 * time.Minute

type App struct {
	mu      sync.Mutex
	cfg     *Config
	path    string
	pr      *Printer
	asks    chan string
	code    [2]string
	readied bool

	pair      [2]*pairing
	pairSlips [2]int
	refused   [2]int
}

func main() {
	path := flag.String("config", "/var/lib/island/island.json", "where the island keeps its settings")
	test := flag.Bool("test-strip", false, "print the test strip and stop")
	demo := flag.String("demo", "", "show a page on a computer, touching no Wi-Fi: setup or island (on ISLAND_LISTEN, else 127.0.0.1:8080)")
	flag.Parse()
	log.SetFlags(0)
	if *test {
		if err := newPrinter().Print(testStrip("ISLAND")); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := loadConfig(*path)
	if err != nil {
		log.Fatalf("the settings in %s could not be read: %v", *path, err)
	}
	a := &App{cfg: cfg, path: *path, pr: newPrinter(), asks: make(chan string, 4)}
	a.fit()
	if *demo != "" {
		a.demo(*demo)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("island %s: %s, %s", version, cfg.Name, map[string]string{"": "not set up yet", "online": "online", "offline": "an offline island"}[cfg.Mode])
	go a.button()
	a.run(ctx)
}

func (a *App) conf() *Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := *a.cfg
	return &c
}

func (a *App) update(f func(*Config)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f(a.cfg)
	if err := a.cfg.save(a.path); err != nil {
		log.Printf("the settings could not be kept: %v", err)
	}
}

func (a *App) setWaiting(i int, code string) { a.mu.Lock(); a.code[i] = code; a.mu.Unlock() }
func (a *App) waiting(i int) string          { a.mu.Lock(); defer a.mu.Unlock(); return a.code[i] }

func (a *App) ask(what string) {
	select {
	case a.asks <- what:
	default:
	}
}

func (a *App) fit() {
	p := a.conf().Paper
	if p == "" {
		p = os.Getenv("ISLAND_PAPER")
	}
	if p == "" {
		p = paperOf(a.pr.Model())
	}
	if p != paper {
		log.Printf("paper %s mm", p)
	}
	setPaper(p)
}

func (a *App) print(b []byte) {
	if err := a.pr.Print(b); err != nil {
		log.Printf("no slip: %v", err)
	}
}

func (a *App) nap(ctx context.Context, d time.Duration) string {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return "stop"
	case w := <-a.asks:
		return w
	case <-t.C:
		return ""
	}
}

func (a *App) run(ctx context.Context) {
	force := quickStart()
	if force {
		log.Printf("a start cut short: the setup Wi-Fi opens")
	}
	go func() { time.Sleep(2 * time.Minute); upLong() }()
	for ctx.Err() == nil {
		var next string
		switch {
		case force || a.conf().Mode == "":
			force = false
			next = a.setup(ctx)
		case a.conf().Mode == "offline":
			next = a.island(ctx)
		default:
			next = a.online(ctx)
		}
		switch next {
		case "setup":
			force = true
		case "off":
			a.off()
			return
		}
	}
}

func (a *App) setup(ctx context.Context) string {
	if !hasWifi() {
		log.Printf("no Wi-Fi on this Pi (%s)", iface)
		return a.nap(ctx, 10*time.Minute)
	}
	fresh := a.conf().Mode == ""
	p := &portal{a: a, setup: true, chosen: make(chan Choice, 1), known: saved()}
	p.nets = scan()
	failed := false
	for {
		c := a.conf()
		if ok, why := openAP(setupSSID(c), c.SetupPass); !ok {
			log.Printf("the setup Wi-Fi did not open: %s", why)
			if !failed {
				failed = true
				a.print(textSlip(c.Name, "NO WI-FI", "THE SETUP WI-FI DID NOT OPEN", why))
			}
			return a.nap(ctx, 5*time.Minute)
		}
		log.Printf("the setup Wi-Fi is open: %s", setupSSID(c))
		if !a.readied {
			a.readied = true
			a.pr.WaitFor(time.Minute)
			a.fit()
			a.print(welcomeSlip(c))
		}
		pctx, cancel := context.WithCancel(ctx)
		go p.serve(pctx)
		var ch Choice
		got, done := false, false
		window := time.NewTimer(10 * time.Minute)
		if fresh {
			window.Stop()
		}
		for !done {
			select {
			case ch = <-p.chosen:
				got, done = true, true
			case <-window.C:
				done = true
			case w := <-a.asks:
				if w == "off" {
					cancel()
					closeAP()
					return "off"
				}
			case <-ctx.Done():
				done = true
			}
		}
		window.Stop()
		time.Sleep(2 * time.Second)
		cancel()
		closeAP()
		if !got {
			return ""
		}
		why := a.apply(ch)
		if why == "" {
			return ""
		}
		p.note = why
		p.nets = scan()
	}
}

func (a *App) apply(ch Choice) string {
	c := a.conf()
	renamed := ch.Name != c.Name
	if ch.Paper != c.Paper {
		a.update(func(c *Config) { c.Paper = ch.Paper })
		a.fit()
	}
	if ch.Mode == "offline" {
		a.update(func(c *Config) { c.Name, c.Mode = ch.Name, "offline" })
		return ""
	}
	joined := false
	why, reason := "", ""
	if con, ok := strings.CutPrefix(ch.Net, "keep:"); ok {
		joined = rejoin(con)
		ch.Net = con
		for _, k := range saved() {
			if k.Con == con {
				ch.Net = k.SSID
			}
		}
		if !joined {
			why = "COULD NOT JOIN " + ch.Net + "."
		}
	} else {
		var err string
		joined, err = join(ch.Net, ch.Pass, ch.Hidden)
		if !joined {
			log.Printf("could not join %s: %s", ch.Net, err)
			why = "COULD NOT JOIN " + ch.Net + ". CHECK ITS PASSWORD."
			reason = err
		}
	}
	if !joined {
		a.print(textSlip(c.Name, "COULD NOT JOIN", ch.Net, strings.ToUpper(clean(reason, 120)), "JOIN "+setupSSID(c)+" AGAIN", "SLIP KEY  "+c.SetupPass))
		return why
	}
	a.update(func(c *Config) {
		c.Name, c.Mode = ch.Name, "online"
		if ch.Server == defaultServer {
			ch.Server = ""
		}
		if ch.Server != c.Own.Server {
			c.Own = Link{Server: ch.Server}
		}
	})
	if renamed {
		log.Printf("now called %s", ch.Name)
	}
	a.print(textSlip(ch.Name, "ON WI-FI", ch.Net))
	return ""
}

func (a *App) online(ctx context.Context) string {
	var lost time.Time
	var ready [2]bool
	var due [2]time.Time
	for {
		wait := 5 * time.Second
		if !online() {
			if lost.IsZero() {
				lost = time.Now()
				log.Printf("no network")
			}
			if time.Since(lost) > lostFor {
				return "setup"
			}
			if time.Since(lost) > 20*time.Second {
				nm(20*time.Second, "device", "connect", iface)
			}
		} else {
			lost = time.Time{}
			n := a.conf().sites()
			for i := 0; i < n; i++ {
				if time.Now().Before(due[i]) {
					continue
				}
				var w time.Duration
				if l := a.conf().link(i); l.Token == "" {
					ready[i] = false
					w = a.pairStep(i)
				} else {
					if !ready[i] {
						ready[i] = true
						wifi, _ := current()
						a.print(readySlip(a.conf(), l, wifi))
					}
					var next string
					if w, next = a.pullStep(i); next != "" {
						return next
					}
				}
				due[i] = time.Now().Add(w)
			}
			wait = time.Until(due[0])
			if n > 1 && due[1].Before(due[0]) {
				wait = time.Until(due[1])
			}
		}
		if w := a.nap(ctx, wait); w != "" {
			return w
		}
	}
}

func (a *App) island(ctx context.Context) string {
	c := a.conf()
	if ok, why := openAP(trimSSID(c.Name, 32), c.IslandPass); !ok {
		log.Printf("the island's Wi-Fi did not open: %s", why)
		a.print(textSlip(c.Name, "NO WI-FI", "THE ISLAND'S WI-FI DID NOT OPEN", why))
		return a.nap(ctx, 5*time.Minute)
	}
	defer closeAP()
	a.readied = true
	a.pr.WaitFor(time.Minute)
	a.print(islandSlip(c))
	log.Printf("the island is open: %s", c.Name)
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go (&portal{a: a}).serve(pctx)
	for {
		if w := a.nap(ctx, time.Hour); w != "" {
			return w
		}
	}
}

func (a *App) off() {
	a.print(textSlip(a.conf().Name, "TURNING OFF", "UNPLUG ONCE THE GREEN LIGHT STOPS", "TO START: PRESS THE BUTTON, OR PLUG IT BACK IN"))
	if err := exec.Command("systemctl", "poweroff").Run(); err != nil {
		log.Printf("could not turn off: %v", err)
	}
}

const quickMark = "/var/lib/island/quick"

func quickStart() bool {
	bid, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return false
	}
	prev, _ := os.ReadFile(quickMark)
	f, err := os.OpenFile(quickMark, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	f.Write(bid)
	f.Sync()
	f.Close()
	return len(prev) > 0 && strings.TrimSpace(string(prev)) != strings.TrimSpace(string(bid))
}

func upLong() {
	os.Remove(quickMark)
	if d, err := os.Open(filepath.Dir(quickMark)); err == nil {
		d.Sync()
		d.Close()
	}
}

func init() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		os.Exit(0)
	}
}

func (a *App) demo(which string) {
	if os.Getenv("ISLAND_LISTEN") == "" {
		os.Setenv("ISLAND_LISTEN", "127.0.0.1:8080")
	}
	p := &portal{a: a, setup: which == "setup", chosen: make(chan Choice, 1), known: []Known{{"preconfigured", "Home Wi-Fi"}},
		nets: []Net{{"Cafe Wi-Fi", 84, true}, {"Telstra5G-2A1F", 61, true}, {"Library Free Wi-Fi", 33, false}}}
	log.Printf("the %s page: http://%s", which, os.Getenv("ISLAND_LISTEN"))
	go func() {
		for c := range p.chosen {
			log.Printf("chosen: %s, %s, Wi-Fi %q", c.Name, c.Mode, c.Net)
		}
	}()
	go func() {
		for w := range a.asks {
			log.Printf("asked: %s", w)
		}
	}()
	p.serve(context.Background())
}
