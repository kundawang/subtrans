// Package glossary maps source terms to fixed target-language translations and
// protects those terms from the machine translator by replacing them with
// opaque, per-session placeholders.
//
// # Matching rules
//
// Terms are matched as literal substrings of the source line. Matching scans
// the line from left to right and is non-overlapping: at each position the
// LONGEST term that starts there wins; after a match the scan resumes after
// the matched text. Consequently, if the glossary contains both "Storm" and
// "Storm King", the text "Storm King" is matched as the longer term. Terms
// that cross inline markup (for example "Storm<i> King</i>") do not match
// because matching operates on the line exactly as stored by the codec.
//
// Matching is case-sensitive by default. With Options.CaseInsensitive it uses
// Unicode simple case folding (the same rules as strings.EqualFold); terms
// that differ only by case are then duplicates.
//
// # Errors and fallbacks
//
//   - Empty source/target terms, terms containing newlines, and duplicate
//     source terms with different targets are rejected by New.
//   - Exact duplicate entries (same source and target) are de-duplicated.
//   - Overlapping terms are resolved deterministically by the longest-match
//     rule above (a fallback, not an error).
//   - Source text containing the library's reserved placeholder syntax is
//     rejected by Protect with ErrPlaceholderLikeText rather than interpreted.
//   - A translation that drops, duplicates, or invents placeholders is rejected
//     by Restore so the caller can retry the line or fail explicitly.
package glossary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Errors returned by this package.
//
// ErrInvalidGlossary is the parent of every validation failure returned by
// New and Parse. ErrPlaceholderInvalid is the parent of every failure to apply
// glossary placeholders, whether at Protect time (reserved syntax in the
// source text) or Restore time (dropped, duplicated, or foreign placeholders).
var (
	ErrInvalidGlossary       = errors.New("invalid glossary")
	ErrInvalidEntry          = errors.New("invalid entry")
	ErrDuplicateTerm         = errors.New("duplicate source term")
	ErrPlaceholderLikeText   = errors.New("reserved placeholder syntax in text")
	ErrPlaceholderInvalid    = errors.New("placeholder integrity check failed")
	ErrPlaceholderDropped    = errors.New("translation dropped a placeholder")
	ErrPlaceholderDuplicated = errors.New("translation duplicated a placeholder")
	ErrForeignPlaceholder    = errors.New("translation contains an unknown placeholder")
)

// invalidEntry returns a validation error of the given kind. Both the parent
// ErrInvalidGlossary and the specific kind are wrapped, so errors.Is works for
// either; the text reads "invalid glossary: <kind>: <detail>".
func invalidEntry(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidGlossary, kind, fmt.Sprintf(format, args...))
}

func placeholderErr(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrPlaceholderInvalid, kind, fmt.Sprintf(format, args...))
}

// Entry is one source→target term pair.
type Entry struct {
	Source string
	Target string
}

// Options controls term matching.
type Options struct {
	// CaseInsensitive enables Unicode simple case-folding for source matching.
	CaseInsensitive bool
}

// Table is an immutable, validated glossary ready for concurrent use.
type Table struct {
	entries []Entry
	byFirst map[rune][]int // first rune (folded when needed) → entry indices, longest first
	fold    bool
}

