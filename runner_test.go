package main

import (
	"reflect"
	"testing"
)

func TestLineWriter(t *testing.T) {
	cases := []struct {
		name string
		in   []string // written in separate chunks
		want []string // rendered pane after applying replaces
	}{
		{"plain", []string{"a\nb\n"}, []string{"a", "b"}},
		{"blank lines kept", []string{"a\n\nb\n"}, []string{"a", "", "b"}},
		{"crlf", []string{"a\r\nb\r\n"}, []string{"a", "b"}},
		{"progress keeps prior line", []string{"dl\n\r10%\r50%\r100%\ndone\n"}, []string{"dl", "100%", "done"}},
		{"split across chunks", []string{"he", "llo\r", "\n", "x"}, []string{"hello", "x"}},
		{"escape-code spinner", []string{"head\n\x1b[999D\x1b[Jspin1\x1b[999D\x1b[Jspin2\x1b[2K\x1b[1Gdone\n"}, []string{"head", "done"}},
		{"ansi stripped", []string{"\x1b[32mok\x1b[0m\n"}, []string{"ok"}},
	}
	for _, c := range cases {
		var pane []string
		w := &lineWriter{emit: func(l string, replace bool) {
			if replace && len(pane) > 0 {
				pane[len(pane)-1] = l
			} else {
				pane = append(pane, l)
			}
		}}
		for _, chunk := range c.in {
			w.Write([]byte(chunk))
		}
		w.flush()
		if !reflect.DeepEqual(pane, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, pane, c.want)
		}
	}
}
