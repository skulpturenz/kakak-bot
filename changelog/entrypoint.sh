#!/bin/sh -l

if [ -n "${GITHUB_WORKSPACE:-}" ]; then
  git config --global --add safe.directory "${GITHUB_WORKSPACE}"
fi

/app/kakak changelog "$@"
