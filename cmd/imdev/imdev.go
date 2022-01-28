package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	xidraw "golang.org/x/image/draw"
	ximath "golang.org/x/image/math/f64"

	"github.com/brianolson/ballotstudio/draw"
	"github.com/brianolson/ballotstudio/scan"
)

func maybefail(err error, format string, args ...interface{}) {
	if err == nil {
		return
	}
	log.Printf(format, args...)
	os.Exit(1)
}

func bopen(path string) (fin io.ReadCloser, err error) {
	if path == "" || path == "-" {
		fin = os.Stdin
	} else {
		fin, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
	}
	return fin, nil
}

type devcx struct {
	drawBackend string
	flaskPath   string
	erPath      string
	pdfPath     string
	bubblesPath string
	pngPageRoot string
	hmdebug     bool
	hmpage      int
	hmhead      int

	erJsonBlob     []byte
	pdf            []byte
	bubblesJsonStr []byte
	pngbytes       [][]byte

	backendCf  func()
	hasBackend bool

	headers  []*scan.Header
	scanners []scan.Scanner

	testPageJpg [][]byte
}

func (dc *devcx) getEr() error {
	if len(dc.erJsonBlob) > 0 {
		return nil
	}
	fin, err := bopen(dc.erPath)
	if err != nil {
		return err
	}
	blob, err := io.ReadAll(fin)
	if err != nil {
		return fmt.Errorf("%s: %v", dc.erPath, err)
	}
	dc.erJsonBlob = blob
	log.Printf("%s ->\n", dc.erPath)
	return nil
}

func (dc *devcx) getPdf() error {
	if len(dc.pdfPath) > 0 {
		// try to load it
		err := dc.readPdf()
		if err != nil {
			log.Printf("%s: warning, %v\n", dc.pdfPath, err)
		} else if len(dc.pdf) == 0 {
			log.Printf("%s: nothing\n", dc.pdfPath)
		}
	}
	if len(dc.bubblesPath) > 0 {
		// try to load it
		err := dc.readBubbles()
		if err != nil {
			log.Printf("%s: warning, %v\n", dc.bubblesPath, err)
		} else if len(dc.bubblesJsonStr) == 0 {
			log.Printf("%s: nothing\n", dc.bubblesJsonStr)
		}
	}
	if len(dc.pdf) > 0 && len(dc.bubblesJsonStr) > 0 {
		log.Printf("using existing %s %s\n", dc.pdfPath, dc.bubblesPath)
		return nil
	}
	// try to build it
	err := dc.getEr()
	if err != nil {
		return err
	}
	if !dc.hasBackend {
		dc.drawBackend, dc.backendCf, err = draw.EnsureBackend(dc.drawBackend, dc.flaskPath)
		if err != nil {
			return fmt.Errorf("could not start draw server, %v", err)
		}
		dc.hasBackend = true
	}
	log.Printf("pdf draw %s\n", dc.drawBackend)
	bothob, err := draw.DrawElection(dc.drawBackend, string(dc.erJsonBlob))
	if err != nil {
		return err
	}
	dc.pdf = bothob.Pdf
	dc.bubblesJsonStr = bothob.BubblesJson
	if len(dc.pdfPath) > 0 && dc.pdfPath != "-" {
		// try to cache it
		fout, err := os.Create(dc.pdfPath)
		if err == nil {
			fout.Write(dc.pdf)
			fout.Close()
			log.Printf("-> %s\n", dc.pdfPath)
		}
	}
	if len(dc.bubblesPath) > 0 && dc.bubblesPath != "-" {
		fout, err := os.Create(dc.bubblesPath)
		if err == nil {
			fout.Write(dc.bubblesJsonStr)
			fout.Close()
			log.Printf("-> %s\n", dc.bubblesPath)
		}
	}
	return nil
}
func (dc *devcx) readPdf() error {
	if len(dc.pdf) > 0 {
		return nil
	}
	fin, err := bopen(dc.pdfPath)
	if err != nil {
		return err
	}
	var blob []byte
	blob, err = io.ReadAll(fin)
	if err != nil {
		return fmt.Errorf("%s: %v", dc.pdfPath, err)
	}
	fin.Close()
	dc.pdf = blob
	log.Printf("%s ->\n", dc.pdfPath)
	return nil
}
func (dc *devcx) readBubbles() error {
	if len(dc.bubblesJsonStr) > 0 {
		return nil
	}
	fin, err := bopen(dc.bubblesPath)
	if err != nil {
		return err
	}
	var blob []byte
	blob, err = io.ReadAll(fin)
	if err != nil {
		return fmt.Errorf("%s: %v", dc.bubblesPath, err)
	}
	fin.Close()
	dc.bubblesJsonStr = blob
	log.Printf("%s ->\n", dc.bubblesPath)
	return nil
}

