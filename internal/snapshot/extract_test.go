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
	if got.ShippingPrice != "0.00" || got.ShippingCurrency != "USD" {
		t.Fatalf("shipping price: %q currency=%q", got.ShippingPrice, got.ShippingCurrency)
	}
	if got.EstimatedTotal != "637.00" || got.EstimatedTotalCurrency != "USD" {
		t.Fatalf("estimated total: %q currency=%q", got.EstimatedTotal, got.EstimatedTotalCurrency)
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

func TestExtractAmazonUnavailableProductSuppressesTransactionalFields(t *testing.T) {
	html := `<!doctype html>
<html><body>
  <span id="productTitle">Xbox Series X + Elite Core wireless controller blue</span>
  <div id="availability"><span class="a-size-medium">Currently unavailable.</span></div>
  <div id="corePrice_feature_div"><span class="a-offscreen">$48.48</span></div>
  <i class="a-icon a-icon-prime"></i>
  <div id="mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE">
    FREE delivery tomorrow
  </div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg">
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B0CMVPN6GL",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Price != "" || got.Currency != "" || got.ShippingPrice != "" || got.EstimatedTotal != "" {
		t.Fatalf("unavailable product should not expose transactional totals: price=%q currency=%q shipping=%q total=%q",
			got.Price, got.Currency, got.ShippingPrice, got.EstimatedTotal)
	}
	if got.PrimeEligible != nil {
		t.Fatalf("unavailable product should not expose Prime eligibility: %+v", got.PrimeEligible)
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

func TestExtractAmazonFallsBackToMetadataProductTitle(t *testing.T) {
	html := `<!doctype html>
<html><head>
  <link rel="canonical" href="https://www.amazon.com/dp/B08N5WRWNW">
  <meta property="og:title" content="Echo Dot (5th Gen, 2022 release) | Smart speaker">
  <meta name="title" content="Ignored fallback">
</head><body>
  <div id="corePrice_feature_div"><span class="a-offscreen">$49.99</span></div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg">
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08N5WRWNW",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.Title != "Echo Dot (5th Gen, 2022 release) | Smart speaker" {
		t.Fatalf("title: %q", got.Title)
	}
	if got.Confidence != "high" {
		t.Fatalf("confidence: %q", got.Confidence)
	}
}

func TestExtractAmazonDoesNotUseGenericDocumentTitleAsProductTitle(t *testing.T) {
	html := `<!doctype html>
<html><head>
  <title>Amazon.com. Spend less. Smile more.</title>
  <link rel="canonical" href="https://www.amazon.com/dp/B08N5WRWNW">
</head><body>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg">
</body></html>`
	if _, err := ExtractAmazon(html, ExtractOptions{URL: "https://www.amazon.com/dp/B08N5WRWNW"}); err == nil {
		t.Fatal("expected generic Amazon document title to fail closed")
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
	if got.ShippingPrice != "0.00" || got.EstimatedTotal != "637.00" {
		t.Fatalf("free shipping should produce zero shipping and price-only total: shipping=%q total=%q", got.ShippingPrice, got.EstimatedTotal)
	}
}

func TestExtractAmazonAddsPaidShippingToEstimatedTotal(t *testing.T) {
	html := `<!doctype html>
<html><body>
  <span id="productTitle">Xbox Series X - Gaming Console</span>
  <div id="corePrice_feature_div"><span class="a-offscreen">$637.00</span></div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg">
  <div id="mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE">
    $12.49 delivery Tuesday, May 26
  </div>
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.ShippingPrice != "12.49" || got.ShippingCurrency != "USD" {
		t.Fatalf("shipping price: %q currency=%q", got.ShippingPrice, got.ShippingCurrency)
	}
	if got.EstimatedTotal != "649.49" || got.EstimatedTotalCurrency != "USD" {
		t.Fatalf("estimated total: %q currency=%q", got.EstimatedTotal, got.EstimatedTotalCurrency)
	}
}

func TestExtractAmazonShippingPriceIgnoresDeliveryDates(t *testing.T) {
	html := `<!doctype html>
<html><body>
  <span id="productTitle">Xbox Series X - Gaming Console</span>
  <div id="corePrice_feature_div"><span class="a-offscreen">$637.00</span></div>
  <img id="landingImage" src="https://m.media-amazon.com/images/I/xbox.jpg">
  <div id="mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE">
    Delivery May 28 for $12.49
  </div>
</body></html>`
	got, err := ExtractAmazon(html, ExtractOptions{
		URL:        "https://www.amazon.com/dp/B08H75RTZ8",
		CapturedAt: time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.ShippingPrice != "12.49" || got.EstimatedTotal != "649.49" {
		t.Fatalf("shipping should use dollar amount, not delivery date: shipping=%q total=%q", got.ShippingPrice, got.EstimatedTotal)
	}
}
