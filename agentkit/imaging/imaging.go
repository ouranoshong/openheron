// Package imaging compresses uploaded images and derives the file names the
// uploads are stored under.
//
// The package is a leaf: it depends on the standard library, golang.org/x/image
// and github.com/rwcarlsen/goexif only, and imports no openheron package, so
// any layer may use it.
//
// Two problems are solved here:
//
//   - An upload can be far larger than the model needs. The bytes are scaled
//     down and re-encoded before they are stored.
//
//   - Two uploads can carry the same name. Overwriting the first one makes the
//     history lie — it says "photo.png" while the bytes belong to the second
//     upload — so the stored name carries a hash of the upload's identity.
//
// ★ Everything this package returns to a caller is English: the error strings
// end up in log lines and in the upload response, where a mixed language makes
// "is this a bug or did I misconfigure it?" harder to answer.
package imaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif" // registers the GIF decoder, so a GIF is recognized and passed through
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
)

const (
	// compressMaxEdge is the longest edge an uploaded image is scaled down to.
	compressMaxEdge = 1600
	// compressMaxBytes is the size above which an image is re-encoded even when
	// it is already small enough in pixels.
	compressMaxBytes = 200 * 1024
	// jpegQuality is the quality a re-encoded JPEG is written with.
	jpegQuality = 85
	// maxStemRunes is the longest stem (in runes) a stored file name may have.
	maxStemRunes = 60
	// hashRunes is the number of hex characters of the digest kept in a name.
	hashRunes = 8
)

// Compress scales an uploaded image down and re-encodes it.
//
// The format is decided by the content and never by the name: a PNG that was
// renamed to ".jpg" is still a PNG, and a JPEG that was renamed to ".png" is
// still a JPEG.
//
// Only PNG and JPEG are re-encoded. A GIF is animated and decoding it yields
// its first frame only, so re-encoding it would throw the animation away; WebP
// has no encoder in the standard library. Both are returned unchanged, and so
// is data that is not an image at all — an upload this package cannot improve
// is still an upload, and refusing it here would be a policy decision that
// does not belong to a leaf.
//
// The name is only used to describe a failure; it never decides what happens
// to the bytes.
func Compress(name string, data []byte) (out []byte, changed bool, err error) {
	return recode(name, data, compressMaxEdge, compressMaxBytes)
}

// Resize scales an image down so that its longest edge is at most maxEdge.
//
// maxEdge <= 0 returns the data unchanged. An image that already fits is
// returned unchanged as well: scaling it would cost quality and change the
// bytes for nothing.
//
// The format is decided by the content. Only PNG and JPEG are re-encoded; a
// GIF, a WebP and data that is not an image are returned unchanged, without an
// error.
//
// A JPEG is turned upright before it is scaled — the same orientation handling
// Compress uses, so both paths agree on what the picture looks like.
func Resize(data []byte, maxEdge int) (out []byte, changed bool, err error) {
	return recode("", data, maxEdge, 0)
}

// recode is the shared body of Compress and Resize.
//
// maxEdge <= 0 disables the scaling trigger, maxBytes <= 0 disables the size
// trigger. The bytes are re-encoded when either trigger fires.
func recode(name string, data []byte, maxEdge, maxBytes int) ([]byte, bool, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// ★ Not an image (or a format this build cannot even probe): the bytes
		// are kept as they are.
		return data, false, nil
	}
	if format != "png" && format != "jpeg" {
		// ★ GIF is animated and WebP has no encoder here: re-encoding either
		// one would destroy the animation or fail outright.
		return data, false, nil
	}

	edge := cfg.Width
	if cfg.Height > edge {
		edge = cfg.Height
	}
	scale := maxEdge > 0 && edge > maxEdge
	tooBig := maxBytes > 0 && len(data) > maxBytes
	if !scale && !tooBig {
		return data, false, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("imaging: decode %s image %q: %w", format, name, err)
	}

	img := src
	if format == "jpeg" {
		// ★ The orientation is applied before the scaling. The stored pixels
		// are sideways; scaling them first would bake the rotation into the
		// resampling and blur the picture.
		img = orient(img, jpegOrientation(data))
	}

	if scale {
		w, h := scaledSize(img.Bounds().Dx(), img.Bounds().Dy(), maxEdge)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		// ★ The destination is transparent and the pixels are composited with
		// draw.Over. A white background would turn every transparent pixel of a
		// PNG into an opaque one, and the model would see a white box where the
		// picture used to be empty.
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		img = dst
	}

	var buf bytes.Buffer
	switch format {
	case "png":
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, false, fmt.Errorf("imaging: encode png %q: %w", name, err)
		}
	case "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, false, fmt.Errorf("imaging: encode jpeg %q: %w", name, err)
		}
	}
	return buf.Bytes(), true, nil
}

// scaledSize returns the size an image of w x h is scaled to so that its
// longest edge is exactly maxEdge.
func scaledSize(w, h, maxEdge int) (int, int) {
	if w <= 0 || h <= 0 || maxEdge <= 0 {
		return w, h
	}
	if w >= h {
		nh := int(math.Round(float64(h) * float64(maxEdge) / float64(w)))
		if nh < 1 {
			nh = 1
		}
		return maxEdge, nh
	}
	nw := int(math.Round(float64(w) * float64(maxEdge) / float64(h)))
	if nw < 1 {
		nw = 1
	}
	return nw, maxEdge
}

