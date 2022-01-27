#!/bin/bash
set -e
set -x
(cd cmd/imdev && go build)
# -draw-backend http://127.0.0.1:8081
PATH=${HOME}/psrc/poppler/build/utils:${PATH} cmd/imdev/imdev -flask ${HOME}/src/ve3/bin/flask -er rr.json -pdf rr.pdf -bubbles rr_bubbles.json -png-root rr- "$@"
