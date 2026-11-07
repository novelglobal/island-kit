// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type pairing struct {
	code, device string
	slip         bool
	every        time.Duration
	until        time.Time
}

func (a *App) pairStep(i int) time.Duration {
	c := a.conf()
	l := c.link(i)
	if a.pair[i] == nil || time.Now().After(a.pair[i].until) {
		r, err := call("POST", l.Server+"/api/pair", "", map[string]string{"name": c.Name, "v": version, "paper": paper})
		if err != nil {
			log.Printf("no answer from %s: %v", l.Server, err)
			return 15 * time.Second
		}
		if r.Status == 429 {
			return 5 * time.Minute
		}
		if r.Status != 201 || str(r.Body, "code") == "" {
			log.Printf("%s answered %d to a pairing", l.Server, r.Status)
			return time.Minute
		}
		every := time.Duration(num(r.Body, "every")) * time.Second
		if every < 2*time.Second || every > time.Minute {
			every = 5 * time.Second
		}
		a.pair[i] = &pairing{code: str(r.Body, "code"), device: str(r.Body, "device"), every: every, until: time.Now().Add(14 * time.Minute)}
		a.setWaiting(i, a.pair[i].code)
		log.Printf("pairing code %s for %s", showCode(a.pair[i].code), l.Server)
	}
	r, err := call("POST", l.Server+"/api/pair/poll", "", map[string]string{"device": a.pair[i].device})
	if err != nil {
		return 10 * time.Second
	}
	switch r.Status {
	case 202:
		if !a.pair[i].slip && a.pairSlips[i] < 3 {
			a.pair[i].slip = true
			a.pairSlips[i]++
			a.print(pairSlip(c, l.Server, a.pair[i].code))
		}
		return a.pair[i].every
	case 404:
		a.pair[i] = nil
		a.setWaiting(i, "")
		return time.Second
	case 200:
		printer, token := str(r.Body, "printer"), str(r.Body, "token")
		if printer == "" || token == "" {
			return time.Minute
		}
		l.Printer, l.Token = printer, token
		key := str(r.Body, "key")
		a.update(func(c *Config) {
			c.setLink(i, l)
			if i == 0 && key != "" {
				c.Key = key
			}
		})
		if i == 0 {
			a.numbered(int(num(r.Body, "number")))
		}
		a.pair[i], a.pairSlips[i], a.refused[i] = nil, 0, 0
		a.setWaiting(i, "")
		log.Printf("paired as %s on %s", printer, l.Server)
		if i == 0 && key != "" {
			a.print(islandKeySlip(a.conf().Name, l, key))
		} else {
			a.print(pairedSlip(a.conf().Name, l))
		}
		return time.Second
	}
	return 30 * time.Second
}

// novel.global numbers its islands in the order they join, itself the first
func (a *App) numbered(n int) {
	name := fmt.Sprintf("ISLAND %d", n)
	if n < 2 || a.conf().Name == name {
		return
	}
	a.update(func(c *Config) { c.Name = name })
	log.Printf("now %s", name)
}

func showCode(c string) string {
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

func (a *App) pullStep(i int) (time.Duration, string) {
	c := a.conf()
	l := c.link(i)
	q := url.Values{"v": {version}}
	if i == 0 {
		q.Set("can", "off wifi")
	}
	if n, sig := current(); n != "" {
		q.Set("net", n)
		q.Set("rssi", strconv.Itoa(sig/2-100))
	}
	if ip := localIP(); ip != "" {
		q.Set("ip", ip)
	}
	base := l.Server + "/api/printers/" + url.PathEscape(l.Printer)
	r, err := call("GET", base+"/next?"+q.Encode(), l.Token, nil)
	if err != nil {
		log.Printf("no signal: %v", err)
		return 15 * time.Second, ""
	}
	wait := r.Wait
	if wait == 0 {
		wait = 3 * time.Second
	}
	switch r.Status {
	case 401:
		a.refused[i]++
		log.Printf("the token was refused by %s for %s (%d)", l.Server, l.Printer, a.refused[i])
		if a.refused[i] >= 3 {
			a.update(func(c *Config) { c.setLink(i, Link{Server: l.Server}) })
			a.refused[i] = 0
			a.print(textSlip(c.Name, "NOT PAIRED ANY MORE", strings.ToUpper(hostOf(l.Server)), "A NEW CODE FOLLOWS"))
		}
		return time.Minute, ""
	case 204:
		a.refused[i] = 0
		if i == 0 {
			a.numbered(r.Island)
		}
		return wait, ""
	case 200:
		a.refused[i] = 0
		if i == 0 {
			a.numbered(r.Island)
		}
	default:
		log.Printf("the server answered %d", r.Status)
		return time.Minute, ""
	}
	if i == 0 && r.Body["wifi"] == true {
		return 0, "setup"
	}
	if i == 0 && r.Body["off"] == true {
		return 0, "off"
	}
	job := str(r.Body, "job")
	if job == "" {
		return wait, ""
	}
	code := str(r.Body, "code")
	var perr error
	if b64 := str(r.Body, "escpos"); b64 != "" {
		var b []byte
		if b, perr = base64.StdEncoding.DecodeString(b64); perr == nil {
			perr = a.pr.Print(b)
		}
	} else if code == "DA-TEST" {
		perr = a.pr.Print(testStrip(c.Name))
	}
	status, why := "printed", ""
	if perr != nil {
		status, why = "failed", perr.Error()
		log.Printf("%s not printed: %v", code, perr)
	} else {
		a.update(func(c *Config) { c.Printed++ })
		log.Printf("%s printed", code)
	}
	if _, err := call("POST", base+"/jobs/"+url.PathEscape(job), l.Token, map[string]string{"status": status, "error": why}); err != nil {
		log.Printf("%s: could not say how it went: %v", code, err)
	}
	return time.Second, ""
}
