package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestProxyGraphCyclesUnknownGroupsAndOpaqueProvider(t *testing.T) {
	all := map[string]coreProxy{"a": {Type: "VLESS", DialerProxy: "b"}, "b": {Type: "VLESS", DialerProxy: "a"}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("dialer cycle accepted")
	}
	all = map[string]coreProxy{"a": {Type: "Selector", Now: "missing"}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("missing provider invented")
	}
	all = map[string]coreProxy{"a": {Type: "UnsupportedGroup", All: []string{"b"}}}
	if _, e := expandProxy(all, "a"); e == nil {
		t.Fatal("unknown group guessed")
	}
	all = map[string]coreProxy{"owned-exit": {Type: "VLESS", DialerProxy: "airport-choice"}, "airport-choice": {Type: "Selector", Now: "airport-node"}, "airport-node": {Type: "Shadowsocks"}}
	chain, e := expandProxy(all, "owned-exit")
	if e != nil || !reflect.DeepEqual(chain, []string{"airport-node", "owned-exit"}) {
		t.Fatal("external provider chain reversed", chain, e)
	}
	if chain, e = expandProxy(all, "DIRECT"); e != nil || len(chain) != 1 {
		t.Fatal(e)
	}
}
func TestMihomoForbiddenRead(t *testing.T) {
	if err := readCore(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "/group/auto/delay", "", &map[string]any{}); err == nil {
		t.Fatal("side-effecting GET allowed")
	}
}

func TestMihomoResponseBudgets(t *testing.T) {
	wide := map[string]any{}
	for i := 0; i < 129; i++ {
		wide[strings.Repeat("k", i%100)+string(rune(0x100+i))] = 1
	}
	raw, _ := json.Marshal(wide)
	array := "[" + strings.Repeat("{},", 256) + "{}]"
	for _, body := range []string{
		`{"proxies":` + string(raw) + `}`, `{"providers":` + string(raw) + `}`,
		`{"proxies":{"p":{"history":` + array + `}}}`, `{"proxies":{"p":{"all":` + array + `}}}`,
		`{"providers":{"p":{"proxies":` + array + `}}}`, `{"mode":"a","mode":"b"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		for _, path := range []string{"/configs", "/proxies", "/providers/proxies"} {
			var v map[string]any
			if err := readCore(context.Background(), srv.Client(), srv.URL, path, "", &v); err == nil {
				t.Fatal("complex response accepted", path)
			}
			if v != nil {
				t.Fatal("destination allocated before budget check")
			}
		}
		srv.Close()
	}
}