func (dc *devcx) getPng() error {
	if dc.pngbytes != nil {
		return nil
	}
	// try to read files
	if dc.pngPageRoot != "" {
		err := dc.readPng()
		if err == nil && dc.pngbytes != nil {
			return nil
		}
		log.Printf("%s: png read warning, %v", dc.pngPageRoot, err)
	}
	if dc.pngbytes != nil {
		return nil
	}
	// generate from pdf
	err := dc.getPdf()
	if err != nil {
		return nil
	}
	log.Printf("pdf to png\n")
	pngbytes, err := draw.PdfToPng(context.Background(), dc.pdf)
	if err != nil {
		return fmt.Errorf("PdfToPng fail, %v", err)
	}
	dc.pngbytes = pngbytes
	dc.writePng()
	return nil
}
func (dc *devcx) readPng() error {
	if len(dc.pngbytes) > 0 {
		return nil
	}
	pngi := 0
	var pngbytes [][]byte
	for {
		path := fmt.Sprintf("%s%d.png", dc.pngPageRoot, pngi)
		fin, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				// done
				log.Printf("%d png ->\n", len(pngbytes))
				if len(pngbytes) > 0 {
					dc.pngbytes = pngbytes
				}
				return nil
			}
			return err
		}
		blob, err := io.ReadAll(fin)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		pngbytes = append(pngbytes, blob)
		fin.Close()
		log.Printf("%s ->\n", path)
		pngi++
	}
}
func (dc *devcx) writePng() error {
	if len(dc.pngbytes) == 0 {
		return nil
	}
	if dc.pngPageRoot == "" {
		return nil
	}
	var didcreate []string
	defer func() {
		// deferred cleanup on error, clear didcreate to keep outputs
		for _, path := range didcreate {
			os.Remove(path)
		}
	}()
	for pngi, blob := range dc.pngbytes {
		path := fmt.Sprintf("%s%d.png", dc.pngPageRoot, pngi)
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		_, err = fout.Write(blob)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = fout.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		log.Printf("-> %s\n", path)
		didcreate = append(didcreate, path)
	}
	// keep them, don't wipe them
	didcreate = nil
	return nil
}

func (dc *devcx) getScanners() error {
	err := dc.getPng()
	if err != nil {
		return err
	}
	err = dc.readBubbles()
	if err != nil {
		return err
	}
	var bubbles scan.BubblesJson
	err = json.Unmarshal(dc.bubblesJsonStr, &bubbles)
	if len(dc.scanners) < len(dc.pngbytes) {
		dc.scanners = make([]scan.Scanner, len(dc.pngbytes))
	}
	for pngi, blob := range dc.pngbytes {
		var orig image.Image
		orig, _, err = image.Decode(bytes.NewReader(blob))
		if err != nil {
			return err
		}
		dc.scanners[pngi].Bj = bubbles
		err = dc.scanners[pngi].SetOrigImage(orig)
		if err != nil {
			return err
		}
	}
	return nil
}

func (dc *devcx) getPngHeaders() error {
	err := dc.getPdf()
	if err != nil {
		return err
	}
	err = dc.getPng()
	if err != nil {
		return err
	}
	log.Printf("%d pngs to headers\n", len(dc.pngbytes))
	headers, err := scan.ExtractHeaders(dc.bubblesJsonStr, dc.pngbytes)
	if err != nil {
		return err
	}
	dc.headers = headers
	log.Printf("%d headers\n", len(headers))
	for pngi, phim := range headers {
		path := fmt.Sprintf("%s%d_header.png", dc.pngPageRoot, pngi)
		fi, err := os.Stat(path)
		if err == nil && fi.Size() > 0 {
			log.Printf("   %s exists\n", path)
			continue
		}
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		_, err = fout.Write(phim.Png())
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = fout.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		log.Printf("-> %s\n", path)
	}
	for pngi, phim := range headers {
		path := fmt.Sprintf("%s%d_hsd.png", dc.pngPageRoot, pngi)
		log.Printf("%s %s\n", path, phim.HotspotDebugString())
		fi, err := os.Stat(path)
		if err == nil && fi.Size() > 0 {
			log.Printf("   %s exists\n", path)
			continue
		}
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		hsdi := phim.HotspotDebugImage()
		err = png.Encode(fout, hsdi)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = fout.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		log.Printf("-> %s\n", path)
	}
	return err
}

