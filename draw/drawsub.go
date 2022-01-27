package draw

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

type DrawBothOb struct {
	Pdf         []byte
	BubblesJson []byte
}

type DrawBothResponse struct {
	PdfB64  []byte                 `json:"pdfb64"`
	Bubbles map[string]interface{} `json:"bubbles"`
}

// DrawServer runs a development
// FLASK_ENV=development FLASK_APP=draw/app.py "${HOME}/bsvenv/bin/flask" run -p 8081
type DrawServer struct {
	Port      int
	FlaskPath string

	cmd *exec.Cmd
}

func (ds DrawServer) BackendUrl() string {
	return fmt.Sprintf("http://localhost:%d", ds.Port)
}

func (ds *DrawServer) Start() error {
	if len(ds.FlaskPath) == 0 {
		// leave it up to PATH
		ds.FlaskPath = "flask"
	}
	if ds.Port == 0 {
		ds.Port = 8081
	}
	port := strconv.Itoa(ds.Port)
	ds.cmd = exec.Command(ds.FlaskPath, "run", "-p", port)
	ds.cmd.Env = os.Environ()
	ds.cmd.Env = append(ds.cmd.Env, "FLASK_ENV=development")
	ds.cmd.Env = append(ds.cmd.Env, "FLASK_APP=ballotstudio.app")
	ds.cmd.Stdout = os.Stdout
	ds.cmd.Stderr = os.Stderr
	err := ds.cmd.Start()
	if err != nil {
		ds.cmd = nil
	}
	return err
}

type ServerStartTimeout struct {
	inner error
}

func (sst *ServerStartTimeout) Error() string {
	if sst.inner == nil {
		return "DrawServer not ready after timeout"
	}
	return fmt.Sprintf("DrawServer not ready after timeout, %v", sst.inner)
}

func (sst *ServerStartTimeout) Unwrap() error {
	return sst.inner
}

// StartWait waits up to {timeout} for the server to be started and available.
// May return nil or ServerStartTimeout
func (ds *DrawServer) StartWait(timeout time.Duration) error {
	start := time.Now()
	expiry := start.Add(timeout)
	dt := timeout / 10
	if dt < time.Millisecond {
		dt = time.Millisecond
	}
	baseurl, err := url.Parse(ds.BackendUrl())
	if err != nil {
		return err
	}
	newpath := path.Join(baseurl.Path, "/demo.js")
	nurl := baseurl
	nurl.Path = newpath
	demoJsUrl := nurl.String()
	tryget := func(now time.Time) error {
		remaining := expiry.Sub(now)
		hclient := http.Client{Timeout: remaining}
		resp, err := hclient.Get(demoJsUrl)
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			body, _ := ioutil.ReadAll(resp.Body)
			if len(body) > 50 {
				body = body[:50]
			}
			return fmt.Errorf("draw POST %d %#v", resp.StatusCode, string(body))
		}
		// TODO: read the demo.js body an check that it is >1 byte and <100000
		return nil
	}
	now := start
	for true {
		err = tryget(now)
		if err == nil {
			return nil
		}
		if time.Now().Add(dt).After(expiry) {
			return &ServerStartTimeout{err}
		}
		time.Sleep(dt)
		now = time.Now()
		if now.Add(dt).After(expiry) {
			return &ServerStartTimeout{err}
		}
	}
	return nil
}

func (ds *DrawServer) Stop() error {
	if ds.cmd != nil {
		err := ds.cmd.Process.Kill()
		ds.cmd = nil
		return err
	}
	return nil
}

