package brand

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/color"
)

// icoSizes are the sizes Windows picks between: the taskbar, Explorer's various
// view modes, and Alt-Tab.
var icoSizes = []int{16, 20, 24, 32, 48, 64, 128, 256}

// ICO encodes the mark as a Windows icon containing every size in icoSizes.
//
// The entries are PNG rather than BMP. Windows has accepted PNG-in-ICO since
// Vista, and it avoids hand-rolling the bottom-up BMP layout with its separate
// AND mask, which is the traditional source of icons that render with a black
// box behind them.
func ICO() ([]byte, error) {
	type entry struct {
		size int
		data []byte
	}

	entries := make([]entry, 0, len(icoSizes))
	for _, size := range icoSizes {
		data, err := PNG(size, nil)
		if err != nil {
			return nil, fmt.Errorf("encode %dpx: %w", size, err)
		}
		entries = append(entries, entry{size: size, data: data})
	}

	var buf bytes.Buffer

	// ICONDIR: reserved, type 1 (icon), image count.
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(len(entries)))

	// Directory entries are fixed width, so the first image starts after all of
	// them.
	const dirHeader = 6
	const dirEntry = 16
	offset := dirHeader + dirEntry*len(entries)

	for _, e := range entries {
		// 256 is encoded as zero, which is why the field is a single byte.
		dimension := byte(e.size)
		if e.size >= 256 {
			dimension = 0
		}
		buf.WriteByte(dimension)                            // width
		buf.WriteByte(dimension)                            // height
		buf.WriteByte(0)                                    // palette size, zero for truecolour
		buf.WriteByte(0)                                    // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // colour planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}

	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes(), nil
}

// TrayIcon renders the icon for the notification area.
//
// connected adds the green badge, which is the whole point: the tray is where
// people check whether they are protected without opening anything.
func TrayIcon(connected bool) ([]byte, error) {
	var badge color.Color
	if connected {
		badge = Green
	}
	// 32px is what Windows asks for at 100% scaling and downscales cleanly
	// elsewhere; the shell handles the rest.
	return PNG(32, badge)
}
