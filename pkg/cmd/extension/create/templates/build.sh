#!/bin/sh
# Builds ado-<% .Name %> for every platform into dist/, named ado-<% .Name %>-<os>-<arch>[.exe]:
#   dist/ado-<% .Name %>-linux-amd64          dist/ado-<% .Name %>-linux-arm64
#   dist/ado-<% .Name %>-darwin-amd64         dist/ado-<% .Name %>-darwin-arm64
#   dist/ado-<% .Name %>-windows-amd64.exe    dist/ado-<% .Name %>-windows-arm64.exe
# The release pipelines then add dist/checksums.txt and publish everything in dist/.
set -e

echo "TODO: implement script/build.sh" >&2
exit 1
