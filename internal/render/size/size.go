package size

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const maxCells = 1_000_000

type Limits struct {
	Width  int
	Height int
}

func Parse(value string) (Limits, error) {
	if value == "" {
		return Limits{}, errors.New("size must be WIDTHxHEIGHT, WIDTH, or xHEIGHT")
	}
	parts := strings.Split(value, "x")
	if len(parts) > 2 || (len(parts) == 2 && parts[1] == "") {
		return Limits{}, errors.New("size must be WIDTHxHEIGHT, WIDTH, or xHEIGHT")
	}
	if len(parts) == 1 {
		width, err := parsePositive(parts[0])
		if err != nil {
			return Limits{}, err
		}
		return Limits{Width: width}, nil
	}
	var limits Limits
	var err error
	if parts[0] != "" {
		limits.Width, err = parsePositive(parts[0])
		if err != nil {
			return Limits{}, err
		}
	}
	if parts[1] != "" {
		limits.Height, err = parsePositive(parts[1])
		if err != nil {
			return Limits{}, err
		}
	}
	if limits.Width == 0 && limits.Height == 0 {
		return Limits{}, errors.New("size must include a width or height")
	}
	return limits, nil
}

func Fit(sourceWidth, sourceHeight, maxWidth, maxHeight int, cellRatio float64) (int, int, error) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return 0, 0, errors.New("source dimensions must be positive")
	}
	if maxWidth <= 0 || maxHeight <= 0 {
		return 0, 0, errors.New("maximum dimensions must be positive")
	}
	if maxWidth > maxCells || maxHeight > maxCells {
		return 0, 0, fmt.Errorf("requested terminal dimensions exceed the %d-cell safety limit", maxCells)
	}
	if cellRatio <= 0 || math.IsNaN(cellRatio) || math.IsInf(cellRatio, 0) {
		return 0, 0, errors.New("terminal cell aspect ratio must be positive and finite")
	}
	visualRatio := float64(sourceWidth) / float64(sourceHeight)
	cellGridRatio := visualRatio / cellRatio
	w := float64(maxWidth)
	h := w / cellGridRatio
	if h > float64(maxHeight) {
		h = float64(maxHeight)
		w = h * cellGridRatio
	}
	width := int(math.Floor(w + 1e-9))
	height := int(math.Floor(h + 1e-9))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if width > maxWidth {
		width = maxWidth
	}
	if height > maxHeight {
		height = maxHeight
	}
	if int64(width)*int64(height) > maxCells {
		return 0, 0, fmt.Errorf("requested terminal area exceeds the %d-cell safety limit", maxCells)
	}
	return width, height, nil
}

func parsePositive(value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("size dimensions must be positive integers, got %q", value)
	}
	return parsed, nil
}
