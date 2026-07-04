#!/bin/sh -l

git config --global --add safe.directory '*'

/app/kakak match-label "$@"
