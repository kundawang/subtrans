package translator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/heartleo/subtrans/internal/glossary"
)

// fakeCompleter is an in-process stand-in for the LLM API. It records prompts
// and replies with a per-batch translation produced by the supplied function.
type fakeCompleter struct {
	mu      sync.Mutex
	prompts [][]Message
	// translate receives the parsed input lines of a request and returns the
	// translation strings (one per input line). If it returns an error the
	// Complete call fails.
	translate func(req []subtitleInput) ([]string, error)
}

func (f *fakeCompleter) Complete(_ context.Context, messages []Message) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, append([]Message(nil), messages...))
	f.mu.Unlock()

	input, err := extractInput(messages)
	if err != nil {
		return "", err
	}
	out, err := f.translate(input)
	if err != nil {
		return "", err
	}
	if len(out) != len(input) {
		return "", fmt.Errorf("fake: %d translations for %d inputs", len(out), len(input))
	}

	type entry struct {
		Number      int    `json:"number"`
		Translation string `json:"translation"`
	}
	resp := struct {
		Translations []entry `json:"translations"`
	}{}
	for i, t := range out {
		resp.Translations = append(resp.Translations, entry{Number: i + 1, Translation: t})
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// extractInput pulls the JSON array out of the final user message.
func extractInput(messages []Message) ([]subtitleInput, error) {
	if len(messages) == 0 {
		return nil, errors.New("fake: no messages")
	}
	content := messages[len(messages)-1].Content
	start := strings.LastIndex(content, "[{\"number\"")
	if start < 0 {
		return nil, fmt.Errorf("fake: no JSON array in prompt: %q", content)
	}
	// The input array is the last bracketed JSON document in the prompt.
	var input []subtitleInput
	dec := json.NewDecoder(strings.NewReader(content[start:]))
	if err := dec.Decode(&input); err != nil {
		return nil, fmt.Errorf("fake: decode input: %w", err)
	}
	return input, nil
}

func (f *fakeCompleter) batchInputs(t *testing.T, batch int) []subtitleInput {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if batch >= len(f.prompts) {
		t.Fatalf("no prompt recorded for call %d (%d recorded)", batch, len(f.prompts))
	}
	in, err := extractInput(f.prompts[batch])
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// echoTranslator returns a translation that keeps placeholders intact.
func echoTranslator(req []subtitleInput) ([]string, error) {
	out := make([]string, len(req))
	for i, in := range req {
		out[i] = "译:" + in.Text
	}
	return out, nil
}

func newGlossary(t *testing.T, entries []glossary.Entry, opts glossary.Options) *glossary.Table {
	t.Helper()
	tbl, err := glossary.New(entries, opts)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func srtLines(n int) []*Line {
	lines := make([]*Line, n)
	for i := range lines {
		lines[i] = &Line{Number: i + 1, Text: fmt.Sprintf("Line %d.", i+1)}
	}
	return lines
}

func TestTranslateRestoresGlossaryTerms(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{
		{Source: "Aria", Target: "阿莉娅"},
		{Source: "Storm King", Target: "风暴之王"},
	}, glossary.Options{})

	lines := []*Line{
		{Number: 1, Text: "Aria met the Storm King."},
		{Number: 2, Text: "The Storm King spoke."},
	}
	opts := DefaultOptions()
	opts.Glossary = tbl

	fake := &fakeCompleter{translate: echoTranslator}
	batches := BatchLines(lines, opts)
	if err := Translate(context.Background(), batches, opts, fake, BaseHandler{}); err != nil {
		t.Fatalf("Translate: %v", err)
	}

	if got, want := lines[0].Translation, "译:阿莉娅 met the 风暴之王."; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
	if got, want := lines[1].Translation, "译:The 风暴之王 spoke."; got != want {
		t.Fatalf("line 2 = %q, want %q", got, want)
	}

	// The model must never have seen the raw English terms.
	for _, in := range fake.batchInputs(t, 0) {
		if strings.Contains(in.Text, "Aria") || strings.Contains(in.Text, "Storm King") {
			t.Fatalf("prompt leaked glossary term: %q", in.Text)
		}
		if !strings.Contains(in.Text, glossary.ReservedPrefix) {
			t.Fatalf("prompt has no placeholder: %q", in.Text)
		}
	}
}

func TestTranslateWithoutGlossaryUnchanged(t *testing.T) {
	lines := []*Line{{Number: 1, Text: "Aria met the Storm King."}}
	opts := DefaultOptions()

	fake := &fakeCompleter{translate: echoTranslator}
	if err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{}); err != nil {
		t.Fatal(err)
	}
	if got := lines[0].Translation; got != "译:Aria met the Storm King." {
		t.Fatalf("translation = %q", got)
	}
}

func TestTranslateBatchIsolation(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})

	lines := make([]*Line, 4)
	for i := range lines {
		lines[i] = &Line{Number: i + 1, Text: fmt.Sprintf("Aria speaks line %d.", i+1)}
	}
	opts := DefaultOptions()
	opts.MaxBatchSize = 1
	opts.Glossary = tbl

	var mu sync.Mutex
	seen := make(map[string]int) // placeholder → prompt index
	fake := &fakeCompleter{}
	fake.translate = func(req []subtitleInput) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		idx := len(fake.prompts) - 1
		for _, in := range req {
			for _, tok := range glossary.PlaceholderRE.FindAllString(in.Text, -1) {
				if prev, ok := seen[tok]; ok && prev != idx {
					return nil, fmt.Errorf("placeholder reused across batches: %s", tok)
				}
				seen[tok] = idx
			}
		}
		return echoTranslator(req)
	}

	if err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(seen) != len(lines) {
		t.Fatalf("saw %d distinct placeholders, want %d", len(seen), len(lines))
	}
	for i, l := range lines {
		if want := fmt.Sprintf("译:阿莉娅 speaks line %d.", i+1); l.Translation != want {
			t.Fatalf("line %d = %q, want %q", i+1, l.Translation, want)
		}
	}
}

