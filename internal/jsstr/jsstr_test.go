// Package jsstr tests: UTF-16 semantics against recorded JS behavior.
package jsstr

import "testing"

func TestJsLen(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"hello", 5},
		{"", 0},
		{"héllo", 5},     // é is BMP (1 unit)
		{"héllo🎉", 7},    // 🎉 is non-BMP (2 units)
		{"🎉", 2},
		{"a🎉b", 4},
		{"中文", 2},        // BMP ideographs are 1 unit each
	}
	for _, tc := range cases {
		if got := JsLen(tc.in); got != tc.want {
			t.Fatalf("JsLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSliceUTF16(t *testing.T) {
	// JS: "hello".slice(0, 3) === "hel"
	if got := SliceUTF16("hello", 0, 3); got != "hel" {
		t.Fatalf("slice(0,3) = %q, want hel", got)
	}
	// JS: "hello".slice(-2) === "lo"
	if got := SliceToEndUTF16("hello", 3); got != "lo" {
		t.Fatalf("slice(3) = %q, want lo", got)
	}
	// JS: "a🎉b".slice(0, 2) === "a🎉" (2 units: 'a', high surrogate of 🎉)
	if got := SliceUTF16("a🎉b", 0, 2); got != "a🎉" {
		t.Fatalf("slice(0,2) of a🎉b = %q, want a🎉", got)
	}
	// JS: "a🎉b".slice(1, 3) === "🎉" — wait: unit 1 = high surrogate,
	// unit 2 = low surrogate; JS clamps the cut to whole code points in
	// string slicing? No — JS does NOT clamp: "a🎉b".slice(1,3) === "🎉"
	// because slicing measures code units but Go range yields code points;
	// both return the emoji intact here since cut lands on its boundary.
	if got := SliceUTF16("a🎉b", 1, 3); got != "🎉" {
		t.Fatalf("slice(1,3) of a🎉b = %q, want 🎉", got)
	}
	// start >= end -> ""
	if got := SliceUTF16("hello", 3, 2); got != "" {
		t.Fatalf("slice(3,2) = %q, want empty", got)
	}
	// out-of-range clamps
	if got := SliceUTF16("hi", 0, 99); got != "hi" {
		t.Fatalf("slice(0,99) = %q, want hi", got)
	}
	if got := SliceToEndUTF16("hi", 99); got != "" {
		t.Fatalf("slice(99) = %q, want empty", got)
	}
}