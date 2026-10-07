package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type errorWriter struct {
	err    error
	failAt int
	calls  int
}

func (writer *errorWriter) Write(data []byte) (int, error) {
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, writer.err
	}
	return len(data), nil
}

func TestRunReturnsFailureWhenHelpOrVersionOutputFails(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"root help", []string{"--help"}},
		{"version", []string{"--version"}},
		{"command help", []string{"feed", "--help"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &errorWriter{err: errors.New("write failed"), failAt: 1}
			if code := Run(test.args, writer, io.Discard); code == 0 {
				t.Fatal("Run returned success after output failed")
			}
		})
	}
}

func TestOutputHelpersReturnWriteError(t *testing.T) {
	for _, test := range []struct {
		name  string
		print func(io.Writer) error
	}{
		{"json", func(out io.Writer) error { return printJSON(out, map[string]any{"ok": true}) }},
		{"empty notifications", func(out io.Writer) error {
			return printNotifications(context.Background(), out, nil, map[string]any{}, false)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantErr := errors.New("write failed")
			writer := &errorWriter{err: wantErr, failAt: 1}
			if err := test.print(writer); !errors.Is(err, wantErr) {
				t.Fatalf("output error=%v, want original write error", err)
			}
		})
	}
}

func TestUploadImagesStopsWhenProgressOutputFails(t *testing.T) {
	wantErr := errors.New("write failed")
	writer := &errorWriter{err: wantErr, failAt: 1}
	images, err := uploadImages(context.Background(), nil, []string{"unopened.jpg"}, "", writer)
	if !errors.Is(err, wantErr) || len(images) != 0 {
		t.Fatalf("uploadImages returned images=%v err=%v, want original write error", images, err)
	}
}

func TestMonitorOutputStopsOnWriteError(t *testing.T) {
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	for _, operation := range []struct {
		name  string
		write func(*monitorOutput) error
	}{
		{"status", func(m *monitorOutput) error { return m.Status(tm, "waiting", "next refresh in 60s") }},
		{"tick", func(m *monitorOutput) error { return m.Tick("next refresh in 59s") }},
		{"separator", func(m *monitorOutput) error { return m.NewSeparator(tm, 1, true) }},
	} {
		for _, failure := range []struct {
			name   string
			failAt int
		}{
			{"clear", 1},
			{"content", 2},
		} {
			t.Run(operation.name+"/"+failure.name, func(t *testing.T) {
				wantErr := errors.New("write failed")
				writer := &errorWriter{err: wantErr, failAt: failure.failAt}
				monitor := &monitorOutput{out: writer, statusRows: 1, statusAt: tm, status: "waiting", hasStatus: true}
				if err := operation.write(monitor); !errors.Is(err, wantErr) {
					t.Fatalf("monitor output error=%v, want original write error", err)
				}
				if writer.calls != failure.failAt {
					t.Fatalf("writes=%d, want stop after failed write %d", writer.calls, failure.failAt)
				}
			})
		}
	}
}
