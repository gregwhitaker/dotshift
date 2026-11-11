package dashboard

import (
	"net/url"
	"testing"
)

func TestBrowserRouteKeepsTokenOnLocalOrigin(t *testing.T) {
	record := Record{URL: "http://127.0.0.1:1234", Token: "private"}
	for _, route := range []string{"/", "/models", "/?filter=attention", "/goals/a%2Fb"} {
		got, err := record.BrowserRoute(route)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(got)
		if err != nil || u.Host != "127.0.0.1:1234" || u.Fragment != "token=private" || u.Query().Get("token") != "" {
			t.Fatalf("unsafe route %q: %s %v", route, got, err)
		}
	}
	for _, route := range []string{"https://example.com", "//example.com", "///example.com", "projects", "/\\example.com", "/#other", "/\nother", "/%zz"} {
		if got, err := record.BrowserRoute(route); err == nil {
			t.Fatalf("accepted %q: %s", route, got)
		}
	}
}
