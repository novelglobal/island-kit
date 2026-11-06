// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"image"
	"image/color"
	"strings"

	"rsc.io/qr"
)

var (
	paper = "80"
	dots  = 512
	cols  = 42
)

func setPaper(p string) {
	if p == "58" {
		paper, dots, cols = "58", 384, 32
	} else {
		paper, dots, cols = "80", 512, 42
	}
}

type Slip struct{ b bytes.Buffer }

func NewSlip() *Slip {
	s := &Slip{}
	s.b.WriteString("\x1b@\x1bt\x00")
	return s
}

func (s *Slip) Bytes() []byte { return s.b.Bytes() }

func (s *Slip) Center() *Slip { s.b.WriteString("\x1ba\x01"); return s }
func (s *Slip) Left() *Slip   { s.b.WriteString("\x1ba\x00"); return s }

func (s *Slip) Bold(on bool) *Slip {
	s.b.WriteString("\x1bE")
	s.b.WriteByte(b2(on))
	return s
}

func (s *Slip) Big(on bool) *Slip {
	s.b.WriteString("\x1d!")
	if on {
		s.b.WriteByte(0x11)
	} else {
		s.b.WriteByte(0)
	}
	return s
}

func (s *Slip) Size(w, h int) *Slip {
	s.b.WriteString("\x1d!")
	s.b.WriteByte(byte((w-1)<<4 | (h - 1)))
	return s
}

func (s *Slip) Spaced(n int) *Slip {
	s.b.WriteString("\x1b ")
	s.b.WriteByte(byte(n))
	return s
}

func (s *Slip) Rule(h int) *Slip {
	return s.Left().Raster(bytes.Repeat([]byte{0xff}, dots/8*h), dots/8, h)
}

func (s *Slip) Teeth(up bool) *Slip {
	const tw, h = 32, 14
	wb := dots / 8
	bits := make([]byte, wb*h)
	for y := 0; y < h; y++ {
		reach := (h - y) * tw / 2 / h
		if up {
			reach = (y + 1) * tw / 2 / h
		}
		for x := 0; x < dots; x++ {
			if d := x%tw - tw/2; d >= -reach && d < reach {
				bits[y*wb+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return s.Left().Raster(bits, wb, h)
}

func (s *Slip) Line(text string, width int) *Slip {
	for _, l := range wrap(ascii(text), width) {
		s.b.WriteString(l)
		s.b.WriteByte('\n')
	}
	if text == "" {
		s.b.WriteByte('\n')
	}
	return s
}

func (s *Slip) Text(text string) *Slip { return s.Line(text, cols) }

func (s *Slip) Feed(n int) *Slip {
	s.b.WriteString("\x1bd")
	s.b.WriteByte(byte(n))
	return s
}

func (s *Slip) Cut() *Slip {
	s.b.WriteString("\x1bd\x04\x1dVB\x00")
	return s
}

func (s *Slip) QR(data string, size int) *Slip {
	c, err := qr.Encode(data, qr.M)
	if err != nil {
		return s
	}
	n := (c.Size + 8) * size
	if n > dots {
		size = dots / (c.Size + 8)
		n = (c.Size + 8) * size
	}
	wb := dots / 8
	left := (dots - n) / 2
	bits := make([]byte, wb*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if c.Black(x/size-4, y/size-4) {
				X := left + x
				bits[y*wb+X/8] |= 0x80 >> (X % 8)
			}
		}
	}
	s.Left().Raster(bits, wb, n)
	return s
}

func (s *Slip) Raster(bits []byte, wb, h int) *Slip {
	const band = 128
	for y := 0; y < h; y += band {
		n := band
		if y+n > h {
			n = h - y
		}
		s.b.WriteString("\x1dv0\x00")
		s.b.Write([]byte{byte(wb & 255), byte(wb >> 8), byte(n & 255), byte(n >> 8)})
		s.b.Write(bits[y*wb : (y+n)*wb])
	}
	return s
}

func (s *Slip) Image(img image.Image) *Slip {
	bits, wb, h := toBits(img, dots, 3000)
	return s.Raster(bits, wb, h)
}

func toBits(img image.Image, width, maxH int) ([]byte, int, int) {
	r := img.Bounds()
	if r.Dx() == 0 || r.Dy() == 0 {
		return nil, 0, 0
	}
	if r.Dx() < width {
		width = r.Dx()
	}
	h := r.Dy() * width / r.Dx()
	if h > maxH {
		h = maxH
	}
	wb := (width + 7) / 8
	bits := make([]byte, wb*h)
	for y := 0; y < h; y++ {
		sy := r.Min.Y + y*r.Dy()/h
		for x := 0; x < width; x++ {
			sx := r.Min.X + x*r.Dx()/width
			g := color.GrayModel.Convert(img.At(sx, sy)).(color.Gray)
			_, _, _, a := img.At(sx, sy).RGBA()
			if a > 0x7fff && g.Y < 128 {
				bits[y*wb+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return bits, wb, h
}

func ascii(s string) string {
	r := strings.NewReplacer("·", "-", "—", "-", "–", "-", "‘", "'", "’", "'", "“", `"`, "”", `"`, "…", "...", "é", "e", "è", "e", "ñ", "n", "ü", "u", "ö", "o", "ä", "a")
	s = r.Replace(s)
	out := make([]byte, 0, len(s))
	for _, c := range s {
		if c >= 32 && c < 127 {
			out = append(out, byte(c))
		} else if c == '\n' {
			out = append(out, ' ')
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}

func wrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if len(s) <= width {
		return []string{s}
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len(w) > width {
			if line != "" {
				out = append(out, line)
				line = ""
			}
			out = append(out, w[:width])
			w = w[width:]
		}
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func b2(on bool) byte {
	if on {
		return 1
	}
	return 0
}

func wifiQR(ssid, pass string) string {
	e := func(s string) string {
		var b strings.Builder
		for _, c := range s {
			if strings.ContainsRune(`\;,:"`, c) {
				b.WriteByte('\\')
			}
			b.WriteRune(c)
		}
		return b.String()
	}
	if pass == "" {
		return "WIFI:T:nopass;S:" + e(ssid) + ";;"
	}
	return "WIFI:T:WPA;S:" + e(ssid) + ";P:" + e(pass) + ";;"
}
