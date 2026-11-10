package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/rwcarlsen/goexif/exif"
)

// hashNameRE splits a stored name into its stem, its hash and its extension.
var hashNameRE = regexp.MustCompile(`^(.*)-([0-9a-f]{8})(\.[^.]*)?$`)

// noiseImage builds an image with a smooth pattern. It compresses well, so it
// fits the paths where the bytes are few and the pixels are many.
func noiseImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8(x * 7 % 251),
				G: uint8(y * 13 % 241),
				B: uint8((x + y) * 3 % 239),
				A: 255,
			})
		}
	}
	return img
}

// randomImage builds an image whose pixels do not compress away. A linear
// gradient compresses far too well to be a heavy fixture.
func randomImage(w, h int) *image.NRGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8(rng.Uint32()),
				G: uint8(rng.Uint32()),
				B: uint8(rng.Uint32()),
				A: 255,
			})
		}
	}
	return img
}

// encodePNG encodes a fixture as a PNG.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png fixture: %v", err)
	}
	return buf.Bytes()
}

// encodeJPEG encodes a fixture as a JPEG.
func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg fixture: %v", err)
	}
	return buf.Bytes()
}

// encodeGIF encodes a fixture as a GIF. The GIF encoder takes a paletted
// image, so the fixture is quantized to black and white.
func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()
	b := img.Bounds()
	pal := image.NewPaletted(b, color.Palette{color.Black, color.White})
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if (r+g+bl)/3 < 0x8000 {
				pal.Set(x, y, color.Black)
			} else {
				pal.Set(x, y, color.White)
			}
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, pal, nil); err != nil {
		t.Fatalf("encode gif fixture: %v", err)
	}
	return buf.Bytes()
}

// decodeConfig probes the format and the size of an encoded image.
func decodeConfig(t *testing.T, data []byte) (image.Config, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg, format
}

// decodeImage decodes an encoded image.
func decodeImage(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	return img
}

// bandImage draws an image whose top half is top and whose bottom half is
// bottom.
func bandImage(w, h int, top, bottom color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		c := bottom
		if y < h/2 {
			c = top
		}
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// rotateCCW rotates an image by 90 degrees counter-clockwise. It is written
// out on purpose: the test must not reuse the implementation it checks.
func rotateCCW(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(y, b.Dx()-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// withOrientation inserts a minimal APP1 EXIF segment carrying the given
// orientation into a JPEG.
func withOrientation(data []byte, orientation int) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("II")
	tiff.Write([]byte{0x2A, 0x00})             // little-endian byte order, magic 42
	tiff.Write([]byte{0x08, 0x00, 0x00, 0x00}) // the first IFD starts at offset 8
	tiff.Write([]byte{0x01, 0x00})             // one entry
	tiff.Write([]byte{0x12, 0x01})             // tag 0x0112: Orientation
	tiff.Write([]byte{0x03, 0x00})             // type 3: SHORT
	tiff.Write([]byte{0x01, 0x00, 0x00, 0x00}) // one value
	tiff.Write([]byte{byte(orientation), 0x00, 0x00, 0x00})
	tiff.Write([]byte{0x00, 0x00, 0x00, 0x00}) // no second IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := make([]byte, 0, len(payload)+4)
	segment = append(segment, 0xFF, 0xE1)
	segment = binary.BigEndian.AppendUint16(segment, uint16(len(payload)+2))
	segment = append(segment, payload...)

	out := make([]byte, 0, len(data)+len(segment))
	out = append(out, data[:2]...) // the segment goes right behind SOI
	out = append(out, segment...)
	out = append(out, data[2:]...)
	return out
}

// readOrientation reads the orientation back, so that a broken fixture cannot
// pass the orientation test unnoticed.
func readOrientation(t *testing.T, data []byte) int {
	t.Helper()
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode exif: %v", err)
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		t.Fatalf("read orientation tag: %v", err)
	}
	value, err := tag.Int(0)
	if err != nil {
		t.Fatalf("read orientation value: %v", err)
	}
	return value
}

// isRed reports whether a pixel is (still) red.
func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 170 && g>>8 < 90 && b>>8 < 90
}

