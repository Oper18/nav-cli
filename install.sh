#!/bin/bash

go build -o nav ./cmd/main.go
mv nav ~/.local/bin/
