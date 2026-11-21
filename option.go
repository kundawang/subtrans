package subtrans

import (
	"github.com/heartleo/subtrans/internal/glossary"
	"github.com/heartleo/subtrans/internal/subtitle"
)

// GlossaryEntry is one source→target term pair. Use with [WithGlossary].
type GlossaryEntry = glossary.Entry

// Option configures translation behavior.
// Use the With* functions to create options.
type Option interface {
	apply(*options)
}

// options holds all translation-level configuration with defaults.
type options struct {
	instructions          string
	prompt                string
	maxBatchSize          int
	batchSplitPunctuation string
	includeOriginal       bool
	stripPunctuation      bool
	format                subtitle.Format
	glossary              []GlossaryEntry
	glossaryCaseFold      bool
}

// formatOption selects the subtitle codec.
type formatOption subtitle.Format

func (o formatOption) apply(opts *options) { opts.format = subtitle.Format(o) }

// WithFormat selects the subtitle codec (default: [subtitle.FormatSRT]).
// Use [subtitle.DetectByExt] to infer the value from a filename.
func WithFormat(f subtitle.Format) Option { return formatOption(f) }

// instructionsOption sets custom system instructions.
type instructionsOption string

func (o instructionsOption) apply(opts *options) { opts.instructions = string(o) }

// WithInstructions sets custom system instructions appended to the default prompt.
func WithInstructions(s string) Option { return instructionsOption(s) }

// promptOption sets a custom user prompt prefix.
type promptOption string

func (o promptOption) apply(opts *options) { opts.prompt = string(o) }

// WithPrompt sets a custom user prompt prefix.
func WithPrompt(s string) Option { return promptOption(s) }

// maxBatchSizeOption sets the number of subtitle lines per API call.
type maxBatchSizeOption int

func (o maxBatchSizeOption) apply(opts *options) { opts.maxBatchSize = int(o) }

// WithMaxBatchSize sets the number of subtitle lines per API call (default: 30).
func WithMaxBatchSize(n int) Option { return maxBatchSizeOption(n) }

// batchSplitPunctuationOption sets the punctuation characters used as batch split points.
type batchSplitPunctuationOption string

func (o batchSplitPunctuationOption) apply(opts *options) { opts.batchSplitPunctuation = string(o) }

// WithBatchSplitPunctuation sets punctuation characters for batch splitting (default: ".").
func WithBatchSplitPunctuation(s string) Option { return batchSplitPunctuationOption(s) }

// includeOriginalOption includes original text alongside translation in output.
type includeOriginalOption bool

func (o includeOriginalOption) apply(opts *options) { opts.includeOriginal = bool(o) }

// WithIncludeOriginal includes original text alongside translation in the output SRT.
func WithIncludeOriginal(b bool) Option { return includeOriginalOption(b) }

// stripPunctuationOption controls stripping of trailing punctuation.
type stripPunctuationOption bool

func (o stripPunctuationOption) apply(opts *options) { opts.stripPunctuation = bool(o) }

// WithStripPunctuation controls stripping trailing periods and commas from
// translated text (default: true).
func WithStripPunctuation(b bool) Option { return stripPunctuationOption(b) }

// glossaryOption supplies glossary entries.
type glossaryOption []GlossaryEntry

func (o glossaryOption) apply(opts *options) { opts.glossary = append([]GlossaryEntry(nil), o...) }

// WithGlossary adds source→target term pairs that must survive translation
// unchanged. Terms are replaced with per-batch placeholders before each LLM
// call and restored afterwards, so character names are never machine
// translated and are spelled identically everywhere they occur.
//
// Matching is literal, non-overlapping, and longest-term-first, and is
// case-sensitive unless [WithGlossaryCaseInsensitive] is also used.
//
// Invalid glossaries (empty source/target, duplicate source terms with
// different targets, or terms containing newlines or reserved placeholder
// syntax) make [Translator.Translate] return an error wrapping
// [ErrInvalidGlossary] before any API call is made.
func WithGlossary(entries ...GlossaryEntry) Option { return glossaryOption(entries) }

// glossaryCaseFoldOption toggles case-insensitive term matching.
type glossaryCaseFoldOption bool

func (o glossaryCaseFoldOption) apply(opts *options) { opts.glossaryCaseFold = bool(o) }

// WithGlossaryCaseInsensitive enables Unicode case-insensitive glossary
// matching (default: false). Terms that differ only by case are treated as
// duplicates by [WithGlossary] when this is enabled.
func WithGlossaryCaseInsensitive(b bool) Option { return glossaryCaseFoldOption(b) }
