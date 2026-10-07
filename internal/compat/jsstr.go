// package compat implements JavaScript string semantics (UTF-16 code units)
// that several ports depend on: string length, and String.prototype.slice
// with negative indices. Byte-level Go operations differ from JS for any
// input containing non-BMP characters (emoji etc.) — these helpers keep the
// Go engine byte-identical with the TS engine.
package compat

import "unicode/utf8"

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

const omitEnd = int(^uint(0) >> 1) // MaxInt sentinel: "no end argument"

// SliceUTF16 mirrors JS `str.slice(start, end)` measured in UTF-16 code
// units: negative indices count from the end, out-of-range clamps,
// start >= end yields "". end < 0 counts from the end (JS semantics).
func SliceUTF16(s string, start, end int) string { return slice16(s, start, end, false) }

// SliceToEndUTF16 mirrors JS `str.slice(start)` (no end argument).
func SliceToEndUTF16(s string, start int) string { return slice16(s, start, 0, true) }

func slice16(s string, start, end int, noEnd bool) string {
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
	// Normalize end.
	endJS := 0
	if noEnd {
		endJS = total
	} else {
		if end < 0 {
			end = total + end
		}
		if end < 0 {
			end = 0
		}
		if end > total {
			end = total
		}
		endJS = end
	}
	if start >= endJS {
		return ""
	}

	// Walk runes tracking the UTF-16 index; collect the byte range covering
	// [start, endJS). byteEnd must be the byte offset AFTER the rune that
	// crosses endJS (previous off-by-one cut one character short).
	byteStart, byteEnd := -1, -1
	idx := 0
	for i, r := range s {
		units, bytes := 1, utf8.RuneLen(r)
		if r > 0xFFFF {
			units = 2
		}
		if byteStart == -1 && idx >= start {
			byteStart = i
		}
		idx += units
		if idx >= endJS && byteStart != -1 {
			byteEnd = i + bytes
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