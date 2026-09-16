#!/bin/bash

set -euo pipefail

# OTEL env vars are exported from .envrc (loaded by direnv).
go run .
