// Example: LLM-powered travel page generator with checkpointing.
//
// Demonstrates a real-world content pipeline using multiple LLM agents,
// structured output, web research via Tavily search, RAG-based offer
// selection, and human-in-the-loop review via interrupt.
//
// Pipeline:
//
//	plan → research (web search) → hero_text → seo_text → select_offers → review (interrupt)
//
// Prerequisites:
//
//   - TAVILY_API_KEY: API key from https://app.tavily.com
//
// Run:
//
//	go run ./graph-travel-page

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"
	"github.com/joho/godotenv"
)

// ─── Types ───────────────────────────────────────────────────────────────────

type Page struct {
	Title       string `json:"title"       description:"SEO-optimized page title, max 60 chars"`
	URL         string `json:"url"         description:"URL slug like /family-trips-mallorca"`
	Description string `json:"description" description:"Meta description, max 155 chars"`
}

type Offer struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Destination string `json:"destination"`
	Price       string `json:"price"`
	Duration    string `json:"duration"`
}

type OfferSelection struct {
	OfferIDs []string `json:"offer_ids" description:"Up to 3 offer IDs most relevant to the page topic"`
}

type State struct {
	graph.GraphState
	Prompt         string  `json:"prompt"`
	Page           Page    `json:"page"`
	Research       string  `json:"research"`
	HeroText       string  `json:"hero_text"`
	SEOText        string  `json:"seo_text"`
	SelectedOffers []Offer `json:"selected_offers"`
	Approved       bool    `json:"approved"`
	Status         string  `json:"status"`
}

// ─── Offer catalog (mocked RAG data) ────────────────────────────────────────

