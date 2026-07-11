package feedtui

import (
	"bufio"
	"errors"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

type keyEvent string

const (
	keyUp        keyEvent = "up"
	keyDown      keyEvent = "down"
	keyLeft      keyEvent = "left"
	keyRight     keyEvent = "right"
	keyPageUp    keyEvent = "page-up"
	keyPageDown  keyEvent = "page-down"
	keyCtrlA     keyEvent = "ctrl-a"
	keyCtrlB     keyEvent = "ctrl-b"
	keyCtrlC     keyEvent = "ctrl-c"
	keyCtrlD     keyEvent = "ctrl-d"
	keyCtrlE     keyEvent = "ctrl-e"
	keyCtrlF     keyEvent = "ctrl-f"
	keyCtrlG     keyEvent = "ctrl-g"
	keyCtrlJ     keyEvent = "ctrl-j"
	keyCtrlU     keyEvent = "ctrl-u"
	keyCtrlY     keyEvent = "ctrl-y"
	keyEscape    keyEvent = "escape"
	keyTab       keyEvent = "tab"
	keyBackspace keyEvent = "backspace"
	keyDelete    keyEvent = "delete"
	keyHome      keyEvent = "home"
	keyEnd       keyEvent = "end"
)

type terminalKeyDecoder struct {
	escape []byte
	utf8   []byte
}

func (decoder *terminalKeyDecoder) hasPendingEscape() bool {
	return len(decoder.escape) > 0
}

func (decoder *terminalKeyDecoder) push(value byte) []keyEvent {
	if len(decoder.escape) > 0 {
		return decoder.pushEscape(value)
	}
	if value == 27 {
		decoder.escape = append(decoder.escape[:0], value)
		return nil
	}
	return decoder.pushNormal(value)
}

func (decoder *terminalKeyDecoder) pushEscape(value byte) []keyEvent {
	decoder.escape = append(decoder.escape, value)
	switch len(decoder.escape) {
	case 2:
		if value == '[' || value == 'O' {
			return nil
		}
		return decoder.flushEscape()
	case 3:
		switch value {
		case 'A':
			return decoder.finishEscape(keyUp)
		case 'B':
			return decoder.finishEscape(keyDown)
		case 'C':
			return decoder.finishEscape(keyRight)
		case 'D':
			return decoder.finishEscape(keyLeft)
		case 'H':
			return decoder.finishEscape(keyHome)
		case 'F':
			return decoder.finishEscape(keyEnd)
		case '1', '3', '4', '5', '6':
			if decoder.escape[1] == '[' {
				return nil
			}
		}
		return decoder.flushEscape()
	case 4:
		if decoder.escape[1] == '[' && value == '~' {
			switch decoder.escape[2] {
			case '1':
				return decoder.finishEscape(keyHome)
			case '3':
				return decoder.finishEscape(keyDelete)
			case '4':
				return decoder.finishEscape(keyEnd)
			case '5':
				return decoder.finishEscape(keyPageUp)
			case '6':
				return decoder.finishEscape(keyPageDown)
			}
		}
		return decoder.flushEscape()
	default:
		return decoder.flushEscape()
	}
}

func (decoder *terminalKeyDecoder) finishEscape(key keyEvent) []keyEvent {
	decoder.escape = decoder.escape[:0]
	return []keyEvent{key}
}

func (decoder *terminalKeyDecoder) flushEscape() []keyEvent {
	if len(decoder.escape) == 0 {
		return nil
	}
	remainder := append([]byte(nil), decoder.escape[1:]...)
	decoder.escape = decoder.escape[:0]
	events := []keyEvent{keyEscape}
	for _, value := range remainder {
		events = append(events, decoder.push(value)...)
	}
	return events
}

func (decoder *terminalKeyDecoder) pushNormal(value byte) []keyEvent {
	if len(decoder.utf8) == 0 && value < utf8.RuneSelf {
		switch value {
		case 1:
			return []keyEvent{keyCtrlA}
		case 2:
			return []keyEvent{keyCtrlB}
		case 3:
			return []keyEvent{keyCtrlC}
		case 4:
			return []keyEvent{keyCtrlD}
		case 5:
			return []keyEvent{keyCtrlE}
		case 6:
			return []keyEvent{keyCtrlF}
		case 7:
			return []keyEvent{keyCtrlG}
		case 8, 127:
			return []keyEvent{keyBackspace}
		case '\t':
			return []keyEvent{keyTab}
		case '\r':
			return []keyEvent{"\r"}
		case '\n':
			return []keyEvent{keyCtrlJ}
		case 21:
			return []keyEvent{keyCtrlU}
		case 25:
			return []keyEvent{keyCtrlY}
		default:
			return []keyEvent{keyEvent(string(value))}
		}
	}
	decoder.utf8 = append(decoder.utf8, value)
	if !utf8.FullRune(decoder.utf8) {
		return nil
	}
	r, size := utf8.DecodeRune(decoder.utf8)
	decoder.utf8 = decoder.utf8[size:]
	events := []keyEvent{keyEvent(string(r))}
	for len(decoder.utf8) > 0 && utf8.FullRune(decoder.utf8) {
		r, size = utf8.DecodeRune(decoder.utf8)
		decoder.utf8 = decoder.utf8[size:]
		events = append(events, keyEvent(string(r)))
	}
	return events
}

func readKeys(in *os.File, keys chan<- keyEvent, errs chan<- error) {
	bytes := make(chan byte, 256)
	readErrors := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		for {
			count, err := in.Read(buffer)
			for _, value := range buffer[:count] {
				bytes <- value
			}
			if err != nil {
				readErrors <- err
				return
			}
		}
	}()

	var decoder terminalKeyDecoder
	var escapeTimer *time.Timer
	var escapeTimeout <-chan time.Time
	updateEscapeTimer := func() {
		if !decoder.hasPendingEscape() {
			if escapeTimer != nil && !escapeTimer.Stop() {
				select {
				case <-escapeTimer.C:
				default:
				}
			}
			escapeTimeout = nil
			return
		}
		if escapeTimer == nil {
			escapeTimer = time.NewTimer(50 * time.Millisecond)
		} else {
			if !escapeTimer.Stop() {
				select {
				case <-escapeTimer.C:
				default:
				}
			}
			escapeTimer.Reset(50 * time.Millisecond)
		}
		escapeTimeout = escapeTimer.C
	}
	emit := func(events []keyEvent) {
		for _, key := range events {
			keys <- key
		}
	}

	for {
		select {
		case value := <-bytes:
			emit(decoder.push(value))
			updateEscapeTimer()
		case <-escapeTimeout:
			emit(decoder.flushEscape())
			escapeTimeout = nil
		case err := <-readErrors:
			emit(decoder.flushEscape())
			errs <- err
			return
		}
	}
}