// +/- this many degrees
const testPageDegreeErrorDegrees = 10

// Apply a small random affine transform (rotate+scale) to a png page
func testPage(pngbytes []byte) (outim image.Image, spec TestPage, err error) {
	th := (rand.Float64() - 0.5) * (math.Pi * ((testPageDegreeErrorDegrees * 2) / 180.0))
	// +/- 20%
	scale := 1 + ((rand.Float64() - 0.5) * 0.4)
	spec.Rotation = th
	spec.Scale = scale
	log.Printf("th %f scale %f\n", th, scale)
	var orig image.Image
	orig, _, err = image.Decode(bytes.NewReader(pngbytes))
	if err != nil {
		return
	}
	orect := orig.Bounds()
	dy := float64(0)
	extraHeight := float64(0)
	dx := float64(0)
	extraWidth := float64(0)
	if th < 0 {
		// translate to allow for rotation above old top
		dy = math.Sin(th) * float64(orect.Max.X) * -1
		extraHeight = dy - ((1 - math.Cos(th)) * float64(orect.Max.Y))
		extraWidth = -math.Sin(th) * float64(orect.Max.Y)
	} else {
		dx = math.Sin(th) * float64(orect.Max.Y)
		extraWidth = dx - ((1 - math.Cos(th)) * float64(orect.Max.X))
		extraHeight = math.Sin(th) * float64(orect.Max.X)
	}
	tr := [6]float64{
		math.Cos(th) * scale, -math.Sin(th) * scale, dx,
		math.Sin(th) * scale, math.Cos(th) * scale, dy,
	}
	traff3 := ximath.Aff3(tr)
	log.Print(traff3)
	db := orig.Bounds()
	db.Max.X = int(scale * (float64(db.Max.X) + extraWidth))
	db.Max.Y = int(scale * (float64(db.Max.Y) + extraHeight))
	rgbim := image.NewNRGBA(db)
	whiteClear := color.NRGBA{255, 255, 255, 255}
	for y := db.Min.Y; y < db.Max.Y; y++ {
		for x := db.Min.X; x < db.Max.X; x++ {
			rgbim.SetNRGBA(x, y, whiteClear)
		}
	}
	xidraw.BiLinear.Transform(rgbim, traff3, orig, orig.Bounds(), xidraw.Src, nil)
	outim = rgbim
	return
}

var jpeg90 = jpeg.Options{Quality: 90}

type TestPagesSpec struct {
	Pages []TestPage `json:"pages"`
}
type TestPage struct {
	Rotation float64 `json:"rotation"`
	Scale    float64 `json:"scale"`
}

func (dc *devcx) doTestPages() (err error) {
	if len(dc.testPageJpg) != 0 {
		return nil
	}
	err = dc.getPng()
	if err != nil {
		return err
	}
	var tps TestPagesSpec
	dc.testPageJpg = make([][]byte, 0, len(dc.pngbytes))
	for pngi, blob := range dc.pngbytes {
		path := fmt.Sprintf("%s%d_tp.jpg", dc.pngPageRoot, pngi)
		fi, err := os.Stat(path)
		if err == nil && fi.Size() > 0 {
			log.Printf("   %s ->\n", path)
			jpegBytes, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("%s: %v", path, err)
			}
			dc.testPageJpg = append(dc.testPageJpg, jpegBytes)
			continue
		}
		tp, spec, err := testPage(blob)
		if err != nil {
			return fmt.Errorf("%s: test page err, %v", path, err)
		}
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		var buf bytes.Buffer
		err = jpeg.Encode(&buf, tp, &jpeg90)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		jpegBytes := buf.Bytes()
		_, err = fout.Write(jpegBytes)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = fout.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		log.Printf("-> %s\n", path)
		tps.Pages = append(tps.Pages, spec)
		dc.testPageJpg = append(dc.testPageJpg, jpegBytes)
	}
	if len(tps.Pages) == len(dc.pngbytes) {
		path := fmt.Sprintf("%stestpages.json", dc.pngPageRoot)
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		enc := json.NewEncoder(fout)
		err = enc.Encode(tps)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = fout.Close()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
	}
	return nil
}