var offerCatalog = []Offer{
	{ID: "MAL-001", Title: "Family Beach Resort Mallorca", Destination: "Mallorca", Price: "€1,299/person", Duration: "7 nights"},
	{ID: "MAL-002", Title: "Mallorca Adventure & Water Park", Destination: "Mallorca", Price: "€1,499/person", Duration: "10 nights"},
	{ID: "MAL-003", Title: "Quiet Finca Retreat Mallorca", Destination: "Mallorca", Price: "€899/person", Duration: "5 nights"},
	{ID: "MAL-004", Title: "All-Inclusive Palma Bay", Destination: "Mallorca", Price: "€1,699/person", Duration: "7 nights"},
	{ID: "MAL-005", Title: "Mallorca Cycling & Beach Combo", Destination: "Mallorca", Price: "€1,149/person", Duration: "7 nights"},
	{ID: "GRE-001", Title: "Greek Island Hopping", Destination: "Greece", Price: "€1,899/person", Duration: "14 nights"},
	{ID: "GRE-002", Title: "Crete Family All-Inclusive", Destination: "Greece", Price: "€1,399/person", Duration: "7 nights"},
	{ID: "GRE-003", Title: "Santorini & Athens Explorer", Destination: "Greece", Price: "€2,299/person", Duration: "10 nights"},
	{ID: "ITA-001", Title: "Amalfi Coast Family Tour", Destination: "Italy", Price: "€2,199/person", Duration: "10 nights"},
	{ID: "ITA-002", Title: "Sardinia Beach & Culture", Destination: "Italy", Price: "€1,599/person", Duration: "7 nights"},
	{ID: "ITA-003", Title: "Sicily Family Adventure", Destination: "Italy", Price: "€1,349/person", Duration: "8 nights"},
	{ID: "ESP-001", Title: "Costa Brava Family Resort", Destination: "Spain", Price: "€999/person", Duration: "7 nights"},
	{ID: "ESP-002", Title: "Barcelona & Beach Combo", Destination: "Spain", Price: "€1,449/person", Duration: "5 nights"},
	{ID: "TUR-001", Title: "Antalya All-Inclusive Family", Destination: "Turkey", Price: "€849/person", Duration: "7 nights"},
	{ID: "TUR-002", Title: "Bodrum Luxury Family Resort", Destination: "Turkey", Price: "€1,799/person", Duration: "10 nights"},
	{ID: "CRO-001", Title: "Dubrovnik & Islands Family", Destination: "Croatia", Price: "€1,699/person", Duration: "7 nights"},
	{ID: "CRO-002", Title: "Split Riviera Beach Holiday", Destination: "Croatia", Price: "€1,199/person", Duration: "7 nights"},
	{ID: "POR-001", Title: "Algarve Family Sun & Fun", Destination: "Portugal", Price: "€1,099/person", Duration: "7 nights"},
	{ID: "POR-002", Title: "Lisbon & Algarve Explorer", Destination: "Portugal", Price: "€1,549/person", Duration: "10 nights"},
	{ID: "EGY-001", Title: "Red Sea Family Snorkeling", Destination: "Egypt", Price: "€799/person", Duration: "7 nights"},
	{ID: "EGY-002", Title: "Nile Cruise & Beach Combo", Destination: "Egypt", Price: "€1,899/person", Duration: "12 nights"},
}

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())
	cp := memory.New()

	// ─── Agents ──────────────────────────────────────────────────────────

	planner, err := agent.Worker(provider, prompt.Text(
		"You are a travel content planner. Given a page request, generate an SEO-optimized "+
			"page title (max 60 chars), a URL slug (lowercase, hyphens, starts with /), "+
			"and a meta description (max 155 chars). Return structured JSON only.",
	), nil, auto.WithLogging(), agent.WithName("planner"))
	if err != nil {
		log.Fatal(err)
	}

	researcher, err := agent.Default(provider, prompt.Text(
		"You are a travel researcher. Given a destination and topic, search the web for "+
			"current, practical information. Find family-friendly activities, best time to visit, "+
			"local highlights, and any recent travel tips. Summarize your findings in 4-6 sentences. "+
			"Always search before answering.",
	), []tool.Tool{
		tavily.New(os.Getenv("TAVILY_API_KEY")),
		webfetch.New(),
	},
		agent.WithMaxIterations(10),
		auto.WithLogging(),
		agent.WithName("researcher"),
	)
	if err != nil {
		log.Fatal(err)
	}

	heroWriter, err := agent.Worker(provider, prompt.Text(
		"You are a travel copywriter. Write a compelling hero section text (2-3 sentences) "+
			"for a travel landing page. It should be emotional, inspiring, and make families "+
			"want to book immediately. Use the research provided. Return only the hero text.",
	), nil, auto.WithLogging(), agent.WithName("hero-writer"))
	if err != nil {
		log.Fatal(err)
	}

	seoWriter, err := agent.Worker(provider, prompt.Text(
		"You are an SEO content writer. Write a 150-200 word informational text section "+
			"for the bottom of a travel landing page. It should be keyword-rich, informative, "+
			"and help with search rankings. Use the research provided. Return only the text.",
	), nil, auto.WithLogging(), agent.WithName("seo-writer"))
	if err != nil {
		log.Fatal(err)
	}

	// RAG-based offer selection.
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
	offerStore := rag.NewMemoryStore()
	offerDocs := make([]string, len(offerCatalog))
	for i, o := range offerCatalog {
		offerDocs[i] = fmt.Sprintf("[%s] %s — %s, %s, %s", o.ID, o.Title, o.Destination, o.Price, o.Duration)
	}
	if err := rag.Ingest(agent.Background(), offerStore, embedder, offerDocs, nil, rag.WithConcurrency(10)); err != nil {
		log.Fatalf("ingest offers: %v", err)
	}

	offerSelector, err := agent.RAGAgent(provider, prompt.Text(
		"You are a travel offer curator. Given a page topic, select up to 3 offers "+
			"from the retrieved context that are most relevant. Return only the offer IDs as structured JSON. "+
			"Each offer starts with [ID] — use those IDs.",
	), rag.NewRetriever(embedder, offerStore, rag.WithMaxResults(5)), nil,
		auto.WithLogging(), agent.WithName("offer-selector"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ─── Node functions ──────────────────────────────────────────────────

	planNode := func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		page, err := agent.InvokeStructured[Page](c, planner, s.Prompt)
		if err != nil {
			return s, err
		}
		s.Page = page
		s.AddUsage(c.Usage())
		return s, nil
	}

	researchNode := func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Research this topic for a travel landing page: %s\nPage title: %s", s.Prompt, s.Page.Title)
		research, err := researcher.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.Research = research
		s.AddUsage(c.Usage())
		return s, nil
	}

	heroNode := func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Page: %s\nResearch: %s", s.Page.Title, s.Research)
		hero, err := heroWriter.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.HeroText = strings.TrimSpace(hero)
		s.AddUsage(c.Usage())
		return s, nil
	}

	seoNode := func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Page: %s\nResearch: %s\nKeywords: %s", s.Page.Title, s.Research, s.Page.Description)
		seo, err := seoWriter.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.SEOText = strings.TrimSpace(seo)
		s.AddUsage(c.Usage())
		return s, nil
	}

	selectOffersNode := func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Select offers for: %s (page: %s)", s.Prompt, s.Page.Title)
		selection, err := agent.InvokeStructured[OfferSelection](c, offerSelector, input)
		if err != nil {
			return s, err
		}
		idSet := make(map[string]bool)
		for _, id := range selection.OfferIDs {
			idSet[id] = true
		}
		for _, offer := range offerCatalog {
			if idSet[offer.ID] {
				s.SelectedOffers = append(s.SelectedOffers, offer)
			}
		}
		s.AddUsage(c.Usage())
		return s, nil
	}

	reviewNode := func(_ context.Context, s State) (State, error) {
		if s.Approved {
			s.Status = "published"
		} else {
			s.Status = "rejected"
		}
		return s, nil
	}

	// ─── Graph wiring ────────────────────────────────────────────────────

	g, err := graph.New[State](
		graph.WithCheckpointer(cp),
		graph.WithMaxIterations(20),
		auto.WithGraphLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	g.SetEntry("plan")
	g.AddNode("plan", planNode)
	g.AddEdge("plan", "research")

	g.AddNode("research", researchNode)
	g.InterruptAfter("research")
	g.AddFork("research", []string{"hero_text", "seo_text", "select_offers"})

	g.AddNode("hero_text", heroNode)
	g.AddNode("seo_text", seoNode)
	g.AddNode("select_offers", selectOffersNode)

	g.AddNode("review", reviewNode)
	g.AddJoin("review", []string{"hero_text", "seo_text", "select_offers"})
	g.InterruptBefore("review")

	// ─── Run ─────────────────────────────────────────────────────────────

	ctx := context.Background()
	threadID := "travel-page-1"
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Print("🌍 What page should I create? ")
	scanner.Scan()
	topic := strings.TrimSpace(scanner.Text())
	if topic == "" {
		topic = "create a page for family trips to mallorca"
	}

	fmt.Printf("\n🚀 Generating travel page for: %s\n\n", topic)

	_, err = g.Run(ctx, State{Prompt: topic}, graph.WithThreadID(threadID))

	var intErr *graph.GraphInterruptError
	if !errors.As(err, &intErr) {
		log.Fatalf("expected interrupt after research, got: %v", err)
	}

	// ─── Interactive research review ─────────────────────────────────────

	for {
		var researchState State
		stateJSON, _ := json.Marshal(intErr.Result.Checkpoint.State)
		json.Unmarshal(stateJSON, &researchState)

		fmt.Println("═══════════════════════════════════════════════════════════")
		fmt.Println("🔍 RESEARCH COMPLETE — REVIEW BEFORE CONTENT GENERATION")
		fmt.Println("═══════════════════════════════════════════════════════════")
		fmt.Printf("\n📌 Page: %s (%s)\n", researchState.Page.Title, researchState.Page.URL)
		fmt.Printf("\n── Research ──\n%s\n", researchState.Research)
		fmt.Println()
		fmt.Print("Type 'yes' to continue, or provide feedback to re-research: ")
		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())

		if strings.ToLower(input) == "yes" {
			break
		}
		if input == "" {
			continue
		}

		// Rewind to after "plan" and re-research with feedback.
		fmt.Println("\n🔄 Re-researching with your feedback...")
		if err := g.RewindTo(ctx, threadID, 1); err != nil {
			log.Fatal(err)
		}

		updatedState := researchState
		updatedState.Prompt = fmt.Sprintf("%s\n\nAdditional guidance: %s", researchState.Prompt, input)
		updatedState.Research = ""
		_, err = g.Resume(ctx, threadID, &updatedState)
		if !errors.As(err, &intErr) {
			log.Fatalf("expected interrupt after research, got: %v", err)
		}
	}

	// ─── Generate content ────────────────────────────────────────────────

	fmt.Println("\n📝 Generating content...")
	_, err = g.Resume(ctx, threadID, nil)
	if !errors.As(err, &intErr) {
		log.Fatalf("expected interrupt before review, got: %v", err)
	}

	// ─── Page review ─────────────────────────────────────────────────────

	checkpoint := intErr.Result.Checkpoint
	var pageState State
	pageJSON, _ := json.Marshal(checkpoint.State)
	json.Unmarshal(pageJSON, &pageState)

	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("📄 PAGE READY FOR REVIEW")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("\n📌 Title: %s\n", pageState.Page.Title)
	fmt.Printf("🔗 URL: %s\n", pageState.Page.URL)
	fmt.Printf("📝 Description: %s\n", pageState.Page.Description)
	fmt.Printf("\n── Hero Section ──\n%s\n", pageState.HeroText)
	fmt.Printf("\n── SEO Text ──\n%s\n", pageState.SEOText)
	fmt.Printf("\n── Selected Offers (%d) ──\n", len(pageState.SelectedOffers))
	for _, offer := range pageState.SelectedOffers {
		fmt.Printf("  • %s — %s (%s)\n", offer.Title, offer.Price, offer.Duration)
	}
	fmt.Println()

	// ─── Publish ─────────────────────────────────────────────────────────

	fmt.Print("Approve and publish? (y/n): ")
	scanner.Scan()
	approved := strings.TrimSpace(strings.ToLower(scanner.Text())) == "y"

	result, err := g.Resume(ctx, threadID, &State{Approved: approved})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n🎉 Status: %s\n", result.State.Status)
	fmt.Printf("📊 Total tokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
