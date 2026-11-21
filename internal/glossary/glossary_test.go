package glossary

import (
	"errors"
	"strings"
	"testing"
)

func mustTable(t *testing.T, entries []Entry, opts Options) *Table {
	t.Helper()
	tbl, err := New(entries, opts)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return tbl
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		wantErr error
	}{
		{"empty source", []Entry{{Source: "", Target: "x"}}, ErrInvalidEntry},
		{"blank source", []Entry{{Source: "   ", Target: "x"}}, ErrInvalidEntry},
		{"empty target", []Entry{{Source: "Aria", Target: ""}}, ErrInvalidEntry},
		{"blank target", []Entry{{Source: "Aria", Target: "  "}}, ErrInvalidEntry},
		{"newline in source", []Entry{{Source: "A\nB", Target: "x"}}, ErrInvalidEntry},
		{"newline in target", []Entry{{Source: "A", Target: "x\ny"}}, ErrInvalidEntry},
		{"conflicting duplicate", []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "Aria", Target: "艾莉亚"}}, ErrDuplicateTerm},
		{"case-conflicting duplicate", []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "aria", Target: "艾莉亚"}}, ErrDuplicateTerm},
		{"reserved syntax in source", []Entry{{Source: ReservedPrefix + "x]]", Target: "y"}}, ErrPlaceholderLikeText},
		{"reserved syntax in target", []Entry{{Source: "x", Target: ReservedPrefix + "x]]"}}, ErrPlaceholderLikeText},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.entries, Options{CaseInsensitive: strings.Contains(tc.name, "case")})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if !errors.Is(err, ErrInvalidGlossary) {
				t.Fatalf("error %v does not wrap ErrInvalidGlossary", err)
			}
		})
	}
}

func TestNewDeduplicatesIdenticalEntries(t *testing.T) {
	tbl := mustTable(t, []Entry{
		{Source: "Aria", Target: "阿莉娅"},
		{Source: "Aria", Target: "阿莉娅"},
	}, Options{})
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tbl.Len())
	}
}

func TestProtectLongestMatch(t *testing.T) {
	tbl := mustTable(t, []Entry{
		{Source: "Storm", Target: "风暴"},
		{Source: "Storm King", Target: "风暴之王"},
	}, Options{})

	s, err := NewSession(tbl)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Protect("The Storm King met Storm.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "Storm") {
		t.Fatalf("term not protected: %q", p.Text)
	}
	// Two placeholders: one for "Storm King", one for "Storm".
	if got := strings.Count(p.Text, ReservedPrefix); got != 2 {
		t.Fatalf("placeholder count = %d, want 2 (%q)", got, p.Text)
	}
	restored, err := p.Restore(p.Text)
	if err != nil {
		t.Fatal(err)
	}
	if want := "The 风暴之王 met 风暴."; restored != want {
		t.Fatalf("restored = %q, want %q", restored, want)
	}
}

func TestProtectCaseSensitivity(t *testing.T) {
	entries := []Entry{{Source: "Aria", Target: "阿莉娅"}}

	strict := mustTable(t, entries, Options{})
	s, _ := NewSession(strict)
	p, err := s.Protect("aria and Aria")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(p.Text, ReservedPrefix); got != 1 {
		t.Fatalf("case-sensitive placeholder count = %d, want 1", got)
	}

	fold := mustTable(t, entries, Options{CaseInsensitive: true})
	s2, _ := NewSession(fold)
	p2, err := s2.Protect("aria and ARIA and Aria")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(p2.Text, ReservedPrefix); got != 3 {
		t.Fatalf("case-insensitive placeholder count = %d, want 3", got)
	}
	restored, err := p2.Restore(p2.Text)
	if err != nil {
		t.Fatal(err)
	}
	if want := "阿莉娅 and 阿莉娅 and 阿莉娅"; restored != want {
		t.Fatalf("restored = %q, want %q", restored, want)
	}
}

func TestProtectNonOverlappingRepeatedTerm(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "aa", Target: "X"}}, Options{})
	s, _ := NewSession(tbl)
	p, err := s.Protect("aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(p.Text, ReservedPrefix); got != 2 {
		t.Fatalf("placeholder count = %d, want 2", got)
	}
	restored, _ := p.Restore(p.Text)
	if restored != "XX" {
		t.Fatalf("restored = %q, want XX", restored)
	}
}

func TestProtectPreservesMarkupAndWhitespace(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "Aria", Target: "阿莉娅"}}, Options{})
	s, _ := NewSession(tbl)
	src := "<i>Aria</i>{\\b1} said\\Nhello"
	p, err := s.Protect(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Text, "<i>") || !strings.Contains(p.Text, "</i>{\\b1} said\\Nhello") {
		t.Fatalf("markup not preserved: %q", p.Text)
	}
	restored, err := p.Restore(p.Text)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<i>阿莉娅</i>{\\b1} said\\Nhello"; restored != want {
		t.Fatalf("restored = %q, want %q", restored, want)
	}
}

func TestProtectRejectsForgedPlaceholder(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "Aria", Target: "阿莉娅"}}, Options{})
	s, _ := NewSession(tbl)
	for _, src := range []string{
		"[[TRM:0123456789abcdef:1]]",
		"prefix [[TRM:deadbeefdeadbeef:9]] suffix",
	} {
		if _, err := s.Protect(src); !errors.Is(err, ErrPlaceholderLikeText) {
			t.Fatalf("Protect(%q) err = %v, want ErrPlaceholderLikeText", src, err)
		}
	}
}

