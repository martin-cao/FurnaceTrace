#!/usr/bin/env fish
set -l root (path resolve (dirname (status filename))/..)
exec sh $root/scripts/stop.sh $argv
