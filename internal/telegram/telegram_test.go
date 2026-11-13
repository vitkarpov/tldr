package telegram

import "testing"

func TestBestPhoto(t *testing.T) {
	m := &Message{Photo: []PhotoSize{
		{FileID: "s", Width: 90, Height: 60, FileSize: 1_000},
		{FileID: "m", Width: 800, Height: 533, FileSize: 60_000},
		{FileID: "huge", Width: 5000, Height: 3333, FileSize: MaxImageBytes + 1},
	}}
	if got := m.BestPhoto(); got == nil || got.FileID != "m" {
		t.Fatalf("BestPhoto = %+v, want m", got)
	}
	if (&Message{}).BestPhoto() != nil || (*Message)(nil).BestPhoto() != nil {
		t.Fatal("BestPhoto without photos should be nil")
	}
}

func TestOriginName(t *testing.T) {
	ch := &Chat{Title: "Канал"}
	cases := []struct {
		o    *MessageOrigin
		want string
	}{
		{nil, ""},
		{&MessageOrigin{Type: "user", SenderUser: &User{FirstName: "Андрей", LastName: "А"}}, "Андрей А"},
		{&MessageOrigin{Type: "hidden_user", SenderUserName: "Аноним"}, "Аноним"},
		{&MessageOrigin{Type: "channel", Chat: ch}, "Канал"},
		{&MessageOrigin{Type: "channel", Chat: ch, AuthorSignature: "Андрей"}, "Андрей (Канал)"},
		{&MessageOrigin{Type: "chat", SenderChat: ch}, "Канал"},
	}
	for _, c := range cases {
		if got := c.o.Name(); got != c.want {
			t.Errorf("Name(%+v) = %q, want %q", c.o, got, c.want)
		}
	}
}
