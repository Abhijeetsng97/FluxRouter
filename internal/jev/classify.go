// Package jev mirrors old/src/classify.ts: the System One client for routing
// classification, plus buildJevState (head+tail excerpts) and response
// parsing. Error semantics are contract-relevant: 408/504 and aborts map to
// JevTimeoutError; everything else to JevUnavailableError; both collapse to
// classification==nil in the router, which routes via jev_*_fallback.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abhijeet/fluxrouter/internal/jsstr"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// Options mirrors classify.ts JevClientOptions.
type Options struct {
	BaseURL  string
	Model    string
	TimeoutMs int64
	APIKey   string
	// HTTPClient lets tests inject a stub server; nil uses a shared default.
	HTTPClient *http.Client
}

// RouteQuestions mirrors classify.ts ROUTE_QUESTIONS — byte-identical JSON
// shape and strings; the classifier pins behavior to this exact payload.
var RouteQuestions = map[string]any{
	"category": map[string]any{
		"type": "choice",
		"instructions": "Classify the primary intent of the LAST user message in this conversation excerpt. " +
			"Pick the single best category.",
		"criteria": map[string]string{
			"math":              "Maths: equations, arithmetic, competition-style problems, probability",
			"code_debug":        "Diagnosing a bug, error, test failure, stack trace, or wrong behavior",
			"code_implement":    "Write new code / implement a feature or function from scratch",
			"code_explain":      "Explain how code works, walk through code, teach a concept",
			"refactor":          "Restructure, rename, or improve existing code without changing behavior",
			"greeting_chitchat": "Greetings, small talk, pleasantries, thanks, one-word replies",
			"summarize":         "Condense a document, thread, diff, or long text into a summary",
			"tool_planning":     "Deciding how to use tools, planning multi-step agent work",
			"creative_writing":  "Prose, stories, marketing copy, naming, brainstorming flavor",
			"other":             "None of the above fit cleanly",
		},
	},
	"complexity": map[string]any{
		"type":         "score",
		"instructions": "How hard is this for a state-of-the-art LLM to complete correctly? Rate the task, not the message length.",
		"criteria": []string{
			"Routine: simple lookup, greeting, small edit, anything a small cheap model reliably does",
			"Judgement: multi-step reasoning, moderate math/coding, needs care but is standard",
			"Hard: deep reasoning, large-scope engineering, subtle debugging, competition math",
		},
	},
	"is_trivial": map[string]any{
		"type": "noul",
		"instructions": "Is this request trivially answerable — a greeting, thanks, filler, or something needing essentially no thought? Answer true/false.",
	},
}

// TimeoutError mirrors JevTimeoutError.
type TimeoutError struct{ Ms int64 }

func (e *TimeoutError) Error() string { return fmt.Sprintf("Jev classified timed out after %dms", e.Ms) }

// UnavailableError mirrors JevUnavailableError.
type UnavailableError struct{ Msg string }

func (e *UnavailableError) Error() string { return e.Msg }

// Message mirrors the router's chat message input.
type Message struct {
	Role    string
	Content string
}

// BuildJevState mirrors classify.ts buildJevState: head+tail excerpts with
// [LAST]/[HIST-n] prefixes and "…[truncated]…" markers. JS slice semantics
// (UTF-16 units, negative indices) are replicated via jsstr.
func BuildJevState(messages []Message, maxCharsEach int) string {
	if maxCharsEach <= 0 {
		maxCharsEach = 2000
	}
	var parts []string
	lastIdx := len(messages) - 1
	for i, m := range messages {
		isLast := i == lastIdx
		content := m.Content
		var slice string
		if jsstr.JsLen(content) <= maxCharsEach {
			slice = content
		} else if isLast {
			// head+tail of the final (most important) message
			half := maxCharsEach / 2 // floor, matches Math.floor(maxCharsEach / 2)
			slice = jsstr.SliceUTF16(content, 0, half) +
				" …[truncated]… " +
				jsstr.SliceToEndUTF16(content, jsstr.JsLen(content)-half) // JS: content.slice(-half)
		} else {
			slice = jsstr.SliceUTF16(content, 0, maxCharsEach/4) + " …[truncated]…"
		}
		label := "[LAST]"
		if !isLast {
			label = fmt.Sprintf("[HIST-%d]", len(messages)-1-i)
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s", label, m.Role, slice))
	}
	return strings.Join(parts, "\n")
}

// Classify mirrors classify.ts classify: one System One call per decision.
func Classify(ctx context.Context, state string, opts Options) (types.ClassificationResult, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: time.Duration(opts.TimeoutMs) * time.Millisecond}
	}
	body := map[string]any{
		"model":     opts.Model,
		"state":     state,
		"questions": RouteQuestions,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return types.ClassificationResult{}, &UnavailableError{Msg: "Jev request encode failed: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(opts.TimeoutMs)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return types.ClassificationResult{}, &UnavailableError{Msg: "Jev request failed: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		// Mirror TS: an aborted/expired context is a timeout; anything else
		// is unavailability.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return types.ClassificationResult{}, &TimeoutError{Ms: opts.TimeoutMs}
		}
		return types.ClassificationResult{}, &UnavailableError{Msg: "Jev request failed: " + err.Error()}
	}
	defer res.Body.Close()

	if res.StatusCode == 408 || res.StatusCode == 504 {
		return types.ClassificationResult{}, &TimeoutError{Ms: opts.TimeoutMs}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return types.ClassificationResult{}, &UnavailableError{Msg: fmt.Sprintf("Jev HTTP %d: %s", res.StatusCode, string(text))}
	}
	var parsed types.JevResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return types.ClassificationResult{}, &UnavailableError{Msg: "Jev request failed: " + err.Error()}
	}
	return ParseJevResponse(parsed)
}

// ParseJevResponse mirrors classify.ts parseJevResponse: missing answers -->
// UnavailableError ("missing" in text so message-matching tests pass).
func ParseJevResponse(jsonResp types.JevResponse) (types.ClassificationResult, error) {
	cat, ok1 := jsonResp.Answers["category"]
	comp, ok2 := jsonResp.Answers["complexity"]
	triv, ok3 := jsonResp.Answers["is_trivial"]
	if !ok1 || !ok2 || !ok3 {
		return types.ClassificationResult{}, &UnavailableError{Msg: "Jev response missing category/complexity/is_trivial answers"}
	}
	category := "other"
	if cat.Choice != "" {
		category = cat.Choice
	}
	complexity := 0.0
	if comp.Score != nil {
		complexity = *comp.Score
	}
	trivial := 0.0
	if triv.Noul != nil {
		trivial = *triv.Noul
	}
	res := types.ClassificationResult{
		Category:             category,
		Complexity:           complexity,
		ComplexityConfidence: derefOrZero(comp.Confidence),
		CategoryConfidence:   derefOrZero(cat.Confidence),
		TrivialNoul:          trivial,
		JevModel:             jsonResp.Model,
		JevUsage:             jsonResp.Usage,
	}
	return res, nil
}

func derefOrZero(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}