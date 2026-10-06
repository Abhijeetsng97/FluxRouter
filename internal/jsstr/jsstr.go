// Package jsstr implements JavaScript string semantics (UTF-16 code units)
// that several ports depend on: string length, and String.prototype.slice
// with negative indices. Byte-level Go operations differ from JS for any
// input containing non-BMP characters (emoji etc.) — these helpers keep the
// Go engine byte-identical with the TS engine.
package jsstr

// JsLen mirrors JS `str.length`: number of UTF-16 code units.
// Non-BMP runes (supplementary planes) count as 2.
func JsLen(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// SliceUTF16 mirrors JS `str.slice(start, end)` measured in UTF-16 code
// units, with JS semantics: negative indices count from the end, out-of-range
// clamps, start >= end yields "". end < 0 means "omit end" when end == intMin.
func SliceUTF16(s string, start, end int) string {
	total := JsLen(s)

	// Normalize start (JS slice semantics).
	if start < 0 {
		start = total + start
		if start < 0 {
			start = 0
		}
	}
	if start > total {
		start = total
	}
	// Normalize end; endJS == -1 sentinel means "unset" (slice to the end).
	endJS := end
	if endJS < 0 {
		endJS = total + endJS
		if endJS < 0 {
			endJS = 0
		}
	}
	if endJS > total {
		endJS = total
	}
	if start >= endJS {
		return ""
	}

	// Walk runes tracking UTF-16 index; collect the byte range covering
	// [start, endJS).
	byteStart, byteEnd := -1, -1
	idx := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if byteStart == -1 && idx >= start {
			byteStart = i
		}
		idx += width
		if idx >= endJS && byteStart != -1 {
			byteEnd = i
			break
		}
	}
	if byteStart == -1 {
		return ""
	}
	if byteEnd == -1 {
		byteEnd = len(s)
	}
	return s[byteStart:byteEnd]
}

// SliceToEndUTF16 mirrors JS `str.slice(start)` (no end argument).
func SliceToEndUTF16(s string, start int) string {
	return SliceUTF16(s, start, -1)
}