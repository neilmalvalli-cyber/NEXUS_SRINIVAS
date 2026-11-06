// Separate module: NEXUS does not depend on it, and `go build ./...` /
// `go test ./...` at the repository root do not include it. run.sh clones
// the independent recovery implementation into ./.recovery at a pinned
// commit and builds this program against it.
module crosscheck

go 1.22

require kipdb v0.0.0

replace kipdb => ./.recovery
