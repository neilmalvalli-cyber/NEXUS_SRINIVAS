// Package scenarios holds the executable versions of the 32 official crash
// scenarios (problem statement §5, groups A–F) as Go tests:
//
//	go test ./src/scenarios -v                         quick (process scenarios use fewer iterations)
//	KVDB_SCENARIOS=full go test ./src/scenarios -v -timeout 60m
//	KVDB_SCENARIO_REPORT=docs/SCENARIO_RESULTS.md ...  also write the results table
//
// Precise crash points (a kill before the first byte, in the middle of a
// write, between write and fsync, between the two renames of compaction, in
// the middle of recovery) are produced in-process with the faultfs
// simulated crash, which stops the engine at exactly that operation and
// closes its handles without any cleanup, as a kill would. Random kills of a
// real process (TerminateProcess on Windows) use the kvdb and torture
// binaries, which TestMain builds from this module.
package scenarios
