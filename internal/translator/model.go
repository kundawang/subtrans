// Package translator contains the core subtitle translation types and logic.
package translator

import (
	"time"

	"github.com/heartleo/subtrans/internal/glossary"
)

// Line is a single subtitle entry.
type Line struct {
	Number      int
	Start       time.Duration
	End         time.Duration
	Text        string
	Translation string // filled in-place by the translator after parsing
	// Metadata is codec-private per-line state captured during Parse and
	// consumed during Format (e.g. ASS Dialogue non-text fields). The
	// translator pipeline does not inspect or mutate this field.
	Metadata any

	// glossaryText is Text with glossary terms replaced by placeholders; it is
	// what prompts send to the LLM. Empty means Text is sent unchanged.
	glossaryText string
	// protected maps glossaryText placeholders back to target terms. It is
	// cleared once the line's translation has been successfully restored.
	protected *glossary.Protected
	// glossaryDone marks a line whose translation already went through
	// Restore, so a retry prompt never overwrites it with an unrestored value.
	glossaryDone bool
}

// promptText returns the text that should be sent to the LLM.
func (l *Line) promptText() string {
	if l.glossaryText != "" {
		return l.glossaryText
	}
	return l.Text
}

// settled reports whether the line's translation must not be overwritten by a
// later response. Only glossary lines are settled; default translation keeps
// its original fill-in-place behaviour.
func (l *Line) settled() bool {
	return l.glossaryDone && l.Translation != ""
}

// Batch is a group of lines sent in a single API call.
type Batch struct {
	Number      int
	Lines       []*Line
	RawResponse string // raw AI response text; kept for debugging
	Errors      []error
}

// Options controls translation behaviour.
type Options struct {
	TargetLanguage           string
	Instructions             string // system instructions text (resolved to string before calling translator)
	Prompt                   string // user prompt prefix
	MaxBatchSize             int    // default: 30
	BatchSplitPunctuation    string // punctuation characters used as batch split points; default: "."
	Temperature              float64
	MaxRetries               int  // API-level retries on 429/5xx only; default: 3
	IncludeOriginal          bool // write original text alongside translation in output SRT
	StripTrailingPunctuation bool // strip trailing periods and commas from each subtitle line; default: true
	// Glossary, when non-nil and non-empty, protects source terms from the
	// translator and restores their fixed target translations afterwards.
	Glossary *glossary.Table
}

// Default translation settings.
const (
	DefaultMaxBatchSize          = 30
	DefaultBatchSplitPunctuation = "."
)

// DefaultOptions returns an Options with sensible defaults.
func DefaultOptions() Options {
	return Options{
		TargetLanguage:           "zh",
		MaxBatchSize:             DefaultMaxBatchSize,
		BatchSplitPunctuation:    DefaultBatchSplitPunctuation,
		MaxRetries:               3,
		StripTrailingPunctuation: true,
	}
}

// Message is a single message in a chat conversation.
// Defined in subtrans (consumer side) rather than in the openai package
// to avoid an import cycle.
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}