// isBlue reports whether a pixel is (still) blue.
func isBlue(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return b>>8 > 170 && r>>8 < 90 && g>>8 < 90
}

func TestCompressPNG(t *testing.T) {
	data := encodePNG(t, noiseImage(2000, 1000))

	out, changed, err := Compress("noise.png", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too large")
	}
	cfg, format := decodeConfig(t, out)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	if edge := max(cfg.Width, cfg.Height); edge > 1600 {
		t.Fatalf("long edge = %d, want at most 1600", edge)
	}
}

func TestCompressJPEG(t *testing.T) {
	data := encodeJPEG(t, noiseImage(2000, 1500), 90)

	out, changed, err := Compress("noise.jpg", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too large")
	}
	cfg, format := decodeConfig(t, out)
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	if edge := max(cfg.Width, cfg.Height); edge > 1600 {
		t.Fatalf("long edge = %d, want at most 1600", edge)
	}
}

// TestCompressLargeEnoughImageIsReEncoded covers the second trigger: an image
// whose pixels are few but whose bytes are many is re-encoded without being
// scaled.
func TestCompressLargeEnoughImageIsReEncoded(t *testing.T) {
	data := encodePNG(t, randomImage(1600, 1600))
	if len(data) <= compressMaxBytes {
		t.Fatalf("the fixture is %d bytes, want more than %d", len(data), compressMaxBytes)
	}

	out, changed, err := Compress("heavy.png", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too heavy")
	}
	cfg, format := decodeConfig(t, out)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	if cfg.Width != 1600 || cfg.Height != 1600 {
		t.Fatalf("size = %dx%d, want 1600x1600: the image fits and must not be scaled", cfg.Width, cfg.Height)
	}
}

// TestCompressHeavyJPEGGetsSmaller checks that the re-encoding is worth its
// bytes: a JPEG written with a high quality is written again with a lower one.
func TestCompressHeavyJPEGGetsSmaller(t *testing.T) {
	data := encodeJPEG(t, randomImage(1600, 1600), 100)
	if len(data) <= compressMaxBytes {
		t.Fatalf("the fixture is %d bytes, want more than %d", len(data), compressMaxBytes)
	}

	out, changed, err := Compress("heavy.jpg", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too heavy")
	}
	cfg, format := decodeConfig(t, out)
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	if cfg.Width != 1600 || cfg.Height != 1600 {
		t.Fatalf("size = %dx%d, want 1600x1600: the image fits and must not be scaled", cfg.Width, cfg.Height)
	}
	if len(out) >= len(data) {
		t.Fatalf("the result is %d bytes, want less than the %d bytes it started with", len(out), len(data))
	}
}

