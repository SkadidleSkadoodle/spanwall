// Package display finds the connected monitors and their physical sizes.
package display

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// EDID holds the fields Spanwall uses from a monitor's EDID block.
type EDID struct {
	Maker       string // 3 letter PnP ID, e.g. "DEL"
	ProductCode uint16
	Serial      uint32 // numeric serial, often 0
	SerialText  string // serial descriptor, if any
	Name        string // monitor name descriptor, if any
	WidthMM     int    // from the first detailed timing descriptor (precise)
	HeightMM    int
	WidthCM     int // basic block, rounded to whole cm
	HeightCM    int
}

var edidHeader = []byte{0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00}

// ParseEDID reads the 128 byte base block.
func ParseEDID(b []byte) (EDID, error) {
	var e EDID
	if len(b) < 128 {
		return e, errors.New("edid: shorter than 128 bytes")
	}
	for i, v := range edidHeader {
		if b[i] != v {
			return e, errors.New("edid: bad header")
		}
	}
	m := binary.BigEndian.Uint16(b[8:10])
	e.Maker = string([]byte{
		byte('A' - 1 + (m>>10)&0x1F),
		byte('A' - 1 + (m>>5)&0x1F),
		byte('A' - 1 + m&0x1F),
	})
	e.ProductCode = binary.LittleEndian.Uint16(b[10:12])
	e.Serial = binary.LittleEndian.Uint32(b[12:16])
	e.WidthCM, e.HeightCM = int(b[21]), int(b[22])

	foundDTD := false
	for off := 54; off <= 108; off += 18 {
		d := b[off : off+18]
		if d[0] != 0 || d[1] != 0 { // detailed timing descriptor
			if !foundDTD {
				e.WidthMM = int(d[12]) | int(d[14]&0xF0)<<4
				e.HeightMM = int(d[13]) | int(d[14]&0x0F)<<8
				foundDTD = true
			}
			continue
		}
		text := descriptorText(d[5:18])
		switch d[3] {
		case 0xFC:
			e.Name = text
		case 0xFF:
			e.SerialText = text
		}
	}
	return e, nil
}

func descriptorText(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, 0x0A); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// StableID identifies a physical monitor across ports, reboots and Windows
// renumbering: maker, product code and serial.
func (e EDID) StableID() string {
	serial := e.SerialText
	if serial == "" && e.Serial != 0 {
		serial = fmt.Sprintf("%08X", e.Serial)
	}
	if serial == "" {
		serial = "0"
	}
	return fmt.Sprintf("%s-%04X-%s", e.Maker, e.ProductCode, serial)
}

// SizeMM returns the most precise physical size available.
func (e EDID) SizeMM() (w, h float64) {
	if e.WidthMM > 0 && e.HeightMM > 0 {
		return float64(e.WidthMM), float64(e.HeightMM)
	}
	return float64(e.WidthCM * 10), float64(e.HeightCM * 10)
}
