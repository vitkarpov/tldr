package texts

import "testing"

func TestMinutes(t *testing.T) {
	cases := map[int]string{
		1: "минута", 2: "минуты", 4: "минуты", 5: "минут", 10: "минут",
		11: "минут", 12: "минут", 14: "минут", 21: "минута", 22: "минуты", 25: "минут", 111: "минут",
	}
	for n, want := range cases {
		if got := Minutes(n); got != want {
			t.Errorf("Minutes(%d) = %q, want %q", n, got, want)
		}
	}
}