func TestCompressSmallImageIsUntouched(t *testing.T) {
	data := encodePNG(t, noiseImage(64, 48))

	out, changed, err := Compress("small.png", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for a small image")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestCompressGIFIsUntouched(t *testing.T) {
	data := encodeGIF(t, noiseImage(300, 200))

	out, changed, err := Compress("animation.gif", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for a GIF")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestCompressNonImageIsUntouched(t *testing.T) {
	data := []byte("this is not an image at all")

	out, changed, err := Compress("notes.txt", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for data that is not an image")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestCompressKeepsPNGTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			if x < 1000 {
				img.Set(x, y, color.NRGBA{})
				continue
			}
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	data := encodePNG(t, img)

	out, _, err := Compress("transparent.png", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	got := decodeImage(t, out)
	_, _, _, alpha := got.At(50, 500).RGBA()
	if alpha != 0 {
		t.Fatalf("alpha = %d, want 0: the transparent half must stay transparent", alpha)
	}
}

// TestCompressJPEGOrientation checks that the EXIF orientation is applied
// before the image is scaled.
//
// The stored picture is the wanted picture turned 90 degrees counter-clockwise,
// so the wanted picture has red on top and blue at the bottom. The file claims
// orientation 6, which means "rotate 90 degrees clockwise to show it" — so the
// red band has to come back on top.
func TestCompressJPEGOrientation(t *testing.T) {
	wanted := bandImage(1400, 1800, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255})
	stored := rotateCCW(wanted)
	data := withOrientation(encodeJPEG(t, stored, 100), 6)
	if got := readOrientation(t, data); got != 6 {
		t.Fatalf("the fixture carries orientation %d, want 6", got)
	}

	out, changed, err := Compress("photo.jpg", data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too large")
	}
	cfg, format := decodeConfig(t, out)
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	if edge := max(cfg.Width, cfg.Height); edge != 1600 {
		t.Fatalf("long edge = %d, want 1600", edge)
	}
	if cfg.Height <= cfg.Width {
		t.Fatalf("the image is %dx%d, want it upright: the stored one was sideways", cfg.Width, cfg.Height)
	}
	got := decodeImage(t, out)
	if !isRed(got.At(cfg.Width/2, cfg.Height/10)) {
		t.Fatal("the top of the picture is not red, want the red band on top")
	}
	if !isBlue(got.At(cfg.Width/2, cfg.Height*9/10)) {
		t.Fatal("the bottom of the picture is not blue, want the blue band at the bottom")
	}
}

// TestCompressJPEGOrientationDiffers checks that the orientation really is
// read: the same stored picture without the EXIF tag keeps its stored
// orientation, so the two results cannot be the same.
func TestCompressJPEGOrientationDiffers(t *testing.T) {
	wanted := bandImage(1400, 1800, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255})
	stored := rotateCCW(wanted)
	plain := encodeJPEG(t, stored, 100)

	rotated, _, err := Compress("photo.jpg", withOrientation(plain, 6))
	if err != nil {
		t.Fatalf("compress with orientation: %v", err)
	}
	upright, _, err := Compress("photo.jpg", plain)
	if err != nil {
		t.Fatalf("compress without orientation: %v", err)
	}

	rotatedCfg, _ := decodeConfig(t, rotated)
	uprightCfg, _ := decodeConfig(t, upright)
	if rotatedCfg.Width == uprightCfg.Width && rotatedCfg.Height == uprightCfg.Height {
		t.Fatalf("both results are %dx%d, want the oriented one to be turned", uprightCfg.Width, uprightCfg.Height)
	}
	// Without the tag the stored picture stays as it is: the red band is on the
	// left, the blue one on the right.
	got := decodeImage(t, upright)
	if !isRed(got.At(uprightCfg.Width/10, uprightCfg.Height/2)) {
		t.Fatal("the left of the picture is not red, want the stored band untouched")
	}
	if !isBlue(got.At(uprightCfg.Width*9/10, uprightCfg.Height/2)) {
		t.Fatal("the right of the picture is not blue, want the stored band untouched")
	}
}

func TestResizeSmallImageIsUntouched(t *testing.T) {
	data := encodePNG(t, noiseImage(400, 300))

	out, changed, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for an image that already fits")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestResizeLargeImage(t *testing.T) {
	data := encodePNG(t, noiseImage(2000, 1000))

	out, changed, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too large")
	}
	cfg, format := decodeConfig(t, out)
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
	if cfg.Width != 800 {
		t.Fatalf("width = %d, want 800", cfg.Width)
	}
	if cfg.Height != 400 {
		t.Fatalf("height = %d, want 400", cfg.Height)
	}
}

func TestResizeJPEGKeepsItsFormat(t *testing.T) {
	data := encodeJPEG(t, noiseImage(2000, 1000), 90)

	out, changed, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an image that is too large")
	}
	cfg, format := decodeConfig(t, out)
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	if edge := max(cfg.Width, cfg.Height); edge != 800 {
		t.Fatalf("long edge = %d, want 800", edge)
	}
}

func TestResizeKeepsPNGTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			if x < 1000 {
				img.Set(x, y, color.NRGBA{})
				continue
			}
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	data := encodePNG(t, img)

	out, _, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	got := decodeImage(t, out)
	_, _, _, alpha := got.At(50, 500).RGBA()
	if alpha != 0 {
		t.Fatalf("alpha = %d, want 0: the transparent half must stay transparent", alpha)
	}
}

func TestResizeGIFIsUntouched(t *testing.T) {
	data := encodeGIF(t, noiseImage(300, 200))

	out, changed, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for a GIF")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestResizeNonImageIsUntouched(t *testing.T) {
	data := []byte("this is not an image at all")

	out, changed, err := Resize(data, 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false for data that is not an image")
	}
	if !bytes.Equal(out, data) {
		t.Fatal("the bytes changed, want them returned as they are")
	}
}

