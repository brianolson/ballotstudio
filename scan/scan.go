package scan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
)

func maybeFail(err error, format string, args ...interface{}) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

// func pxy(it *image.YCbCr, x, y int) {
// 	fmt.Printf("(%d,%d) Y=%d, (%d,%d) CrCb=%d\n", x, y, it.YOffset(x, y), x, y, it.COffset(x, y))
// }

func yHistogram(it *image.YCbCr) []uint {
	out := make([]uint, 256)
	for y := 0; y < it.Rect.Max.Y; y++ {
		for x := 0; x < it.Rect.Max.X; x++ {
			yv := it.Y[(it.YStride*y)+x]
			out[yv]++
		}
	}
	return out
}

func generalBrightnessHistogram(im image.Image) []uint {
	bounds := im.Bounds()
	out := make([]uint, 256)
	for y := 0; y < bounds.Max.Y; y++ {
		for x := 0; x < bounds.Max.X; x++ {
			c := im.At(x, y)
			r, g, b, _ := c.RGBA()
			yv, _, _ := color.RGBToYCbCr(
				uint8(r>>8),
				uint8(g>>8),
				uint8(b>>8),
			)
			out[yv]++
		}
	}
	return out
}

// https://en.wikipedia.org/wiki/Otsu%27s_method
func otsuThreshold(hist []uint) uint8 {
	sumB := uint(0)
	wB := uint(0)
	max := 0.0
	total := uint(0)
	sum1 := uint(0)
	best := 0
	for i, hv := range hist {
		total += hv
		sum1 += uint(i) * hv
	}
	for i := 1; i < 256; i++ {
		if wB > 0 && total > wB {
			wF := total - wB
			mF := float64(sum1-sumB) / float64(wF)
			fwB := float64(wB)
			fwF := float64(wF)
			fsumB := float64(sumB)
			val := fwB * fwF * ((fsumB / fwB) - mF) * ((fsumB / fwB) - mF)
			if val >= max {
				best = i
				max = val
			}
		}
		wB += hist[i]
		sumB += uint(i) * hist[i]
	}
	return uint8(best)
}

// population standard deviation
func stddev(they []float64) (mean, out float64) {
	sum := they[0]
	for _, v := range they[1:] {
		sum += v
	}
	mean = sum / float64(len(they))
	ssd := float64(0.0)
	for _, v := range they {
		dv := v - mean
		ssd += dv * dv
	}
	out = math.Sqrt(ssd / float64(len(they)))
	return
}

// point-line-distance population standard deviation
// the line is the 'mean', []they is already distance to mean
func pldstddev(they []float64) (out float64) {
	ssd := float64(0.0)
	for _, v := range they {
		ssd += v * v
	}
	out = math.Sqrt(ssd / float64(len(they)))
	return
}

const darkPxCountThreshold = 4

// Search the Y compoment of YCbCr for a left edge
func yLeftLineFind(it *image.YCbCr, ySeekCenter int, threshold uint8) (edgeX int) {
	darkPxCount := 0
	leftEdge := 0
	rightEdge := 10
	for y := ySeekCenter - 1; y < ySeekCenter+2; y++ {
		for x := leftEdge; x < rightEdge; x++ {
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount++
			}
		}
	}
	for rightEdge < it.Rect.Max.X && darkPxCount < darkPxCountThreshold {
		for y := ySeekCenter - 1; y < ySeekCenter+2; y++ {
			x := leftEdge
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount--
			}
			x = rightEdge
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount++
			}
		}
		leftEdge++
		rightEdge++
	}
	return rightEdge - 1
}

// scan a block 3 px wide and 10 px tall
func yTopLineFind(it *image.YCbCr, xSeekCenter int, threshold uint8) (edgeY int) {
	darkPxCount := 0
	topEdge := 0
	bottomEdge := 10
	// initial sum
	for y := topEdge; y < bottomEdge; y++ {
		for x := xSeekCenter - 1; x < xSeekCenter+2; x++ {
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount++
			}
		}
	}
	// progressive seek, subtract top px, add bottom px
	// continue until threshold exceeded
	for bottomEdge < it.Rect.Max.Y && darkPxCount < darkPxCountThreshold {
		for x := xSeekCenter - 1; x < xSeekCenter+2; x++ {
			y := topEdge
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount--
			}
			y = bottomEdge
			if it.Y[(it.YStride*y)+x] < threshold {
				darkPxCount++
			}
		}
		topEdge++
		bottomEdge++
	}
	return bottomEdge - 1
}

// https://en.wikipedia.org/wiki/Simple_linear_regression#Fitting_the_regression_line
func ordinaryLeastSquares(points []point) (slope, intercept float64) {
	xsum := int64(0)
	ysum := int64(0)
	for _, pt := range points {
		xsum += int64(pt.x)
		ysum += int64(pt.y)
	}
	xavg := float64(xsum) / float64(len(points))
	yavg := float64(ysum) / float64(len(points))
	N := 0.0
	D := 0.0
	for _, pt := range points {
		dx := (float64(pt.x) - xavg)
		N += dx * (float64(pt.y) - yavg)
		D += dx * dx
	}
	slope = N / D
	intercept = yavg - (slope * xavg)
	return
}

// https://en.wikipedia.org/wiki/Distance_from_a_point_to_a_line
func pointLineDistance(slope, intercept float64, x, y int) float64 {
	// ax + by + c = 0
	// (x0, y0)
	// abs(a*x0 + b*y0 + c)/sqrt(a*a + b*b)
	// y = slope*x + intercept
	// a = slope
	// b = -1
	// c = intercept
	return math.Abs((slope*float64(x))+(-1.0*float64(y))+intercept) / math.Sqrt((slope*slope)+1)
}

type point struct {
	x int
	y int
}

type AffineTransform interface {
	TransformInt(x, y int) (int, int)
	Transform(x, y float64) (float64, float64)
}

type NopTransform struct {
}

func (mt NopTransform) TransformInt(x, y int) (int, int) {
	return x, y
}
func (mt NopTransform) Transform(x, y float64) (float64, float64) {
	return x, y
}

