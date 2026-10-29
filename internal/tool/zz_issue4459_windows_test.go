//go:build windows

package tool

import (
	"testing"
	"unsafe"
)

// #4459: the wheel delta must land in MOUSEINPUT.mouseData - dx/dy are
// ignored for MOUSEEVENTF_WHEEL, so the old mouseEventInput(mouseWheel, 0,
// d) form sent zero-delta wheel events and scroll silently no-opped.
func TestWheelEventInputCarriesDeltaInMouseData(t *testing.T) {
	for _, delta := range []int32{-120, 120, -360} {
		in := wheelEventInput(delta)
		if in.Type != inputMouse {
			t.Fatalf("Type = %d, want %d", in.Type, inputMouse)
		}
		mi := (*winMOUSEINPUT)(unsafe.Pointer(&in.U[0]))
		if int32(mi.MouseData) != delta {
			t.Errorf("MouseData = %d, want %d", int32(mi.MouseData), delta)
		}
		if mi.DwFlags != mouseWheel {
			t.Errorf("DwFlags = %#x, want %#x", mi.DwFlags, mouseWheel)
		}
	}
}
