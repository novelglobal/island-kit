// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	iface   = "wlan0"
	apAddr  = "10.42.0.1"
	apCon   = "island-ap"
	joinPre = "island: "
)

type Net struct {
	Name   string
	Signal int
	Locked bool
}

func nm(timeout time.Duration, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nmcli", args...).CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		return 1, err.Error()
	}
	return 0, string(out)
}

func fields(line string) []string {
	var out []string
	var cur strings.Builder
	esc := false
	for _, c := range line {
		switch {
		case esc:
			cur.WriteRune(c)
			esc = false
		case c == '\\':
			esc = true
		case c == ':':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	return append(out, cur.String())
}

func online() bool {
	_, out := nm(15*time.Second, "-t", "-f", "NAME,TYPE,STATE", "connection", "show", "--active")
	for _, l := range strings.Split(out, "\n") {
		f := fields(l)
		if len(f) >= 3 && f[0] != apCon && (f[1] == "802-11-wireless" || f[1] == "802-3-ethernet") && f[2] == "activated" {
			return true
		}
	}
	return false
}

func scan() []Net {
	_, out := nm(40*time.Second, "-t", "-f", "SSID,SIGNAL,SECURITY", "device", "wifi", "list", "ifname", iface, "--rescan", "yes")
	return parseScan(out)
}

func parseScan(out string) []Net {
	best := map[string]Net{}
	for _, l := range strings.Split(out, "\n") {
		f := fields(l)
		if len(f) < 3 || f[0] == "" || strings.HasSuffix(f[0], " SETUP") {
			continue
		}
		sig, _ := strconv.Atoi(f[1])
		if b, ok := best[f[0]]; !ok || sig > b.Signal {
			best[f[0]] = Net{f[0], sig, f[2] != "" && f[2] != "--"}
		}
	}
	nets := make([]Net, 0, len(best))
	for _, n := range best {
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Signal > nets[j].Signal })
	if len(nets) > 20 {
		nets = nets[:20]
	}
	return nets
}

func current() (string, int) {
	_, out := nm(10*time.Second, "-t", "-f", "ACTIVE,SSID,SIGNAL", "device", "wifi", "list", "--rescan", "no")
	for _, l := range strings.Split(out, "\n") {
		f := fields(l)
		if len(f) >= 3 && f[0] == "yes" {
			s, _ := strconv.Atoi(f[2])
			return f[1], s
		}
	}
	return "", 0
}

type Known struct{ Con, SSID string }

func saved() []Known {
	_, out := nm(15*time.Second, "-t", "-f", "NAME,TYPE", "connection", "show")
	var s []Known
	for _, l := range strings.Split(out, "\n") {
		if f := fields(l); len(f) >= 2 && f[1] == "802-11-wireless" && f[0] != apCon {
			_, ssid := nm(10*time.Second, "-g", "802-11-wireless.ssid", "connection", "show", f[0])
			if ssid = strings.TrimSpace(ssid); ssid == "" {
				ssid = f[0]
			}
			s = append(s, Known{f[0], ssid})
		}
	}
	return s
}

func openAP(ssid, pass string) (bool, string) {
	nm(20*time.Second, "connection", "delete", apCon)
	args := []string{"connection", "add", "type", "wifi", "ifname", iface, "con-name", apCon, "autoconnect", "no", "ssid", ssid,
		"802-11-wireless.mode", "ap", "802-11-wireless.band", "bg", "802-11-wireless.channel", "6", "ipv4.method", "shared", "ipv4.addresses", apAddr + "/24",
		"ipv6.method", "disabled"}
	if pass != "" {
		args = append(args, "wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", pass, "wifi-sec.proto", "rsn",
			"wifi-sec.pairwise", "ccmp", "wifi-sec.group", "ccmp", "wifi-sec.pmf", "disable")
	}
	if code, out := nm(20*time.Second, args...); code != 0 {
		return false, oneLine(out)
	}
	if code, out := nm(60*time.Second, "--wait", "50", "connection", "up", apCon); code != 0 {
		return false, oneLine(out)
	}
	return true, ""
}

func closeAP() { nm(60*time.Second, "--wait", "30", "connection", "down", apCon) }

func join(name, pass string, hidden bool) (bool, string) {
	con := joinPre + name
	nm(20*time.Second, "connection", "delete", con)
	args := []string{"--wait", "45", "device", "wifi", "connect", name}
	if pass != "" {
		args = append(args, "password", pass)
	}
	args = append(args, "ifname", iface, "name", con)
	if hidden {
		args = append(args, "hidden", "yes")
	}
	why := ""
	for i := 0; i < 3; i++ {
		nm(20*time.Second, "device", "wifi", "rescan", "ifname", iface)
		time.Sleep(8 * time.Second)
		code, out := nm(60*time.Second, args...)
		if code == 0 {
			nm(20*time.Second, "connection", "modify", con, "connection.autoconnect", "yes")
			return true, ""
		}
		why = oneLine(out)
		nm(20*time.Second, "connection", "delete", con)
		if strings.Contains(out, "ecrets") || strings.Contains(out, "assword") {
			break
		}
	}
	return false, why
}

func rejoin(con string) bool {
	code, _ := nm(60*time.Second, "--wait", "45", "connection", "up", con)
	return code == 0
}

func hasWifi() bool {
	_, err := os.Stat("/sys/class/net/" + iface)
	return err == nil
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