type MatrixTransform struct {
	mat []float64
}

func (mt MatrixTransform) TransformInt(x, y int) (int, int) {
	fx := float64(x)
	fy := float64(y)
	ox := (fx * mt.mat[0]) + (fy * mt.mat[1]) + mt.mat[2]
	oy := (fx * mt.mat[3]) + (fy * mt.mat[4]) + mt.mat[5]
	ow := (fx * mt.mat[6]) + (fy * mt.mat[7]) + mt.mat[8]
	ox /= ow
	oy /= ow
	return int(ox), int(oy)
}

func (mt MatrixTransform) Transform(x, y float64) (float64, float64) {
	fx := x
	fy := y
	ox := (fx * mt.mat[0]) + (fy * mt.mat[1]) + mt.mat[2]
	oy := (fx * mt.mat[3]) + (fy * mt.mat[4]) + mt.mat[5]
	ow := (fx * mt.mat[6]) + (fy * mt.mat[7]) + mt.mat[8]
	ox /= ow
	oy /= ow
	return ox, oy
}

type transform struct {
	orig    point
	scale   float64
	rotRads float64
	costh   float64
	sinth   float64
	dest    point
}

func newTransform(origTopLeft, origTopRight, destTopLeft, destTopRight point) transform {
	// TODO: compase a 2d transformation matrix
	dy := destTopRight.y - destTopLeft.y
	dx := destTopRight.x - destTopLeft.x
	rotRads := math.Atan2(float64(dy), float64(dx))
	actualTopLineLengthPx := math.Sqrt(float64((dx * dx) + (dy * dy)))
	ody := origTopRight.y - origTopLeft.y
	odx := origTopRight.x - origTopLeft.x
	origTopLineLength := math.Sqrt(float64((odx * odx) + (ody * ody)))
	scale := actualTopLineLengthPx / origTopLineLength
	costh := math.Cos(rotRads)
	sinth := math.Sin(rotRads)
	return transform{
		orig:    origTopLeft,
		scale:   scale,
		rotRads: rotRads,
		costh:   costh,
		sinth:   sinth,
		dest:    destTopLeft,
	}
}

func (t transform) TransformInt(origx, origy int) (x, y int) {
	// TODO: compose this into a transform matrix
	x = origx - t.orig.x
	y = origy - t.orig.y
	nx := float64(x) * t.scale
	ny := float64(y) * t.scale

	x2 := (nx * t.costh) - (ny * t.sinth)
	y2 := (nx * t.sinth) + (ny * t.costh)

	x = int(x2) + t.dest.x
	y = int(y2) + t.dest.y
	return
}

func (t transform) Transform(origx, origy float64) (x, y float64) {
	// TODO: compose this into a transform matrix
	dx := origx - float64(t.orig.x)
	dy := origy - float64(t.orig.y)
	nx := float64(dx) * t.scale
	ny := float64(dy) * t.scale

	x2 := (nx * t.costh) - (ny * t.sinth)
	y2 := (nx * t.sinth) + (ny * t.costh)

	x = x2 + float64(t.dest.x)
	y = y2 + float64(t.dest.y)
	return
}

func colorY(c color.Color) uint8 {
	r, g, b, _ := c.RGBA()
	br := uint8(r >> 8)
	bg := uint8(g >> 8)
	bb := uint8(b >> 8)
	y, _, _ := color.RGBToYCbCr(br, bg, bb)
	return y
}

type Scanner struct {
	Bj BubblesJson

	orig         image.Image
	orect        image.Rectangle
	origPxPerPt  float64
	origTopLeft  point
	origTopRight point
	origYThresh  uint8

	hist       []uint
	scanThresh uint8

	// hotspots are coords within orig.Bounds() ((0,0)-(w,h))
	hotspots     []point
	hotspotSnaps [][hotspotSize * hotspotSize]uint8

	transformTarget image.Image
	origToScanned   AffineTransform

	DebugOut io.Writer

	TargetsPngPath string
	DebugPngPath   string
	BubblesPngPath string
}

func (s *Scanner) debug(format string, args ...interface{}) {
	if s.DebugOut != nil {
		fmt.Fprintf(s.DebugOut, format, args...)
	}
}

func (s *Scanner) ReadBubblesJson(path string) error {
	fin, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fin.Close()
	jd := json.NewDecoder(fin)
	return jd.Decode(&s.Bj)
}

func (s *Scanner) ReadOrigImage(origname string) error {
	r, err := os.Open(origname)
	maybeFail(err, "%s: %s", origname, err)
	defer r.Close()
	orig, format, err := image.Decode(r)
	maybeFail(err, "%s: %v %s", origname, format, err)
	return s.SetOrigImage(orig)
}

func imageYHist(orig image.Image, orect image.Rectangle) uint8 {
	var hist [256]uint
	for iy := orect.Min.Y; iy < orect.Max.Y; iy++ {
		for ix := orect.Min.X; ix < orect.Max.X; ix++ {
			y := colorY(orig.At(ix, iy))
			hist[y]++
		}
	}
	return otsuThreshold(hist[:])
}

func (s *Scanner) SetOrigImage(orig image.Image) error {
	s.orig = orig
	orect := orig.Bounds()
	s.orect = orect
	if orect.Min.X != 0 || orect.Min.Y != 0 {
		return fmt.Errorf("nonzero origin for original pic. WAT?\n")
	}
	if len(s.Bj.DrawSettings.PageSize) < 2 {
		return errors.New("no bubbles draw settings page size")
	}
	s.debug("orig %T %v\n", orig, orect)
	origPxPerPtX := float64(orect.Max.X-orect.Min.X) / s.Bj.DrawSettings.PageSize[0]
	origPxPerPtY := float64(orect.Max.Y-orect.Min.Y) / s.Bj.DrawSettings.PageSize[1]
	if math.Abs((origPxPerPtY/origPxPerPtX)-1) > 0.01 {
		return fmt.Errorf("orig scale not square: mx = %f, my = %f\n", origPxPerPtX, origPxPerPtY)
	}
	s.origPxPerPt = (origPxPerPtX + origPxPerPtY) / 2.0
	s.origTopLeft = point{
		x: int(s.Bj.DrawSettings.PageMargin * s.origPxPerPt),
		y: int(s.Bj.DrawSettings.PageMargin * s.origPxPerPt),
	}
	s.origTopRight = point{
		x: int((s.Bj.DrawSettings.PageSize[0] - s.Bj.DrawSettings.PageMargin) * s.origPxPerPt),
		y: int(s.Bj.DrawSettings.PageMargin * s.origPxPerPt),
	}
	s.debug("top line orig (%d,%d)-(%d,%d)\n", s.origTopLeft.x, s.origTopLeft.y, s.origTopRight.x, s.origTopRight.y)

	s.origYThresh = imageYHist(orig, orect)
	s.hotspots, s.hotspotSnaps = findImageHotspots(numHeaderHotspots, s.orig, s.origYThresh)
	return nil
}