func (dc *devcx) testHeaderMatch() error {
	err := dc.getPdf()
	if err != nil {
		return err
	}
	err = dc.getScanners()
	if err != nil {
		return err
	}
	err = dc.doTestPages()
	if err != nil {
		return err
	}
	err = dc.getPngHeaders()
	if err != nil {
		return err
	}
	var bubbles scan.BubblesJson
	err = json.Unmarshal(dc.bubblesJsonStr, &bubbles)
	if err != nil {
		return fmt.Errorf("bad bubble json, %v", err)
	}
	for jpgi, jpgbytes := range dc.testPageJpg {
		if dc.hmpage != -1 && dc.hmpage != jpgi {
			continue
		}
		jpstart := time.Now()
		path := fmt.Sprintf("%s%d_tp.jpg", dc.pngPageRoot, jpgi)
		im, format, err := image.Decode(bytes.NewReader(jpgbytes))
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		ycc, ok := im.(*image.YCbCr)
		if !ok {
			return fmt.Errorf("%s: %s not YCbCr", path, format)
		}
		headerScores := make([]float64, len(dc.headers))
		scanners := make([]*scan.ScanContext, len(dc.headers))
		bestHScore := math.MaxFloat64
		bestH := -1
		for hi, h := range dc.headers {
			if dc.hmhead != -1 && dc.hmhead != hi {
				continue
			}
			fmt.Fprintf(os.Stderr, "hm testpage=%d orig=%d\n", jpgi, hi)
			hstart := time.Now()
			sc := dc.scanners[hi].NewScanContext()
			scanners[hi] = sc
			var debugi scan.SettableImage
			if dc.hmdebug {
				debugi = h.DebugImage()
			}
			score, err := h.CheckPage(sc, ycc, debugi)
			if err != nil {
				return err
			}
			headerScores[hi] = score
			if score < bestHScore {
				bestHScore = score
				bestH = hi
			}
			dt := time.Now().Sub(hstart)
			fmt.Fprintf(os.Stderr, "hm page[%2d] header[%2d] hscore=%f (%s)\n", jpgi, hi, score, dt.String())
			if debugi != nil {
				dbp := fmt.Sprintf("%s%d_%d_tp_db.png", dc.pngPageRoot, jpgi, hi)
				dbfout, err := os.Create(dbp)
				if err != nil {
					return fmt.Errorf("%s: %v", dbp, err)
				}
				png.Encode(dbfout, debugi)
				dbfout.Close()
			}
		}
		for hi, hscore := range headerScores {
			if (hi != bestH) && (hscore > (bestHScore * 5)) {
				continue
			}
			pstart := time.Now()
			_, score, err := scanners[hi].ProcessScannedImage(im)
			dt := time.Now().Sub(pstart)
			fmt.Fprintf(os.Stderr, "hm testpage=%d orig=%d hscore %f pscore %f (%s)\n", jpgi, hi, hscore, score, dt.String())
			if err != nil {
				fmt.Fprintf(os.Stderr, "hm testpage=%d orig=%d: ERROR %v\n", jpgi, hi, err)

			}
		}
		jpt := time.Now().Sub(jpstart)
		fmt.Fprintf(os.Stderr, "hm testpage=%d tpdone %s\n", jpgi, jpt.String())
	}
	return err
}

func (dc *devcx) Close() {
	if dc.hasBackend {
		dc.backendCf()
	}
}

func main() {
	var dc devcx
	flag.StringVar(&dc.drawBackend, "draw-backend", "", "url to drawing backend")
	flag.StringVar(&dc.flaskPath, "flask", "", "path to flask for running draw/app.py")
	flag.StringVar(&dc.erPath, "er", "", "path to ElectionRecord json")
	flag.StringVar(&dc.pdfPath, "pdf", "", "path to rendered election pdf")
	flag.StringVar(&dc.bubblesPath, "bubbles", "", "path to bubbles json from pdf rendering of election")
	flag.StringVar(&dc.pngPageRoot, "png-root", "", "path to png pages {root}{%d}.png")
	flag.BoolVar(&dc.hmdebug, "hmdebug", false, "debug header-match")
	flag.IntVar(&dc.hmpage, "hmpage", -1, "header-match page to debug (default all)")
	flag.IntVar(&dc.hmhead, "hmhead", -1, "header-match header to debug (default all")
	flag.Parse()

	defer dc.Close()

	scan.DebugOut = os.Stderr

	fmt.Println(dc.testHeaderMatch())
}
