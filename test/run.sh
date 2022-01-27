#!/bin/bash
set -e
make
PATH="${HOME}/psrc/poppler/build/utils:${HOME}/src/bsvenv/bin:${PATH}" ./ballotstudio -sqlite ballotstudio.sqlite -cookie-key eMpRmOZbYjNuWDF1NBAnSA== -debug "$@"
