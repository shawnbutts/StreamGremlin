package lua

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// FuzzReadFrame: a plugin's worker runs untrusted Lua, so what it writes
// back is outside input. A read frame is within MaxFrame, and written
// again it reads back the same (M9 slice 1, D-110).
func FuzzReadFrame(f *testing.F) {
	var seed bytes.Buffer
	_ = writeFrame(&seed, frame{Type: "host", ID: 7, Op: "chat", Payload: map[string]any{"text": "hi"}})
	_ = writeFrame(&seed, frame{Type: "done", ID: 7, Result: []any{1.0, "x"}, Timers: []timerDue{{ID: 1, InMS: 5}}})
	f.Add(seed.Bytes())
	big := make([]byte, 4)
	binary.BigEndian.PutUint32(big, MaxFrame+1)
	f.Add(big)
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bytes.NewReader(b)
		for range 4 {
			before := r.Len()
			fr, err := readFrame(r)
			if err != nil {
				return
			}
			if before-r.Len() > MaxFrame+4 {
				t.Fatalf("read %d bytes for one frame", before-r.Len())
			}
			var out bytes.Buffer
			if err := writeFrame(&out, fr); err != nil {
				continue // re-encoded larger than MaxFrame, which is refused on writing
			}
			again, err := readFrame(&out)
			if err != nil || !reflect.DeepEqual(again, fr) {
				t.Fatalf("written again:\n%+v\n%+v %v", fr, again, err)
			}
		}
	})
}
