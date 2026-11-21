package glossary

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ReservedPrefix begins every protected-term placeholder. Source or target
// text containing this prefix is rejected so a subtitle cannot forge a
// placeholder that would be substituted during Restore.
const ReservedPrefix = "[[TRM:"

// PlaceholderRE matches a complete placeholder: [[TRM:<16 hex>:<id>]].
// Token and id are deliberately narrow so a corrupted or hand-written token
// does not parse as a valid placeholder.
var PlaceholderRE = regexp.MustCompile(`\[\[TRM:[0-9a-f]{16}:[0-9]+\]\]`)

// Session allocates placeholders for one batch. Every session has a random
// token, so placeholder text from a different batch or a concurrent request
// can never be restored by this session.
//
// A Session is not safe for concurrent use; use one Session per batch.
type Session struct {
	table *Table
	token string
	next  int
}

// NewSession creates an isolated session for t. The random token makes
// placeholders from other sessions detectable as foreign.
func NewSession(t *Table) (*Session, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("glossary: generate session token: %w", err)
	}
	return &Session{table: t, token: hex.EncodeToString(raw[:])}, nil
}

// Protected is a line of text with glossary terms replaced by placeholders,
// plus the per-line mapping needed to restore them.
type Protected struct {
	// Text is the placeholder-substituted text to send to the translator.
	Text string

	owner   *Session
	targets map[string]string // full placeholder token → target term
}

// HasPlaceholders reports whether any glossary term was replaced.
func (p *Protected) HasPlaceholders() bool { return p != nil && len(p.targets) > 0 }

func (s *Session) placeholder(id int) string {
	return ReservedPrefix + s.token + ":" + strconv.Itoa(id) + "]]"
}

// Protect replaces every glossary term in text with a session placeholder.
// Matching is non-overlapping and longest-first (see package documentation).
//
// Returns ErrPlaceholderLikeText if text already contains the reserved
// placeholder prefix; such text is rejected rather than trusted.
func (s *Session) Protect(text string) (*Protected, error) {
	p := &Protected{owner: s, targets: make(map[string]string)}
	if strings.Contains(text, ReservedPrefix) {
		return nil, placeholderErr(ErrPlaceholderLikeText, "%q", ReservedPrefix)
	}
	if s.table == nil || s.table.Len() == 0 {
		p.Text = text
		return p, nil
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if idx, end, ok := s.table.matchAt(text, i); ok {
			s.next++
			ph := s.placeholder(s.next)
			p.targets[ph] = s.table.entries[idx].Target
			b.WriteString(ph)
			i = end
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		b.WriteString(text[i : i+size])
		i += size
	}
	p.Text = b.String()
	return p, nil
}

// Restore substitutes this line's placeholders back with their target terms.
//
// Validation is strict: every placeholder allocated for the line must appear
// exactly once, and the translation must not contain a placeholder belonging
// to another line, batch, or request. Violations return
// ErrPlaceholderDropped, ErrPlaceholderDuplicated, or ErrForeignPlaceholder.
func (p *Protected) Restore(translation string) (string, error) {
	if p == nil || p.owner == nil {
		return translation, nil
	}

	// Reject malformed/foreign placeholder fragments even if the regex does
	// not match them (e.g. a truncated [[TRM: token).
	for off := 0; ; {
		i := strings.Index(translation[off:], ReservedPrefix)
		if i < 0 {
			break
		}
		i += off
		m := PlaceholderRE.FindStringIndex(translation[i:])
		if m == nil || m[0] != 0 {
			return "", placeholderErr(ErrForeignPlaceholder, "malformed or foreign token near %q",
				excerpt(translation[i:]))
		}
		off = i + m[1]
	}

	counts := make(map[string]int, len(p.targets))
	for _, tok := range PlaceholderRE.FindAllString(translation, -1) {
		if _, ok := p.targets[tok]; !ok {
			return "", placeholderErr(ErrForeignPlaceholder, "%q", tok)
		}
		counts[tok]++
	}

	for tok := range p.targets {
		switch counts[tok] {
		case 1:
		case 0:
			return "", placeholderErr(ErrPlaceholderDropped, "expected %q", tok)
		default:
			return "", placeholderErr(ErrPlaceholderDuplicated, "%q appears %d times", tok, counts[tok])
		}
	}

	if len(p.targets) == 0 {
		return translation, nil
	}
	return PlaceholderRE.ReplaceAllStringFunc(translation, func(tok string) string {
		return p.targets[tok]
	}), nil
}

// Strip removes all placeholder tokens from s. It is intended for length
// heuristics (e.g. merged-line detection), not for output.
func Strip(s string) string {
	return PlaceholderRE.ReplaceAllString(s, "")
}

func excerpt(s string) string {
	const maxLen = 32
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
