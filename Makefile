ALLSRC=$(shell find . -name \*.go)

ballotstudio:	${ALLSRC} static/demoelection.json
	go build ./cmd/ballotstudio

imdev:	${ALLSRC}
	go build ./cmd/imdev

er:	${ALLSRC}
	go build ./cmd/er

all:	ballotstudio imdev er
	go vet ./...

static/demoelection.json:	python/ballotstudio/demorace.py
	python3 python/ballotstudio/demorace.py > static/demoelection.json
