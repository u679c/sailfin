package device

import "testing"

func TestUA(t *testing.T) {
	cases := []struct{ ua, kind string }{{"", "Unknown"}, {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36", "Desktop"}, {"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1", "Mobile"}, {"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "Bot"}}
	for _, c := range cases {
		d := Parse(c.ua)
		if d.Type != c.kind {
			t.Fatalf("%s: %+v", c.kind, d)
		}
	}
}
