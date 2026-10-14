package terminal

import (
	"errors"
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type Size struct {
	Columns   int
	Rows      int
	CellRatio float64
}

func IsTerminal(file *os.File) bool {
	return file != nil && term.IsTerminal(int(file.Fd()))
}

func GetSize(file *os.File) (Size, error) {
	if file == nil || !IsTerminal(file) {
		return Size{}, errors.New("stdout is not an interactive terminal")
	}
	winsize, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return Size{}, fmt.Errorf("get terminal size: %w", err)
	}
	columns := int(winsize.Col)
	rows := int(winsize.Row)
	if columns <= 0 || rows <= 0 {
		return Size{}, errors.New("terminal reported invalid dimensions")
	}
	cellRatio := 0.5
	if winsize.Xpixel > 0 && winsize.Ypixel > 0 {
		cellWidth := float64(winsize.Xpixel) / float64(columns)
		cellHeight := float64(winsize.Ypixel) / float64(rows)
		if cellWidth > 0 && cellHeight > 0 {
			cellRatio = cellWidth / cellHeight
		}
	}
	if math.IsNaN(cellRatio) || math.IsInf(cellRatio, 0) || cellRatio <= 0 {
		cellRatio = 0.5
	}
	return Size{Columns: columns, Rows: rows, CellRatio: cellRatio}, nil
}
