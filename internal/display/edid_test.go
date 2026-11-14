package display

import "testing"

// A synthetic EDID: maker DEL, product 0xA1F7, serial text "ABC123",
// name "DELL S3422DWG", DTD size 797 x 334 mm, basic size 80 x 33 cm.
func sampleEDID() []byte {
	b := make([]byte, 128)
	copy(b, edidHeader)
	// "DEL": D=4, E=5, L=12 -> 0b0_00100_00101_01100
	m := uint16(4)<<10 | uint16(5)<<5 | uint16(12)
	b[8], b[9] = byte(m>>8), byte(m)
	b[10], b[11] = 0xF7, 0xA1
	b[21], b[22] = 80, 33
	// DTD at 54: pixel clock nonzero, size 797 x 334 (0x31D x 0x14E)
	b[54], b[55] = 0x01, 0x02
	b[54+12] = 0x1D
	b[54+13] = 0x4E
	b[54+14] = 0x31
	// serial descriptor at 72
	b[72+3] = 0xFF
	copy(b[72+5:], "ABC123\n     ")
	// name descriptor at 90
	b[90+3] = 0xFC
	copy(b[90+5:], "DELL S3422DWG")
	return b
}

func TestParseEDID(t *testing.T) {
	e, err := ParseEDID(sampleEDID())
	if err != nil {
		t.Fatal(err)
	}
	if e.Maker != "DEL" || e.ProductCode != 0xA1F7 {
		t.Fatalf("maker/product = %s/%04X", e.Maker, e.ProductCode)
	}
	if e.Name != "DELL S3422DWG" || e.SerialText != "ABC123" {
		t.Fatalf("name/serial = %q/%q", e.Name, e.SerialText)
	}
	if e.WidthMM != 797 || e.HeightMM != 334 {
		t.Fatalf("size = %dx%d", e.WidthMM, e.HeightMM)
	}
	if id := e.StableID(); id != "DEL-A1F7-ABC123" {
		t.Fatalf("stable id = %s", id)
	}
}

func TestParseEDIDRejectsGarbage(t *testing.T) {
	if _, err := ParseEDID(make([]byte, 128)); err == nil {
		t.Fatal("expected bad header error")
	}
}