func DrawElection(backendUrl string, electionjson string) (both *DrawBothOb, err error) {
	baseurl, err := url.Parse(backendUrl)
	if err != nil {
		return nil, fmt.Errorf("bad url, %v", err)
	}
	newpath := path.Join(baseurl.Path, "/draw")
	nurl := baseurl
	nurl.Path = newpath
	nurl.RawQuery = "both=1"
	drawurl := nurl.String()
	postbody := strings.NewReader(electionjson)
	resp, err := http.DefaultClient.Post(drawurl, "application/json", postbody)
	if err != nil {
		return nil, fmt.Errorf("draw POST, %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		if len(body) > 50 {
			body = body[:50]
		}
		return nil, fmt.Errorf("draw POST %d %#v", resp.StatusCode, string(body))
	}
	body, err := ioutil.ReadAll(resp.Body)
	//dec := json.NewDecoder(resp.Body)
	var dbr DrawBothResponse
	//err = dec.Decode(&dbr)
	err = json.Unmarshal(body, &dbr)
	if err != nil {
		dbb := body
		if len(dbb) > 50 {
			dbb = dbb[:50]
		}
		return nil, fmt.Errorf("draw POST bad response, %v, %#v", err, string(dbb))
	}
	// json.Unmarshal helpfully converts "base64 content" unpacked into []byte
	/*
		pdf := make([]byte, base64.StdEncoding.DecodedLen(len(dbr.PdfB64)))
		actual, err := base64.StdEncoding.Decode(pdf, dbr.PdfB64)
		if err != nil {
			dp := dbr.PdfB64
			if len(dp) > 50 {
				dp = dp[:50]
			}
			return nil, fmt.Errorf("draw POST bad response b64, %v, %#v", err, string(dp))
		}
		if actual < len(pdf) {
			pdf = pdf[:actual]
		}
	*/
	bj, err := json.Marshal(dbr.Bubbles)
	if err != nil {
		return nil, fmt.Errorf("draw POST bad response bj, %v", err)
	}
	return &DrawBothOb{Pdf: dbr.PdfB64, BubblesJson: bj}, nil
}

type errorOrPngbytes struct {
	err      error
	pngpages [][]byte
}

func pngPageReader(reader io.Reader, out chan errorOrPngbytes) {
	var sizebytes [8]byte
	r := errorOrPngbytes{
		err:      nil,
		pngpages: make([][]byte, 0, 10),
	}
	for {
		_, err := io.ReadFull(reader, sizebytes[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			out <- r
			close(out)
			return
		}
		if err != nil {
			r.err = fmt.Errorf("reading page size, %s", err)
			out <- r
			return
		}
		pnglen := binary.BigEndian.Uint64(sizebytes[:])
		b := binary.LittleEndian.Uint64(sizebytes[:])
		if b < pnglen {
			pnglen = b
		}
		debug("got png page len=%d\n", pnglen)
		nextpng := make([]byte, pnglen)
		_, err = io.ReadFull(reader, nextpng)
		if err != nil {
			r.err = fmt.Errorf("reading page, %s", err)
			out <- r
			return
		}
		r.pngpages = append(r.pngpages, nextpng)
		debug("got png page[%d]\n", len(r.pngpages)-1)
	}
}

// uses subprocess `pdftoppm`
func PdfToPng(ctx context.Context, pdf []byte) (pngbytes [][]byte, err error) {
	if len(pdf) == 0 {
		return nil, fmt.Errorf("pdftopng but empty pdf")
	}
	// requires poppler fork from https://github.com/brianolson/poppler
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-pngMultiBlock") // , "-singlefile"
	if err != nil {
		return nil, fmt.Errorf("could not cmd pdftoppm, %v", err)
	}
	cmd.Stdin = bytes.NewReader(pdf)
	reader, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cm.StdoutPipe(), %v", err)
	}
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	pchan := make(chan errorOrPngbytes, 1)
	go pngPageReader(reader, pchan)
	err = cmd.Run()
	if err != nil {
		se := string(stderr.Bytes())
		if len(se) > 50 {
			se = se[:50]
		}
		return nil, fmt.Errorf("pdftoppm err, %v, %v", err, se)
	}
	debug("pdftoppm ran, getting result...\n")
	select {
	case r := <-pchan:
		return r.pngpages, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var DebugOut io.Writer

func debug(format string, args ...interface{}) {
	if DebugOut == nil {
		return
	}
	fmt.Fprintf(DebugOut, format, args...)
}

func nop() {
}

func exists(path string) (out string, ok bool) {
	_, err := os.Stat(path)
	if err == nil {
		return path, true
	}
	return "", false
}

func EnsureBackend(drawBackend, flaskPath string) (backendUrl string, cf func(), err error) {
	if len(drawBackend) != 0 {
		return drawBackend, nop, nil
	}
	if flaskPath == "" {
		for _, fp := range []string{"./flask", "bsvenv/bin/flask"} {
			var ok bool
			flaskPath, ok = exists(fp)
			if ok {
				break
			}
		}
	}
	var drawserver DrawServer
	drawserver.FlaskPath = flaskPath
	err = drawserver.Start()
	if err != nil {
		return "", nop, err
	}
	err = drawserver.StartWait(time.Second)
	if err != nil {
		drawserver.Stop()
		return "", nop, err
	}
	cf = func() {
		drawserver.Stop()
	}
	backendUrl = drawserver.BackendUrl()
	log.Printf("started draw server %d %s\n", drawserver.cmd.Process.Pid, backendUrl)
	return
}
