// Package epic fetches current free games from the public Epic API.
package epic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const endpoint = "https://store-site-backend-static.epicgames.com/freeGamesPromotions"

type Game struct {
	Title     string    `json:"title"`
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	Slug      string    `json:"slug"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
	URL       string    `json:"url"`
}

type Client struct {
	HTTP            *http.Client
	Locale, Country string
}

func NewClient(locale, country string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Locale: locale, Country: country}
}

type promoOffer struct {
	PromotionalOffers []struct {
		StartDate       time.Time `json:"startDate"`
		EndDate         time.Time `json:"endDate"`
		DiscountSetting struct {
			DiscountPercentage int `json:"discountPercentage"`
		} `json:"discountSetting"`
	} `json:"promotionalOffers"`
}

type response struct {
	Data struct {
		Catalog struct {
			SearchStore struct {
				Elements []struct {
					Title         string `json:"title"`
					ID            string `json:"id"`
					Namespace     string `json:"namespace"`
					ProductSlug   string `json:"productSlug"`
					OfferMappings []struct {
						PageSlug string `json:"pageSlug"`
					} `json:"offerMappings"`
					CatalogNs struct {
						Mappings []struct {
							PageSlug string `json:"pageSlug"`
						} `json:"mappings"`
					} `json:"catalogNs"`
					Price struct {
						TotalPrice struct {
							DiscountPrice int `json:"discountPrice"`
						} `json:"totalPrice"`
					} `json:"price"`
					Promotions *struct {
						PromotionalOffers []promoOffer `json:"promotionalOffers"`
					} `json:"promotions"`
				} `json:"elements"`
			} `json:"searchStore"`
		} `json:"Catalog"`
	} `json:"data"`
}

// CurrentFreeGames returns titles that are free right now.
func (c *Client) CurrentFreeGames(ctx context.Context) ([]Game, error) {
	q := url.Values{"locale": {c.Locale}, "country": {c.Country}, "allowCountries": {c.Country}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epic api: status %d", resp.StatusCode)
	}
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	now := time.Now()
	var out []Game
	for _, e := range r.Data.Catalog.SearchStore.Elements {
		if e.Promotions == nil || e.Price.TotalPrice.DiscountPrice != 0 {
			continue
		}
		for _, po := range e.Promotions.PromotionalOffers {
			for _, o := range po.PromotionalOffers {
				if o.DiscountSetting.DiscountPercentage != 0 || now.Before(o.StartDate) || now.After(o.EndDate) {
					continue
				}
				slug := e.ProductSlug
				if len(e.OfferMappings) > 0 && e.OfferMappings[0].PageSlug != "" {
					slug = e.OfferMappings[0].PageSlug
				} else if len(e.CatalogNs.Mappings) > 0 {
					slug = e.CatalogNs.Mappings[0].PageSlug
				}
				g := Game{Title: e.Title, ID: e.ID, Namespace: e.Namespace, Slug: slug, StartDate: o.StartDate, EndDate: o.EndDate}
				if slug != "" {
					g.URL = "https://store.epicgames.com/p/" + slug
				}
				out = append(out, g)
			}
		}
	}
	return out, nil
}
