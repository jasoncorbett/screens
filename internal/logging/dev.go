package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset = "\033[0m"
	ansiDim   = "\033[2m"

	// Level badge styles (background + foreground).
	debugBadge = "\033[100m\033[97m"
	infoBadge  = "\033[44m\033[97m"
	warnBadge  = "\033[43m\033[30m"
	errorBadge = "\033[41m\033[97m"

	// Message foreground colors.
	debugMsgColor = "\033[90m"
	infoMsgColor  = "\033[97m"
	warnMsgColor  = "\033[33m"
	errorMsgColor = "\033[31m"

	// Attribute colors.
	ansiAttrKey = "\033[36m"
)

// devHandler is a slog.Handler that writes colorized, human-readable output
// intended for local development.
type devHandler struct {
	level  slog.Level
	w      io.Writer
	mu     *sync.Mutex
	pre    []slog.Attr // accumulated via WithAttrs (already wrapped in groups)
	groups []string    // open group path from WithGroup
}

func newDevHandler(w io.Writer, level slog.Level) *devHandler {
	return &devHandler{
		level: level,
		w:     w,
		mu:    &sync.Mutex{},
	}
}

func (h *devHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *devHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}

	badge, msgColor := levelColors(r.Level)

	fmt.Fprintf(&buf, "%s[ %s ]%s  %s %-5s %s  %s%s%s\n",
		ansiDim, t.Format("03:04:05 PM"), ansiReset,
		badge, r.Level.String(), ansiReset,
		msgColor, r.Message, ansiReset,
	)

	// Collect pre-set and record attributes.
	var allAttrs []slog.Attr
	allAttrs = append(allAttrs, h.pre...)

	var recordAttrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})
	allAttrs = append(allAttrs, wrapInGroups(h.groups, recordAttrs)...)

	if len(allAttrs) > 0 {
		entries := attrsToEntries(allAttrs)
		renderEntries(entries, 1, &buf)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *devHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	pre := slices.Clone(h.pre)
	pre = append(pre, wrapInGroups(h.groups, attrs)...)
	return &devHandler{
		level:  h.level,
		w:      h.w,
		mu:     h.mu,
		pre:    pre,
		groups: slices.Clone(h.groups),
	}
}

func (h *devHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &devHandler{
		level:  h.level,
		w:      h.w,
		mu:     h.mu,
		pre:    slices.Clone(h.pre),
		groups: append(slices.Clone(h.groups), name),
	}
}

// --- rendering helpers ---

func levelColors(l slog.Level) (badge, msg string) {
	switch {
	case l >= slog.LevelError:
		return errorBadge, errorMsgColor
	case l >= slog.LevelWarn:
		return warnBadge, warnMsgColor
	case l >= slog.LevelInfo:
		return infoBadge, infoMsgColor
	default:
		return debugBadge, debugMsgColor
	}
}

// entry is an intermediate representation used for rendering attributes.
type entry struct {
	key      string
	value    string  // set for leaf attrs
	children []entry // set for group attrs
}

func attrsToEntries(attrs []slog.Attr) []entry {
	var entries []entry
	for _, a := range attrs {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			children := attrsToEntries(v.Group())
			if a.Key == "" {
				// Inline group — merge children up.
				entries = append(entries, children...)
			} else {
				entries = append(entries, entry{key: a.Key, children: children})
			}
		} else {
			entries = append(entries, entry{key: a.Key, value: v.String()})
		}
	}
	return entries
}

func renderEntries(entries []entry, indent int, buf *bytes.Buffer) {
	maxKeyLen := 0
	for _, e := range entries {
		if len(e.key) > maxKeyLen {
			maxKeyLen = len(e.key)
		}
	}

	prefix := strings.Repeat("  ", indent)
	for _, e := range entries {
		if len(e.children) > 0 {
			fmt.Fprintf(buf, "%s%s%s%s:\n", prefix, ansiAttrKey, e.key, ansiReset)
			renderEntries(e.children, indent+1, buf)
		} else {
			padding := strings.Repeat(" ", maxKeyLen-len(e.key))
			fmt.Fprintf(buf, "%s%s%s%s%s: %s\n", prefix, ansiAttrKey, e.key, padding, ansiReset, e.value)
		}
	}
}

// wrapInGroups nests attrs under the given group path.
func wrapInGroups(groups []string, attrs []slog.Attr) []slog.Attr {
	if len(groups) == 0 {
		return attrs
	}
	wrapped := attrs
	for i := len(groups) - 1; i >= 0; i-- {
		anys := make([]any, len(wrapped))
		for j, a := range wrapped {
			anys[j] = a
		}
		wrapped = []slog.Attr{slog.Group(groups[i], anys...)}
	}
	return wrapped
}
