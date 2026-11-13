package config

import "testing"

func TestWhitelist(t *testing.T) {
	t.Setenv("BOT_TOKEN_PARAM", "/x")
	t.Setenv("ALLOWED_CHAT_IDS", " -1001234567890, 42 ,")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{-1001234567890: true, 42: true, 7: false} {
		if got := c.Allowed(id); got != want {
			t.Errorf("Allowed(%d) = %v, want %v", id, got, want)
		}
	}
}

func TestEmptyWhitelistDeniesAll(t *testing.T) {
	t.Setenv("BOT_TOKEN_PARAM", "/x")
	t.Setenv("ALLOWED_CHAT_IDS", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Allowed(42) {
		t.Fatal("empty whitelist must deny everyone")
	}
}

func TestBadChatID(t *testing.T) {
	t.Setenv("BOT_TOKEN_PARAM", "/x")
	t.Setenv("ALLOWED_CHAT_IDS", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-numeric chat id")
	}
}
