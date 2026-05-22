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

func TestExtractAmazonFallsBackToHiddenProductTitleValue(t *testing.T) {
	html := `<!doctype html>
<html><head><link rel="canonical" href="https://www.amazon.com/dp/B08H75RTZ8"></head>
<body>
  <input type="hidden" id="productTitle" name="productTitle" value="Xbox Series X - Gaming Console"/>
  <div id="corePrice_feature_div"><span class="a-offscreen">$637.00</span></div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg">
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Title != "Xbox Series X - Gaming Console" {
		t.Fatalf("title: %q", got.Title)
	}
	if got.Confidence != "high" {
		t.Fatalf("confidence: %q", got.Confidence)
	}
}

func TestExtractAmazonFallsBackToImageWrapperPhoto(t *testing.T) {
	html := `<!doctype html>
<html><body>
  <span id="productTitle">Xbox Series X - Gaming Console</span>
  <div id="corePrice_feature_div"><span class="a-offscreen">$637.00</span></div>
  <div id="imgTagWrapperId">
    <img data-old-hires="https://m.media-amazon.com/images/I/xbox-hires.jpg"
         src="https://m.media-amazon.com/images/I/xbox-inline.jpg">
  </div>
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.ImageURL == "" || len(got.Images) == 0 {
		t.Fatalf("expected at least one product image: image=%q images=%v", got.ImageURL, got.Images)
	}
	if got.ImageURL != "https://m.media-amazon.com/images/I/xbox-hires.jpg" {
		t.Fatalf("image_url: %q", got.ImageURL)
	}
}

func TestExtractAmazonUsesSecondaryDeliveryEstimate(t *testing.T) {
	html := `<!doctype html>
<html><body>
  <span id="productTitle">Xbox Series X - Gaming Console</span>
  <div id="corePrice_feature_div"><span class="a-offscreen">$637.00</span></div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg">
  <div id="mir-layout-DELIVERY_BLOCK-slot-SECONDARY_DELIVERY_MESSAGE_LARGE">
    FREE delivery Tuesday, May 26
  </div>
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.ShippingSummary != "FREE delivery Tuesday, May 26" {
		t.Fatalf("shipping summary: %q", got.ShippingSummary)
	}
	if got.PrimeEligible == nil || *got.PrimeEligible {
		t.Fatalf("free delivery should be known non-prime unless a prime marker is present: %+v", got.PrimeEligible)
	}
}