func TestTranslateConcurrentSessionsIsolate(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})

	// Run several translations concurrently; each gets its own sessions.
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lines := []*Line{{Number: 1, Text: "Aria."}}
			opts := DefaultOptions()
			opts.Glossary = tbl
			fake := &fakeCompleter{translate: echoTranslator}
			if err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{}); err != nil {
				errs <- err
				return
			}
			if lines[0].Translation != "译:阿莉娅." {
				errs <- fmt.Errorf("goroutine %d: translation = %q", i, lines[0].Translation)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestTranslateRetriesDroppedPlaceholder(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{{Number: 1, Text: "Aria."}}
	opts := DefaultOptions()
	opts.Glossary = tbl

	calls := 0
	fake := &fakeCompleter{}
	fake.translate = func(req []subtitleInput) ([]string, error) {
		calls++
		out := make([]string, len(req))
		for i, in := range req {
			out[i] = "译:" + in.Text
		}
		if calls == 1 {
			// First response silently drops the glossary placeholder.
			out[0] = "译:阿莉娅."
		}
		return out, nil
	}

	if err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{}); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected a retry after dropped placeholder, got %d calls", calls)
	}
	if lines[0].Translation != "译:阿莉娅." {
		t.Fatalf("translation = %q", lines[0].Translation)
	}
}

func TestTranslateExhaustsRetriesOnBrokenPlaceholder(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{{Number: 1, Text: "Aria."}}
	opts := DefaultOptions()
	opts.Glossary = tbl

	fake := &fakeCompleter{translate: func(req []subtitleInput) ([]string, error) {
		out := make([]string, len(req))
		for i := range req {
			out[i] = "no placeholder here"
		}
		return out, nil
	}}

	err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{})
	if !errors.Is(err, glossary.ErrPlaceholderDropped) {
		t.Fatalf("err = %v, want ErrPlaceholderDropped", err)
	}
}

func TestTranslateRejectsForgedPlaceholderInSource(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{{Number: 1, Text: "Aria says [[TRM:0123456789abcdef:42]]."}}
	opts := DefaultOptions()
	opts.Glossary = tbl

	fake := &fakeCompleter{}
	err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{})
	if !errors.Is(err, glossary.ErrPlaceholderLikeText) {
		t.Fatalf("err = %v, want ErrPlaceholderLikeText", err)
	}
	if len(fake.prompts) != 0 {
		t.Fatal("API was called despite protection failure")
	}
}

func TestTranslatePreservesTimingAndMetadata(t *testing.T) {
	// Glossary protection must not touch Start/End/Number/Text/Metadata.
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{{Number: 7, Start: 1500, End: 2500, Text: "Aria.", Metadata: "codec-state"}}
	src := lines[0]

	opts := DefaultOptions()
	opts.Glossary = tbl
	fake := &fakeCompleter{translate: echoTranslator}
	if err := Translate(context.Background(), BatchLines(lines, opts), opts, fake, BaseHandler{}); err != nil {
		t.Fatal(err)
	}
	if lines[0].Number != src.Number || lines[0].Start != src.Start || lines[0].End != src.End ||
		lines[0].Text != src.Text || lines[0].Metadata != src.Metadata {
		t.Fatalf("line state mutated: %+v", lines[0])
	}
}

func TestProtectBatchLeavesUnmatchedLinesUnchanged(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{
		{Number: 1, Text: "Nobody here."},
		{Number: 2, Text: "Aria."},
	}
	batch := &Batch{Number: 1, Lines: lines}
	if err := protectBatch(batch, tbl); err != nil {
		t.Fatal(err)
	}
	if lines[0].glossaryText != "" || lines[0].protected != nil {
		t.Fatal("unmatched line was protected")
	}
	if lines[1].glossaryText == "" || lines[1].protected == nil {
		t.Fatal("matched line was not protected")
	}
	if lines[0].Translation != "" {
		t.Fatal("Text changed")
	}
}

func TestRestoreBatchClearsOnlyRejectedLines(t *testing.T) {
	tbl := newGlossary(t, []glossary.Entry{{Source: "Aria", Target: "阿莉娅"}}, glossary.Options{})
	lines := []*Line{{Number: 1, Text: "Aria."}, {Number: 2, Text: "Aria."}}
	batch := &Batch{Number: 1, Lines: lines}
	if err := protectBatch(batch, tbl); err != nil {
		t.Fatal(err)
	}

	ph := glossary.PlaceholderRE.FindString(lines[0].glossaryText)
	if ph == "" {
		t.Fatal("no placeholder generated")
	}
	lines[0].Translation = "译:" + ph // keeps the placeholder, so restore succeeds
	lines[1].Translation = "dropped" // placeholder omitted

	errs := restoreBatch(lines)
	if len(errs) != 1 || !errors.Is(errs[0], glossary.ErrPlaceholderDropped) {
		t.Fatalf("errs = %v", errs)
	}
	if lines[0].Translation != "译:阿莉娅" {
		t.Fatalf("valid line mutated: %q", lines[0].Translation)
	}
	if lines[1].Translation != "" {
		t.Fatalf("rejected line not cleared: %q", lines[1].Translation)
	}
}
