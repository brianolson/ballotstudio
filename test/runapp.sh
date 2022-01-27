#!/bin/bash -x
FLASK_ENV=development FLASK_APP=python/ballotstudio/app.py ${HOME}/src/bsvenv/bin/flask run -p 8081