// New validates entries and builds a matcher.
//
// Errors wrap ErrInvalidEntry, ErrDuplicateTerm, or ErrPlaceholderLikeText.
func New(entries []Entry, opts Options) (*Table, error) {
	t := &Table{
		entries: make([]Entry, 0, len(entries)),
		byFirst: make(map[rune][]int),
		fold:    opts.CaseInsensitive,
	}

	seen := make(map[string]string, len(entries))
	for i, e := range entries {
		if strings.TrimSpace(e.Source) == "" {
			return nil, invalidEntry(ErrInvalidEntry, "entry %d: source is empty", i+1)
		}
		if strings.TrimSpace(e.Target) == "" {
			return nil, invalidEntry(ErrInvalidEntry, "entry %d (%q): target is empty", i+1, e.Source)
		}
		if strings.ContainsAny(e.Source, "\r\n") {
			return nil, invalidEntry(ErrInvalidEntry, "entry %d (%q): source must not contain a newline", i+1, e.Source)
		}
		if strings.ContainsAny(e.Target, "\r\n") {
			return nil, invalidEntry(ErrInvalidEntry, "entry %d (%q): target must not contain a newline", i+1, e.Source)
		}
		if strings.Contains(e.Source, ReservedPrefix) || strings.Contains(e.Target, ReservedPrefix) {
			return nil, invalidEntry(ErrPlaceholderLikeText,
				"entry %d: source and target must not contain reserved placeholder syntax", i+1)
		}

		key := e.Source
		if t.fold {
			key = foldString(e.Source)
		}
		if prev, ok := seen[key]; ok {
			if prev == e.Target {
				// Identical duplicate: harmless, keep one copy.
				continue
			}
			return nil, invalidEntry(ErrDuplicateTerm, "%q maps to both %q and %q", e.Source, prev, e.Target)
		}
		seen[key] = e.Target
		t.entries = append(t.entries, e)
	}

	for i := range t.entries {
		r, _ := utf8.DecodeRuneInString(t.entries[i].Source)
		key := r
		if t.fold {
			key = foldKey(r)
		}
		t.byFirst[key] = append(t.byFirst[key], i)
	}

	// Longest-match first, independent of the order entries were supplied in.
	for key := range t.byFirst {
		indices := t.byFirst[key]
		sort.SliceStable(indices, func(a, b int) bool {
			la := utf8.RuneCountInString(t.entries[indices[a]].Source)
			lb := utf8.RuneCountInString(t.entries[indices[b]].Source)
			return la > lb
		})
	}

	return t, nil
}

// Len reports the number of terms in the table.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Entries returns a copy of the table's entries.
func (t *Table) Entries() []Entry {
	if t == nil {
		return nil
	}
	out := make([]Entry, len(t.entries))
	copy(out, t.entries)
	return out
}

// matchAt reports the longest term starting at byte offset pos.
func (t *Table) matchAt(text string, pos int) (entry int, end int, ok bool) {
	r, size := utf8.DecodeRuneInString(text[pos:])
	if size == 0 {
		return 0, 0, false
	}
	key := r
	if t.fold {
		key = foldKey(r)
	}
	for _, idx := range t.byFirst[key] {
		src := t.entries[idx].Source
		if t.hasPrefixAt(text[pos:], src) {
			return idx, pos + len(src), true
		}
	}
	return 0, 0, false
}

func (t *Table) hasPrefixAt(s, prefix string) bool {
	if !t.fold {
		return strings.HasPrefix(s, prefix)
	}
	for len(prefix) > 0 {
		pr, psize := utf8.DecodeRuneInString(prefix)
		sr, ssize := utf8.DecodeRuneInString(s)
		if ssize == 0 || foldKey(sr) != foldKey(pr) {
			return false
		}
		s = s[ssize:]
		prefix = prefix[psize:]
	}
	return true
}

// foldKey returns a canonical representative of r's Unicode simple-fold cycle.
// This makes first-rune indexing agree with EqualFold-style comparison.
func foldKey(r rune) rune {
	min := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < min {
			min = f
		}
	}
	return min
}

func foldString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(foldKey(r))
	}
	return b.String()
}

// Parse reads a glossary definition. Two encodings are accepted:
//
// JSON: either an object mapping source to target:
//
//	{"Aria": "阿莉娅", "Storm King": "风暴之王"}
//
// or an array of {"source": ..., "target": ...} objects, or an array of
// two-element [source, target] arrays.
//
// Line-based text: one "source<TAB>target" or "source=target" pair per line.
// Blank lines and lines whose first non-space character is '#' are ignored.
// Values are trimmed; internal spaces are preserved.
//
// Encoding is chosen by the first non-space byte ('{' or '[' means JSON).
// Duplicate sources are handled by New; Parse itself rejects duplicate JSON
// object keys because a JSON decoder would otherwise silently keep only one.
func Parse(data []byte) ([]Entry, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, invalidEntry(ErrInvalidEntry, "glossary is empty")
	}

	switch trimmed[0] {
	case '{', '[':
		return parseJSON(data)
	default:
		return parseLines(data)
	}
}

