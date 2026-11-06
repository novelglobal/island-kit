// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var fallback = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
	if err != nil {
		return d.DialContext(ctx, "udp", "9.9.9.9:53")
	}
	return c, nil
}}

func dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	c, err := d.DialContext(ctx, network, addr)
	var de *net.DNSError
	if err == nil || !errors.As(err, &de) {
		return c, err
	}
	host, port, _ := net.SplitHostPort(addr)
	ips, ferr := fallback.LookupIPAddr(ctx, host)
	if ferr != nil {
		return nil, err
	}
	for _, ip := range ips {
		if c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port)); e == nil {
			return c, nil
		} else {
			err = e
		}
	}
	return nil, err
}

var client = &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{
	DialContext: dial, TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 2,
}}

type Reply struct {
	Status int
	Body   map[string]any
	Wait   time.Duration
	Island int
}

func call(method, url, token string, body any) (*Reply, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "island/"+version)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	r := &Reply{Status: res.StatusCode, Body: map[string]any{}}
	if len(raw) > 0 && strings.Contains(res.Header.Get("content-type"), "json") {
		_ = json.Unmarshal(raw, &r.Body)
	}
	if w, err := time.ParseDuration(res.Header.Get("x-wait") + "s"); err == nil && w >= time.Second && w <= 5*time.Minute {
		r.Wait = w
	}
	if n, err := strconv.Atoi(res.Header.Get("x-island")); err == nil && n > 0 {
		r.Island = n
	}
	return r, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) float64 {
	f, _ := m[k].(float64)
	return f
}

func localIP() string {
	c, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
