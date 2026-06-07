// Example: Prompt caching with agent.WithCaching().
//
// Demonstrates system prompt caching: agent.WithCaching() attaches a cache
// breakpoint to the system prompt so the first call writes it to cache
// (CacheWriteTokens > 0) and subsequent calls read it back (CacheReadTokens > 0).
//
// For document caching — where the same file is re-used across multiple questions
// — see examples/prompt-caching-document.
//
// Caching requires Claude models on Bedrock. bedrock.Standard() resolves to
// Claude Sonnet 4.6, which requires at least 1,024 tokens before a cache checkpoint.
//
// Run:
//
//	go run ./prompt-caching
package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

// productCatalog is intentionally verbose to exceed the 1,024-token minimum
// required for Bedrock prompt caching on Claude Sonnet 4.6.
const productCatalog = `You are a knowledgeable customer support assistant for Acme Corp. Your role is to
help customers find the right products, understand our policies, and resolve any
issues they may have. Always be friendly, accurate, and concise.

PRODUCT CATALOG
===============

Industrial Widget (SKU: IW-001) — $49.99
  The Industrial Widget is our flagship heavy-duty solution for professional and
  industrial environments. Built from aircraft-grade aluminum with a corrosion-
  resistant coating, it withstands temperatures from -40°C to 120°C. Available in
  three sizes: Small (5cm), Medium (10cm), and Large (20cm). Each unit ships with
  a calibration certificate and a two-year limited warranty covering manufacturing
  defects. Compatible with all standard mounting brackets (Series 3 and above).
  Replacement parts are stocked for a minimum of 10 years post-purchase.

Consumer Gadget (SKU: CG-002) — $19.99
  The Consumer Gadget is designed for everyday home and office use. Lightweight
  at just 85g, it fits comfortably in a pocket or bag. The USB-C charging port
  delivers a full charge in under two hours, and the 1,200mAh battery provides
  up to 72 hours of standby time. Available in five colors: Midnight Black, Arctic
  White, Ocean Blue, Sunset Red, and Forest Green. Includes a one-year warranty
  against defects and a 30-day satisfaction guarantee.

Premium Device (SKU: PD-003) — $299.99
  The Premium Device is our flagship consumer product, engineered for demanding
  professionals who need reliability without compromise. The titanium housing
  provides exceptional durability while keeping weight under 120g. The 4,500mAh
  battery supports up to 96 hours of continuous use. Features include: dual-band
  Wi-Fi 6E, Bluetooth 5.3, NFC, and a dedicated hardware security module (HSM)
  for enterprise key storage. Comes with a five-year comprehensive warranty that
  covers accidental damage (two incidents per year), and next-business-day
  on-site support for customers in the continental US, Canada, and Western Europe.
  Premium customers receive a dedicated support line with a guaranteed four-hour
  response SLA. Free expedited shipping on all orders.

RETURN POLICY
=============
All products purchased directly from Acme Corp may be returned within 30 days of
delivery for a full refund to the original payment method. Items must be returned
in their original packaging with all included accessories and documentation. A
valid receipt or order confirmation is required for cash refunds. Store credit
will be issued for returns made between 15 and 30 days without a receipt.
Electronics and software have a 14-day return window. Software licenses that have
been activated or redeemed cannot be returned. Opened software is non-returnable
unless the product is defective. For defective items, contact support within 7
days of delivery; we will arrange a prepaid return label and ship a replacement
within 3 business days of receiving the defective unit.

SHIPPING POLICY
===============
Standard shipping (5–7 business days) is free on all orders over $50. Orders
under $50 incur a flat $4.99 shipping fee. Express shipping (2 business days) is
available for $12.99. Overnight shipping is available for $24.99 on orders placed
before 2pm Eastern Time on business days. International shipping is available to
over 60 countries; rates and delivery times vary by destination. Customers are
responsible for any import duties or customs fees.

SUPPORT POLICY
==============
Standard support is available Monday through Friday, 9am–6pm Eastern Time, via
chat, email, and phone. Average response time is under 4 hours during business
hours. Premium Device customers receive 24/7 priority support with a guaranteed
4-hour response SLA at all times, including weekends and holidays. On-site
support is available in the continental US, Canada, and major Western European
cities for Premium Device customers with an active warranty.

FREQUENTLY ASKED QUESTIONS
===========================
Q: Can I return a product I bought as a gift?
A: Yes. Gift returns are accepted within 30 days with a gift receipt (full
   refund to original payment method) or without a gift receipt (store credit
   at the current selling price).

Q: What happens if my product arrives damaged?
A: Report damaged-on-arrival items within 48 hours of delivery by contacting
   support@acmecorp.example. Include your order number and a photo of the damage.
   We will arrange a free replacement shipment, typically within 2 business days.

Q: How do I register my product for warranty coverage?
A: Register your product within 30 days of purchase at acmecorp.example/register.
   You will need your order number and the product serial number (printed on the
   bottom of the device or on the packaging).

Q: Are replacement parts available for the Industrial Widget?
A: Yes. Replacement parts are stocked for a minimum of 10 years from the original
   product release date. Acme Corp offers same-day dispatch on in-stock parts for
   orders placed before noon Eastern Time on business days.

Answer all customer questions accurately and concisely using the information
provided above. If a question falls outside the scope of this catalog, politely
direct the customer to contact support@acmecorp.example.`

func main() {
	godotenv.Load() //nolint

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text(productCatalog),
		nil,
		agent.WithCaching(),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Scenario 1: system prompt caching ─────────────────────────────────
	// agent.WithCaching() injects CachingEnabled on every call so the provider
	// caches the system prompt. First call writes, subsequent calls read.
	fmt.Println("── Call 1: system prompt written to cache ──")
	ctx1 := agent.Background()
	if err := a.InvokeStream(ctx1, "What is the price of Product C?", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println()

	fmt.Println("\n── Call 2: system prompt served from cache ──")
	ctx2 := agent.Background()
	if err := a.InvokeStream(ctx2, "Does the Industrial Widget come with a warranty?", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}
