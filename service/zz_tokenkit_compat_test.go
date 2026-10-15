package service

import "github.com/QuantumNous/new-api/tokenkit"

// Existing host regression cases exercise the extracted tokenkit through their original argument order.
func CountTextToken(text, model string) int { return tokenkit.Count(model, text) }

func imageTokenFallback(model, detail string) int {
	return tokenkit.ImageTokens(model, tokenkit.Image{Detail: detail})
}
