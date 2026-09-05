package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
)

type colorLogHandler struct {
	mu    *sync.Mutex
	out   io.Writer
	inner slog.Handler
	buf   *bytes.Buffer
}

func newColorLogHandler(out io.Writer) *colorLogHandler {
	buf := &bytes.Buffer{}
	return &colorLogHandler{
		mu:    &sync.Mutex{},
		out:   out,
		inner: slog.NewTextHandler(buf, nil),
		buf:   buf,
	}
}

func (h *colorLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *colorLogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.buf.Reset()
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	line := bytes.TrimRight(h.buf.Bytes(), "\n")

	code := ""
	switch {
	case r.Level >= slog.LevelError:
		code = ansiRed
	case r.Level >= slog.LevelWarn:
		code = ansiAmber
	}

	if code != "" && colorEnabled {
		_, err := io.WriteString(h.out, code+string(line)+ansiReset+"\n")
		return err
	}
	_, err := io.WriteString(h.out, string(line)+"\n")
	return err
}

func (h *colorLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &colorLogHandler{mu: h.mu, out: h.out, inner: h.inner.WithAttrs(attrs), buf: h.buf}
}

func (h *colorLogHandler) WithGroup(name string) slog.Handler {
	return &colorLogHandler{mu: h.mu, out: h.out, inner: h.inner.WithGroup(name), buf: h.buf}
}
