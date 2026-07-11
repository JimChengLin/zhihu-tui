package feedtui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

func renderStyledLine(line styledLine, maxWidth int) (string, int) {
	suffix := truncateCells(line.suffix, maxWidth)
	suffixWidth := stringCellWidth(suffix)
	tail := truncateCells(line.tail, maxInt(0, maxWidth-suffixWidth))
	tailWidth := stringCellWidth(tail)
	contentWidth := maxInt(0, maxWidth-suffixWidth-tailWidth)
	text := truncateCells(line.text, contentWidth)
	textWidth := stringCellWidth(text)
	middle := truncateCells(line.middle, maxInt(0, contentWidth-textWidth))
	middleWidth := stringCellWidth(middle)
	paddingWidth := minInt(line.padding, maxInt(0, maxWidth-suffixWidth-tailWidth-textWidth-middleWidth))
	padding := strings.Repeat(" ", paddingWidth)
	rendered := styleText(text, line.style) + styleText(middle, line.middleStyle) + styleText(tail, line.tailStyle) + padding + styleText(suffix, line.suffixStyle)
	return rendered, textWidth + middleWidth + tailWidth + paddingWidth + suffixWidth
}

func styleText(text, style string) string {
	if text == "" || style == "" {
		return text
	}
	return style + text + ansiReset
}

func writeFrame(out interface{ Write([]byte) (int, error) }, lines []styledLine, width, height int) error {
	var builder strings.Builder
	builder.WriteString("\033[?25l\033[H")
	cursorRow, cursorCell := -1, -1
	for row := 0; row < height; row++ {
		builder.WriteString("\033[2K")
		if row < len(lines) {
			if lines[row].raw {
				builder.WriteString(lines[row].text)
			} else {
				text, _ := renderStyledLine(lines[row], maxInt(1, width-1))
				builder.WriteString(text)
			}
			if lines[row].hasCursor {
				cursorRow = row
				cursorCell = minInt(maxInt(0, lines[row].cursorCell), maxInt(0, width-1))
			}
		}
		if row+1 < height {
			builder.WriteString("\r\n")
		}
	}
	if cursorRow >= 0 {
		builder.WriteString(fmt.Sprintf("\033[%d;%dH\033[?25h", cursorRow+1, cursorCell+1))
	}
	_, err := out.Write([]byte(builder.String()))
	return err
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(paragraphs))
	for index, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			if index > 0 && len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		var builder strings.Builder
		cells := 0
		for _, token := range textTokens(paragraph) {
			if token == " " {
				if cells > 0 && cells < width {
					builder.WriteByte(' ')
					cells++
				}
				continue
			}
			tokenWidth := stringCellWidth(token)
			if cells > 0 && cells+tokenWidth > width {
				runes := []rune(token)
				if len(runes) == 1 && isClosingPunctuation(runes[0]) {
					current := textUnits(strings.TrimSpace(builder.String()))
					last := current[len(current)-1]
					prefix := strings.TrimSpace(strings.Join(current[:len(current)-1], ""))
					if prefix != "" {
						lines = append(lines, prefix)
					}
					builder.Reset()
					builder.WriteString(last)
					builder.WriteString(token)
					cells = stringCellWidth(last) + tokenWidth
					continue
				}
				lines = append(lines, strings.TrimSpace(builder.String()))
				builder.Reset()
				cells = 0
			}
			if tokenWidth <= width {
				builder.WriteString(token)
				cells += tokenWidth
				continue
			}
			for _, unit := range textUnits(token) {
				unitWidth := stringCellWidth(unit)
				if cells > 0 && cells+unitWidth > width {
					lines = append(lines, strings.TrimSpace(builder.String()))
					builder.Reset()
					cells = 0
				}
				builder.WriteString(unit)
				cells += unitWidth
			}
		}
		if builder.Len() > 0 {
			lines = append(lines, strings.TrimSpace(builder.String()))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func textTokens(text string) []string {
	tokens := make([]string, 0, len([]rune(text)))
	var ascii strings.Builder
	flushASCII := func() {
		if ascii.Len() > 0 {
			tokens = append(tokens, ascii.String())
			ascii.Reset()
		}
	}
	for _, unit := range textUnits(text) {
		runes := []rune(unit)
		r := runes[0]
		switch {
		case len(runes) > 1:
			flushASCII()
			tokens = append(tokens, unit)
		case unicode.IsSpace(r):
			flushASCII()
			if len(tokens) == 0 || tokens[len(tokens)-1] != " " {
				tokens = append(tokens, " ")
			}
		case r >= 0x21 && r <= 0x7e:
			ascii.WriteRune(r)
		default:
			flushASCII()
			tokens = append(tokens, unit)
		}
	}
	flushASCII()
	return tokens
}

func textUnits(text string) []string {
	graphemes := uniseg.NewGraphemes(text)
	units := make([]string, 0, len([]rune(text)))
	for graphemes.Next() {
		units = append(units, graphemes.Str())
	}
	return units
}

func isClosingPunctuation(r rune) bool {
	return strings.ContainsRune("，。！？；：、）】》〉」』…,.!?;:)]}", r)
}

func truncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if stringCellWidth(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var builder strings.Builder
	cells := 0
	for _, unit := range textUnits(text) {
		unitWidth := stringCellWidth(unit)
		if cells+unitWidth > width-1 {
			break
		}
		builder.WriteString(unit)
		cells += unitWidth
	}
	return builder.String() + "…"
}

func stringCellWidth(text string) int {
	return uniseg.StringWidth(text)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
