package input

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"time"
)

const maxGIFFrames = 1024
const maxGIFFramePixels = 40_000_000
const maxGIFCompositePixels = 20_000_000

func decodeGIF(data []byte, config image.Config) (Image, error) {
	frameCount, decodedPixels, err := inspectGIF(data, config.Width, config.Height)
	if err != nil {
		return Image{}, fmt.Errorf("inspect GIF frames: %w", err)
	}
	canvasPixels := int64(config.Width) * int64(config.Height)
	if frameCount > 1 && canvasPixels*int64(frameCount) > maxGIFCompositePixels {
		return Image{}, errors.New("animated GIF exceeds the decoded frame memory limit")
	}
	if decodedPixels > maxGIFFramePixels {
		return Image{}, errors.New("GIF frames exceed the decoded pixel limit")
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode GIF frames: %w", err)
	}
	if animation.Config.Width != config.Width || animation.Config.Height != config.Height || len(animation.Image) != frameCount {
		return Image{}, errors.New("GIF frame metadata changed while decoding")
	}
	background := gifBackground(animation.Config.ColorModel, animation.BackgroundIndex)
	canvas := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
	for y := 0; y < canvas.Bounds().Dy(); y++ {
		for x := 0; x < canvas.Bounds().Dx(); x++ {
			canvas.SetNRGBA(x, y, background)
		}
	}
	frames := make([]Frame, 0, frameCount)
	var previousBounds image.Rectangle
	var previousDisposal byte
	var previousCanvas *image.NRGBA
	for index, source := range animation.Image {
		if source == nil || source.Bounds().Empty() || !source.Bounds().In(canvas.Bounds()) {
			return Image{}, fmt.Errorf("GIF frame %d has invalid bounds", index+1)
		}
		if index > 0 {
			switch previousDisposal {
			case gif.DisposalBackground:
				draw.Draw(canvas, previousBounds, &image.Uniform{C: background}, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if previousCanvas == nil {
					return Image{}, fmt.Errorf("GIF frame %d requires an unavailable previous canvas", index)
				}
				canvas = previousCanvas
			}
		}
		var savedCanvas *image.NRGBA
		if index < len(animation.Disposal) && animation.Disposal[index] == gif.DisposalPrevious {
			savedCanvas = cloneNRGBA(canvas)
		}
		draw.Draw(canvas, source.Bounds(), source, source.Bounds().Min, draw.Over)
		delay := 100 * time.Millisecond
		if index < len(animation.Delay) && animation.Delay[index] > 0 {
			delay = time.Duration(animation.Delay[index]) * 10 * time.Millisecond
		}
		frames = append(frames, Frame{Pixels: cloneNRGBA(canvas), Delay: delay})
		previousBounds = source.Bounds()
		previousDisposal = gif.DisposalNone
		if index < len(animation.Disposal) {
			previousDisposal = animation.Disposal[index]
		}
		previousCanvas = savedCanvas
	}
	if len(frames) == 0 {
		return Image{}, errors.New("GIF contains no frames")
	}
	return Image{Pixels: frames[0].Pixels, Format: "gif", Frames: frames, LoopCount: animation.LoopCount}, nil
}

func inspectGIF(data []byte, width, height int) (int, int64, error) {
	if len(data) < 13 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	if string(data[:3]) != "GIF" {
		return 0, 0, errors.New("invalid GIF signature")
	}
	logicalWidth := int(binary.LittleEndian.Uint16(data[6:8]))
	logicalHeight := int(binary.LittleEndian.Uint16(data[8:10]))
	if logicalWidth != width || logicalHeight != height {
		return 0, 0, errors.New("invalid logical screen dimensions")
	}
	packed := data[10]
	position := 13
	if packed&0x80 != 0 {
		position += 3 * (1 << ((packed & 0x07) + 1))
	}
	if position > len(data) {
		return 0, 0, io.ErrUnexpectedEOF
	}
	frameCount := 0
	var totalPixels int64
	for position < len(data) {
		marker := data[position]
		position++
		switch marker {
		case 0x2c:
			if position+9 > len(data) {
				return 0, 0, io.ErrUnexpectedEOF
			}
			descriptor := data[position : position+9]
			left := int(binary.LittleEndian.Uint16(descriptor[0:2]))
			top := int(binary.LittleEndian.Uint16(descriptor[2:4]))
			frameWidth := int(binary.LittleEndian.Uint16(descriptor[4:6]))
			frameHeight := int(binary.LittleEndian.Uint16(descriptor[6:8]))
			if frameWidth <= 0 || frameHeight <= 0 || left+frameWidth > width || top+frameHeight > height {
				return 0, 0, errors.New("frame lies outside the logical screen")
			}
			frameCount++
			if frameCount > maxGIFFrames {
				return 0, 0, errors.New("GIF exceeds the frame count limit")
			}
			totalPixels += int64(frameWidth) * int64(frameHeight)
			if totalPixels > maxGIFFramePixels {
				return 0, 0, errors.New("GIF frames exceed the decoded pixel limit")
			}
			position += 9
			if descriptor[8]&0x80 != 0 {
				position += 3 * (1 << ((descriptor[8] & 0x07) + 1))
			}
			if position >= len(data) {
				return 0, 0, io.ErrUnexpectedEOF
			}
			position++
			var err error
			position, err = skipGIFSubBlocks(data, position)
			if err != nil {
				return 0, 0, err
			}
		case 0x21:
			if position >= len(data) {
				return 0, 0, io.ErrUnexpectedEOF
			}
			position++
			var err error
			position, err = skipGIFSubBlocks(data, position)
			if err != nil {
				return 0, 0, err
			}
		case 0x3b:
			if frameCount == 0 {
				return 0, 0, errors.New("GIF contains no frames")
			}
			return frameCount, totalPixels, nil
		default:
			return 0, 0, fmt.Errorf("unexpected GIF block marker 0x%02x", marker)
		}
	}
	return 0, 0, io.ErrUnexpectedEOF
}

func skipGIFSubBlocks(data []byte, position int) (int, error) {
	for position < len(data) {
		blockSize := int(data[position])
		position++
		if blockSize == 0 {
			return position, nil
		}
		if position+blockSize > len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		position += blockSize
	}
	return 0, io.ErrUnexpectedEOF
}

func gifBackground(model color.Model, index byte) color.NRGBA {
	palette, ok := model.(color.Palette)
	if !ok || int(index) >= len(palette) {
		return color.NRGBA{}
	}
	return color.NRGBAModel.Convert(palette[index]).(color.NRGBA)
}

func cloneNRGBA(source *image.NRGBA) *image.NRGBA {
	clone := image.NewNRGBA(source.Bounds())
	draw.Draw(clone, clone.Bounds(), source, source.Bounds().Min, draw.Src)
	return clone
}
