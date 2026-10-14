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
	if err := validateGIFLimits(config, frameCount, decodedPixels); err != nil {
		return Image{}, err
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode GIF frames: %w", err)
	}
	if animation.Config.Width != config.Width || animation.Config.Height != config.Height || len(animation.Image) != frameCount {
		return Image{}, errors.New("GIF frame metadata changed while decoding")
	}
	frames, err := compositeGIF(animation)
	if err != nil {
		return Image{}, err
	}
	return Image{Pixels: frames[0].Pixels, Format: "gif", Frames: frames, LoopCount: animation.LoopCount}, nil
}

func validateGIFLimits(config image.Config, frameCount int, decodedPixels int64) error {
	canvasPixels := int64(config.Width) * int64(config.Height)
	if frameCount > 1 && canvasPixels*int64(frameCount) > maxGIFCompositePixels {
		return errors.New("animated GIF exceeds the decoded frame memory limit")
	}
	if decodedPixels > maxGIFFramePixels {
		return errors.New("GIF frames exceed the decoded pixel limit")
	}
	return nil
}

func compositeGIF(animation *gif.GIF) ([]Frame, error) {
	background := gifBackground(animation.Config.ColorModel, animation.BackgroundIndex)
	canvas := image.NewNRGBA(image.Rect(0, 0, animation.Config.Width, animation.Config.Height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	frames := make([]Frame, 0, len(animation.Image))
	previous := gifFrameState{}
	for index, source := range animation.Image {
		if err := validateGIFFrame(source, canvas, index); err != nil {
			return nil, err
		}
		canvas, err := restoreGIFCanvas(canvas, previous, index, background)
		if err != nil {
			return nil, err
		}
		current := gifFrameState{bounds: source.Bounds(), disposal: gifDisposal(animation, index)}
		if current.disposal == gif.DisposalPrevious {
			current.canvas = cloneNRGBA(canvas)
		}
		draw.Draw(canvas, source.Bounds(), source, source.Bounds().Min, draw.Over)
		frames = append(frames, Frame{Pixels: cloneNRGBA(canvas), Delay: gifFrameDelay(animation, index)})
		previous = current
	}
	return frames, nil
}

type gifFrameState struct {
	bounds   image.Rectangle
	disposal byte
	canvas   *image.NRGBA
}

func validateGIFFrame(source *image.Paletted, canvas *image.NRGBA, index int) error {
	if source == nil || source.Bounds().Empty() || !source.Bounds().In(canvas.Bounds()) {
		return fmt.Errorf("GIF frame %d has invalid bounds", index+1)
	}
	return nil
}

func restoreGIFCanvas(canvas *image.NRGBA, previous gifFrameState, index int, background color.NRGBA) (*image.NRGBA, error) {
	if index == 0 {
		return canvas, nil
	}
	switch previous.disposal {
	case gif.DisposalBackground:
		draw.Draw(canvas, previous.bounds, &image.Uniform{C: background}, image.Point{}, draw.Src)
	case gif.DisposalPrevious:
		if previous.canvas == nil {
			return nil, fmt.Errorf("GIF frame %d requires an unavailable previous canvas", index)
		}
		return previous.canvas, nil
	}
	return canvas, nil
}

func gifDisposal(animation *gif.GIF, index int) byte {
	if index >= len(animation.Disposal) {
		return gif.DisposalNone
	}
	return animation.Disposal[index]
}

func gifFrameDelay(animation *gif.GIF, index int) time.Duration {
	if index >= len(animation.Delay) || animation.Delay[index] <= 0 {
		return 100 * time.Millisecond
	}
	return time.Duration(animation.Delay[index]) * 10 * time.Millisecond
}

func inspectGIF(data []byte, width, height int) (int, int64, error) {
	position, err := inspectGIFHeader(data, width, height)
	if err != nil {
		return 0, 0, err
	}
	inspection := gifInspection{width: width, height: height}
	for position < len(data) {
		marker := data[position]
		position++
		var err error
		switch marker {
		case 0x2c:
			position, err = inspectGIFImage(data, position, &inspection)
		case 0x21:
			position, err = inspectGIFExtension(data, position)
		case 0x3b:
			if inspection.frameCount == 0 {
				return 0, 0, errors.New("GIF contains no frames")
			}
			return inspection.frameCount, inspection.totalPixels, nil
		default:
			return 0, 0, fmt.Errorf("unexpected GIF block marker 0x%02x", marker)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	return 0, 0, io.ErrUnexpectedEOF
}

type gifInspection struct {
	width       int
	height      int
	frameCount  int
	totalPixels int64
}

func inspectGIFHeader(data []byte, width, height int) (int, error) {
	if len(data) < 13 {
		return 0, io.ErrUnexpectedEOF
	}
	if string(data[:3]) != "GIF" {
		return 0, errors.New("invalid GIF signature")
	}
	if int(binary.LittleEndian.Uint16(data[6:8])) != width || int(binary.LittleEndian.Uint16(data[8:10])) != height {
		return 0, errors.New("invalid logical screen dimensions")
	}
	position := 13
	packed := data[10]
	if packed&0x80 != 0 {
		position += 3 * (1 << ((packed & 0x07) + 1))
	}
	if position > len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	return position, nil
}

func inspectGIFImage(data []byte, position int, inspection *gifInspection) (int, error) {
	if position+9 > len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	descriptor := data[position : position+9]
	left := int(binary.LittleEndian.Uint16(descriptor[0:2]))
	top := int(binary.LittleEndian.Uint16(descriptor[2:4]))
	frameWidth := int(binary.LittleEndian.Uint16(descriptor[4:6]))
	frameHeight := int(binary.LittleEndian.Uint16(descriptor[6:8]))
	if frameWidth <= 0 || frameHeight <= 0 || left+frameWidth > inspection.width || top+frameHeight > inspection.height {
		return 0, errors.New("frame lies outside the logical screen")
	}
	inspection.frameCount++
	if inspection.frameCount > maxGIFFrames {
		return 0, errors.New("GIF exceeds the frame count limit")
	}
	inspection.totalPixels += int64(frameWidth) * int64(frameHeight)
	if inspection.totalPixels > maxGIFFramePixels {
		return 0, errors.New("GIF frames exceed the decoded pixel limit")
	}
	position += 9
	if descriptor[8]&0x80 != 0 {
		position += 3 * (1 << ((descriptor[8] & 0x07) + 1))
	}
	if position >= len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	position++
	return skipGIFSubBlocks(data, position)
}

func inspectGIFExtension(data []byte, position int) (int, error) {
	if position >= len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	position++
	return skipGIFSubBlocks(data, position)
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
