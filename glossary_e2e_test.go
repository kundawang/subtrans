package subtrans_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/heartleo/subtrans"
	"github.com/heartleo/subtrans/internal/glossary"
)

// fakeLLM is an OpenAI-compatible chat completions server. It replaces any
// glossary placeholder with a marker translation and otherwise prefixes the
// text with "T:", so tests can assert both that placeholders arrived intact
// and that they were restored to the glossary target.
type fakeLLM struct {
	mu       sync.Mutex
	requests []recordedRequest
	// corruptPlaceholders, when non-nil, is consulted per line to simulate a
	// model that drops or mangles the placeholder.
	corrupt func(line string) string
}

type recordedRequest struct {
	Language string
	Lines    []string
}

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, "no messages", http.StatusBadRequest)
		return
	}
	user := req.Messages[len(req.Messages)-1].Content

	start := strings.LastIndex(user, "[{\"number\"")
	if start < 0 {
		http.Error(w, "no input array", http.StatusBadRequest)
		return
	}
	var input []struct {
		Number      int    `json:"number"`
		Text        string `json:"text"`
		Translation string `json:"translation"`
	}
	if err := json.Unmarshal([]byte(user[start:]), &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	lang := detectLanguage(req.Messages)
	f.mu.Lock()
	rec := recordedRequest{Language: lang}
	for _, in := range input {
		rec.Lines = append(rec.Lines, in.Text)
	}
	f.requests = append(f.requests, rec)
	corrupt := f.corrupt
	f.mu.Unlock()

	out := make([]map[string]any, 0, len(input))
	for i, in := range input {
		if in.Translation != "" {
			// Context-only line: echo it verbatim.
			out = append(out, map[string]any{"number": i + 1, "translation": in.Translation})
			continue
		}
		translated := "T:" + in.Text
		if corrupt != nil {
			translated = corrupt(translated)
		}
		out = append(out, map[string]any{"number": i + 1, "translation": translated})
	}

	payload, err := json.Marshal(map[string]any{
		"translations": out,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := chatResponse{}
	resp.Choices = append(resp.Choices, struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}{})
	resp.Choices[0].Message.Content = string(payload)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func detectLanguage(messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	for _, m := range messages {
		if m.Role != "system" {
			continue
		}
		switch {
		case strings.Contains(m.Content, "Simplified Chinese"):
			return "zh"
		case strings.Contains(m.Content, "Japanese"):
			return "ja"
		case strings.Contains(m.Content, "French"):
			return "fr"
		}
	}
	return ""
}

func newTestTranslator(t *testing.T, server *httptest.Server) *subtrans.Translator {
	t.Helper()
	tr, err := subtrans.New(subtrans.Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func glossaryOption(t *testing.T, lang string) subtrans.Option {
	t.Helper()
	m, ok := glossaries[lang]
	if !ok {
		t.Fatalf("no glossary fixture for %q", lang)
	}
	var entries []subtrans.GlossaryEntry
	for src, dst := range m {
		entries = append(entries, subtrans.GlossaryEntry{Source: src, Target: dst})
	}
	return subtrans.WithGlossary(entries...)
}

func TestE2EGlossarySRTMultilingual(t *testing.T) {
	for _, lang := range []string{"zh", "ja", "fr"} {
		t.Run(lang, func(t *testing.T) {
			fake := &fakeLLM{}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			tr := newTestTranslator(t, srv)

			// The SRT fixture contains a lowercase "aria"; enable case folding
			// for every language so it is protected too.
			opts := []subtrans.Option{
				subtrans.WithFormat(subtrans.FormatSRT),
				glossaryOption(t, lang),
				subtrans.WithGlossaryCaseInsensitive(true),
			}

			result, err := tr.Translate(context.Background(), srtFixture, lang, opts...)
			if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			if result.LineCount != 4 {
				t.Fatalf("LineCount = %d, want 4", result.LineCount)
			}

			g := glossaries[lang]
			out := result.SRT

			// Every glossary target must appear; the English source terms must not.
			for _, target := range g {
				if !strings.Contains(out, target) {
					t.Errorf("output missing glossary target %q:\n%s", target, out)
				}
			}
			if strings.Contains(out, "aria") || strings.Contains(out, "Aria ") {
				t.Errorf("source term Aria leaked into %s output:\n%s", lang, out)
			}
			if strings.Contains(out, "Storm King") {
				t.Errorf("source term Storm King leaked into output:\n%s", out)
			}

			// Timings and untranslated text are preserved.
			for _, ts := range []string{
				"00:00:01,000 --> 00:00:03,000",
				"00:00:03,500 --> 00:00:05,000",
				"00:00:05,500 --> 00:00:07,000",
				"00:00:07,500 --> 00:00:09,000",
			} {
				if !strings.Contains(out, ts) {
					t.Errorf("missing timestamp %q", ts)
				}
			}
			if !strings.Contains(out, "T:Nobody else was there") {
				t.Errorf("non-glossary line was not translated normally:\n%s", out)
			}
			// Inline markup is preserved and the glossary term restored inside it.
			if !strings.Contains(out, "<i>The "+g["Storm King"]+"</i>") &&
				!strings.Contains(out, "<i>"+g["Storm King"]+"</i>") {
				t.Errorf("inline <i> markup not preserved around glossary term:\n%s", out)
			}

			// The fake service must never have seen the raw source term text.
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.requests) == 0 {
				t.Fatal("fake service received no requests")
			}
			for _, req := range fake.requests {
				for _, line := range req.Lines {
					if strings.Contains(line, "Storm King") || strings.Contains(line, "Aria") {
						t.Errorf("glossary term leaked to the model: %q", line)
					}
				}
			}
		})
	}
}

func TestE2EGlossaryVTT(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	result, err := tr.Translate(context.Background(), vttFixture, "ja",
		subtrans.WithFormat(subtrans.FormatVTT), glossaryOption(t, "ja"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	out := result.SRT
	g := glossaries["ja"]

	if !strings.HasPrefix(out, "WEBVTT") {
		t.Errorf("VTT header lost:\n%s", out)
	}
	if !strings.Contains(out, "NOTE opening scene\nThis note must survive translation.") {
		t.Errorf("NOTE block lost:\n%s", out)
	}
	if !strings.Contains(out, "Kind: captions") || !strings.Contains(out, "Language: en") {
		t.Errorf("header metadata lost:\n%s", out)
	}
	if !strings.Contains(out, "cue-1") {
		t.Errorf("cue identifier lost:\n%s", out)
	}
	if !strings.Contains(out, "align:start position:10%") {
		t.Errorf("cue settings lost:\n%s", out)
	}
	if !strings.Contains(out, g["Storm King"]) || !strings.Contains(out, g["Aria"]) {
		t.Errorf("glossary targets missing:\n%s", out)
	}
	if strings.Contains(out, "Storm King") || strings.Contains(out, "Aria drew") {
		t.Errorf("source text leaked:\n%s", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:03.000 align:start position:10%") {
		t.Errorf("timestamp/settings line altered:\n%s", out)
	}
}

func TestE2EGlossaryASS(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	result, err := tr.Translate(context.Background(), assFixture, "zh",
		subtrans.WithFormat(subtrans.FormatASS), glossaryOption(t, "zh"))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	out := result.SRT
	g := glossaries["zh"]

	for _, want := range []string{
		"[Script Info]",
		"Style: Default,Arial",
		"[Events]",
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,Aria,0,0,0,,T:{\\b1}" + g["Aria"] + "{\\b0} rides north",
		"Dialogue: 0,0:00:03.50,0:00:05.00,Default,,0,0,0,,",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, g["Storm King"]) {
		t.Errorf("glossary target missing:\n%s", out)
	}
	// ASS escapes and override blocks survive.
	if !strings.Contains(out, "{\\b1}") || !strings.Contains(out, "{\\b0}") {
		t.Errorf("ASS override blocks lost:\n%s", out)
	}
	// \N newline escape is decoded on parse and re-encoded on format.
	if !strings.Contains(out, `\N`) {
		t.Errorf("ASS \\N escape lost:\n%s", out)
	}
	// Dialogue fields (style, name) preserved verbatim.
	if !strings.Contains(out, ",Default,Aria,0,0,0,,") {
		t.Errorf("Dialogue style/name fields changed:\n%s", out)
	}
}

func TestE2EGlossaryLRCAndSBV(t *testing.T) {
	tests := []struct {
		name    string
		content string
		format  subtrans.Format
		checks  []string
	}{
		{
			name:    "lrc",
			content: lrcFixture,
			format:  subtrans.FormatLRC,
			checks:  []string{"[ti:Ballad of Aria]", "[ar:The Storm King]", "[00:01.00]", "[00:03.50]"},
		},
		{
			name:    "sbv",
			content: sbvFixture,
			format:  subtrans.FormatSBV,
			checks:  []string{"0:00:01.000,0:00:03.000", "0:00:03.500,0:00:05.000"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLM{}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			tr := newTestTranslator(t, srv)

			result, err := tr.Translate(context.Background(), tc.content, "zh",
				subtrans.WithFormat(tc.format), glossaryOption(t, "zh"))
			if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			out := result.SRT
			for _, want := range tc.checks {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			g := glossaries["zh"]
			if !strings.Contains(out, g["Storm King"]) {
				t.Errorf("glossary target missing:\n%s", out)
			}
			// LRC metadata tags are not glossary-protected or translated.
			if tc.format == subtrans.FormatLRC && !strings.Contains(out, "[ti:Ballad of Aria]") {
				t.Errorf("LRC ID tag altered:\n%s", out)
			}
		})
	}
}

func TestE2EGlossaryBatchIsolation(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	// One line per batch forces each batch through its own glossary session.
	result, err := tr.Translate(context.Background(), srtFixture, "zh",
		subtrans.WithFormat(subtrans.FormatSRT),
		subtrans.WithMaxBatchSize(1),
		glossaryOption(t, "zh"),
		subtrans.WithGlossaryCaseInsensitive(true))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if result.BatchCount < 4 {
		t.Fatalf("BatchCount = %d, want >= 4", result.BatchCount)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	// Collect placeholders per request; no placeholder may appear twice across
	// batches, which proves sessions are isolated.
	seen := map[string]int{}
	for reqIdx, req := range fake.requests {
		for _, line := range req.Lines {
			for _, tok := range glossary.PlaceholderRE.FindAllString(line, -1) {
				if prev, ok := seen[tok]; ok {
					t.Errorf("placeholder %q reused in requests %d and %d", tok, prev, reqIdx)
				}
				seen[tok] = reqIdx
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no placeholders seen; isolation not exercised")
	}

	// Same-term consistency: every occurrence is restored to the same target.
	if strings.Count(result.SRT, "阿莉娅") < 2 {
		t.Errorf("expected Aria restored consistently multiple times:\n%s", result.SRT)
	}
	if strings.Count(result.SRT, "风暴之王") != 3 {
		t.Errorf("expected Storm King restored 3 times:\n%s", result.SRT)
	}
}

func TestE2EGlossaryEmptyAndInvalid(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	tests := []struct {
		name string
		opt  subtrans.Option
		want error
	}{
		{"empty source", subtrans.WithGlossary(subtrans.GlossaryEntry{Source: "", Target: "x"}), subtrans.ErrInvalidGlossary},
		{"empty target", subtrans.WithGlossary(subtrans.GlossaryEntry{Source: "Aria", Target: ""}), subtrans.ErrInvalidGlossary},
		{"duplicate", subtrans.WithGlossary(
			subtrans.GlossaryEntry{Source: "Aria", Target: "阿莉娅"},
			subtrans.GlossaryEntry{Source: "Aria", Target: "艾莉亚"},
		), subtrans.ErrInvalidGlossary},
		{"forged placeholder term", subtrans.WithGlossary(
			subtrans.GlossaryEntry{Source: "Aria", Target: glossary.ReservedPrefix + "x]]"},
		), subtrans.ErrInvalidGlossary},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tr.Translate(context.Background(), srtFixture, "zh", tc.opt)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 0 {
		t.Fatalf("API called %d times for invalid glossaries", len(fake.requests))
	}
}

func TestE2EGlossaryForgedPlaceholderInSubtitle(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	forged := "1\n00:00:01,000 --> 00:00:02,000\nAria said [[TRM:0123456789abcdef:1]].\n"
	_, err := tr.Translate(context.Background(), forged, "zh", glossaryOption(t, "zh"))
	if !errors.Is(err, subtrans.ErrGlossaryPlaceholder) {
		t.Fatalf("err = %v, want ErrGlossaryPlaceholder", err)
	}
	if !errors.Is(err, glossary.ErrPlaceholderLikeText) {
		t.Fatalf("err = %v, want ErrPlaceholderLikeText", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 0 {
		t.Fatal("API called despite forged placeholder")
	}
}

func TestE2EGlossaryModelCorruptsPlaceholderFails(t *testing.T) {
	fake := &fakeLLM{corrupt: func(line string) string {
		return glossary.Strip(line) // model drops the placeholder entirely
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	_, err := tr.Translate(context.Background(), srtFixture, "zh", glossaryOption(t, "zh"))
	if !errors.Is(err, subtrans.ErrTranslationIncomplete) {
		t.Fatalf("err = %v, want ErrTranslationIncomplete", err)
	}
	if !errors.Is(err, subtrans.ErrGlossaryPlaceholder) {
		t.Fatalf("err = %v, want ErrGlossaryPlaceholder", err)
	}
}

func TestE2EGlossaryEmptyGlossaryNoChange(t *testing.T) {
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	plain, err := tr.Translate(context.Background(), srtFixture, "zh")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := tr.Translate(context.Background(), srtFixture, "zh", subtrans.WithGlossary())
	if err != nil {
		t.Fatal(err)
	}
	if plain.SRT != empty.SRT {
		t.Fatalf("empty glossary changed output:\n--- without ---\n%s\n--- with ---\n%s", plain.SRT, empty.SRT)
	}
}

func TestE2EParseGlossaryRoundTrip(t *testing.T) {
	entries, err := subtrans.ParseGlossary([]byte(`{"Aria":"阿莉娅","Storm King":"风暴之王"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	fake := &fakeLLM{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tr := newTestTranslator(t, srv)

	result, err := tr.Translate(context.Background(), srtFixture, "zh", subtrans.WithGlossary(entries...))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.SRT, "风暴之王") || !strings.Contains(result.SRT, "阿莉娅") {
		t.Fatalf("parsed glossary not applied:\n%s", result.SRT)
	}
	if strings.Contains(result.SRT, "Storm King") {
		t.Fatalf("source term leaked:\n%s", result.SRT)
	}
}

func TestE2EGlossaryDefaultTranslationUnchanged(t *testing.T) {
	// Without a glossary, the exact same request/response behavior is used:
	// no placeholders, no restore validation, no extra calls.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()

		var req chatRequest
		if err := json.Unmarshal(b, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user := req.Messages[len(req.Messages)-1].Content
		start := strings.LastIndex(user, "[{\"number\"")
		var input []struct {
			Number int    `json:"number"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal([]byte(user[start:]), &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := make([]map[string]any, len(input))
		for i, in := range input {
			out[i] = map[string]any{"number": i + 1, "translation": "T:" + in.Text}
		}
		payload, _ := json.Marshal(map[string]any{"translations": out})
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, string(payload))
	}))
	defer srv.Close()

	tr := newTestTranslator(t, srv)
	result, err := tr.Translate(context.Background(), srtFixture, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.SRT, glossary.ReservedPrefix) {
		t.Fatal("placeholder leaked into default output")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no requests")
	}
	for _, b := range bodies {
		if strings.Contains(b, glossary.ReservedPrefix) {
			t.Fatal("placeholder sent without glossary")
		}
	}
}
