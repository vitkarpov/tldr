package main

import (
	"testing"

	"github.com/vitkarpov/tldr/internal/telegram"
)

func TestParseCommand(t *testing.T) {
	a := &app{botUsername: "SummaryBot"}
	cases := []struct {
		text, cmd, args string
	}{
		{"/summary", "summary", ""},
		{"/summary@SummaryBot like a pirate", "summary", "like a pirate"},
		{"/TLDR@summarybot", "tldr", ""},
		{"/summary@OtherBot", "", ""},
		{"hello /summary", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		cmd, args := a.parseCommand(&telegram.Message{Text: c.text})
		if cmd != c.cmd || args != c.args {
			t.Errorf("parseCommand(%q) = (%q, %q), want (%q, %q)", c.text, cmd, args, c.cmd, c.args)
		}
	}
}