func (s *Scanner) DebugOrigBubbles(outpath string) error {
	imout, err := os.Create(outpath)
	if err != nil {
		return fmt.Errorf("%s: could not create, %v", outpath, err)
	}
	defer imout.Close()

	sourceSelectionBounds := make([][]float64, 0, 100)
	maxWidth := 0.0
	maxHeight := 0.0
	for _, ballotType := range s.Bj.Bubbles {
		for _, csels := range ballotType { // _ = contestName
			for _, xywh := range csels { // _ = cselName
				sourceSelectionBounds = append(sourceSelectionBounds, xywh)
				maxWidth = fmax(maxWidth, xywh[2])
				maxHeight = fmax(maxHeight, xywh[3])
			}
		}
	}
	maxWidth = math.Ceil(maxWidth * s.origPxPerPt)
	maxHeight = math.Ceil(maxHeight * s.origPxPerPt)
	oiw := int(maxWidth) * 4
	oih := int(maxHeight) * 4 * len(sourceSelectionBounds)
	orect := image.Rect(0, 0, oiw, oih)
	oi := image.NewNRGBA(orect)
	opngBounds := s.orig.Bounds()
	for i, xywh := range sourceSelectionBounds {
		// (printx,printy) coord in pt from bottom left
		printx := xywh[0]
		printy := xywh[1]
		// coords in orig png, bottom left pixel
		opngx := printx * s.origPxPerPt
		opngy := float64(opngBounds.Max.Y) - (printy * s.origPxPerPt)

		outy := (int(maxHeight) * 4 * (i + 1)) - 1
		outWidthPx := int(math.Ceil(xywh[2] * 4 * s.origPxPerPt))
		outHeightPx := int(math.Ceil(xywh[3] * 4 * s.origPxPerPt))
		for iy := 0; iy < outHeightPx; iy++ {
			dy := opngy - (float64(iy) * 0.25)
			for ix := 0; ix < outWidthPx; ix++ {
				pi := ((outy - iy) * oi.Stride) + (ix * 4)
				dx := opngx + (float64(ix) * 0.25)
				oc := ImageBiCatrom(s.orig, dx, dy)
				oi.Pix[pi] = oc.R
				oi.Pix[pi+1] = oc.G
				oi.Pix[pi+2] = oc.B
				oi.Pix[pi+3] = oc.A
			}
		}
	}
	err = png.Encode(imout, oi)
	return err
}

const hotspotSize = 15
const hotspotBytes = hotspotSize * hotspotSize

// check potential hotspot center (tx,ty) for quality
// scratch should be [hotspotSize*hotspotSize]uint8
func (s *Scanner) origImageHotspotsQuality(tx, ty int, scratch []uint8) int {
	return imageHotspotQuality(s.orig, s.origYThresh, tx, ty, scratch)
}

// check potential hotspot center (tx,ty) for quality
// scratch should be [hotspotSize*hotspotSize]uint8
func imageHotspotQuality(orig image.Image, yThresh uint8, tx, ty int, scratch []uint8) int {

	mx := tx - (hotspotSize / 2)
	my := ty - (hotspotSize / 2)
	for iy := 0; iy < hotspotSize; iy++ {
		for ix := 0; ix < hotspotSize; ix++ {
			y := colorY(orig.At(mx+ix, my+iy))
			if y >= yThresh {
				scratch[(hotspotSize*iy)+ix] = 1
			} else {
				scratch[(hotspotSize*iy)+ix] = 0
			}
		}
	}
	dx := 0
	for iy := 0; iy < hotspotSize; iy++ {
		yo := iy * hotspotSize
		for ix := 0; ix < hotspotSize-6; ix++ {
			if (scratch[yo+ix] == scratch[yo+ix+1]) && (scratch[yo+ix] == scratch[yo+ix+2]) && (scratch[yo+ix] != scratch[yo+ix+3]) && (scratch[yo+ix] != scratch[yo+ix+4]) && (scratch[yo+ix] != scratch[yo+ix+5]) {
				dx++
			}
		}
	}
	dy := 0
	for iy := 0; iy < hotspotSize-6; iy++ {
		y0 := iy * hotspotSize
		y1 := (iy + 1) * hotspotSize
		y2 := (iy + 2) * hotspotSize
		y3 := (iy + 3) * hotspotSize
		y4 := (iy + 4) * hotspotSize
		y5 := (iy + 5) * hotspotSize
		for ix := 0; ix < hotspotSize; ix++ {
			if (scratch[y0+ix] == scratch[y1+ix]) && (scratch[y0+ix] == scratch[y2+ix]) && (scratch[y0+ix] != scratch[y3+ix]) && (scratch[y0+ix] != scratch[y4+ix]) && (scratch[y0+ix] != scratch[y5+ix]) {
				dy++
			}
		}
	}
	fx := float64(dx)
	fx = (math.Log10(fx*0.5) + 0.5) * fx
	fy := float64(dy)
	fy = (math.Log10(fy*0.5) + 0.5) * fy
	return int(fx + fy)
}

const pageNHotspots = 20

