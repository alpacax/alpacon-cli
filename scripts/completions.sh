#!/bin/sh
# GoReleaser runs before hooks without a shell, so the redirects live here.
set -e
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/alpacon" .
rm -rf completions
mkdir completions
for sh in bash zsh fish; do
  "$tmp/alpacon" completion "$sh" > "completions/alpacon.$sh"
done
