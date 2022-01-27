package scan

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
)

const hotspotPxLen = hotspotSize * hotspotSize
const numHeaderHotspots = 15

// Header is a little like Scanner, but just a ballot original image header, for matching to an unknown ballot scan
type Header struct {
	// coordinates of header in png page
	orect image.Rectangle

	// page draw coordinates from bubbles json
	drawLeftTopRightBot []float64

	pngbytes []byte

	orig image.Image

	origYThresh uint8

	// hotspots are coords within orig.Bounds() ((0,0)-(w,h))
	hotspots     []point
	hotspotSnaps [][hotspotSize * hotspotSize]uint8

	debugi *image.NRGBA
}

func (h *Header) Png() []byte {
	return h.pngbytes
}

func (h *Header) SetOrigImage(pngbytes []byte) error {
	r := bytes.NewReader(pngbytes)
	orig, _, err := image.Decode(r)
	if err != nil {
		return err
	}
	orect := orig.Bounds()
	if orect.Min.X != 0 || orect.Min.Y != 0 {
		return fmt.Errorf("nonzero origin for original pic. WAT?\n")
	}
	h.orig = orig
	h.origYThresh = imageYHist(orig, orect)
	h.hotspots, h.hotspotSnaps = findImageHotspots(numHeaderHotspots, h.orig, h.origYThresh)
	h.pngbytes = pngbytes
	return nil
}

func (h *Header) HotspotDebugString() string {
	out := strings.Builder{}
	fmt.Fprintf(&out, "ythresh=%d ", h.origYThresh)
	for i, hs := range h.hotspots {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(&out, "(%d,%d)", hs.x, hs.y)
	}
	return out.String()
}

// Build an image of the found header hotspot regions
func (h *Header) HotspotDebugImage() *image.RGBA {
	gridHeight := int(math.Floor(math.Sqrt(float64(len(h.hotspots)))))
	gridWidth := len(h.hotspots) / gridHeight
	for gridHeight*gridWidth < len(h.hotspots) {
		gridWidth++
	}
	gridHeight *= 2 // stack, top block orig, second block thresh
	pxWidth := (hotspotSize * gridWidth) + (gridWidth - 1)
	pxHeight := (hotspotSize * gridHeight) + (gridHeight - 1)
	outrect := image.Rect(0, 0, pxWidth, pxHeight)
	out := image.NewRGBA(outrect)
	gx := 0
	gy := 0

	// orig image
	for _, spot := range h.hotspots {
		mx := spot.x - (hotspotSize / 2)
		my := spot.y - (hotspotSize / 2)
		for iy := 0; iy < hotspotSize; iy++ {
			oy := (gy * (hotspotSize + 1)) + iy
			for ix := 0; ix < hotspotSize; ix++ {
				ox := (gx * (hotspotSize + 1)) + ix
				sc := h.orig.At(mx+ix, my+iy)
				out.Set(ox, oy, sc)
			}
		}

		gx++
		if gx >= gridWidth {
			gx = 0
			gy++
		}
	}
	gy++
	gx = 0
	for _, spt := range h.hotspotSnaps {
		for iy := 0; iy < hotspotSize; iy++ {
			oy := (gy * (hotspotSize + 1)) + iy
			for ix := 0; ix < hotspotSize; ix++ {
				ox := (gx * (hotspotSize + 1)) + ix
				c := spt[(hotspotSize*iy)+ix]
				if c > 0 {
					out.Set(ox, oy, color.Gray{255})
				} else {
					out.Set(ox, oy, color.Gray{0})
				}
			}
		}

		gx++
		if gx >= gridWidth {
			gx = 0
			gy++
		}
	}
	return out
}

func (h *Header) DebugImage() image.Image {
	if h.debugi != nil {
		return h.debugi
	}
	return nil
}