// returns hotspot center points [](x,y)
func findImageHotspots(nHotspots int, orig image.Image, yThresh uint8) (spots []point, snaps [][hotspotBytes]uint8) {
	// find ~20 spots 16x16 px with dx feature and dy feature
	orect := orig.Bounds()

	width := orect.Max.X - orect.Min.X
	height := orect.Max.Y - orect.Min.Y

	spots = make([]point, 0, nHotspots)
	scores := make([]int, nHotspots)
	var scratch [hotspotSize * hotspotSize]uint8
	// check 5x what we want, keep the best
	checkCount := 0
	for checkCount < nHotspots*5 {
		tx := rand.Intn(width-(2*hotspotSize)) + hotspotSize
		ty := rand.Intn(height-(2*hotspotSize)) + hotspotSize
		score := imageHotspotQuality(orig, yThresh, tx, ty, scratch[:])
		if score < 0 {
			continue
		}
		if len(spots) == 0 {
			spots = append(spots, point{tx, ty})
			scores[0] = score
			continue
		}
		pos := len(spots) - 1
		for score > scores[pos] {
			// insertion sort
			if pos < nHotspots {
				if (pos + 1) < len(spots) {
					scores[pos+1] = scores[pos]
					spots[pos+1] = spots[pos]
				} else if (pos + 1) < nHotspots {
					scores[pos+1] = scores[pos]
					spots = append(spots, spots[pos])
				}
			}
			spots[pos].x = tx
			spots[pos].y = ty
			scores[pos] = score
			if pos == 0 {
				break
			}
			pos--
		}
		if pos == (len(spots)-1) && (pos+1) < nHotspots {
			scores[pos+1] = score
			spots = append(spots, point{tx, ty})
		}
		checkCount++
	}

	snaps = make([][hotspotSize * hotspotSize]uint8, len(spots))
	for spoti, spot := range spots {
		// copy thresholded orig to scratch
		mx := spot.x - (hotspotSize / 2)
		my := spot.y - (hotspotSize / 2)
		scratch := snaps[spoti][:]
		for iy := 0; iy < hotspotSize; iy++ {
			for ix := 0; ix < hotspotSize; ix++ {
				sc := orig.At(mx+ix, my+iy)
				y := colorY(sc)
				if y >= yThresh {
					scratch[(hotspotSize*iy)+ix] = 1
				} else {
					scratch[(hotspotSize*iy)+ix] = 0
				}
			}
		}
	}
	return
}

// copy source data in hotspots to image so we can see what targets we're picking
func (s *Scanner) hotspotsDebugImage(spots []point, it *image.YCbCr) *image.RGBA {
	width := hotspotSize * 6
	height := hotspotSize * len(spots)
	s.debug("hots %dx%d\n", width, height)
	outrect := image.Rect(0, 0, width, height)
	out := image.NewRGBA(outrect)
	for i, spt := range spots {
		mx := spt.x - (hotspotSize / 2)
		my := spt.y - (hotspotSize / 2)
		for iy := 0; iy < hotspotSize; iy++ {
			for ix := 0; ix < hotspotSize; ix++ {
				sc := s.orig.At(mx+ix, my+iy)
				out.Set(ix, iy+(i*hotspotSize), sc)

				if it != nil {
					sx, sy := s.origToScanned.Transform(float64(mx+ix), float64(my+iy))
					sc = ImageBiCatrom(it, sx, sy)
					out.Set(ix+hotspotSize, iy+(i*hotspotSize), sc)
				}
			}
		}
	}
	return out
}

// return map[contest @id]map[csel @id](bool marked)
func (s *Scanner) ReadScannedImage(fname string) (marked map[string]map[string]bool, score float64, err error) {
	var r io.ReadCloser
	r, err = os.Open(fname)
	if err != nil {
		return
	}
	defer r.Close()
	im, _, err := image.Decode(r)
	if err != nil {
		return
	}
	return s.ProcessScannedImage(im)
}

// return map[contest @id]map[csel @id](bool marked)
func (s *Scanner) ProcessScannedImage(im image.Image) (marked map[string]map[string]bool, score float64, err error) {
	switch it := im.(type) {
	case *image.YCbCr:
		return s.processYCbCr(it)
	default:
		err = fmt.Errorf("unknown image type %T", im)
		return
	}
}

func fmax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func nopdebug(format string, args ...interface{}) {
}

var NoLineError = errors.New("no line found")

func linePoints(topPoints []point, debug func(string, ...interface{})) (slope, intercept, dstddev float64, pointsout []point, err error) {
	if debug == nil {
		debug = nopdebug
	}
	pointsout = topPoints
	for len(topPoints) > 20 {
		slope, intercept = ordinaryLeastSquares(topPoints)
		plds := make([]float64, len(topPoints))
		worstd := 0.0
		for pti, pt := range topPoints {
			d := pointLineDistance(slope, intercept, pt.x, pt.y)
			plds[pti] = d
			if d > worstd {
				worstd = d
			}
		}
		dstddev = pldstddev(plds)
		debug("    top line %d points, slope=%f intercept=%f, d std %f worst %f\n", len(topPoints), slope, intercept, dstddev, worstd)
		didDrop := false
		i := 0
		dropthresh := dstddev
		if dropthresh < 2 {
			dropthresh = 2
		}
		for i < len(plds) {
			d := plds[i]
			if d > dropthresh {
				debug("drop topPoints[%d]\n", i)
				// drop outliar
				didDrop = true
				copy(plds[i:], plds[i+1:])
				copy(topPoints[i:], topPoints[i+1:])
				plds = plds[:len(plds)-1]
				topPoints = topPoints[:len(topPoints)-1]
				pointsout = topPoints
			} else {
				i++
			}
		}
		if !didDrop {
			return
		}
		slope, intercept = ordinaryLeastSquares(topPoints)
		worstd = 0.0
		for pti, pt := range topPoints {
			d := pointLineDistance(slope, intercept, pt.x, pt.y)
			plds[pti] = d
			if d > worstd {
				worstd = d
			}
		}
		dstddev = pldstddev(plds)
	}
	debug("too few points, no line found")
	err = NoLineError
	return
}

