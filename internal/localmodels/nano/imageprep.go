package nano

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"math"
)

// Preprocessing from FORMATS.md: resize the short side to 145, then crop the
// centre 128 × 128.
const resizeShortSide = 145

// maxDecodePixels refuses images whose header promises more pixels than any
// photo needs, before decoding allocates them.
const maxDecodePixels = 40_000_000

// PrepareImage decodes a PNG or JPEG into the 128 × 128 RGB bytes the image
// model reads. EXIF orientation is not applied, as in System One.
func PrepareImage(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read the image: %w", err)
	}
	if cfg.Width*cfg.Height > maxDecodePixels {
		return nil, fmt.Errorf("the image is %d × %d; the most this reads is %d pixels", cfg.Width, cfg.Height, maxDecodePixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read the image: %w", err)
	}
	return centreCrop(resizeShort(toRGB(img), resizeShortSide), ImageSide), nil
}

// rgbImage is 8-bit RGB, row by row.
type rgbImage struct {
	w, h int
	pix  []byte
}

// toRGB drops alpha without blending, as Pillow's convert("RGB") does.
func toRGB(img image.Image) rgbImage {
	b := img.Bounds()
	out := rgbImage{b.Dx(), b.Dy(), make([]byte, b.Dx()*b.Dy()*3)}
	for y := range out.h {
		for x := range out.w {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := (y*out.w + x) * 3
			out.pix[i], out.pix[i+1], out.pix[i+2] = c.R, c.G, c.B
		}
	}
	return out
}

// resizeShort scales so the short side is short; the long side is truncated,
// as torchvision's Resize(int) computes it.
func resizeShort(img rgbImage, short int) rgbImage {
	w, h := img.w, img.h
	if w <= h {
		h = int(float64(short) * float64(img.h) / float64(img.w))
		w = short
	} else {
		w = int(float64(short) * float64(img.w) / float64(img.h))
		h = short
	}
	return resizeAxis(resizeAxis(img, w, true), h, false)
}

// resizeAxis is Pillow's antialiased bilinear filter along one axis: a
// triangle kernel widened by the scale when shrinking.
func resizeAxis(img rgbImage, size int, horizontal bool) rgbImage {
	in := img.h
	if horizontal {
		in = img.w
	}
	weights := triangleWeights(in, size)
	out := rgbImage{img.w, img.h, nil}
	if horizontal {
		out.w = size
	} else {
		out.h = size
	}
	out.pix = make([]byte, out.w*out.h*3)
	for y := range out.h {
		for x := range out.w {
			pos, other := x, y
			if !horizontal {
				pos, other = y, x
			}
			for ch := range 3 {
				var acc float64
				for _, wt := range weights[pos] {
					sx, sy := wt.src, other
					if !horizontal {
						sx, sy = other, wt.src
					}
					acc += wt.w * float64(img.pix[(sy*img.w+sx)*3+ch])
				}
				out.pix[(y*out.w+x)*3+ch] = clampByte(acc)
			}
		}
	}
	return out
}

type tap struct {
	src int
	w   float64
}

func triangleWeights(in, out int) [][]tap {
	scale := float64(in) / float64(out)
	support := math.Max(scale, 1)
	taps := make([][]tap, out)
	for i := range taps {
		centre := (float64(i) + 0.5) * scale
		lo := max(int(centre-support+0.5), 0)
		hi := min(int(centre+support+0.5), in)
		var total float64
		for j := lo; j < hi; j++ {
			w := math.Max(0, 1-math.Abs((float64(j)-centre+0.5)/support))
			taps[i] = append(taps[i], tap{j, w})
			total += w
		}
		for k := range taps[i] {
			taps[i][k].w /= total
		}
	}
	return taps
}

func clampByte(v float64) byte {
	return byte(math.Min(math.Max(math.Round(v), 0), 255))
}

// centreCrop cuts a side × side square from the middle. torchvision rounds the
// offset with Python's round, half to even: 145 → 128 starts at 8, not 9.
func centreCrop(img rgbImage, side int) []byte {
	top := int(math.RoundToEven(float64(img.h-side) / 2))
	left := int(math.RoundToEven(float64(img.w-side) / 2))
	out := make([]byte, 0, side*side*3)
	for y := top; y < top+side; y++ {
		row := (y*img.w + left) * 3
		out = append(out, img.pix[row:row+side*3]...)
	}
	return out
}
