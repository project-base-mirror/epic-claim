package epic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCurrentFreeGamesFailsClosed(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, price, discount string
		start, end            time.Time
		want                  int
	}{
		{"active", `"discountPrice":0`, `"discountPercentage":0`, now.Add(-time.Hour), now.Add(time.Hour), 1},
		{"missing-price", ``, `"discountPercentage":0`, now.Add(-time.Hour), now.Add(time.Hour), 0},
		{"missing-discount", `"discountPrice":0`, ``, now.Add(-time.Hour), now.Add(time.Hour), 0},
		{"paid", `"discountPrice":1`, `"discountPercentage":0`, now.Add(-time.Hour), now.Add(time.Hour), 0},
		{"expired", `"discountPrice":0`, `"discountPercentage":0`, now.Add(-2 * time.Hour), now.Add(-time.Hour), 0},
		{"future", `"discountPrice":0`, `"discountPercentage":0`, now.Add(time.Hour), now.Add(2 * time.Hour), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offer := fmt.Sprintf(`{"startDate":%q,"endDate":%q,"discountSetting":{%s}}`, tc.start.Format(time.RFC3339), tc.end.Format(time.RFC3339), tc.discount)
			body := fmt.Sprintf(`{"data":{"Catalog":{"searchStore":{"elements":[{"title":"Game","id":"o","namespace":"n","productSlug":"game","price":{"totalPrice":{%s}},"promotions":{"promotionalOffers":[{"promotionalOffers":[%s,%s]}]}}]}}}}`, tc.price, offer, offer)
			c := NewClient("en-US", "US")
			c.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			games, err := c.CurrentFreeGames(context.Background())
			if err != nil || len(games) != tc.want {
				t.Fatalf("got %v %v", games, err)
			}
		})
	}
}

func TestRealResponseFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/freegames-2026-10-15.json")
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient("en-US", "US")
	c.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.String(), DefaultEndpoint+"?") {
			t.Errorf("unexpected url %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	games, err := c.CurrentFreeGamesAt(context.Background(), time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, g := range games {
		got[g.Title] = g.Slug
	}
	if len(games) != 2 || got["Out of Sight"] != "out-of-sight-b96ca8" || got["TerraScape"] != "terrascape-2b12b1" {
		t.Fatalf("unexpected games %+v", games)
	}
}

func TestNormalizeSlug(t *testing.T) {
	for in, want := range map[string]string{"thems-fightin-herds/home": "thems-fightin-herds", "/a/": "a", "b": "b", "": ""} {
		if got := normalizeSlug(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
