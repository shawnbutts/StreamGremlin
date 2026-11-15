#!/bin/sh
# Runs every Go fuzz target in the main module for FUZZTIME each
# (M9 slice 1, D-110). A failing input is kept in that package's
# testdata/fuzz, so `make test` runs it from then on.
set -eu
GO=${GO:-go}
FUZZTIME=${FUZZTIME:-15s}
for pkg in $($GO list ./...); do
	dir=$($GO list -f '{{.Dir}}' "$pkg")
	for f in $(grep -ho '^func Fuzz[A-Za-z0-9_]*' "$dir"/*_test.go 2>/dev/null | sed 's/^func //'); do
		echo "$pkg $f"
		if ! $GO test -run '^$' -fuzz "^$f\$" -fuzztime "$FUZZTIME" "$pkg" >/dev/null; then
			echo "FAILED: $pkg $f. Run it again with: go test -run '^$f\$' $pkg"
			exit 1
		fi
	done
done
