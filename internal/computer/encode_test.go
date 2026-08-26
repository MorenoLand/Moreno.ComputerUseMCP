package computer

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestEncodeImageFormats(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 4, 2))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	for _, test := range []struct {
		format   string
		mimeType string
		decode   func([]byte) (image.Image, error)
	}{
		{format: "png", mimeType: "image/png", decode: func(data []byte) (image.Image, error) { return png.Decode(bytes.NewReader(data)) }},
		{format: "jpeg", mimeType: "image/jpeg", decode: func(data []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(data)) }},
	} {
		data, mimeType, width, height, err := EncodeImage(source, ScreenshotOptions{Format: test.format, MaxWidth: 2, Quality: 80})
		if err != nil {
			t.Fatalf("%s: %v", test.format, err)
		}
		if mimeType != test.mimeType || width != 2 || height != 1 {
			t.Fatalf("%s metadata = %s %dx%d", test.format, mimeType, width, height)
		}
		decoded, err := test.decode(data)
		if err != nil {
			t.Fatalf("%s decode: %v", test.format, err)
		}
		if decoded.Bounds().Dx() != 2 || decoded.Bounds().Dy() != 1 {
			t.Fatalf("%s dimensions = %v", test.format, decoded.Bounds())
		}
	}
}

func TestEncodeImageRejectsUnsupportedFormat(t *testing.T) {
	_, _, _, _, err := EncodeImage(image.NewRGBA(image.Rect(0, 0, 1, 1)), ScreenshotOptions{Format: "webp"})
	if err == nil {
		t.Fatal("expected unsupported format error")
	}
}