// find the top border and calculate an initial transform based on it
func (s *Scanner) topLineYCbCr(it *image.YCbCr) error {
	if s.origToScanned != nil && s.transformTarget == it {
		s.debug("topLineYCbCr skipped, already have origToScanned\n")
		return nil
	}
	s.debug("topLineYCbCr NOT skipped it=%p s.tt=%p\n", it, s.transformTarget)
	s.transformTarget = it
	misscount := 0
	hitcount := 0
	topPoints := make([]point, 0, 100)
	for x := 10; x < it.Rect.Max.X-100; x += 25 {
		yte := yTopLineFind(it, x, s.scanThresh)
		if yte < it.Rect.Max.Y/2 {
			s.debug("[%d,%d]\n", x, yte)
			topPoints = append(topPoints, point{x, yte})
			hitcount++
		} else {
			misscount++
		}
	}
	slope, intercept, dstddev, topPoints, err := linePoints(topPoints, s.debug)
	if err != nil {
		return err
	}
	x := topPoints[1].x
	y := topPoints[1].y
	const step = 5
	dthresh := dstddev
	if dthresh < 2 {
		dthresh = 2
	}
	for true {
		nx := x - step
		yte := yTopLineFind(it, nx, s.scanThresh)
		d := pointLineDistance(slope, intercept, nx, yte)
		s.debug("TL corner seek: (%d,%d) %f\n", nx, yte, d)
		if d > dthresh {
			break
		}
		x = nx
		y = yte
	}
	topLeft := point{x, y}
	last := len(topPoints) - 2
	x = topPoints[last].x
	y = topPoints[last].y
	for true {
		nx := x + step
		yte := yTopLineFind(it, nx, s.scanThresh)
		d := pointLineDistance(slope, intercept, nx, yte)
		s.debug("TR corner seek: (%d,%d) %f\n", nx, yte, d)
		if d > dthresh {
			break
		}
		x = nx
		y = yte
	}
	topRight := point{x, y}
	s.debug("topleft (%d,%d) topright (%d,%d)\n", topLeft.x, topLeft.y, topRight.x, topRight.y)

	s.origToScanned = newTransform(s.origTopLeft, s.origTopRight, topLeft, topRight)
	// TODO: detect if we failed to detect a reasonable top line and return error
	return nil
}