func TestRestoreErrors(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "Rook", Target: "洛克"}}, Options{})
	s, _ := NewSession(tbl)
	p, err := s.Protect("Aria and Rook")
	if err != nil {
		t.Fatal(err)
	}
	toks := PlaceholderRE.FindAllString(p.Text, -1)
	if len(toks) != 2 {
		t.Fatalf("expected 2 placeholders, got %d", len(toks))
	}

	tests := []struct {
		name string
		in   string
		want error
	}{
		{"dropped", "只有 " + toks[0], ErrPlaceholderDropped},
		{"duplicated", toks[0] + " " + toks[0] + " " + toks[1], ErrPlaceholderDuplicated},
		{"foreign well-formed", toks[0] + " " + toks[1] + " [[TRM:ffffffffffffffff:7]]", ErrForeignPlaceholder},
		{"foreign malformed", toks[0] + " " + toks[1] + " [[TRM:notahex:3]]", ErrForeignPlaceholder},
		{"truncated", toks[0] + " " + toks[1] + " [[TRM:", ErrForeignPlaceholder},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Restore(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !errors.Is(err, ErrPlaceholderInvalid) {
				t.Fatalf("err %v does not wrap ErrPlaceholderInvalid", err)
			}
		})
	}
}

func TestRestoreRejectsForeignSessionPlaceholder(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "Aria", Target: "阿莉娅"}}, Options{})

	s1, _ := NewSession(tbl)
	s2, _ := NewSession(tbl)
	p1, _ := s1.Protect("Aria")
	p2, _ := s2.Protect("Aria")

	// A placeholder minted by session 2 must not be restorable by session 1.
	if _, err := p1.Restore("x " + p2.Text); !errors.Is(err, ErrForeignPlaceholder) {
		t.Fatalf("cross-session restore err = %v, want ErrForeignPlaceholder", err)
	}
	if p1.Text == p2.Text {
		t.Fatal("two sessions produced identical placeholders")
	}
}

func TestRestoreWithoutPlaceholdersPassesThrough(t *testing.T) {
	tbl := mustTable(t, []Entry{{Source: "Aria", Target: "阿莉娅"}}, Options{})
	s, _ := NewSession(tbl)
	p, err := s.Protect("no terms here")
	if err != nil {
		t.Fatal(err)
	}
	if p.HasPlaceholders() {
		t.Fatal("HasPlaceholders = true, want false")
	}
	got, err := p.Restore("这里没有术语")
	if err != nil {
		t.Fatal(err)
	}
	if got != "这里没有术语" {
		t.Fatalf("got %q", got)
	}
}

func TestStrip(t *testing.T) {
	in := "a [[TRM:0123456789abcdef:1]] b [[TRM:0123456789abcdef:2]]"
	if got := Strip(in); got != "a  b " {
		t.Fatalf("Strip = %q", got)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    []Entry
		wantErr error
	}{
		{
			name: "json object",
			data: `{"Aria":"阿莉娅","Storm King":"风暴之王"}`,
			want: []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "Storm King", Target: "风暴之王"}},
		},
		{
			name: "json array of objects",
			data: `[{"source":"Aria","target":"阿莉娅"}]`,
			want: []Entry{{Source: "Aria", Target: "阿莉娅"}},
		},
		{
			name: "json array of pairs",
			data: `[["Aria","阿莉娅"],["Rook","洛克"]]`,
			want: []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "Rook", Target: "洛克"}},
		},
		{
			name: "bom json",
			data: "\xEF\xBB\xBF{\"Aria\":\"阿莉娅\"}",
			want: []Entry{{Source: "Aria", Target: "阿莉娅"}},
		},
		{
			name:    "duplicate json key",
			data:    `{"Aria":"阿莉娅","Aria":"艾莉亚"}`,
			wantErr: ErrDuplicateTerm,
		},
		{
			name:    "json non-string value",
			data:    `{"Aria":123}`,
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "empty json object",
			data:    `{}`,
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "trailing json",
			data:    `{"Aria":"阿莉娅"} garbage`,
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "invalid json",
			data:    `{"Aria":}`,
			wantErr: ErrInvalidEntry,
		},
		{
			name: "tab lines with comments",
			data: "# names\nAria\t阿莉娅\n\nRook=洛克\n",
			want: []Entry{{Source: "Aria", Target: "阿莉娅"}, {Source: "Rook", Target: "洛克"}},
		},
		{
			name: "value with spaces preserved",
			data: "Storm King=风暴之王",
			want: []Entry{{Source: "Storm King", Target: "风暴之王"}},
		},
		{
			name:    "line without separator",
			data:    "Aria 阿莉娅",
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "empty source line",
			data:    "=阿莉娅",
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "empty target line",
			data:    "Aria=",
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "only comments",
			data:    "# nothing\n",
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "empty input",
			data:    "   \n\t",
			wantErr: ErrInvalidEntry,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.data))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if !errors.Is(err, ErrInvalidGlossary) {
					t.Fatalf("err %v does not wrap ErrInvalidGlossary", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestNilTableIsNoop(t *testing.T) {
	var tbl *Table
	if tbl.Len() != 0 {
		t.Fatal("nil table Len != 0")
	}
	s, err := NewSession(tbl)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Protect("Aria")
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "Aria" || p.HasPlaceholders() {
		t.Fatalf("nil table changed text: %q", p.Text)
	}
	got, err := p.Restore("阿莉娅")
	if err != nil || got != "阿莉娅" {
		t.Fatalf("Restore = %q, %v", got, err)
	}
}
