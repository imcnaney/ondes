// Package regression holds the Go-side render parity harness. It has no
// runtime code of its own; regression_test.go renders every fixture in
// renders.lst through the real engine and diffs the result against the
// committed Java reference summaries. This file exists only so the
// directory is a buildable package (a test-only package trips up
// `go build ./...`).
package regression