func parseJSON(data []byte) ([]Entry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, invalidEntry(ErrInvalidEntry, "invalid JSON: %v", err)
	}

	var entries []Entry
	switch tok {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, invalidEntry(ErrInvalidEntry, "invalid JSON object: %v", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, invalidEntry(ErrInvalidEntry, "JSON object key is not a string")
			}
			if seen[key] {
				return nil, invalidEntry(ErrDuplicateTerm, "duplicate JSON key %q", key)
			}
			seen[key] = true

			var target string
			if err := dec.Decode(&target); err != nil {
				return nil, invalidEntry(ErrInvalidEntry, "value for %q must be a string: %v", key, err)
			}
			entries = append(entries, Entry{Source: key, Target: target})
		}
		if _, err := dec.Token(); err != nil { // closing '}'
			return nil, invalidEntry(ErrInvalidEntry, "invalid JSON object: %v", err)
		}
	case json.Delim('['):
		// The '[' token was already consumed; decode elements until ']'.
		for i := 0; dec.More(); i++ {
			var item json.RawMessage
			if err := dec.Decode(&item); err != nil {
				return nil, invalidEntry(ErrInvalidEntry, "array item %d: invalid JSON: %v", i+1, err)
			}
			e, err := parseJSONItem(item)
			if err != nil {
				return nil, invalidEntry(ErrInvalidEntry, "array item %d: %v", i+1, err)
			}
			entries = append(entries, e)
		}
		if _, err := dec.Token(); err != nil { // closing ']'
			return nil, invalidEntry(ErrInvalidEntry, "invalid JSON array: %v", err)
		}
	default:
		return nil, invalidEntry(ErrInvalidEntry, "unexpected JSON token %v", tok)
	}

	if _, err := dec.Token(); err != io.EOF {
		return nil, invalidEntry(ErrInvalidEntry, "trailing data after glossary JSON")
	}
	if len(entries) == 0 {
		return nil, invalidEntry(ErrInvalidEntry, "glossary is empty")
	}
	return entries, nil
}

func parseJSONItem(raw json.RawMessage) (Entry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return Entry{}, errors.New("empty item")
	}
	if trimmed[0] == '[' {
		var pair []string
		if err := json.Unmarshal(trimmed, &pair); err != nil {
			return Entry{}, fmt.Errorf("expected [source, target]: %v", err)
		}
		if len(pair) != 2 {
			return Entry{}, fmt.Errorf("expected 2 elements, got %d", len(pair))
		}
		return Entry{Source: pair[0], Target: pair[1]}, nil
	}

	var obj struct {
		Source *string `json:"source"`
		Target *string `json:"target"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return Entry{}, fmt.Errorf("expected object with source/target: %v", err)
	}
	if obj.Source == nil || obj.Target == nil {
		return Entry{}, errors.New("object must contain string fields \"source\" and \"target\"")
	}
	return Entry{Source: *obj.Source, Target: *obj.Target}, nil
}

func parseLines(data []byte) ([]Entry, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var entries []Entry
	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var source, target string
		if idx := strings.IndexByte(line, '\t'); idx >= 0 {
			source, target = line[:idx], line[idx+1:]
		} else if idx := strings.IndexByte(line, '='); idx >= 0 {
			source, target = line[:idx], line[idx+1:]
		} else {
			return nil, invalidEntry(ErrInvalidEntry, "line %d: expected \"source<TAB>target\" or \"source=target\"", lineNo)
		}

		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" {
			return nil, invalidEntry(ErrInvalidEntry, "line %d: source is empty", lineNo)
		}
		if target == "" {
			return nil, invalidEntry(ErrInvalidEntry, "line %d (%q): target is empty", lineNo, source)
		}
		entries = append(entries, Entry{Source: source, Target: target})
	}

	if len(entries) == 0 {
		return nil, invalidEntry(ErrInvalidEntry, "glossary is empty")
	}
	return entries, nil
}
