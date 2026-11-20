package parser

import (
	"fmt"
	"testing"
	"time"
)

func TestAdapters(t *testing.T) {
	stamp := time.Now().Format("02/Jan/2006:15:04:05 -0700")
	line := fmt.Sprintf(`127.0.0.1 - - [%s] "GET /home?token=secret HTTP/1.1" 200 42 "-" "Mozilla/5.0"`, stamp)
	for _, kind := range []string{"nginx", "apache"} {
		r, e := Parse(kind, line, nil)
		if e != nil || r.Path != "/home" || r.Status != 200 {
			t.Fatalf("%s: %+v %v", kind, r, e)
		}
	}
	for _, test := range []struct {
		kind, line string
		fields     map[string]string
	}{{"json", `{"path":"/a?secret=x","status":201,"ua":"test","timestamp":1795000000000}`, nil}, {"json", `{"uri":"/mapped","code":202}`, map[string]string{"path": "uri", "status": "code"}}, {"caddy", `{"ts":1795000000,"status":200,"request":{"uri":"/caddy?x=y","method":"GET","headers":{"User-Agent":["test"]}}}`, nil}, {"docker-json", `{"log":"{\"path\":\"/docker\",\"status\":200}"}`, nil}} {
		r, e := Parse(test.kind, test.line, test.fields)
		if e != nil || r.Path == "" || Validate(r) != nil {
			t.Fatalf("%+v %v", r, e)
		}
	}
	if _, e := Parse("nginx", "broken", nil); e == nil {
		t.Fatal("invalid line accepted")
	}
}
