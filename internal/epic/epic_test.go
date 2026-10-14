package epic

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
