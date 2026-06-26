package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"":      slog.LevelInfo,
		"bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResolvePretty(t *testing.T) {
	var buf bytes.Buffer // not a terminal
	if resolvePretty("pretty", &buf) != true {
		t.Error("explicit pretty should be pretty")
	}
	if resolvePretty("json", &buf) != false {
		t.Error("explicit json should not be pretty")
	}
	if resolvePretty("", &buf) != false {
		t.Error("non-terminal default should not be pretty")
	}
}

func TestBuildJSON(t *testing.T) {
	var buf bytes.Buffer
	build(&buf, false, slog.LevelInfo).Info("hello", "k", "v")
	s := buf.String()
	if !strings.Contains(s, `"msg":"hello"`) || !strings.Contains(s, `"k":"v"`) {
		t.Errorf("expected JSON output, got: %s", s)
	}
}

func TestBuildPretty(t *testing.T) {
	var buf bytes.Buffer
	build(&buf, true, slog.LevelInfo).Info("hello", "k", "v")
	s := buf.String()
	if !strings.Contains(s, "hello") {
		t.Errorf("expected message in output, got: %s", s)
	}
	if strings.Contains(s, `"msg"`) {
		t.Errorf("pretty output should not be JSON, got: %s", s)
	}
}

func TestBuildLevelGating(t *testing.T) {
	var buf bytes.Buffer
	log := build(&buf, false, slog.LevelWarn)
	log.Info("suppressed")
	if buf.Len() != 0 {
		t.Errorf("info should be gated below warn, got: %s", buf.String())
	}
	log.Warn("shown")
	if !strings.Contains(buf.String(), "shown") {
		t.Error("warn should pass the level filter")
	}
}
