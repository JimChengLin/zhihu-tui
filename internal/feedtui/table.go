package feedtui

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	tableStartMarker     = "\ue000table-start\ue001"
	tableEndMarker       = "\ue000table-end\ue001"
	tableRowMarker       = "\ue000table-row\ue001"
	tableCellMarker      = "\ue000table-cell\ue001"
	tableHeaderMarker    = "\ue000table-header\ue001"
	minTableContentWidth = 8
)

var tableStructurePattern = regexp.MustCompile(`(?is)` + codeBlockStartMarker + `|` + codeBlockEndMarker + `|<(/?)(table|tr|td|th)\b[^>]*>`)

type tableCell struct {
	text   string
	header bool
}

func markHTMLTables(value string) string {
	inCodeBlock := false
	return tableStructurePattern.ReplaceAllStringFunc(value, func(tag string) string {
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
		match := tableStructurePattern.FindStringSubmatch(tag)
		name := strings.ToLower(match[2])
		if match[1] == "/" {
			if name == "table" {
				return "\n" + tableEndMarker + "\n"
			}
			return "\n"
		}
		marker := tableStartMarker
		switch name {
		case "tr":
			marker = tableRowMarker
		case "td":
			marker = tableCellMarker
		case "th":
			marker = tableHeaderMarker
		}
		return "\n" + marker + "\n"
	})
}

func layoutTableLines(source []string, width int, commentID string) []styledLine {
	var rows [][]tableCell
	var caption []string
	for _, line := range source {
		switch line {
		case tableRowMarker:
			rows = append(rows, nil)
		case tableCellMarker, tableHeaderMarker:
			if len(rows) == 0 {
				rows = append(rows, nil)
			}
			last := len(rows) - 1
			rows[last] = append(rows[last], tableCell{header: line == tableHeaderMarker})
		default:
			if len(rows) == 0 {
				caption = append(caption, line)
				continue
			}
			row := rows[len(rows)-1]
			if len(row) > 0 {
				row[len(row)-1].text += line + "\n"
			}
		}
	}

	var result []styledLine
	if text := strings.TrimSpace(strings.Join(caption, "\n")); text != "" {
		result = append(result, layoutContentLines(text, width, commentID)...)
	}
	var widths []int
	for _, row := range rows {
		for column := range row {
			row[column].text = strings.TrimSpace(row[column].text)
			if column == len(widths) {
				widths = append(widths, 2)
			}
			for _, line := range layoutContentLines(row[column].text, width, commentID) {
				widths[column] = maxInt(widths[column], stringCellWidth(styledLineText(line)))
			}
		}
	}
	if len(widths) == 0 {
		return result
	}

	// Columns share borders and have one padding cell on each side.
	available := width - 3*len(widths) - 1
	minimum, total := 0, 0
	for _, size := range widths {
		minimum += minInt(size, minTableContentWidth)
		total += size
	}
	if available < minimum {
		return append(result, layoutStackedTable(rows, width, commentID)...)
	}
	for total > available {
		widest := 0
		for column, size := range widths {
			if size > widths[widest] {
				widest = column
			}
		}
		widths[widest]--
		total--
	}

	border := func(left, middle, right string) styledLine {
		parts := make([]string, len(widths))
		for column, size := range widths {
			parts[column] = strings.Repeat("─", size+2)
		}
		return styledLine{text: left + strings.Join(parts, middle) + right, style: ansiDim, commentID: commentID}
	}
	result = append(result, border("┌", "┬", "┐"))
	for rowIndex, row := range rows {
		cells := make([][]styledLine, len(widths))
		height := 1
		for column, cell := range row {
			cells[column] = layoutContentLines(cell.text, widths[column], commentID)
			height = maxInt(height, len(cells[column]))
		}
		for lineIndex := 0; lineIndex < height; lineIndex++ {
			line := styledLine{commentID: commentID}
			line.segments = appendStyledSegment(line.segments, "│", ansiDim)
			for column, size := range widths {
				line.segments = appendStyledSegment(line.segments, " ", "")
				used := 0
				if lineIndex < len(cells[column]) {
					cellLine := cells[column][lineIndex]
					style := ""
					if row[column].header {
						style = ansiBold
					}
					line.segments = appendStyledSegment(line.segments, cellLine.text, style+cellLine.style)
					for _, segment := range cellLine.segments {
						line.segments = appendStyledSegment(line.segments, segment.text, style+segment.style)
					}
					line.segments = appendStyledSegment(line.segments, cellLine.middle, style+cellLine.middleStyle)
					line.segments = appendStyledSegment(line.segments, cellLine.tail, style+cellLine.tailStyle)
					used = stringCellWidth(styledLineText(cellLine))
				}
				line.segments = appendStyledSegment(line.segments, strings.Repeat(" ", size-used+1), "")
				line.segments = appendStyledSegment(line.segments, "│", ansiDim)
			}
			result = append(result, line)
		}
		if rowIndex+1 < len(rows) {
			result = append(result, border("├", "┼", "┤"))
		}
	}
	return append(result, border("└", "┴", "┘"))
}

func layoutStackedTable(rows [][]tableCell, width int, commentID string) []styledLine {
	headers := rows[0]
	hasHeaders := len(headers) > 0 && len(rows) > 1
	for _, cell := range headers {
		hasHeaders = hasHeaders && cell.header
	}
	if hasHeaders {
		rows = rows[1:]
	}
	var result []styledLine
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			result = append(result, styledLine{text: strings.Repeat("─", width), style: ansiDim, commentID: commentID})
		}
		for column, cell := range row {
			label := fmt.Sprintf("第 %d 列", column+1)
			if hasHeaders && column < len(headers) {
				label = compactLine(blockMarkerReplacer.Replace(stripInlineLinkMarkers(headers[column].text)))
			}
			prefix := label + "："
			prefixWidth := stringCellWidth(prefix)
			// Put long labels on their own line to leave room for the cell content.
			if prefixWidth+minTableContentWidth > width {
				result = append(result, layoutProseLines(prefix, width, commentID)...)
				result = append(result, layoutContentLines(cell.text, width, commentID)...)
				continue
			}
			lines := layoutContentLines(cell.text, width-prefixWidth, commentID)
			for index, line := range lines {
				if index > 0 {
					prefix = strings.Repeat(" ", prefixWidth)
				}
				result = append(result, prependStyledLine(line, prefix, ""))
			}
		}
	}
	return result
}
