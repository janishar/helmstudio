package media

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // registers GIF for DecodeConfig and Decode
	"image/jpeg"
	_ "image/png" // registers PNG
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotImage means a thumbnail was asked of something the standard library
// cannot decode. Video and audio thumbnails wait for the ffmpeg decision
// (docs/decisions.md M4 Q13).
var ErrNotImage = errors.New("not an image the standard library decodes (png, jpeg, gif)")

// ProbeImage reads an image's dimensions without decoding its pixels.
func ProbeImage(path string) (width, height int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, ErrNotImage
	}
	return cfg.Width, cfg.Height, nil
}

// ThumbName is the derived file for a variant (02 §5: thumb-320).
func ThumbName(width int) string { return fmt.Sprintf("thumb-%d.jpg", width) }

// Thumb returns the derived thumbnail of an image blob, making it once: at
// most width pixels wide, never enlarged, JPEG. It returns the absolute path,
// its path relative to the derived root, and whether it was just made.
func (e *Engine) Thumb(blobRel, sha string, width int) (abs, rel string, made bool, err error) {
	rel = filepath.ToSlash(filepath.Join(sha, ThumbName(width)))
	abs = filepath.Join(e.Derived, filepath.FromSlash(rel))
	if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() {
		return abs, rel, false, nil
	}
	src, err := os.Open(e.BlobPath(blobRel))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", false, err
		}
		return "", "", false, fmt.Errorf("opening the blob: %w", err)
	}
	defer src.Close()
	img, _, err := image.Decode(src)
	if err != nil {
		return "", "", false, ErrNotImage
	}
	out := scale(img, width)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", "", false, err
	}
	tmp := abs + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", "", false, err
	}
	if err := jpeg.Encode(f, out, &jpeg.Options{Quality: 85}); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return "", "", false, fmt.Errorf("encoding the thumbnail: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", "", false, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return "", "", false, err
	}
	return abs, rel, true, nil
}

// scale shrinks img to width with a box filter; the standard library has no
// resampler, and a thumbnail needs no better.
func scale(img image.Image, width int) image.Image {
	b := img.Bounds()
	if b.Dx() <= width {
		width = b.Dx()
	}
	height := max(1, b.Dy()*width/max(1, b.Dx()))
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	fx := float64(b.Dx()) / float64(width)
	fy := float64(b.Dy()) / float64(height)
	for y := 0; y < height; y++ {
		y0 := b.Min.Y + int(float64(y)*fy)
		y1 := min(b.Min.Y+int(float64(y+1)*fy), b.Max.Y)
		for x := 0; x < width; x++ {
			x0 := b.Min.X + int(float64(x)*fx)
			x1 := min(b.Min.X+int(float64(x+1)*fx), b.Max.X)
			var r, g, bl, a, n uint64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			out.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return out
}
