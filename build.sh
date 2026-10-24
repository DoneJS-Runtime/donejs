#!/bin/sh
# Build the jit interpreter into ./donejs.
set -e

go build -o donejs .
