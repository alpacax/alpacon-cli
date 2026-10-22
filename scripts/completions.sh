#!/bin/sh
# GoReleaser runs before hooks without a shell, so the redirects live here.
set -e
rm -rf completions
mkdir completions
for sh in bash zsh fish; do
  go run . completion "$sh" > "completions/alpacon.$sh"
done
