// Example: Prompt caching with WithCaching() and CacheableBlock.
//
// Demonstrates both approaches to prompt caching in gude-agents:
//
//  1. System prompt caching (WithCaching): pass WithCaching() at provider
//     construction time and use the agent as normal. The provider automatically
//     attaches a cache breakpoint to the system prompt. The first call writes it
//     to cache (CacheWriteTokens > 0); repeat calls read it back (CacheReadTokens > 0).
//
//  2. Message-level cache breakpoints (CacheableBlock): wrap a ContentBlock in
//     agent.CacheableBlock when building ConverseParams directly. Useful for large
//     reference documents or few-shot prompts that span multiple provider calls.
//     Cache token counts appear in the ProviderResponse.Usage returned by
//     a.CallProvider().
//
// Caching is supported for Claude models on Bedrock. bedrock.Standard() resolves
// to Claude Sonnet, which supports prompt caching via the Bedrock Converse API.
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

// productCatalog simulates a large, stable system prompt worth caching.
// On the first call, this is written to the provider's cache (CacheWriteTokens > 0).
// On repeat calls with the same system prompt, it is served from cache (CacheReadTokens > 0).
//
// NOTE: Bedrock prompt caching requires a minimum of 1,024 tokens. This prompt
// is intentionally verbose to exceed that threshold and trigger caching.
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
  against defects and a 30-day satisfaction guarantee. Compatible with iOS 14+
  and Android 10+. Accessories (cases, charging cables, screen protectors) sold
  separately in the Acme accessory catalog.

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
responsible for any import duties or customs fees. Acme Corp is not liable for
delays caused by customs processing.

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

Q: Does the Consumer Gadget work internationally?
A: The Consumer Gadget ships with a universal USB-C charging adapter rated
   100–240V, 50/60Hz, making it compatible with electrical outlets worldwide.
   The device itself supports dual-band Wi-Fi (2.4 GHz and 5 GHz) and LTE
   bands 1/2/3/4/5/7/8/12/17/20/28/38/40/41, covering most global carriers.
   Check with your carrier for specific band compatibility in your region.

Q: Is my purchase covered if I lose the product?
A: Standard and Premium Device warranties cover manufacturing defects and
   accidental damage; they do not cover loss or theft. We recommend purchasing
   a third-party insurance policy or checking whether your homeowners or
   renters insurance covers personal electronics for loss protection.

Q: How do I register my product for warranty coverage?
A: Register your product within 30 days of purchase at
   acmecorp.example/register. You will need your order number and the product
   serial number (printed on the bottom of the device or on the packaging).
   Registration is optional for standard warranty coverage but required to
   activate the Premium Device 5-year warranty and next-day support benefits.

Q: Are replacement parts available for the Industrial Widget?
A: Yes. Replacement parts are stocked for a minimum of 10 years from the
   original product release date. Order parts directly from our website or
   contact your regional distributor. Acme Corp offers same-day dispatch on
   in-stock parts for orders placed before noon Eastern Time on business days.