// jpegOrientation reads the EXIF orientation of a JPEG.
//
// ★ A missing EXIF block, a missing tag and a value outside 1..8 all mean the
// same thing to a viewer: the picture is stored the way it is to be shown.
func jpegOrientation(data []byte) int {
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	value, err := tag.Int(0)
	if err != nil {
		return 1
	}
	if value < 1 || value > 8 {
		return 1
	}
	return value
}

// orient returns the image as it is meant to be shown, for the given EXIF
// orientation.
//
// ★ The transformation is an exact permutation of the pixels, not a resample:
// every EXIF orientation is a multiple of 90 degrees, with a mirror in some
// cases, so each destination pixel has exactly one source pixel. Resampling
// here would blur the picture for nothing — the resample belongs to the
// scaling step, which runs after this one.
//
// The eight values are the ones of the EXIF specification:
//
//	1 normal          2 mirrored horizontally  3 rotated 180°   4 mirrored vertically
//	5 transposed     6 rotated 90° clockwise  7 transversed     8 rotated 90° counter-clockwise
func orient(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	switch orientation {
	case 2: // mirrored horizontally
		return permute(src, w, h, func(x, y, w, h int) (int, int) {
			return w - 1 - x, y
		})
	case 3: // rotated 180°
		return permute(src, w, h, func(x, y, w, h int) (int, int) {
			return w - 1 - x, h - 1 - y
		})
	case 4: // mirrored vertically
		return permute(src, w, h, func(x, y, w, h int) (int, int) {
			return x, h - 1 - y
		})
	case 5: // transposed: the main diagonal
		return permute(src, h, w, func(x, y, w, h int) (int, int) {
			return y, x
		})
	case 6: // rotated 90° clockwise
		return permute(src, h, w, func(x, y, w, h int) (int, int) {
			return y, h - 1 - x
		})
	case 7: // transversed: the secondary diagonal
		return permute(src, h, w, func(x, y, w, h int) (int, int) {
			return w - 1 - y, h - 1 - x
		})
	case 8: // rotated 90° counter-clockwise
		return permute(src, h, w, func(x, y, w, h int) (int, int) {
			return w - 1 - y, x
		})
	}
	return src
}

// permute copies src into a new image of dw x dh, taking the source pixel of
// every destination pixel from the given mapping.
func permute(src image.Image, dw, dh int, at func(x, y, w, h int) (int, int)) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx, sy := at(x, y, b.Dx(), b.Dy())
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// DisplayName turns the name a client sent into a name that can be shown.
//
// ★ Only the directory part and the control characters are dropped. Everything
// else is kept exactly as the client sent it: the display name is what the
// user recognizes, so "my file.png" must not become "my_file.png", and the
// Chinese, the parentheses and the dots are all part of the name.
//
// Both separators are handled, because a Windows client sends "a\b.png" and a
// POSIX client sends "a/b.png". A directory part is never part of a name and
// would be read as a path by whoever reads it.
//
// A name that is empty after the cleaning is returned as an empty string: the
// caller decides what to show then.
func DisplayName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(dropControl, name)
	name = strings.TrimSpace(name)
	// "." and ".." name a directory, not a file.
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// dropControl removes the control characters from a name.
func dropControl(r rune) rune {
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

// HashedName returns the name an uploaded file is stored under.
//
// The name is the display name, the extension and a hash of the upload's
// identity: the raw name, the size and the modification time.
//
// ★ The hash covers the *raw* name, not the cleaned one: it has to record what
// the client actually sent, so that two uploads which differ only in a
// directory part still land on different files.
//
// ★ The zero bytes separate the parts. Without them "ab" + "12" and "a" +
// "b12" would hash to the same thing, and the second upload would overwrite
// the first — which is exactly the history that lies.
//
// The result is stable: the same three inputs always produce the same name.
func HashedName(name string, size int64, mtime time.Time) string {
	stem := DisplayName(name)
	ext := extensionOf(stem)
	stem = strings.TrimSuffix(stem, ext)

	// ★ The stem and the extension become a file name on disk, so the
	// characters Windows rejects are dropped here. Everything else is kept:
	// the hash is appended to the stem and the extension is kept as it was.
	stem = strings.Map(dropReserved, stem)
	ext = strings.Map(dropReserved, ext)
	stem = truncate(stem, maxStemRunes)

	return stem + "-" + hashOf(name, size, mtime) + ext
}

// dropReserved removes the characters that a file name on disk may not carry.
func dropReserved(r rune) rune {
	switch r {
	case ':', '/', '\\':
		return -1
	}
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

// extensionOf returns the extension of a name, the dot included.
//
// ★ A leading dot does not start an extension: ".gitignore" is a name, and
// splitting there would leave an empty stem and a name that starts with the
// hash separator. A trailing dot is no extension either — Windows drops it,
// and keeping it would produce a name that no file system accepts.
func extensionOf(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx <= 0 || idx == len(name)-1 {
		return ""
	}
	return name[idx:]
}

// hashOf returns the short digest that identifies an upload.
//
// ★ The size and the modification time are written as fixed-width big-endian
// numbers, so the digest does not depend on how a number would be spelled in
// some other notation.
func hashOf(name string, size int64, mtime time.Time) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(size))
	h.Write(n[:])
	h.Write([]byte{0})
	binary.BigEndian.PutUint64(n[:], uint64(mtime.UnixMilli()))
	h.Write(n[:])
	return hex.EncodeToString(h.Sum(nil))[:hashRunes]
}

// truncate shortens a string to at most n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
