package observability

import (
	"context"
	"log/slog"
	"testing"
)

// TestFanoutHandlerSkipsDisabledDestinations proves a destination that reports
// itself disabled receives no record and does not veto the others.
func TestFanoutHandlerSkipsDisabledDestinations(t *testing.T) {
	counting := &countingHandler{}
	handler := fanoutHandler{handlers: []slog.Handler{disabledHandler{}, counting}}

	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("fanout reported disabled while one destination is enabled")
	}
	if err := handler.Handle(context.Background(), slog.Record{Level: slog.LevelInfo}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if counting.records != 1 {
		t.Errorf("enabled destination received %d records, want 1", counting.records)
	}

	none := fanoutHandler{handlers: []slog.Handler{disabledHandler{}}}
	if none.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("fanout reported enabled while every destination is disabled")
	}
}

// disabledHandler reports itself disabled and fails the test if it is used.
type disabledHandler struct{}

func (disabledHandler) Enabled(context.Context, slog.Level) bool { return false }
func (disabledHandler) Handle(context.Context, slog.Record) error {
	panic("a disabled destination received a record")
}
func (h disabledHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h disabledHandler) WithGroup(string) slog.Handler      { return h }

// countingHandler counts the records it receives.
type countingHandler struct {
	records int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.records++
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }
