#!/bin/bash
set -e
set -x
dropdb --if-exists logintest
createdb --owner=logintest logintest
(cd cmd/ballotstudio && go test -postgres "dbname=logintest sslmode=disable user=logintest password=AAAA")
