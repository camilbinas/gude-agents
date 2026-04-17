// This example demonstrates InvokeStructured — forcing the LLM to return
// typed, schema-validated JSON instead of free-form text.
//
// Run:
//
//	go run ./structured-output

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

// Recipe is the structured response we want from the LLM.
// Struct tags control the generated JSON Schema — the LLM sees field
// descriptions, required markers, and enum constraints.
type Recipe struct {
	Name        string   `json:"name"         description:"Name of the recipe"                   required:"true"`
	Cuisine     string   `json:"cuisine"      description:"Cuisine type"                         enum:"italian,french,japanese,mexican,indian,american,other" required:"true"`
	PrepMinutes int      `json:"prep_minutes" description:"Preparation time in minutes"          required:"true"`
	Difficulty  string   `json:"difficulty"   description:"Difficulty level"                     enum:"easy,medium,hard" required:"true"`
	Ingredients []string `json:"ingredients"  description:"List of ingredients with quantities"  required:"true"`
	Steps       []string `json:"steps"        description:"Ordered list of preparation steps"    required:"true"`
	Tips        []string `json:"tips"         description:"Optional cooking tips"`
}

func main() {
	ctx := context.Background()

	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a professional chef. Provide detailed, accurate recipes."),
		nil, // InvokeStructured handles tool setup internally — no tools needed here
	)
	if err != nil {
		log.Fatal(err)
	}

	recipe, usage, err := agent.InvokeStructured[Recipe](
		ctx, a, "Give me a classic recipe for spaghetti carbonara.",
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Recipe:     %s\n", recipe.Name)
	fmt.Printf("Cuisine:    %s\n", recipe.Cuisine)
	fmt.Printf("Prep time:  %d minutes\n", recipe.PrepMinutes)
	fmt.Printf("Difficulty: %s\n", recipe.Difficulty)
	fmt.Printf("\nIngredients:\n")
	for _, ing := range recipe.Ingredients {
		fmt.Printf("  - %s\n", ing)
	}
	fmt.Printf("\nSteps:\n")
	for i, step := range recipe.Steps {
		fmt.Printf("  %d. %s\n", i+1, step)
	}
	if len(recipe.Tips) > 0 {
		fmt.Printf("\nTips:\n")
		for _, tip := range recipe.Tips {
			fmt.Printf("  * %s\n", tip)
		}
	}
	fmt.Printf("\n%s\n", strings.Repeat("-", 40))
	fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}
