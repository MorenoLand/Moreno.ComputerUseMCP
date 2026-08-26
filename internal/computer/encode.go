package computer

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
)

func EncodeImage(source *image.RGBA, options ScreenshotOptions) ([]byte, string, int, int, error) {
	if options.MaxWidth > 0 && source.Bounds().Dx() > options.MaxWidth {
		source = resizeNearest(source, options.MaxWidth)
	}
	var encoded bytes.Buffer
	mimeType := "image/jpeg"
	switch options.Format {
	case "jpeg":
		if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: options.Quality}); err != nil {
			return nil, "", 0, 0, err
		}
	case "png":
		mimeType = "image/png"
		if err := png.Encode(&encoded, source); err != nil {
			return nil, "", 0, 0, err
		}
	default:
		return nil, "", 0, 0, &invalidImageFormatError{format: options.Format}
	}
	return encoded.Bytes(), mimeType, source.Bounds().Dx(), source.Bounds().Dy(), nil
}

type invalidImageFormatError struct {
	format string
}

func (e *invalidImageFormatError) Error() string {
	return "unsupported screenshot format: " + e.format
}

func resizeNearest(source *image.RGBA, width int) *image.RGBA {
	height := source.Bounds().Dy() * width / source.Bounds().Dx()
	if height < 1 {
		height = 1
	}
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := y * source.Bounds().Dy() / height
		for x := 0; x < width; x++ {
			sourceX := x * source.Bounds().Dx() / width
			sourceOffset := sourceY*source.Stride + sourceX*4
			targetOffset := y*result.Stride + x*4
			copy(result.Pix[targetOffset:targetOffset+4], source.Pix[sourceOffset:sourceOffset+4])
		}
	}
	return result
}
