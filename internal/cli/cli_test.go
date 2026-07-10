package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpAdvertisesInteractiveModes(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(--help)=%d, stderr=%q", code, stderr.String())
	}
	for _, command := range []string{
		"zhihu feed --tui",
		"zhihu notifications --monitor",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Fatalf("root help does not contain %q:\n%s", command, stdout.String())
		}
	}
	lines := strings.Split(stdout.String(), "\n")
	descriptionColumns := make([]int, 0, 2)
	for _, line := range lines {
		for _, description := range []string{"Browse your following feed", "Monitor new notifications"} {
			if column := strings.Index(line, description); column >= 0 {
				descriptionColumns = append(descriptionColumns, column)
			}
		}
	}
	if len(descriptionColumns) != 2 || descriptionColumns[0] != descriptionColumns[1] {
		t.Fatalf("interactive descriptions are not aligned: columns=%v\n%s", descriptionColumns, stdout.String())
	}
}

func TestFeedTUIDispatchesBeforeRegularFeedOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"feed", "--tui"}, &stdout, &stderr); code != 1 {
		t.Fatalf("Run(feed --tui)=%d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "requires an interactive terminal") {
		t.Fatalf("feed --tui was not dispatched to the TUI: %q", stderr.String())
	}
}
