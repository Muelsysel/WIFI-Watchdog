//go:build windows

package main

import (
	"testing"
	"unsafe"
)

// The owner-draw callback receives native structs from Win32. Their exact
// amd64/arm64 layout matters; a mismatch can look like an intermittent hang.
func TestFluentWin32StructLayouts(t *testing.T) {
	if got := unsafe.Sizeof(uiDrawItem{}); got != 64 {
		t.Fatalf("DRAWITEMSTRUCT size = %d; want 64", got)
	}
	if got := unsafe.Offsetof(uiDrawItem{}.HwndItem); got != 24 {
		t.Fatalf("DRAWITEMSTRUCT hwndItem offset = %d; want 24", got)
	}
	if got := unsafe.Offsetof(uiDrawItem{}.HDC); got != 32 {
		t.Fatalf("DRAWITEMSTRUCT hdc offset = %d; want 32", got)
	}
	if got := unsafe.Offsetof(uiDrawItem{}.Rect); got != 40 {
		t.Fatalf("DRAWITEMSTRUCT rcItem offset = %d; want 40", got)
	}
	if got := unsafe.Offsetof(uiDrawItem{}.ItemData); got != 56 {
		t.Fatalf("DRAWITEMSTRUCT itemData offset = %d; want 56", got)
	}
	if got := unsafe.Sizeof(uiPaintStruct{}); got != 72 {
		t.Fatalf("PAINTSTRUCT size = %d; want 72", got)
	}
	if got := unsafe.Sizeof(processMemoryCountersEx{}); got != 80 {
		t.Fatalf("PROCESS_MEMORY_COUNTERS_EX size = %d; want 80", got)
	}
}

func TestFluentNavigationIDsAreContiguous(t *testing.T) {
	ids := []int{ID_NAV_OVERVIEW, ID_NAV_CHECKS, ID_NAV_RECOVERY, ID_NAV_VPN}
	for i, id := range ids {
		if id-ID_NAV_OVERVIEW != i {
			t.Fatalf("nav index mismatch at %d: %d", i, id)
		}
	}
}

func TestFluentColorUsesWin32BGR(t *testing.T) {
	got := uiRGB(35, 105, 235)
	if got != 0xEB6923 {
		t.Fatalf("COLORREF = 0x%06x; want 0xEB6923", got)
	}
}
