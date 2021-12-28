package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"io"
	"log"
	"os"

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

	erJsonBlob     []byte
	pdf            []byte
	bubblesJsonStr []byte
	pngbytes       [][]byte

	backendCf  func()
	hasBackend bool
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
	log.Printf("%d headers\n", len(headers))
	for pngi, phim := range headers {
		path := fmt.Sprintf("%s%d_header.png", dc.pngPageRoot, pngi)
		fout, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		err = png.Encode(fout, phim)
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
	flag.Parse()

	defer dc.Close()

	fmt.Println(dc.getPngHeaders())
}