Answer all customer questions accurately and concisely using the information
provided above. If a question falls outside the scope of this catalog, politely
direct the customer to contact support@acmecorp.example.`

func main() {
	godotenv.Load() //nolint

	// ── Scenario 1: system prompt caching via WithCaching() ────────────────
	//
	// WithCaching() attaches a cache breakpoint to the system prompt.
	// Use the agent exactly as normal — caching is transparent.
	// bedrock.Standard() resolves to Claude Sonnet, which supports caching.
	provider := bedrock.Must(bedrock.Standard(bedrock.WithCaching()))

	a, err := agent.Default(
		provider,
		prompt.Text(productCatalog),
		nil,
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background()

	// Call 1: the system prompt is written to cache (CacheWriteTokens > 0).
	fmt.Println("── Call 1: first call — system prompt written to cache ──")
	for ev := range a.InvokeEventStream(ctx, "What is the price of Product C?") {
		switch ev.Type {
		case agent.EventTextChunk:
			fmt.Print(ev.TextChunk)
		case agent.EventInvokeEnd:
			fmt.Println()
			if ev.Err != nil {
				log.Fatal(ev.Err)
			}
			printUsage(ev.Usage)
		}
	}

	// Call 2: same system prompt — served from cache (CacheReadTokens > 0).
	fmt.Println("\n── Call 2: repeat call — system prompt served from cache ──")
	for ev := range a.InvokeEventStream(ctx, "Does Product A come with a warranty?") {
		switch ev.Type {
		case agent.EventTextChunk:
			fmt.Print(ev.TextChunk)
		case agent.EventInvokeEnd:
			fmt.Println()
			if ev.Err != nil {
				log.Fatal(ev.Err)
			}
			printUsage(ev.Usage)
		}
	}

	// ── Scenario 2: message-level cache breakpoints via CacheableBlock ──────
	//
	// agent.CacheableBlock wraps any ContentBlock and marks that position as a
	// cache breakpoint. Use a.CallProvider() when you need to build
	// ConverseParams directly and inspect per-call cache token counts.
	//
	// A typical use case: a large reference document prepended to the user
	// message that stays unchanged across multiple turns.
	fmt.Println("\n── Scenario 2: CacheableBlock for message-level caching ──")

	refDoc := agent.CacheableBlock{
		Inner: agent.TextBlock{
			Text: `ACME CORP RETURN & REFUND POLICY — Full Text (2024 Edition)

1. STANDARD RETURNS
   All products purchased directly from Acme Corp may be returned within 30 days
   of the delivery date for a full refund to the original payment method. Items
   must be in their original, unopened packaging with all included accessories,
   manuals, and warranty cards present. A valid receipt or order confirmation email
   is required to process a cash or card refund.

2. RETURNS WITHOUT RECEIPT
   Store credit equal to the current selling price will be issued for returns made
   within 15 to 30 days of purchase when no receipt is available. Returns requested
   more than 30 days after purchase are not accepted, except in cases of
   manufacturing defects covered under the product warranty.

3. ELECTRONICS AND SOFTWARE
   Electronics carry a 14-day return window from the delivery date. Software
   products—including digital downloads and physical media—are non-returnable
   once the license key has been activated or the seal broken, regardless of
   whether the product was used. Defective software must be reported within 7 days
   of delivery; a replacement will be issued at no charge after verification.

4. DEFECTIVE PRODUCTS
   If a product is found to be defective on arrival or within the warranty period,
   contact our support team at support@acmecorp.example within 7 days. We will
   provide a prepaid return shipping label and dispatch a replacement unit within
   3 business days of receiving and verifying the defective item. Expedited
   replacement (next business day) is available for Premium Device customers.

5. INTERNATIONAL RETURNS
   Customers outside the continental United States must ship returns at their own
   expense. Acme Corp is not responsible for items lost or damaged in transit.
   Import duties and customs fees paid on the original order are non-refundable.
   Please declare the package as a "return of goods" to avoid additional customs
   charges on the return shipment.

6. EXCHANGES
   Direct exchanges are available for the same product in a different size or color,
   subject to availability, within 30 days of purchase. For exchanges of a different
   product, please return the original item for a refund and place a new order.

7. GIFT RETURNS
   Items received as gifts may be returned for store credit with a gift receipt.
   Without a gift receipt, store credit will be issued for the current selling price.`,
		},
	}

	params := agent.ConverseParams{
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					refDoc, // cached reference document
					agent.TextBlock{Text: "Can I return opened software?"},
				},
			},
		},
	}

	// CallProvider applies the agent's retry and timeout settings.
	resp, err := a.CallProvider(ctx, params, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Text)
	printUsage(resp.Usage)
}

// printUsage prints input/output and cache token counts.
func printUsage(u agent.TokenUsage) {
	fmt.Printf(
		"  tokens: input=%d output=%d  cache_write=%d cache_read=%d\n",
		u.InputTokens, u.OutputTokens, u.CacheWriteTokens, u.CacheReadTokens,
	)
}
