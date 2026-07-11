package feedtui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestReadKeyRecognizesNavigationSequences(t *testing.T) {
	tests := []struct {
		input string
		want  keyEvent
	}{
		{"j", "j"},
		{"\x1b[A", keyUp},
		{"\x1b[B", keyDown},
		{"\x1b[5~", keyPageUp},
		{"\x1b[6~", keyPageDown},
		{"\x1b[3~", keyDelete},
		{"\x1b[H", keyHome},
		{"\x1b[F", keyEnd},
		{"\x01", keyCtrlA},
		{"\x02", keyCtrlB},
		{"\x03", keyCtrlC},
		{"\x04", keyCtrlD},
		{"\x05", keyCtrlE},
		{"\x06", keyCtrlF},
		{"\x1b", keyEscape},
		{"\x07", keyCtrlG},
		{"\x0a", keyCtrlJ},
		{"\x15", keyCtrlU},
		{"\x19", keyCtrlY},
		{"\t", keyTab},
		{"\x7f", keyBackspace},
		{"你", "你"},
	}
	for _, test := range tests {
		got, err := readKey(bufio.NewReader(strings.NewReader(test.input)))
		if err != nil {
			t.Fatalf("readKey(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("readKey(%q)=%q, want %q", test.input, got, test.want)
		}
	}
}

func TestWriteFrameUsesTerminalNativeCursor(t *testing.T) {
	var output bytes.Buffer
	lines := []styledLine{{text: "│  ", middle: "test", hasCursor: true, cursorCell: 4}}
	if err := writeFrame(&output, lines, 20, 1); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	frame := output.String()
	if !strings.Contains(frame, "\033[?25l") || !strings.Contains(frame, "\033[1;5H\033[?25h") {
		t.Fatalf("frame does not position native cursor: %q", frame)
	}
}

func TestTerminalKeyDecoderSeparatesEscapeFromNavigation(t *testing.T) {
	var decoder terminalKeyDecoder
	if events := decoder.push(27); len(events) != 0 || !decoder.hasPendingEscape() {
		t.Fatalf("escape start events=%v pending=%v", events, decoder.hasPendingEscape())
	}
	if events := decoder.flushEscape(); len(events) != 1 || events[0] != keyEscape {
		t.Fatalf("standalone escape events=%v", events)
	}

	var arrow terminalKeyDecoder
	var events []keyEvent
	for _, value := range []byte("\x1b[A") {
		events = append(events, arrow.push(value)...)
	}
	if len(events) != 1 || events[0] != keyUp || arrow.hasPendingEscape() {
		t.Fatalf("arrow events=%v pending=%v", events, arrow.hasPendingEscape())
	}

	var deletion terminalKeyDecoder
	events = nil
	for _, value := range []byte("\x1b[3~") {
		events = append(events, deletion.push(value)...)
	}
	if len(events) != 1 || events[0] != keyDelete || deletion.hasPendingEscape() {
		t.Fatalf("delete events=%v pending=%v", events, deletion.hasPendingEscape())
	}

	var followed terminalKeyDecoder
	events = nil
	for _, value := range []byte("\x1bq") {
		events = append(events, followed.push(value)...)
	}
	if len(events) != 2 || events[0] != keyEscape || events[1] != "q" {
		t.Fatalf("escape followed by text events=%v", events)
	}
}

func TestTerminalKeyDecoderKeepsUTF8Input(t *testing.T) {
	var decoder terminalKeyDecoder
	var events []keyEvent
	for _, value := range []byte("你") {
		events = append(events, decoder.push(value)...)
	}
	if len(events) != 1 || events[0] != "你" {
		t.Fatalf("UTF-8 events=%v", events)
	}
}
