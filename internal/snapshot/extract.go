package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type ExtractOptions struct {
	URL        string
	CapturedAt time.Time
}

func ExtractAmazon(htmlText string, opts ExtractOptions) (Snapshot, error) {
	root, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse html: %w", err)
	}
	now := opts.CapturedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := Snapshot{
		Provider:                 "browser_capture",
		ProviderVersion:          "amazon-dom-v1",
		Merchant:                 "amazon",
		URL:                      opts.URL,
		ExternalID:               asinFromURL(opts.URL),
		CapturedAt:               now,
		RequiresUserConfirmation: true,
	}
	out.Title = firstNonEmpty(
		firstTextByID(root, "productTitle"),
		firstAttrByID(root, "productTitle", "value"),
	)
	out.CanonicalURL = firstAttr(root, "link", "rel", "canonical", "href")
	if out.CanonicalURL == "" {
		out.CanonicalURL = opts.URL
	}
	out.ImageURL = firstNonEmpty(
		firstAttrByID(root, "landingImage", "src"),
		firstProductImageAttr(root, "data-old-hires"),
		firstProductImageAttr(root, "src"),
	)
	out.Images = uniqueNonEmpty(dynamicImages(firstAttrByID(root, "landingImage", "data-a-dynamic-image")))
	if out.ImageURL != "" && !contains(out.Images, out.ImageURL) {
		out.Images = append([]string{out.ImageURL}, out.Images...)
	}
	out.Price, out.Currency = normalizePrice(firstNonEmpty(
		firstTextBySelector(root, "corePrice_feature_div", "a-offscreen"),
		firstTextBySelector(root, "apex_desktop", "a-offscreen"),
		firstTextByClass(root, "a-offscreen"),
	))
	out.Availability = amazonAvailability(root)
	out.Seller = amazonSeller(root)
	out.ShipsFrom = amazonShipsFrom(root, out.Seller)
	out.ShippingSummary = amazonShippingSummary(root)
	out.ShippingPrice, out.ShippingCurrency = amazonShippingPrice(out.ShippingSummary)
	out.EstimatedTotal, out.EstimatedTotalCurrency = estimatedTotal(out.Price, out.Currency, out.ShippingPrice, out.ShippingCurrency)
	out.PrimeEligible = amazonPrimeEligible(root, out.ShippingSummary)
	out.Rating = firstNonEmpty(firstTextByID(root, "acrPopover"), firstTextByClass(root, "a-icon-alt"))
	out.ReviewCount = firstTextByID(root, "acrCustomerReviewText")
	out.Description = strings.Join(textsByClassUnderID(root, "feature-bullets", "a-list-item", 8), "\n")
	if amazonUnavailable(out.Availability) {
		clearTransactionalFields(&out)
	}
	if out.Title == "" {
		return Snapshot{}, errors.New("amazon product title not found")
	}
	if out.Price != "" && out.ImageURL != "" {
		out.Confidence = "high"
	} else {
		out.Confidence = "medium"
	}
	return out, nil
}

func amazonUnavailable(availability string) bool {
	availability = strings.ToLower(clean(availability))
	return strings.Contains(availability, "currently unavailable") ||
		strings.Contains(availability, "temporarily out of stock") ||
		strings.Contains(availability, "out of stock")
}

func clearTransactionalFields(out *Snapshot) {
	out.Price = ""
	out.Currency = ""
	out.Seller = ""
	out.ShipsFrom = ""
	out.ShippingSummary = ""
	out.ShippingPrice = ""
	out.ShippingCurrency = ""
	out.EstimatedTotal = ""
	out.EstimatedTotalCurrency = ""
	out.PrimeEligible = nil
}

func amazonAvailability(root *html.Node) string {
	return cleanAvailability(firstNonEmpty(
		firstTextByClassUnderID(root, "availability", "primary-availability-message"),
		firstTextByID(root, "outOfStock"),
		firstTextByID(root, "availability"),
	))
}

func amazonSeller(root *html.Node) string {
	seller := firstTextByID(root, "sellerProfileTriggerId")
	if seller != "" {
		return seller
	}
	merchant := firstTextByID(root, "merchant-info")
	if merchant != "" {
		re := regexp.MustCompile(`(?i)\bSold by\s+(.+?)(?:\s+and\s+Fulfilled by|\s+and\s+fulfilled by|\.|$)`)
		if match := re.FindStringSubmatch(merchant); len(match) == 2 {
			return clean(match[1])
		}
	}
	return offerDisplayValue(root, "Seller")
}

