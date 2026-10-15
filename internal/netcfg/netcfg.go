// Package netcfg resolves the Epic browser's network route, timezone and
// language from DB settings (global + per-account override). .env values are
// only the initial defaults.
package netcfg

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
)

const DefaultProxy = "http://host.docker.internal:7890"

type Global struct {
	Route    string `json:"route"` // direct | proxy
	ProxyURL string `json:"proxy_url"`
	TZ       string `json:"tz"`   // auto | IANA zone
	Lang     string `json:"lang"` // auto | BCP-47
}

type Settings interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Effective is what a browser launch uses.
type Effective struct {
	Proxy string `json:"proxy"` // "direct" or URL
	TZ    string `json:"tz"`
	Lang  string `json:"lang"`
}

func envDefaults() Global {
	g := Global{Route: "direct", ProxyURL: DefaultProxy, TZ: "auto", Lang: "auto"}
	if p := os.Getenv("EPIC_BROWSER_PROXY"); p != "" && p != "direct" {
		g.Route = "proxy"
		if ValidProxy(p) == nil {
			g.ProxyURL = p
		}
	}
	if v := os.Getenv("EPIC_BROWSER_TZ"); v != "" {
		g.TZ = v
	}
	if v := os.Getenv("EPIC_BROWSER_LANG"); v != "" {
		g.Lang = v
	}
	return g
}

func Load(ctx context.Context, s Settings) (Global, error) {
	g := envDefaults()
	for k, p := range map[string]*string{"browser_route": &g.Route, "browser_proxy_url": &g.ProxyURL, "browser_tz": &g.TZ, "browser_lang": &g.Lang} {
		v, ok, err := s.Setting(ctx, k)
		if err != nil {
			return g, err
		}
		if ok && v != "" {
			*p = v
		}
	}
	return g, nil
}

func Save(ctx context.Context, s Settings, g Global) error {
	if g.Route != "direct" && g.Route != "proxy" {
		return errors.New("线路只能是直连或代理")
	}
	if g.Route == "proxy" || g.ProxyURL != "" {
		if err := ValidProxy(g.ProxyURL); err != nil {
			return err
		}
	}
	if !validToken(g.TZ, "/_+-") || !validToken(g.Lang, "-_") {
		return errors.New("时区或语言格式不正确")
	}
	for k, v := range map[string]string{"browser_route": g.Route, "browser_proxy_url": g.ProxyURL, "browser_tz": g.TZ, "browser_lang": g.Lang} {
		if err := s.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

func validToken(s, extra string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(extra, r)) {
			return false
		}
	}
	return true
}

func ValidProxy(p string) error {
	u, err := url.Parse(p)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return errors.New("代理地址需形如 http://host:port 或 socks5://host:port")
	}
	return nil
}

// ValidAccountRoute accepts "" (follow global), "direct", or a proxy URL.
func ValidAccountRoute(r string) error {
	if r == "" || r == "direct" {
		return nil
	}
	return ValidProxy(r)
}

// Resolve applies an account override ("" = follow global) and the auto rules.
func Resolve(g Global, account string) Effective {
	e := Effective{Proxy: "direct"}
	switch {
	case account == "direct":
	case account != "":
		e.Proxy = account
	case g.Route == "proxy":
		e.Proxy = g.ProxyURL
	}
	tz, lang := "Asia/Shanghai", "zh-CN"
	if e.Proxy != "direct" {
		tz, lang = "America/Los_Angeles", "en-US"
	}
	e.TZ, e.Lang = g.TZ, g.Lang
	if e.TZ == "auto" || e.TZ == "" {
		e.TZ = tz
	}
	if e.Lang == "auto" || e.Lang == "" {
		e.Lang = lang
	}
	return e
}

// Env returns the variables the workers' plain browser reads.
func (e Effective) Env() []string {
	return []string{"EPIC_BROWSER_PROXY=" + e.Proxy, "EPIC_BROWSER_TZ=" + e.TZ, "EPIC_BROWSER_LANG=" + e.Lang}
}