// like 'refineTransform' mapping header hotspots to matches in image,
// keep page with smallest error
func (h *Header) CheckPage(s *Scanner, it *image.YCbCr, debug bool) (score float64, err error) {
	err = s.FindTopLineTransform(it)
	if err != nil {
		return
	}
	// debugi top to bottom
	// [0]: orig header from png
	// [1]: from top line transform
	// [2]: from transform refined here
	// [4]: hotspots
	if debug {
		h.debugi = image.NewNRGBA(image.Rectangle{
			Min: image.Point{0, 0},
			Max: image.Point{h.orect.Dx(), h.orect.Dy()*4 + 3}})
	}
	dgy := 0

	if h.debugi != nil {
		// png copy of header
		draw.Draw(h.debugi, h.orig.Bounds(), h.orig, h.orig.Bounds().Min, draw.Src)
		dgy++

		// header from top-line alignment
		for hy := h.orect.Min.Y; hy < h.orect.Max.Y; hy++ {
			dy := ((h.orect.Dy() + 1) * dgy) + (hy - h.orect.Min.Y)
			for hx := h.orect.Min.X; hx < h.orect.Max.X; hx++ {
				dx := hx - h.orect.Min.X
				sx, sy := s.origToScanned.Transform(float64(hx), float64(hy))
				syv := YBiCatrom(it, sx, sy)
				h.debugi.Set(dx, dy, color.Gray{syv})
			}
		}
		dgy++

		// TODO: header after header-feature transform refinement
	}
	sources := make([]FPoint, len(h.hotspotSnaps)) // source coord
	dests := make([]FPoint, len(h.hotspotSnaps))   // dest coord
	ssds := make([]int, len(h.hotspotSnaps))       // how good/bad was the fit?
	for spoti, scratch := range h.hotspotSnaps {
		spot := h.hotspots[spoti]
		mx := spot.x - (hotspotSize / 2)
		my := spot.y - (hotspotSize / 2)
		bestdx := math.MaxFloat64
		bestdy := math.MaxFloat64
		bestssd := hotspotSize * hotspotSize
		// seek match
		const subpx = 5
		const seekSize = hotspotSize * subpx * 2
		for dyi := 0; dyi < seekSize; dyi++ {
			dy := float64(dyi-(seekSize/2)) / float64(subpx)
			for dxi := 0; dxi < seekSize; dxi++ {
				dx := float64(dxi-(seekSize/2)) / float64(subpx)
				ssd := 0
				// compare spot, offset by (dx,dy)
				for iy := 0; iy < hotspotSize; iy++ {
					y := dy + float64(my+iy+h.orect.Min.Y)
					for ix := 0; ix < hotspotSize; ix++ {
						x := dx + float64(mx+ix+h.orect.Min.X)
						sx, sy := s.origToScanned.Transform(x, y)
						syv := YBiCatrom(it, sx, sy)
						var stv uint8
						if syv > s.scanThresh {
							stv = 1
						} else {
							stv = 0
						}
						if stv != scratch[(hotspotSize*iy)+ix] {
							ssd++
						}
					}
				}
				if ssd < bestssd {
					bestdx = dx
					bestdy = dy
					bestssd = ssd
				}
			}
		}
		if debug {
			if bestdx != 0 || bestdy != 0 {
				s.debug("refine transform %d,%d -> %f,%f (%f, %f)\n", spot.x, spot.y, float64(spot.x)+bestdx, float64(spot.y)+bestdy, bestdx, bestdy)
			} else {
				s.debug("refine transform no change\n")
			}
		}
		sources[spoti].SetInt(spot.x, spot.y)
		dests[spoti].X, dests[spoti].Y = s.origToScanned.Transform(float64(spot.x)+bestdx, float64(spot.y)+bestdy)
		ssds[spoti] = bestssd
	}
	minssd := ssds[0]
	maxssd := ssds[0]
	sumssd := ssds[0]
	for _, xs := range ssds[1:] {
		if xs < minssd {
			minssd = xs
		}
		if xs > maxssd {
			maxssd = xs
		}
		sumssd += xs
	}
	meanssd := float64(sumssd) / float64(len(ssds))
	fmat := FindTransform(sources, dests)
	htr := MatrixTransform{fmat}
	if h.debugi != nil {
		// header from refined alignment
		for hy := h.orect.Min.Y; hy < h.orect.Max.Y; hy++ {
			dy := ((h.orect.Dy() + 1) * dgy) + (hy - h.orect.Min.Y)
			for hx := h.orect.Min.X; hx < h.orect.Max.X; hx++ {
				dx := hx - h.orect.Min.X
				sx, sy := htr.Transform(float64(hx), float64(hy))
				syv := YBiCatrom(it, sx, sy)
				h.debugi.Set(dx, dy, color.Gray{syv})
			}
		}
		dgy++

		// hotspots as captured
		hyb := (h.orect.Dy() + 1) * dgy
		for spti, spt := range h.hotspotSnaps {
			gx := spti
			for iy := 0; iy < hotspotSize; iy++ {
				oy := hyb + iy
				for ix := 0; ix < hotspotSize; ix++ {
					ox := (gx * (hotspotSize + 1)) + ix
					c := spt[(hotspotSize*iy)+ix]
					if c > 0 {
						h.debugi.Set(ox, oy, color.Gray{255})
					} else {
						h.debugi.Set(ox, oy, color.Gray{0})
					}
				}
			}
		}

		// hotspots as matched in target
		hyb += hotspotSize + 1
		for spti, spt := range dests {
			gx := spti
			for iy := 0; iy < hotspotSize; iy++ {
				oy := hyb + iy
				for ix := 0; ix < hotspotSize; ix++ {
					ox := (gx * (hotspotSize + 1)) + ix
					syv := YBiCatrom(it, spt.X, spt.Y)
					h.debugi.Set(ox, oy, color.Gray{syv})
				}
			}
		}
	}
	// TODO: how bad was ssds[]? how much error is there residual is fmat(sources) -> dests ?
	// If some points don't line up (because this is the wrong header) this error score will be bad
	terr := TransformError(sources, dests, &htr)
	// TODO: full match ssd, hotspots might not have hit what was unique about header
	// TODO: is this the right measure?
	score = terr * meanssd
	s.debug("score %f, err %f, minssd %d, maxssd %d, meanssd %f, transform %v\n", score, terr, minssd, maxssd, meanssd, fmat)
	return
}
