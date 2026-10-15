package netcfg

import "testing"

func TestResolve(t *testing.T) {
	g := Global{Route: "direct", ProxyURL: DefaultProxy, TZ: "auto", Lang: "auto"}
	if e := Resolve(g, ""); e.Proxy != "direct" || e.TZ != "Asia/Shanghai" || e.Lang != "zh-CN" {
		t.Fatal(e)
	}
	if e := Resolve(g, "http://p:1"); e.Proxy != "http://p:1" || e.TZ != "America/Los_Angeles" {
		t.Fatal(e)
	}
	g.Route, g.TZ = "proxy", "Europe/Berlin"
	if e := Resolve(g, ""); e.Proxy != DefaultProxy || e.TZ != "Europe/Berlin" || e.Lang != "en-US" {
		t.Fatal(e)
	}
	if e := Resolve(g, "direct"); e.Proxy != "direct" {
		t.Fatal(e)
	}
	if ValidProxy("http://h:1") != nil || ValidProxy("ftp://x") == nil || ValidProxy("http://h:1/x?y") == nil {
		t.Fatal("proxy validation")
	}
}