func amazonShipsFrom(root *html.Node, seller string) string {
	merchant := firstTextByID(root, "merchant-info")
	if strings.Contains(strings.ToLower(merchant), "fulfilled by amazon") {
		return "Amazon"
	}
	if value := offerDisplayValue(root, "Ships from"); value != "" {
		return value
	}
	if value := offerDisplayValue(root, "Shipper / Seller"); value != "" {
		return value
	}
	if seller != "" && strings.Contains(firstTextByID(root, "offerDisplayFeatures_desktop"), "Shipper / Seller") {
		return seller
	}
	if seller == "Amazon.com" || seller == "Amazon Resale" {
		return "Amazon"
	}
	return ""
}

func amazonShippingSummary(root *html.Node) string {
	return firstNonEmpty(
		firstTextByID(root, "mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE"),
		firstTextByID(root, "mir-layout-DELIVERY_BLOCK-slot-SECONDARY_DELIVERY_MESSAGE_LARGE"),
		firstTextByID(root, "deliveryBlockMessage"),
		firstTextByID(root, "primeShippingMessage_feature_div"),
	)
}

func amazonPrimeEligible(root *html.Node, shippingSummary string) *bool {
	if classExists(root, "a-icon-prime") {
		return boolPtr(true)
	}
	shipping := strings.ToLower(shippingSummary)
	if strings.Contains(shipping, "prime") {
		return boolPtr(true)
	}
	if strings.Contains(shipping, "free delivery") || strings.Contains(shipping, "free shipping") {
		return boolPtr(false)
	}
	return nil
}

func amazonShippingPrice(shippingSummary string) (string, string) {
	summary := strings.ToLower(clean(shippingSummary))
	if summary == "" {
		return "", ""
	}
	if strings.Contains(summary, "free delivery") || strings.Contains(summary, "free shipping") {
		return "0.00", "USD"
	}
	return normalizeUSDPrice(shippingSummary)
}

func estimatedTotal(price, currency, shippingPrice, shippingCurrency string) (string, string) {
	if price == "" || currency == "" || shippingPrice == "" || shippingCurrency == "" || currency != shippingCurrency {
		return "", ""
	}
	priceCents, ok := parseMoneyCents(price)
	if !ok {
		return "", ""
	}
	shippingCents, ok := parseMoneyCents(shippingPrice)
	if !ok {
		return "", ""
	}
	return formatMoneyCents(priceCents + shippingCents), currency
}

func offerDisplayValue(root *html.Node, label string) string {
	container := nodeByID(root, "offerDisplayFeatures_desktop")
	if container == nil {
		return ""
	}
	fields := strings.Fields(clean(nodeText(container)))
	if len(fields) == 0 {
		return ""
	}
	labelFields := strings.Fields(strings.ToLower(label))
	for i := 0; i+len(labelFields) < len(fields); i++ {
		if !fieldsEqualFold(fields[i:i+len(labelFields)], labelFields) {
			continue
		}
		start := i + len(labelFields)
		if start >= len(fields) {
			return ""
		}
		out := []string{}
		for j := start; j < len(fields); j++ {
			word := strings.Trim(fields[j], " /")
			if isOfferDisplayStopWord(word) {
				break
			}
			out = append(out, fields[j])
			if len(out) >= 6 {
				break
			}
		}
		value := clean(strings.Join(out, " "))
		parts := strings.Fields(value)
		if len(parts)%2 == 0 && len(parts) > 0 {
			half := len(parts) / 2
			if sameWordSlice(parts[:half], parts[half:]) {
				value = strings.Join(parts[:half], " ")
			}
		}
		return value
	}
	return ""
}

func fieldsEqualFold(values, lowerLabel []string) bool {
	if len(values) != len(lowerLabel) {
		return false
	}
	for i := range values {
		if strings.ToLower(strings.Trim(values[i], " /")) != lowerLabel[i] {
			return false
		}
	}
	return true
}

func sameWordSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func isOfferDisplayStopWord(word string) bool {
	switch strings.ToLower(word) {
	case "", "shipper", "seller", "condition", "returns", "return", "payment", "gift", "support", "packaging":
		return true
	default:
		return false
	}
}

func firstTextByID(root *html.Node, id string) string {
	var found string
	walk(root, func(n *html.Node) bool {
		if attr(n, "id") == id {
			found = nodeText(n)
			return false
		}
		return true
	})
	return clean(found)
}