func readKey(reader *bufio.Reader) (keyEvent, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch first {
	case 1:
		return keyCtrlA, nil
	case 2:
		return keyCtrlB, nil
	case 3:
		return keyCtrlC, nil
	case 4:
		return keyCtrlD, nil
	case 5:
		return keyCtrlE, nil
	case 6:
		return keyCtrlF, nil
	case 7:
		return keyCtrlG, nil
	case 8, 127:
		return keyBackspace, nil
	case '\t':
		return keyTab, nil
	case '\r':
		return "\r", nil
	case '\n':
		return keyCtrlJ, nil
	case 21:
		return keyCtrlU, nil
	case 25:
		return keyCtrlY, nil
	case 27:
		second, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return keyEscape, nil
			}
			return "", err
		}
		if second != '[' && second != 'O' {
			if err := reader.UnreadByte(); err != nil {
				return "", err
			}
			return keyEscape, nil
		}
		third, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return keyEscape, nil
			}
			return "", err
		}
		switch third {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		case '1', '3', '4', '5', '6':
			if _, err := reader.ReadByte(); err != nil {
				return "", err
			}
			switch third {
			case '1':
				return keyHome, nil
			case '3':
				return keyDelete, nil
			case '4':
				return keyEnd, nil
			case '5':
				return keyPageUp, nil
			default:
				return keyPageDown, nil
			}
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		default:
			return "", nil
		}
	default:
		if first >= 0x80 {
			if err := reader.UnreadByte(); err != nil {
				return "", err
			}
			r, _, err := reader.ReadRune()
			if err != nil {
				return "", err
			}
			return keyEvent(string(r)), nil
		}
		return keyEvent(string(first)), nil
	}
}
