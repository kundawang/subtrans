package translator

import (
	"errors"
	"fmt"

	"github.com/heartleo/subtrans/internal/glossary"
)

// protectBatch replaces glossary terms in every line of batch with
// placeholders. A fresh session is created per batch, so placeholders are
// scoped to this batch and cannot collide with another batch or request.
//
// Protect errors are returned verbatim (wrapped with the line number) — e.g.
// source text that forges the reserved placeholder syntax.
func protectBatch(batch *Batch, table *glossary.Table) error {
	if table == nil || table.Len() == 0 {
		return nil
	}

	session, err := glossary.NewSession(table)
	if err != nil {
		return err
	}

	for _, line := range batch.Lines {
		protected, err := session.Protect(line.Text)
		if err != nil {
			return fmt.Errorf("batch %d line %d: %w", batch.Number, line.Number, err)
		}
		if protected.HasPlaceholders() {
			line.glossaryText = protected.Text
			line.protected = protected
		}
	}
	return nil
}

// restoreBatch substitutes placeholders in lines that received a translation
// this round and reports those whose translation was rejected. Rejected lines
// have their Translation cleared so the retry loop re-requests them, and the
// errors are returned for diagnostic reporting.
//
// Lines whose translation was restored in an earlier round have a nil
// protected handle and are skipped.
func restoreBatch(lines []*Line) []error {
	var errs []error
	for _, line := range lines {
		if line.Translation == "" || line.protected == nil {
			continue
		}
		restored, err := line.protected.Restore(line.Translation)
		if err != nil {
			line.Translation = ""
			errs = append(errs, fmt.Errorf("line %d: %w", line.Number, err))
			continue
		}
		line.Translation = restored
		line.protected = nil
		line.glossaryText = ""
		line.glossaryDone = true
	}
	return errs
}

// joinRestoreErrors flattens per-line restore errors into a single error
// suitable for wrapping into the fatal retry-exhausted error.
func joinRestoreErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
