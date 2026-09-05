package feedtui

import (
	"regexp"
	"strings"
)

const (
	quoteStartMarker = "\ue000quote-start\ue001"
	quoteEndMarker   = "\ue000quote-end\ue001"
)

var quoteStructurePattern = regexp.MustCompile(`(?is)` + codeBlockStartMarker + `|` + codeBlockEndMarker + `|</?blockquote\b[^>]*>`)

func markHTMLQuotes(value string) string {
	inCodeBlock := false
	return quoteStructurePattern.ReplaceAllStringFunc(value, func(tag string) string {
		switch tag {
		case codeBlockStartMarker:
			inCodeBlock = true
			return tag
		case codeBlockEndMarker:
			inCodeBlock = false
			return tag
		}
		if inCodeBlock {
			return tag
		}
		marker := quoteStartMarker
		if strings.HasPrefix(tag, "</") {
			marker = quoteEndMarker
		}
		return "\n" + marker + "\n"
	})
}

func layoutQuoteLines(source []string, width int, commentID string) []styledLine {
	lines := layoutContentLines(strings.Join(source, "\n"), maxInt(1, width-2), commentID)
	for index := range lines {
		line := &lines[index]
		if line.style == "" {
			line.style = ansiQuote
		}
		for index := range line.segments {
			if line.segments[index].style == "" {
				line.segments[index].style = ansiQuote
			}
		}
		if line.middleStyle == "" {
			line.middleStyle = ansiQuote
		}
		*line = prependStyledLine(*line, "│ ", ansiQuote)
	}
	return lines
}