func TestResizeWithoutMaxEdgeIsUntouched(t *testing.T) {
	data := encodePNG(t, noiseImage(2000, 1000))

	for _, maxEdge := range []int{0, -1} {
		out, changed, err := Resize(data, maxEdge)
		if err != nil {
			t.Fatalf("resize with maxEdge %d: %v", maxEdge, err)
		}
		if changed {
			t.Fatalf("changed = true for maxEdge %d, want false", maxEdge)
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("the bytes changed for maxEdge %d, want them returned as they are", maxEdge)
		}
	}
}

func TestHashedNameIsStable(t *testing.T) {
	mtime := time.UnixMilli(1760000000123).UTC()
	name := HashedName("图片相关.png", 2176428, mtime)

	if again := HashedName("图片相关.png", 2176428, mtime); again != name {
		t.Fatalf("name = %q, want %q: the same inputs must give the same name", again, name)
	}
	if other := HashedName("图片相关2.png", 2176428, mtime); other == name {
		t.Fatal("the name did not change, want a different one for a different name")
	}
	if other := HashedName("图片相关.png", 2176429, mtime); other == name {
		t.Fatal("the name did not change, want a different one for a different size")
	}
	if other := HashedName("图片相关.png", 2176428, mtime.Add(time.Millisecond)); other == name {
		t.Fatal("the name did not change, want a different one for a different time")
	}
	if !strings.HasSuffix(name, ".png") {
		t.Fatalf("name = %q, want the extension to be kept", name)
	}
	m := hashNameRE.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("name = %q, want it to look like stem-hash.ext", name)
	}
	if m[1] != "图片相关" {
		t.Fatalf("stem = %q, want %q", m[1], "图片相关")
	}
}

func TestHashedNameCleansTheStem(t *testing.T) {
	name := HashedName("../../etc/pa:ss\nwd.png", 42, time.UnixMilli(1760000000123))

	if strings.ContainsAny(name, `/\:`) {
		t.Fatalf("name = %q, want no path separator and no colon", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			t.Fatalf("name = %q, want no control character", name)
		}
	}
	m := hashNameRE.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("name = %q, want it to look like stem-hash.ext", name)
	}
	if m[1] != "passwd" {
		t.Fatalf("stem = %q, want %q", m[1], "passwd")
	}
}

func TestHashedNameTruncatesTheStem(t *testing.T) {
	name := HashedName(strings.Repeat("名", 100)+".png", 42, time.UnixMilli(1760000000123))

	m := hashNameRE.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("name = %q, want it to look like stem-hash.ext", name)
	}
	if n := len([]rune(m[1])); n > maxStemRunes {
		t.Fatalf("the stem has %d runes, want at most %d", n, maxStemRunes)
	}
}

func TestHashedNameUsesTheDisplayName(t *testing.T) {
	name := HashedName(`a\b/photo.png`, 42, time.UnixMilli(1760000000123))

	m := hashNameRE.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("name = %q, want it to look like stem-hash.ext", name)
	}
	if m[1] != DisplayName(`a\b/photo.png`)[:len("photo")] {
		t.Fatalf("stem = %q, want it to be the display name without the extension", m[1])
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "path is dropped", in: "../../etc/passwd", want: "passwd"},
		{name: "windows separator is dropped", in: `a\b.txt`, want: "b.txt"},
		{name: "chinese is kept", in: "图片相关.png", want: "图片相关.png"},
		{name: "spaces are kept", in: "my file.png", want: "my file.png"},
		{name: "parent directory is empty", in: "..", want: ""},
		{name: "current directory is empty", in: ".", want: ""},
		{name: "separator only is empty", in: "/", want: ""},
		{name: "control characters are dropped", in: "na\x00me.txt", want: "name.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DisplayName(tc.in); got != tc.want {
				t.Fatalf("DisplayName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDisplayNameHasNoPath(t *testing.T) {
	got := DisplayName("../../etc/passwd")
	if strings.Contains(got, "/") {
		t.Fatalf("DisplayName = %q, want no path separator", got)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("DisplayName = %q, want no parent directory", got)
	}
}
