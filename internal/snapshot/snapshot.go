package snapshot

import "time"

type Snapshot struct {
	Provider                 string    `json:"provider"`
	ProviderVersion          string    `json:"provider_version,omitempty"`
	Merchant                 string    `json:"merchant,omitempty"`
	URL                      string    `json:"url"`
	CanonicalURL             string    `json:"canonical_url,omitempty"`
	ExternalID               string    `json:"external_id,omitempty"`
	Title                    string    `json:"title"`
	Variant                  string    `json:"variant,omitempty"`
	Price                    string    `json:"price,omitempty"`
	Currency                 string    `json:"currency,omitempty"`
	Seller                   string    `json:"seller,omitempty"`
	ShipsFrom                string    `json:"ships_from,omitempty"`
	ShippingSummary          string    `json:"shipping_summary,omitempty"`
	ShippingPrice            string    `json:"shipping_price,omitempty"`
	ShippingCurrency         string    `json:"shipping_currency,omitempty"`
	EstimatedTotal           string    `json:"estimated_total,omitempty"`
	EstimatedTotalCurrency   string    `json:"estimated_total_currency,omitempty"`
	PrimeEligible            *bool     `json:"prime_eligible,omitempty"`
	ImageURL                 string    `json:"image_url,omitempty"`
	Images                   []string  `json:"images,omitempty"`
	Description              string    `json:"description,omitempty"`
	Rating                   string    `json:"rating,omitempty"`
	ReviewCount              string    `json:"review_count,omitempty"`
	Availability             string    `json:"availability,omitempty"`
	CapturedAt               time.Time `json:"captured_at"`
	Confidence               string    `json:"confidence,omitempty"`
	RequiresUserConfirmation bool      `json:"requires_user_confirmation"`
}
