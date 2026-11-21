package subtrans

import "github.com/heartleo/subtrans/internal/glossary"

// ParseGlossary parses a glossary definition into entries suitable for
// [WithGlossary].
//
// Accepted encodings:
//
//   - JSON object: {"Aria": "阿莉娅", "Storm King": "风暴之王"}
//   - JSON array: [{"source":"Aria","target":"阿莉娅"}]
//   - JSON array of pairs: [["Aria","阿莉娅"]]
//   - Line-based text: one "source<TAB>target" or "source=target" per line;
//     blank lines and lines starting with '#' are ignored.
func ParseGlossary(data []byte) ([]GlossaryEntry, error) {
	return glossary.Parse(data)
}
