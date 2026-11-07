// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	Server     string `json:"server"`
	Printer    string `json:"printer,omitempty"`
	Token      string `json:"token,omitempty"`
	SetupPass  string `json:"setup_pass"`
	IslandPass string `json:"island_pass,omitempty"`
	Printed    int    `json:"printed"`
	Paper      string `json:"paper,omitempty"`
	Own        Link   `json:"own,omitempty"`
	Key        string `json:"key,omitempty"`
}

type Link struct {
	Server  string `json:"server,omitempty"`
	Printer string `json:"printer,omitempty"`
	Token   string `json:"token,omitempty"`
}

func (c *Config) link(i int) Link {
	if i == 0 {
		return Link{c.Server, c.Printer, c.Token}
	}
	return c.Own
}

func (c *Config) setLink(i int, l Link) {
	if i == 0 {
		c.Printer, c.Token = l.Printer, l.Token
	} else {
		c.Own = l
	}
}

func (c *Config) sites() int {
	if c.Own.Server != "" {
		return 2
	}
	return 1
}

const defaultServer = "https://novel.global"

func loadConfig(path string) (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, c)
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if c.Name == "" {
		c.Name = defaultName()
	}
	if c.Server != defaultServer {
		if c.Server != "" && c.Own.Server == "" {
			c.Own = Link{c.Server, c.Printer, c.Token}
		}
		c.Server, c.Printer, c.Token, c.Key = defaultServer, "", "", ""
	}
	if len(c.SetupPass) < 8 {
		c.SetupPass = randomWord(10)
	}
	return c, c.save(path)
}

func (c *Config) save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomWord(n int) string {
	const abc = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

func defaultName() string {
	b, _ := os.ReadFile("/sys/class/net/" + iface + "/address")
	mac := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(string(b)), ":", ""))
	if len(mac) >= 4 {
		return "ISLAND " + mac[len(mac)-4:]
	}
	return "ISLAND"
}

func clean(s string, n int) string {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	for len(s) > n {
		r := []rune(s)
		s = string(r[:len(r)-1])
	}
	return s
}