func firstTextByClass(root *html.Node, className string) string {
	var found string
	walk(root, func(n *html.Node) bool {
		if hasClass(n, className) {
			found = nodeText(n)
			return false
		}
		return true
	})
	return clean(found)
}

func firstTextByClassUnderID(root *html.Node, id, className string) string {
	container := nodeByID(root, id)
	if container == nil {
		return ""
	}
	return firstTextByClass(container, className)
}

func firstTextBySelector(root *html.Node, id, className string) string {
	container := nodeByID(root, id)
	if container == nil {
		return ""
	}
	return firstTextByClass(container, className)
}

func textsByClassUnderID(root *html.Node, id, className string, limit int) []string {
	container := nodeByID(root, id)
	if container == nil {
		return nil
	}
	out := []string{}
	walk(container, func(n *html.Node) bool {
		if len(out) >= limit {
			return false
		}
		if hasClass(n, className) {
			if text := clean(nodeText(n)); text != "" {
				out = append(out, text)
			}
		}
		return true
	})
	return out
}

func firstAttrByID(root *html.Node, id, name string) string {
	var found string
	walk(root, func(n *html.Node) bool {
		if attr(n, "id") == id {
			found = attr(n, name)
			return false
		}
		return true
	})
	return strings.TrimSpace(found)
}

func firstAttr(root *html.Node, tag, attrName, attrValue, want string) string {
	var found string
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == tag && strings.EqualFold(attr(n, attrName), attrValue) {
			found = attr(n, want)
			return false
		}
		return true
	})
	return strings.TrimSpace(found)
}

func firstProductImageAttr(root *html.Node, name string) string {
	for _, id := range []string{"imgTagWrapperId", "main-image-container"} {
		container := nodeByID(root, id)
		if container == nil {
			continue
		}
		if value := firstImageAttr(container, name); value != "" {
			return value
		}
	}
	return firstImageAttr(root, name)
}

func firstImageAttr(root *html.Node, name string) string {
	var found string
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			found = attr(n, name)
			return found == ""
		}
		return true
	})
	return strings.TrimSpace(found)
}

func nodeByID(root *html.Node, id string) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) bool {
		if attr(n, "id") == id {
			found = n
			return false
		}
		return true
	})
	return found
}

func walk(n *html.Node, fn func(*html.Node) bool) bool {
	if n == nil {
		return true
	}
	if !fn(n) {
		return false
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if !walk(child, fn) {
			return false
		}
	}
	return true
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, className string) bool {
	for _, part := range strings.Fields(attr(n, "class")) {
		if part == className {
			return true
		}
	}
	return false
}

func classExists(root *html.Node, className string) bool {
	found := false
	walk(root, func(n *html.Node) bool {
		if hasClass(n, className) {
			found = true
			return false
		}
		return true
	})
	return found
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(child *html.Node) bool {
		if child.Type == html.TextNode {
			b.WriteString(child.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}

func clean(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func dynamicImages(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var parsed map[string][]int
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, len(parsed))
	for img := range parsed {
		out = append(out, img)
	}
	return out
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func normalizePrice(raw string) (string, string) {
	raw = clean(raw)
	if raw == "" {
		return "", ""
	}
	currency := ""
	if strings.HasPrefix(raw, "$") {
		currency = "USD"
	}
	re := regexp.MustCompile(`[0-9][0-9,]*(\.[0-9]{2})?`)
	price := re.FindString(raw)
	price = strings.ReplaceAll(price, ",", "")
	return price, currency
}

func normalizeUSDPrice(raw string) (string, string) {
	re := regexp.MustCompile(`\$\s*([0-9][0-9,]*(?:\.[0-9]{2})?)`)
	match := re.FindStringSubmatch(raw)
	if len(match) != 2 {
		return "", ""
	}
	return strings.ReplaceAll(match[1], ",", ""), "USD"
}

func parseMoneyCents(value string) (int64, bool) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	cents := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		if len(fraction) != 2 {
			return 0, false
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	return dollars*100 + cents, true
}

func formatMoneyCents(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func cleanAvailability(raw string) string {
	raw = clean(raw)
	if idx := strings.Index(raw, " Delivering to "); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	if idx := strings.Index(raw, "{"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	return raw
}

func boolPtr(value bool) *bool {
	return &value
}

func asinFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(parsed.Path, "/")
	for i, part := range parts {
		if (part == "dp" || part == "gp") && i+1 < len(parts) {
			if part == "gp" && i+2 < len(parts) && parts[i+1] == "product" {
				return parts[i+2]
			}
			return parts[i+1]
		}
	}
	return ""
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
