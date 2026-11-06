// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"log"
	"net/url"
	"strconv"
	"time"
)

type pairing struct {
	code, device string
	every        time.Duration
	until        time.Time
}

func (a *App) pairStep() time.Duration {
	c := a.conf()
	if a.pair == nil || time.Now().After(a.pair.until) {
		r, err := call("POST", c.Server+"/api/pair", "", map[string]string{"name": c.Name, "v": version, "paper": paper})
		if err != nil {
			log.Printf("no answer from %s: %v", c.Server, err)
			return 15 * time.Second
		}
		if r.Status == 429 {
			return 5 * time.Minute
		}
		if r.Status != 201 || str(r.Body, "code") == "" {
			log.Printf("%s answered %d to a pairing", c.Server, r.Status)
			return time.Minute
		}
		every := time.Duration(num(r.Body, "every")) * time.Second
		if every < 2*time.Second || every > time.Minute {
			every = 5 * time.Second
		}
		a.pair = &pairing{code: str(r.Body, "code"), device: str(r.Body, "device"), every: every, until: time.Now().Add(14 * time.Minute)}
		a.setWaiting(a.pair.code)
		log.Printf("pairing code %s", showCode(a.pair.code))
		if a.pairSlips < 3 {
			a.pairSlips++
			a.print(pairSlip(c, a.pair.code))
		}
	}
	r, err := call("POST", c.Server+"/api/pair/poll", "", map[string]string{"device": a.pair.device})
	if err != nil {
		return 10 * time.Second
	}
	switch r.Status {
	case 202:
		return a.pair.every
	case 404:
		a.pair = nil
		a.setWaiting("")
		return time.Second
	case 200:
		printer, token := str(r.Body, "printer"), str(r.Body, "token")
		if printer == "" || token == "" {
			return time.Minute
		}
		a.update(func(c *Config) { c.Printer, c.Token = printer, token })
		a.pair, a.pairSlips, a.refused = nil, 0, 0
		a.setWaiting("")
		log.Printf("paired as %s on %s", printer, c.Server)
		a.print(pairedSlip(a.conf()))
		return time.Second
	}
	return 30 * time.Second
}

func showCode(c string) string {
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

func (a *App) pullStep() (time.Duration, string) {
	c := a.conf()
	q := url.Values{"can": {"off wifi"}, "v": {version}}
	if n, sig := current(); n != "" {
		q.Set("net", n)
		q.Set("rssi", strconv.Itoa(sig/2-100))
	}
	if ip := localIP(); ip != "" {
		q.Set("ip", ip)
	}
	base := c.Server + "/api/printers/" + url.PathEscape(c.Printer)
	r, err := call("GET", base+"/next?"+q.Encode(), c.Token, nil)
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
		a.refused++
		log.Printf("the token was refused by %s for %s (%d)", c.Server, c.Printer, a.refused)
		if a.refused >= 3 {
			a.update(func(c *Config) { c.Printer, c.Token = "", "" })
			a.refused = 0
			a.print(textSlip(c.Name, "NOT PAIRED ANY MORE", "A NEW CODE FOLLOWS"))
		}
		return time.Minute, ""
	case 204:
		a.refused = 0
		return wait, ""
	case 200:
		a.refused = 0
	default:
		log.Printf("the server answered %d", r.Status)
		return time.Minute, ""
	}
	if r.Body["wifi"] == true {
		return 0, "setup"
	}
	if r.Body["off"] == true {
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
	if _, err := call("POST", base+"/jobs/"+url.PathEscape(job), c.Token, map[string]string{"status": status, "error": why}); err != nil {
		log.Printf("%s: could not say how it went: %v", code, err)
	}
	return time.Second, ""
}
