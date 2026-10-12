#!/bin/sh
# ado-<% .Name %> is an ado extension: "ado <% .Name %>" runs it.
set -e

echo "Hello from ado-<% .Name %>!"

# ado passes its context in the environment; a value is empty when ado could not resolve it.
echo "organization: ${ADO_ORG}"
echo "project: ${ADO_PROJECT}"

# Call ado back through ADO_EXECUTABLE: it uses the user's login and the same context.
"${ADO_EXECUTABLE:-ado}" auth status
