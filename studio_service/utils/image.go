// Package utils holds helpers with no service state: checks on uploaded images.
package utils

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"

	_ "golang.org/x/image/webp"
)

const maxPixels = 89_478_485 // Pillow's MAX_IMAGE_PIXELS (the limit agents_service enforces)

// extensions maps a decoder's format name to the file extension we store.
var extensions = map[string]string{"jpeg": "jpg", "png": "png", "webp": "webp"}

// ImageExt sniffs JPEG/PNG/WEBP; error text starts with the label (the 422 detail names the file).
func ImageExt(label string, data []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%s is not a valid JPEG, PNG or WEBP image", label)
	}
	extension, ok := extensions[format]
	if !ok {
		return "", fmt.Errorf("%s is %s; upload JPEG, PNG or WEBP", label, format)
	}
	if config.Width*config.Height > maxPixels {
		return "", fmt.Errorf("%s has too many pixels (max %d)", label, maxPixels)
	}
	return extension, nil
}

// ReadImage reads one uploaded file (at most maxBytes) and checks it is a JPEG, PNG or WEBP image.
func ReadImage(header *multipart.FileHeader, label string, maxBytes int64) ([]byte, string, error) {
	if header.Size > maxBytes {
		return nil, "", fmt.Errorf("%s is larger than %d MB", label, maxBytes>>20)
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("%s is larger than %d MB", label, maxBytes>>20)
	}
	extension, err := ImageExt(label, data)
	return data, extension, err
}

// ContentType returns the media type of an image or zip extension; application/octet-stream for anything else.
func ContentType(extension string) string {
	switch extension {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "zip":
		return "application/zip"
	}
	return "application/octet-stream"
}
