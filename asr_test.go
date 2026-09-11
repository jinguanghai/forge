//go:build windows

package main

import "testing"

func TestIsListenCmd(t *testing.T) {
	cases := []struct {
		in   string
		hit  bool
		path string
	}{
		{"/听", true, ""},
		{"听", true, ""},
		{"/语音", true, ""},
		{"/listen", true, ""},
		{"/voice 听", true, ""},
		{"/voice in", true, ""},
		{"/听 D:/x/a.mp3", true, "D:/x/a.mp3"},
		{"/listen a.wav", true, "a.wav"},
		{"/听  simple.wav", true, "simple.wav"},
		{"/voice on", false, ""},
		{"/help", false, ""},
		{"你好", false, ""},
		{"/voice", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		hit, path := isListenCmd(c.in)
		if hit != c.hit || path != c.path {
			t.Errorf("isListenCmd(%q) = (%v,%q), want (%v,%q)", c.in, hit, path, c.hit, c.path)
		}
	}
}
