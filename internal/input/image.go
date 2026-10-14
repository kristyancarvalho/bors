package input

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

const maxInputBytes = 64 << 20
const maxImagePixels = 40_000_000
const maxImageSide = 32_768

type Image struct {
	Pixels image.Image
	Format string
	PNG    []byte
}

func Load(reader io.Reader) (Image, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("read image: %w", err)
	}
	if len(data) == 0 {
		return Image{}, errors.New("image input is empty")
	}
	if len(data) > maxInputBytes {
		return Image{}, fmt.Errorf("image input exceeds the %d MiB limit", maxInputBytes>>20)
	}
	return decode(bytes.NewReader(data), data)
}

func LoadFile(file *os.File) (Image, error) {
	if file == nil {
		return Image{}, errors.New("image file is nil")
	}
	info, err := file.Stat()
	if err != nil {
		return Image{}, fmt.Errorf("inspect image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Image{}, errors.New("image path is not a regular file")
	}
	if info.Size() == 0 {
		return Image{}, errors.New("image input is empty")
	}
	if info.Size() > maxInputBytes {
		return Image{}, fmt.Errorf("image input exceeds the %d MiB limit", maxInputBytes>>20)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Image{}, fmt.Errorf("seek image: %w", err)
	}
	decoded, err := decode(file, nil)
	if err != nil {
		return Image{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Image{}, fmt.Errorf("rewind image: %w", err)
	}
	return decoded, nil
}

func decode(reader io.Reader, original []byte) (Image, error) {
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		return Image{}, fmt.Errorf("decode image header: %w", err)
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return Image{}, fmt.Errorf("unsupported image format %q", format)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxImageSide || config.Height > maxImageSide || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return Image{}, fmt.Errorf("image dimensions %dx%d exceed the supported limits", config.Width, config.Height)
	}
	var pixels image.Image
	var decodedFormat string
	if original != nil {
		pixels, decodedFormat, err = image.Decode(bytes.NewReader(original))
	} else {
		if seeker, ok := reader.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return Image{}, fmt.Errorf("rewind image before decoding: %w", err)
			}
		}
		pixels, decodedFormat, err = image.Decode(reader)
	}
	if err != nil {
		return Image{}, fmt.Errorf("decode image: %w", err)
	}
	if decodedFormat != format {
		return Image{}, errors.New("image format changed while decoding")
	}
	result := Image{Pixels: pixels, Format: format}
	if format == "png" && original != nil {
		result.PNG = original
	}
	return result, nil
}
