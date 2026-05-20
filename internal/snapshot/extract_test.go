package snapshot

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestExtractAmazonObservedXboxShape(t *testing.T) {
	data, err := os.ReadFile("testdata/amazon_xbox.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ExtractAmazon(string(data), ExtractOptions{
		URL:        "https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Title != "Xbox Series X - Gaming Console - 1TB SSD - Includes Wireless Controller - 4K Gaming - 120FPS - Carbon Black" {
		t.Fatalf("title: %q", got.Title)
	}
	if got.Price != "637.00" || got.Currency != "USD" {
		t.Fatalf("price: %q currency=%q", got.Price, got.Currency)
	}
	if got.Seller != "Amazon Resale" || got.ShipsFrom != "Amazon" {
		t.Fatalf("seller=%q ships_from=%q", got.Seller, got.ShipsFrom)
	}
	if got.PrimeEligible == nil || *got.PrimeEligible {
		t.Fatalf("prime should be known false for free non-prime delivery: %+v", got.PrimeEligible)
	}
	if got.ExternalID != "B08H75RTZ8" {
		t.Fatalf("asin: %q", got.ExternalID)
	}
	if got.ImageURL == "" || len(got.Images) < 2 {
		t.Fatalf("images: image=%q all=%+v", got.ImageURL, got.Images)
	}
	if !strings.Contains(got.Description, "FASTEST, MOST POWERFUL XBOX") {
		t.Fatalf("description: %q", got.Description)
	}
	if got.Confidence != "high" || !got.RequiresUserConfirmation {
		t.Fatalf("provenance: %+v", got)
	}
}

func TestExtractAmazonUnavailableProductStillSnapshots(t *testing.T) {
	data, err := os.ReadFile("testdata/amazon_xbox_unavailable.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ExtractAmazon(string(data), ExtractOptions{
		URL:        "https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B0CMVPN6GL",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Title != "Xbox Series X + Elite Core wireless controller blue" {
		t.Fatalf("title: %q", got.Title)
	}
	if got.Price != "" || got.Currency != "" {
		t.Fatalf("unavailable price should be empty: price=%q currency=%q", got.Price, got.Currency)
	}
	if got.Availability != "Currently unavailable." {
		t.Fatalf("availability: %q", got.Availability)
	}
	if got.PrimeEligible != nil {
		t.Fatalf("prime should be unknown for unavailable product: %+v", got.PrimeEligible)
	}
	if got.Confidence != "medium" {
		t.Fatalf("confidence: %q", got.Confidence)
	}
}

func TestExtractAmazonThirdPartySellerAndNonPrimeShipping(t *testing.T) {
	data, err := os.ReadFile("testdata/amazon_xbox_third_party.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ExtractAmazon(string(data), ExtractOptions{
		URL:        "https://www.amazon.com/dp/B0DL7CKRJ5?th=1",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Seller != "Sole Providers" {
		t.Fatalf("seller: %q", got.Seller)
	}
	if got.ShipsFrom != "Sole Providers" {
		t.Fatalf("ships_from: %q", got.ShipsFrom)
	}
	if got.Price != "527.99" {
		t.Fatalf("price: %q", got.Price)
	}
	if got.PrimeEligible == nil || *got.PrimeEligible {
		t.Fatalf("prime should be known false for paid/non-prime offer: %+v", got.PrimeEligible)
	}
	if !strings.Contains(got.ShippingSummary, "FREE delivery May 26 - 27") {
		t.Fatalf("shipping summary: %q", got.ShippingSummary)
	}
}