func (s *Scanner) refineTransform(it *image.YCbCr) (score float64, err error) {
	var debugi *image.RGBA
	if s.TargetsPngPath != "" {
		debugi = s.hotspotsDebugImage(s.hotspots, it)
	}

	sources := make([]FPoint, len(s.hotspotSnaps)) // source coord
	dests := make([]FPoint, len(s.hotspotSnaps))   // dest coord
	ssds := make([]int, len(s.hotspotSnaps))       // how good/bad was the fit?

	for spoti, scratch := range s.hotspotSnaps {
		spot := s.hotspots[spoti]
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
					y := dy + float64(my+iy+s.orect.Min.Y)
					for ix := 0; ix < hotspotSize; ix++ {
						x := dx + float64(mx+ix+s.orect.Min.X)
						sx, sy := s.origToScanned.Transform(x, y)
						syv := YBiCatrom(it, sx, sy)
						var stv uint8
						if syv > s.scanThresh {
							stv = 1
						} else {
							stv = 0
						}
						if stv != scratch[(hotspotSize*iy)+ix] {
							//s.debug(" %d,%d", x, y)
							ssd++
						}
					}
				}
				//fmt.Print("\n")
				if debugi != nil {
					debugi.Set(dxi+(hotspotSize*2), dyi+(hotspotSize*spoti), color.Gray{uint8(ssd)})
				}
				if ssd < bestssd {
					//s.debug("(%d,%d) %d -> (%d,%d) %d\n", bestdx, bestdy, bestssd, dx, dy, ssd)
					bestdx = dx
					bestdy = dy
					bestssd = ssd
				} else {
					//s.debug("(%d,%d) %d\n", dx, dy, ssd)
				}
			}
		}
		if bestdx != 0 || bestdy != 0 {
			s.debug("refine transform %d,%d -> %f,%f (%f, %f)\n", spot.x, spot.y, float64(spot.x)+bestdx, float64(spot.y)+bestdy, bestdx, bestdy)
		} else {
			s.debug("refine transform no change\n")
		}
		if debugi != nil {
			for iy := 0; iy < hotspotSize; iy++ {
				y := bestdy + float64(my+iy+s.orect.Min.Y)
				for ix := 0; ix < hotspotSize; ix++ {
					x := bestdx + float64(mx+ix+s.orect.Min.X)
					sx, sy := s.origToScanned.Transform(x, y)
					syv := YBiCatrom(it, sx, sy)
					debugi.Set(ix+(hotspotSize*3), iy+(hotspotSize*spoti), color.Gray{syv})
					//sc := ImageBiCatrom(it, sx, sy)
					//debugi.Set(ix+(hotspotSize*3), iy+(hotspotSize*spoti), sc)
				}
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
	terr := TransformError(sources, dests, &htr)
	score = terr * meanssd
	s.debug("score %f, err %f, minssd %d, maxssd %d, meanssd %f, transform %v\n", score, terr, minssd, maxssd, meanssd, fmat)
	s.origToScanned = &MatrixTransform{fmat}
	if s.TargetsPngPath != "" {
		imout, err := os.Create(s.TargetsPngPath)
		maybeFail(err, "%s: %s\n", s.TargetsPngPath, err)
		defer imout.Close()
		err = png.Encode(imout, debugi)
		maybeFail(err, "%s: %s\n", s.TargetsPngPath, err)
	}
	return
}

func (s *Scanner) translateWholeScanToOrig(it *image.YCbCr) (dboi image.Image, err error) {
	orect := s.orig.Bounds()
	oi := image.NewNRGBA(orect)
	for iy := orect.Min.Y; iy < orect.Max.Y; iy++ {
		// zero based coord
		zy := iy - orect.Min.Y
		for ix := orect.Min.X; ix < orect.Max.X; ix++ {
			zx := ix - orect.Min.X
			//v := uint8((ix + iy) & 0x0ff)
			pi := (zy * oi.Stride) + (zx * 4)
			if true {
				sx, sy := s.origToScanned.Transform(float64(zx), float64(zy))
				yv := YBiCatrom(it, sx, sy)
				oi.Set(zx, zy, color.Gray{yv})
			} else if true {
				sx, sy := s.origToScanned.Transform(float64(zx), float64(zy))
				oc := ImageBiCatrom(it, sx, sy)
				oi.Pix[pi] = oc.R
				oi.Pix[pi+1] = oc.G
				oi.Pix[pi+2] = oc.B
				oi.Pix[pi+3] = oc.A
			} else {
				sx, sy := s.origToScanned.TransformInt(zx, zy)
				v := it.Y[(sy*it.YStride)+sx]
				oi.Pix[pi] = v      // R
				oi.Pix[pi+1] = v    // G
				oi.Pix[pi+2] = v    // B
				oi.Pix[pi+3] = 0xff // A
			}
		}
	}
	return oi, nil
}

func (s *Scanner) FindTopLineTransform(it *image.YCbCr) error {
	s.hist = yHistogram(it)
	s.scanThresh = otsuThreshold(s.hist)
	return s.topLineYCbCr(it)
}

// return map[contest @id]map[csel @id](bool marked)
func (s *Scanner) processYCbCr(it *image.YCbCr) (marked map[string]map[string]bool, score float64, err error) {
	if it.Rect.Min.X != 0 || it.Rect.Min.Y != 0 {
		err = fmt.Errorf("image origin not 0,0 but %d,%d", it.Rect.Min.X, it.Rect.Min.Y)
		return
	}
	s.debug("it YStride %d CStride %d SubsampleRatio %v Rect %v\n", it.YStride, it.CStride, it.SubsampleRatio, it.Rect)
	// pxy(it, 0, 0)
	// pxy(it, 1, 0)
	// pxy(it, 2, 0)
	// pxy(it, 0, 1)
	// pxy(it, 0, 2)
	// pxy(it, 50, 50)
	// pxy(it, it.Rect.Max.X-1, it.Rect.Max.Y-1)
	//s.debug("(50,50) Y=%d, (50,50) CrCb=%d\n", it.COffset(50, 50), it.YOffset(50, 50))
	//s.debug("(%d,%d) Y=%d, (%d,%d) CrCb=%d\n", it.Rect.Max.X-1, it.Rect.Max.Y-1, it.COffset(it.Rect.Max.X-1, it.Rect.Max.Y-1), it.Rect.Max.X-1, it.Rect.Max.Y-1, it.YOffset(it.Rect.Max.X-1, it.Rect.Max.Y-1))

	s.hist = yHistogram(it)
	s.scanThresh = otsuThreshold(s.hist)
	s.debug("Otsu threshold %d\n", s.scanThresh)
	if false {
		for i, v := range s.hist {
			s.debug("hist[%3d] %6d\n", i, v)
		}
	}
	misscount := 0
	hitcount := 0
	for y := 100; y < it.Rect.Max.Y-100; y += 50 {
		xle := yLeftLineFind(it, y, s.scanThresh)
		if xle < it.Rect.Max.X/2 {
			//s.debug("[%d,%d]\n", xle, y)
			hitcount++
		} else {
			misscount++
		}
	}
	s.debug("left line %d hit %d miss\n", hitcount, misscount)

	err = s.topLineYCbCr(it)
	if err != nil {
		return
	}
	score, err = s.refineTransform(it)
	if err != nil {
		return
	}
	if s.DebugPngPath != "" {
		var dbimg image.Image
		dbimg, err = s.translateWholeScanToOrig(it)
		if err != nil {
			return
		}
		var dbfout io.WriteCloser
		dbfout, err = os.Create(s.DebugPngPath)
		if err != nil {
			return
		}
		err = png.Encode(dbfout, dbimg)
		if err != nil {
			return
		}
	}
	if s.BubblesPngPath != "" {
		err = s.debugScannedBubbles(it)
		if err != nil {
			return
		}
	}
	marked = s.measureScannedBubbles(it)
	return
}

func (s *Scanner) measureBubble(it *image.YCbCr, xywh []float64) (darkCount, pxCount int) {
	darkCount = 0
	pxCount = 0
	// (printx,printy) coord in pt from bottom left
	printx := xywh[0]
	printy := xywh[1]
	// coords in orig png, bottom left pixel
	opngBounds := s.orig.Bounds()
	opngx := printx * s.origPxPerPt
	opngy := float64(opngBounds.Max.Y) - (printy * s.origPxPerPt)

	//outy := (int(maxHeight) * 4 * (i + 1)) - 1
	outWidthPx := int(math.Ceil(xywh[2] * 4 * s.origPxPerPt))
	outHeightPx := int(math.Ceil(xywh[3] * 4 * s.origPxPerPt))
	centerY := outHeightPx / 2
	minsamplex := outWidthPx / 10
	maxsamplex := (outWidthPx * 9) / 10
	for iiy := -1; iiy <= 1; iiy++ {
		iy := centerY + (iiy * 8)
		//for iy := 0; iy < outHeightPx; iy++ {
		dy := opngy - (float64(iy) * 0.25)
		for ix := minsamplex; ix < maxsamplex; ix += 16 {
			//for ix := 0; ix < outWidthPx; ix++ {
			dx := opngx + (float64(ix) * 0.25)
			sx, sy := s.origToScanned.Transform(dx, dy)
			oc := ImageBiCatrom(it, sx, sy)
			//if (iy == centerY || iy == (centerY-8) || iy == (centerY+8)) && (ix%16 == 0) && (ix > minsamplex) && (ix < maxsamplex) {
			oc.G = 255
			oc.R /= 2
			oc.B /= 2
			syv := YBiCatrom(it, sx, sy)
			if syv < s.scanThresh {
				darkCount++
			}
			pxCount++
			//}
			/*
				if oi != nil {
					pi := ((outy - iy) * oi.Stride) + (ix * 4)
					oi.Pix[pi] = oc.R
					oi.Pix[pi+1] = oc.G
					oi.Pix[pi+2] = oc.B
					oi.Pix[pi+3] = oc.A
				}
			*/
		}
	}
	// TODO: record mediocre matches with 30%-70% fill, flag for inspection
	// TODO: measure extraneous marks in ballot and flag for review
	return
	/*
		s.debug("%d/%d dark/all px\n", darkCount, pxCount)
		if darkCount > ((pxCount * 7) / 10) {
		}
	*/
}

// return map[contest @id]map[csel @id](bool marked)
func (s *Scanner) measureScannedBubbles(it *image.YCbCr) (marked map[string]map[string]bool) {
	marked = make(map[string]map[string]bool)
	for _, ballotType := range s.Bj.Bubbles {
		for contestName, csels := range ballotType {
			conout := make(map[string]bool)
			for cselName, xywh := range csels {
				darkCount, pxCount := s.measureBubble(it, xywh)
				s.debug("%s\t%s\t%d/%d dark/all px\n", contestName, cselName, darkCount, pxCount)
				if darkCount > ((pxCount * 7) / 10) {
					conout[cselName] = true
				}
			}
			marked[contestName] = conout
		}
	}
	return
}

type dsbrec struct {
	xywh        []float64
	contestName string
	cselName    string
}
type dsbreca []dsbrec

func (a *dsbreca) Less(i, j int) bool {
	if (*a)[i].contestName < (*a)[i].contestName {
		return true
	}
	if (*a)[i].contestName > (*a)[i].contestName {
		return false
	}
	if (*a)[i].cselName < (*a)[i].cselName {
		return true
	}
	if (*a)[i].cselName > (*a)[i].cselName {
		return false
	}
	return false
}
func (a *dsbreca) Swap(i, j int) {
	t := (*a)[i]
	(*a)[i] = (*a)[j]
	(*a)[j] = t
}
func (a *dsbreca) Len() int {
	return len(*a)
}

func (s *Scanner) debugScannedBubbles(it *image.YCbCr) error {
	imout, err := os.Create(s.BubblesPngPath)
	if err != nil {
		return fmt.Errorf("%s: %s", s.BubblesPngPath, err)
	}
	defer imout.Close()

	recs := make([]dsbrec, 0, 100)
	maxWidth := 0.0
	maxHeight := 0.0
	for _, ballotType := range s.Bj.Bubbles {
		for contestName, csels := range ballotType {
			for cselName, xywh := range csels {
				recs = append(recs, dsbrec{xywh, contestName, cselName})
				maxWidth = fmax(maxWidth, xywh[2])
				maxHeight = fmax(maxHeight, xywh[3])
			}
		}
	}
	sort.Sort(((*dsbreca)(&recs)))
	maxWidth = math.Ceil(maxWidth * s.origPxPerPt)
	maxHeight = math.Ceil(maxHeight * s.origPxPerPt)
	oiw := int(maxWidth) * 4
	oih := int(maxHeight) * 4 * len(recs)
	orect := image.Rect(0, 0, oiw, oih)
	oi := image.NewNRGBA(orect)
	opngBounds := s.orig.Bounds()
	for i, rec := range recs {
		xywh := rec.xywh
		darkCount := 0
		pxCount := 0
		// (printx,printy) coord in pt from bottom left
		printx := xywh[0]
		printy := xywh[1]
		// coords in orig png, bottom left pixel
		opngx := printx * s.origPxPerPt
		opngy := float64(opngBounds.Max.Y) - (printy * s.origPxPerPt)

		outy := (int(maxHeight) * 4 * (i + 1)) - 1
		outWidthPx := int(math.Ceil(xywh[2] * 4 * s.origPxPerPt))
		outHeightPx := int(math.Ceil(xywh[3] * 4 * s.origPxPerPt))
		centerY := outHeightPx / 2
		minsamplex := outWidthPx / 10
		maxsamplex := (outWidthPx * 9) / 10
		for iy := 0; iy < outHeightPx; iy++ {
			dy := opngy - (float64(iy) * 0.25)
			for ix := 0; ix < outWidthPx; ix++ {
				pi := ((outy - iy) * oi.Stride) + (ix * 4)
				dx := opngx + (float64(ix) * 0.25)
				sx, sy := s.origToScanned.Transform(dx, dy)
				oc := ImageBiCatrom(it, sx, sy)
				if (iy == centerY || iy == (centerY-8) || iy == (centerY+8)) && (ix%16 == 0) && (ix > minsamplex) && (ix < maxsamplex) {
					oc.G = 255
					oc.R /= 2
					oc.B /= 2
					syv := YBiCatrom(it, sx, sy)
					if syv < s.scanThresh {
						darkCount++
					}
					pxCount++
				}
				oi.Pix[pi] = oc.R
				oi.Pix[pi+1] = oc.G
				oi.Pix[pi+2] = oc.B
				oi.Pix[pi+3] = oc.A
			}
		}
		// TODO: record mediocre matches with 30%-70% fill, flag for inspection
		// TODO: measure extraneous marks in ballot and flag for review
		s.debug("%s\t%s\t%d/%d dark/all px (debug)\n", rec.contestName, rec.cselName, darkCount, pxCount)
		if darkCount > ((pxCount * 7) / 10) {
			oc := color.RGBA{0, 255, 0, 255}
			for iy := 0; iy < outHeightPx; iy++ {
				for ix := 0; ix < 3; ix++ {
					pi := ((outy - iy) * oi.Stride) + (ix * 4)
					oi.Pix[pi] = oc.R
					oi.Pix[pi+1] = oc.G
					oi.Pix[pi+2] = oc.B
					oi.Pix[pi+3] = oc.A
				}
			}
		}
	}
	err = png.Encode(imout, oi)
	return err
}

type DrawSettings struct {
	// PageSize: [width pt, height pt],
	PageSize   []float64 `json:"pagesize"`
	PageMargin float64   `json:"pageMargin"`
	// TODO: lots of fields ignored
}

// {"csel1": [44.2, 491.4000000000001, 22.67716535433071, 8.255859375], "csel2": [44.2, 458.2000000000001, 22.67716535433071, 8.255859375]}
// []float64 is length 4, [x,y, width,height]
type ContestSelections map[string][]float64
type Contest map[string]ContestSelections

type BubblesJson struct {
	DrawSettings *DrawSettings `json:"draw_settings"`

	// BsData should be preferred in the future
	// .BsData[ballotStyleIndex].GpUnitIds = [gp unit id str, ...]
	// .BsData[ballotStyleIndex].Bubbles[contest id str][selection id str] = [left, bottom, width, height]
	// .BsData[ballotStyleIndex].Headers[page number str] = [left, top, right, bottom]
	BsData []BsData `json:"bsdata"`

	// Bubbles is a list per ballot style, indexed in the same order as the source document ballot styles.
	// .Bubbles[ballotStyleIndex][contest @id][selection @id][x y width height]
	// Deprecated, use .BsData[ballotStyleIndex].Bubbles instead
	Bubbles []Contest `json:"bubbles"`

	// Headers[ballotStyleIndex][page number string][left top right bottom]
	// page coords are (0,0) bottom left, in points (1/72 inch)
	// Deprecated, use .BsData[ballotStyleIndex].Headers instead
	Headers []map[string][]float64 `json:"headers"`
}

type BsData struct {
	GpUnitIds []string `json:"GpUnitIds"`

	Bubbles Contest `json:"bubbles"`

	// Header map[page number string][]float64{left, top, right, bottom}
	Header map[string][]float64 `json:"headers"`
}

// page is 1-indexed
func (bj BubblesJson) Header(ballotStyleIndex, page int) (left, top, right, bottom float64, err error) {
	if ballotStyleIndex < 0 || ballotStyleIndex > len(bj.Headers) {
		err = fmt.Errorf("invalid ballot style %d of %d", ballotStyleIndex, len(bj.Headers))
		return
	}
	bsh := bj.Headers[ballotStyleIndex]
	pstr := strconv.Itoa(page)
	rect, ok := bsh[pstr]
	if !ok {
		keys := make([]string, len(bsh))
		pos := 0
		for k := range bsh {
			keys[pos] = k
			pos++
		}
		err = fmt.Errorf("style=%d, invalid page %d of {%s}", ballotStyleIndex, page, strings.Join(keys, " "))
		return
	}
	left = rect[0]
	top = rect[1]
	right = rect[2]
	bottom = rect[3]
	return
}

// imSubImage is implemented by most stdlib implementations of image.Image
type imSubImage interface {
	SubImage(r image.Rectangle) image.Image
}

func ExtractHeaders(bubblesJsonStr []byte, pngbytes [][]byte) (headers []*Header, err error) {
	var bubbles BubblesJson
	err = json.Unmarshal(bubblesJsonStr, &bubbles)
	if err != nil {
		err = fmt.Errorf("bad bubble json, %v", err)
		return
	}

	// check that all headers are at the same page position
	var baselineHLTRB []float64 = nil
	for ballotStyleIndex, bsh := range bubbles.Headers {
		for pagestr, headerLeftTopRightBot := range bsh {
			if baselineHLTRB == nil {
				baselineHLTRB = headerLeftTopRightBot
			} else {
				for i, v := range baselineHLTRB {
					if v != headerLeftTopRightBot[i] {
						err = fmt.Errorf("bubbles.headers[%d][%v] baselineHLTRB %#v != %#v", ballotStyleIndex, pagestr, baselineHLTRB, headerLeftTopRightBot)
						return
					}
				}
			}
		}
	}

	// extract headers
	headers = make([]*Header, 0, len(pngbytes))
	widthPt := bubbles.DrawSettings.PageSize[0]
	heightPt := bubbles.DrawSettings.PageSize[1]
	for ballotStyleIndex, bsh := range bubbles.Headers {
		keys := make([]string, len(bsh))
		pos := 0
		for k := range bsh {
			keys[pos] = k
			pos++
		}
		sort.Strings(keys)

		var orect image.Rectangle
		for _, pagestr := range keys {
			pageno := len(headers)
			// page coords in pt, (0,0) (left,bot)
			var headerLeftTopRightBot []float64
			headerLeftTopRightBot = bsh[pagestr]
			var bp int
			bp, err = strconv.Atoi(pagestr)
			if err != nil {
				err = fmt.Errorf("bs[%d] bad page %#v, %v", pageno, pagestr, err)
				return
			}
			var orig image.Image
			var format string
			orig, format, err = image.Decode(bytes.NewReader(pngbytes[pageno]))
			if err != nil {
				err = fmt.Errorf("bad page[%d] png, %v", pageno, err)
				return
			}
			osi, ok := orig.(imSubImage)
			if !ok {
				log.Printf("page[%d] could not SubImage", pageno)
			}
			ib := orig.Bounds()
			widthPx := ib.Max.X - ib.Min.X
			heightPx := ib.Max.Y - ib.Min.Y
			xscale := float64(widthPx) / float64(widthPt)
			yscale := float64(heightPx) / float64(heightPt)
			orect.Min.X = int((headerLeftTopRightBot[0] * xscale) + float64(ib.Min.X))
			orect.Min.Y = ib.Max.Y - int((headerLeftTopRightBot[1]*yscale)+float64(ib.Min.Y))
			orect.Max.X = int((headerLeftTopRightBot[2] * xscale) + float64(ib.Min.X))
			orect.Max.Y = ib.Max.Y - int((headerLeftTopRightBot[3]*yscale)+float64(ib.Min.Y))
			oi := osi.SubImage(orect)
			log.Printf("headers[%d] bsi %d bp %d format %s, %s orect %s oi %s", len(headers), ballotStyleIndex, bp, format, ib, orect, oi.Bounds())
			var spngb bytes.Buffer
			png.Encode(&spngb, oi)
			nh := new(Header)
			nh.SetOrigImage(spngb.Bytes())
			nh.orect = orect
			nh.drawLeftTopRightBot = headerLeftTopRightBot
			headers = append(headers, nh)
		}
	}
	return
}
